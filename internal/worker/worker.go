// Package worker provides the per-account sync goroutine plus the global
// scheduler that drives webhook renewal and polling fallback.
package worker

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	stdsync "sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ryakel/skulid/internal/calendar"
	"github.com/ryakel/skulid/internal/db"
	syncengine "github.com/ryakel/skulid/internal/sync"
)

const (
	pollInterval     = 5 * time.Minute
	channelTTL       = 7 * 24 * time.Hour
	channelRenewWhen = 24 * time.Hour
	channelRenewTick = time.Hour
	staleAfter       = 10 * time.Minute
)

// Manager coordinates per-account work and the cross-cutting scheduler. Jobs
// are fan-outs into per-account inboxes so a slow account never blocks others.
type Manager struct {
	pool       *pgxpool.Pool
	accounts   *db.AccountRepo
	calendars  *db.CalendarRepo
	tokens     *db.SyncTokenRepo
	rules      *db.SyncRuleRepo
	blocks     *db.SmartBlockRepo
	links      *db.EventLinkRepo
	audit      *db.AuditRepo
	clientFor  syncengine.ClientFor
	engine     *syncengine.Engine
	smartEngine *syncengine.SmartBlockEngine

	externalURL string
	log         *slog.Logger

	mu      stdsync.Mutex
	workers map[int64]*accountWorker

	// Smart block recompute is debounced.
	debounceMu stdsync.Mutex
	debounce   map[int64]*time.Timer

	stop chan struct{}
}

type Job struct {
	AccountID  int64
	CalendarID int64 // 0 = all calendars on the account
}

func NewManager(pool *pgxpool.Pool,
	accounts *db.AccountRepo, calendars *db.CalendarRepo, tokens *db.SyncTokenRepo,
	rules *db.SyncRuleRepo, blocks *db.SmartBlockRepo, links *db.EventLinkRepo,
	audit *db.AuditRepo, clientFor syncengine.ClientFor, engine *syncengine.Engine,
	smartEngine *syncengine.SmartBlockEngine, externalURL string, log *slog.Logger) *Manager {
	return &Manager{
		pool:        pool,
		accounts:    accounts,
		calendars:   calendars,
		tokens:      tokens,
		rules:       rules,
		blocks:      blocks,
		links:       links,
		audit:       audit,
		clientFor:   clientFor,
		engine:      engine,
		smartEngine: smartEngine,
		externalURL: externalURL,
		log:         log,
		workers:     map[int64]*accountWorker{},
		debounce:    map[int64]*time.Timer{},
		stop:        make(chan struct{}),
	}
}

// Start spins up workers for every existing account and the scheduler loop.
func (m *Manager) Start(ctx context.Context) error {
	accounts, err := m.accounts.List(ctx)
	if err != nil {
		return err
	}
	for _, a := range accounts {
		m.ensureWorker(a.ID)
	}
	go m.scheduler(ctx)
	return nil
}

func (m *Manager) Stop() {
	close(m.stop)
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, w := range m.workers {
		w.stop()
	}
}

func (m *Manager) ensureWorker(accountID int64) *accountWorker {
	m.mu.Lock()
	defer m.mu.Unlock()
	if w, ok := m.workers[accountID]; ok {
		return w
	}
	w := newAccountWorker(accountID, m)
	m.workers[accountID] = w
	go w.run()
	return w
}

// EnqueueAccount queues a sync for every calendar on an account.
func (m *Manager) EnqueueAccount(accountID int64) {
	w := m.ensureWorker(accountID)
	w.enqueue(Job{AccountID: accountID})
}

// EnqueueCalendar queues a sync for one specific calendar.
func (m *Manager) EnqueueCalendar(accountID, calendarID int64) {
	w := m.ensureWorker(accountID)
	w.enqueue(Job{AccountID: accountID, CalendarID: calendarID})
}

// EnqueueSmartBlocksForCalendar debounces and recomputes any smart blocks that
// reference this calendar as a source.
func (m *Manager) EnqueueSmartBlocksForCalendar(ctx context.Context, calendarID int64) {
	bs, err := m.blocks.ListBySourceCalendar(ctx, calendarID)
	if err != nil {
		m.log.Error("list blocks failed", "err", err)
		return
	}
	for _, b := range bs {
		m.debounceSmartBlock(b.ID)
	}
}

