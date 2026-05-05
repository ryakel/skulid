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
	aiCleanupTick    = 6 * time.Hour
	maintenanceTick  = 6 * time.Hour
)

// Manager coordinates per-account work and the cross-cutting scheduler. Jobs
// are fan-outs into per-account inboxes so a slow account never blocks others.
type Manager struct {
	pool        *pgxpool.Pool
	accounts    *db.AccountRepo
	calendars   *db.CalendarRepo
	tokens      *db.SyncTokenRepo
	rules       *db.SyncRuleRepo
	blocks      *db.SmartBlockRepo
	links       *db.EventLinkRepo
	audit       *db.AuditRepo
	clientFor   syncengine.ClientFor
	engine      *syncengine.Engine
	smartEngine  *syncengine.SmartBlockEngine
	decompEngine *syncengine.DecompressionEngine // optional
	scheduler    *syncengine.Scheduler           // optional; gates daily-maintenance tick
	tasks        *db.TaskRepo
	habits       *db.HabitRepo

	externalURL string
	log         *slog.Logger

	// AI conversation cleanup. Both nil if the assistant feature is unused.
	aiConversations *db.AIConversationRepo
	aiMaxAge        time.Duration

	mu      stdsync.Mutex
	workers map[int64]*accountWorker

	// Smart block recompute is debounced per smart_block.
	debounceMu stdsync.Mutex
	debounce   map[int64]*time.Timer

	// Decompression recompute is debounced per calendar.
	decompDebounceMu stdsync.Mutex
	decompDebounce   map[int64]*time.Timer

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
		workers:        map[int64]*accountWorker{},
		debounce:       map[int64]*time.Timer{},
		decompDebounce: map[int64]*time.Timer{},
		stop:           make(chan struct{}),
	}
}

// SetDecompressionEngine wires in the decompression engine. Optional — when
// not set, no decompress events are written and no decompression cleanup runs.
func (m *Manager) SetDecompressionEngine(e *syncengine.DecompressionEngine) {
	m.decompEngine = e
}

// SetMaintenanceDeps wires in the task/habit repos and scheduler so the
// daily-maintenance tick can refresh placements as the horizon walks forward.
// Without it, MaintenanceTick is a no-op.
func (m *Manager) SetMaintenanceDeps(tasks *db.TaskRepo, habits *db.HabitRepo, sch *syncengine.Scheduler) {
	m.tasks = tasks
	m.habits = habits
	m.scheduler = sch
}

// EnqueueDecompressionForCalendar debounces (15s) and recomputes decompress
// events for the given calendar.
func (m *Manager) EnqueueDecompressionForCalendar(calendarID int64) {
	if m.decompEngine == nil {
		return
	}
	m.decompDebounceMu.Lock()
	defer m.decompDebounceMu.Unlock()
	if t, ok := m.decompDebounce[calendarID]; ok {
		t.Stop()
	}
	m.decompDebounce[calendarID] = time.AfterFunc(15*time.Second, func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		if err := m.decompEngine.Recompute(ctx, calendarID); err != nil {
			m.log.Error("decompression recompute failed", "calendar_id", calendarID, "err", err)
		}
	})
}

// RecomputeAllDecompression runs the decompression engine across every
// connected calendar — used by the manual button on Settings → Buffers.
func (m *Manager) RecomputeAllDecompression(ctx context.Context) {
	if m.decompEngine == nil {
		return
	}
	cals, err := m.calendars.ListAll(ctx)
	if err != nil {
		m.log.Error("list calendars failed", "err", err)
		return
	}
	for _, c := range cals {
		if err := m.decompEngine.Recompute(ctx, c.ID); err != nil {
			m.log.Warn("decompression recompute failed", "calendar_id", c.ID, "err", err)
		}
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
	go m.runScheduler(ctx)
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

// SetAIConversationCleanup wires in the AI conversations repo so the scheduler
// can prune stale chats. Optional: if not called, no AI cleanup runs.
func (m *Manager) SetAIConversationCleanup(repo *db.AIConversationRepo, maxAge time.Duration) {
	m.aiConversations = repo
	m.aiMaxAge = maxAge
}

// runScheduler runs the polling fallback, watch-channel renewal, AI
// conversation cleanup, and the daily maintenance pass that keeps habit/task
// placements fresh as the horizon walks forward.
func (m *Manager) runScheduler(ctx context.Context) {
	pollTicker := time.NewTicker(pollInterval)
	renewTicker := time.NewTicker(channelRenewTick)
	cleanupTicker := time.NewTicker(aiCleanupTick)
	maintTicker := time.NewTicker(maintenanceTick)
	defer pollTicker.Stop()
	defer renewTicker.Stop()
	defer cleanupTicker.Stop()
	defer maintTicker.Stop()

	// Run an initial pass shortly after startup so we don't wait for the first ticks.
	startup := time.NewTimer(15 * time.Second)
	maintStartup := time.NewTimer(60 * time.Second)
	defer startup.Stop()
	defer maintStartup.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-m.stop:
			return
		case <-startup.C:
			m.runPollPass(ctx)
			m.runRenewPass(ctx)
			m.runAICleanup(ctx)
		case <-maintStartup.C:
			m.runMaintenance(ctx)
		case <-pollTicker.C:
			m.runPollPass(ctx)
		case <-renewTicker.C:
			m.runRenewPass(ctx)
		case <-cleanupTicker.C:
			m.runAICleanup(ctx)
		case <-maintTicker.C:
			m.runMaintenance(ctx)
		}
	}
}