func (m *Manager) debounceSmartBlock(blockID int64) {
	m.debounceMu.Lock()
	defer m.debounceMu.Unlock()
	if t, ok := m.debounce[blockID]; ok {
		t.Stop()
	}
	m.debounce[blockID] = time.AfterFunc(15*time.Second, func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		if err := m.smartEngine.Recompute(ctx, blockID); err != nil {
			m.log.Error("smart block recompute failed", "block_id", blockID, "err", err)
		}
	})
}

// RecomputeBlock runs the smart-block engine synchronously against one block.
func (m *Manager) RecomputeBlock(ctx context.Context, blockID int64) error {
	return m.smartEngine.Recompute(ctx, blockID)
}

// RecomputeAllSmartBlocks runs every enabled smart block now (used at startup
// and from the manual buttons in the UI).
func (m *Manager) RecomputeAllSmartBlocks(ctx context.Context) {
	bs, err := m.blocks.List(ctx)
	if err != nil {
		m.log.Error("list smart blocks failed", "err", err)
		return
	}
	for _, b := range bs {
		if !b.Enabled {
			continue
		}
		if err := m.smartEngine.Recompute(ctx, b.ID); err != nil {
			m.log.Error("smart block recompute failed", "block_id", b.ID, "err", err)
		}
	}
}

// scheduler runs the polling fallback and watch-channel renewal loops.
func (m *Manager) scheduler(ctx context.Context) {
	pollTicker := time.NewTicker(pollInterval)
	renewTicker := time.NewTicker(channelRenewTick)
	defer pollTicker.Stop()
	defer renewTicker.Stop()

	// Run an initial pass shortly after startup so we don't wait 5 min for the first sync.
	startup := time.NewTimer(15 * time.Second)
	defer startup.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-m.stop:
			return
		case <-startup.C:
			m.runPollPass(ctx)
			m.runRenewPass(ctx)
		case <-pollTicker.C:
			m.runPollPass(ctx)
		case <-renewTicker.C:
			m.runRenewPass(ctx)
		}
	}
}

func (m *Manager) runPollPass(ctx context.Context) {
	ts, err := m.tokens.ListNeedingPoll(ctx, staleAfter)
	if err != nil {
		m.log.Error("poll pass list failed", "err", err)
		return
	}
	for _, t := range ts {
		m.EnqueueCalendar(t.AccountID, t.CalendarID)
	}
}

func (m *Manager) runRenewPass(ctx context.Context) {
	if m.externalURL == "" {
		return
	}
	ts, err := m.tokens.ListExpiringSoon(ctx, channelRenewWhen)
	if err != nil {
		m.log.Error("renew pass list failed", "err", err)
		return
	}
	for _, t := range ts {
		if err := m.RegisterWatch(ctx, t.AccountID, t.CalendarID); err != nil {
			m.log.Warn("watch renew failed", "cal_id", t.CalendarID, "err", err)
		}
	}
}

// RegisterWatch (re-)registers a Google push channel for a calendar. It stops
// any previous channel first to avoid stale subscriptions.
func (m *Manager) RegisterWatch(ctx context.Context, accountID, calendarID int64) error {
	if m.externalURL == "" {
		return errors.New("EXTERNAL_URL not set")
	}
	cal, err := m.calendars.Get(ctx, calendarID)
	if err != nil {
		return err
	}
	cli, err := m.clientFor(ctx, accountID)
	if err != nil {
		return err
	}
	prior, _ := m.tokens.Get(ctx, accountID, calendarID)
	if prior != nil && prior.WatchChannelID != "" {
		_ = cli.StopChannel(ctx, prior.WatchChannelID, prior.WatchResourceID)
	}
	if err := m.tokens.Ensure(ctx, accountID, calendarID); err != nil {
		return err
	}
	channelID := randomHex(16)
	tokenSecret := randomHex(24)
	addr := m.externalURL + "/api/webhooks/google"
	ch, err := cli.Watch(ctx, cal.GoogleCalendarID, channelID, addr, tokenSecret, channelTTL)
	if err != nil {
		return fmt.Errorf("watch: %w", err)
	}
	expires := time.Now().Add(channelTTL)
	if ch.Expiration > 0 {
		expires = time.UnixMilli(ch.Expiration)
	}
	if err := m.tokens.UpdateWatch(ctx, accountID, calendarID, ch.Id, ch.ResourceId, tokenSecret, expires); err != nil {
		return err
	}
	return nil
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// accountWorker drains a per-account job queue. Jobs coalesce: identical jobs
// queued within the dwell window collapse into one.
type accountWorker struct {
	accountID int64
	mgr       *Manager
	jobs      chan Job
	done      chan struct{}
}

func newAccountWorker(accountID int64, mgr *Manager) *accountWorker {
	return &accountWorker{
		accountID: accountID,
		mgr:       mgr,
		jobs:      make(chan Job, 64),
		done:      make(chan struct{}),
	}
}

func (w *accountWorker) enqueue(j Job) {
	select {
	case w.jobs <- j:
	default:
		// Queue full — drop. The polling loop will catch us back up.
		w.mgr.log.Warn("account worker queue full", "account_id", w.accountID)
	}
}

func (w *accountWorker) stop() { close(w.done) }

func (w *accountWorker) run() {
	for {
		select {
		case <-w.done:
			return
		case j := <-w.jobs:
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
			if err := w.process(ctx, j); err != nil {
				w.mgr.log.Error("worker process failed", "account_id", w.accountID, "err", err)
			}
			cancel()
		}
	}
}

func (w *accountWorker) process(ctx context.Context, j Job) error {
	calendars := []db.Calendar{}
	if j.CalendarID != 0 {
		c, err := w.mgr.calendars.Get(ctx, j.CalendarID)
		if err != nil {
			return err
		}
		calendars = append(calendars, *c)
	} else {
		all, err := w.mgr.calendars.ListByAccount(ctx, j.AccountID)
		if err != nil {
			return err
		}
		calendars = all
	}
	cli, err := w.mgr.clientFor(ctx, j.AccountID)
	if err != nil {
		return err
	}
	for _, cal := range calendars {
		if err := w.syncCalendar(ctx, cli, cal); err != nil {
			w.mgr.log.Error("sync calendar failed", "cal_id", cal.ID, "err", err)
		}
	}
	return nil
}

func (w *accountWorker) syncCalendar(ctx context.Context, cli *calendar.Client, cal db.Calendar) error {
	if err := w.mgr.tokens.Ensure(ctx, cal.AccountID, cal.ID); err != nil {
		return err
	}
	tok, err := w.mgr.tokens.Get(ctx, cal.AccountID, cal.ID)
	if err != nil {
		return err
	}
	syncToken := ""
	if tok != nil {
		syncToken = tok.SyncToken
	}
	res, err := cli.IncrementalSync(ctx, cal.GoogleCalendarID, syncToken, time.Now().AddDate(0, 0, -1))
	if errors.Is(err, calendar.ErrSyncTokenInvalid) {
		// Full resync from now.
		res, err = cli.IncrementalSync(ctx, cal.GoogleCalendarID, "", time.Now().AddDate(0, 0, -1))
	}
	if err != nil {
		return fmt.Errorf("incremental: %w", err)
	}
	for _, ev := range res.Events {
		if err := w.mgr.engine.ProcessChange(ctx, cal.ID, ev); err != nil {
			w.mgr.log.Error("process change failed", "event_id", ev.Id, "err", err)
		}
	}
	if res.NextSyncToken != "" {
		_ = w.mgr.tokens.UpdateSyncToken(ctx, cal.AccountID, cal.ID, res.NextSyncToken)
	} else {
		// Still record we polled, so we don't poll again immediately.
		_ = w.mgr.tokens.UpdateSyncToken(ctx, cal.AccountID, cal.ID, syncToken)
	}
	_ = w.mgr.calendars.MarkSynced(ctx, cal.ID, time.Now())
	// Smart-block changes might be triggered by any event change on a source calendar.
	w.mgr.EnqueueSmartBlocksForCalendar(ctx, cal.ID)
	return nil
}