// runMaintenance refreshes scheduled placements:
//
//   - For every enabled habit, re-runs PlaceHabit so the rolling horizon
//     extends as `today` advances.
//   - For every task that's pending or whose scheduled window has already
//     passed (the user procrastinated past the placement), re-runs PlaceTask
//     so it lands on the next available slot.
//
// Heavy operation; gated behind SetMaintenanceDeps so test harnesses and
// non-scheduler-using deployments stay quiet.
func (m *Manager) runMaintenance(ctx context.Context) {
	if m.scheduler == nil || m.habits == nil || m.tasks == nil {
		return
	}
	now := time.Now()

	hs, err := m.habits.ListEnabled(ctx)
	if err != nil {
		m.log.Error("maintenance list habits failed", "err", err)
	} else {
		for _, h := range hs {
			if err := m.scheduler.PlaceHabit(ctx, h.ID); err != nil {
				m.log.Warn("maintenance place habit failed", "habit_id", h.ID, "err", err)
			}
		}
		if len(hs) > 0 {
			m.log.Info("maintenance habits refreshed", "count", len(hs))
		}
	}

	ts, err := m.tasks.ListAllActive(ctx)
	if err != nil {
		m.log.Error("maintenance list tasks failed", "err", err)
		return
	}
	refreshed := 0
	for _, t := range ts {
		needsRefresh := t.Status == db.TaskPending
		if t.Status == db.TaskScheduled && t.ScheduledEndsAt != nil && t.ScheduledEndsAt.Before(now) {
			needsRefresh = true
		}
		if !needsRefresh {
			continue
		}
		if err := m.scheduler.PlaceTask(ctx, t.ID); err != nil {
			m.log.Warn("maintenance place task failed", "task_id", t.ID, "err", err)
			continue
		}
		refreshed++
	}
	if refreshed > 0 {
		m.log.Info("maintenance tasks refreshed", "count", refreshed)
	}
}

func (m *Manager) runAICleanup(ctx context.Context) {
	if m.aiConversations == nil || m.aiMaxAge <= 0 {
		return
	}
	n, err := m.aiConversations.DeleteOlderThan(ctx, m.aiMaxAge)
	if err != nil {
		m.log.Error("ai cleanup failed", "err", err)
		return
	}
	if n > 0 {
		m.log.Info("ai cleanup", "deleted_conversations", n)
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
// any previous channel first to avoid stale subscriptions. Disabled calendars
// short-circuit and have their existing watch (if any) stopped — keeps Google
// from billing notifications we'll just throw away.
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
	if !cal.Enabled {
		if prior != nil && prior.WatchChannelID != "" {
			_ = cli.StopChannel(ctx, prior.WatchChannelID, prior.WatchResourceID)
			_ = m.tokens.ClearWatch(ctx, accountID, calendarID)
		}
		return nil
	}
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
		// Single-calendar jobs from the webhook handler can fire for a
		// disabled calendar (the watch may still have a few minutes of TTL
		// after toggle-off). Drop the work loudly enough to debug.
		if !c.Enabled {
			w.mgr.log.Debug("skipping sync of disabled calendar", "cal_id", c.ID)
			return nil
		}
		calendars = append(calendars, *c)
	} else {
		all, err := w.mgr.calendars.ListEnabledByAccount(ctx, j.AccountID)
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
	// Decompress events trail real meetings; refresh after every sync of this calendar.
	w.mgr.EnqueueDecompressionForCalendar(cal.ID)
	return nil
}
