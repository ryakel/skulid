package httpx

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/ryakel/skulid/internal/auth"
	"github.com/ryakel/skulid/internal/db"
	"github.com/ryakel/skulid/internal/hours"
	syncengine "github.com/ryakel/skulid/internal/sync"
)

// pageData returns the common keys every layout-rendered template expects.
func (s *Server) pageData(r *http.Request, title string) map[string]any {
	d := map[string]any{
		"Title": title,
		"Features": map[string]bool{
			"Assistant": s.Agent != nil,
		},
		"DevAuthBypass": s.Cfg.DevAuthBypass,
		"Version":       s.Version,
	}
	if sess, ok := auth.SessionFromContext(r.Context()); ok {
		d["Session"] = sess
	}
	return d
}

func (s *Server) render(w http.ResponseWriter, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.Renderer.Render(w, name, data); err != nil {
		s.Log.Error("render failed", "name", name, "err", err)
		http.Error(w, "render error", http.StatusInternalServerError)
	}
}

func (s *Server) secureCookies() bool {
	return strings.HasPrefix(s.Cfg.ExternalURL, "https://")
}

// ---------------------------------------------------------------------------
// Dashboard
// ---------------------------------------------------------------------------

func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	accounts, _ := s.Accounts.List(ctx)
	rules, _ := s.Rules.List(ctx)
	blocks, _ := s.Blocks.List(ctx)
	recent, _ := s.Audit.Recent(ctx, 25)

	data := s.pageData(r, "Dashboard")
	data["Counts"] = map[string]int{
		"Accounts": len(accounts),
		"Rules":    len(rules),
		"Blocks":   len(blocks),
	}
	data["Recent"] = recent
	s.render(w, "dashboard", data)
}

// ---------------------------------------------------------------------------
// Auth: login / OAuth callback / logout
// ---------------------------------------------------------------------------

func (s *Server) handleLoginPage(w http.ResponseWriter, r *http.Request) {
	owner, _ := s.TOFU.OwnerEmail(r.Context())
	data := map[string]any{
		"Claimed":       owner != "",
		"OwnerEmail":    owner,
		"Error":         r.URL.Query().Get("error"),
		"DevAuthBypass": s.Cfg.DevAuthBypass,
		"Version":       s.Version,
	}
	s.render(w, "login", data)
}

func (s *Server) handleLoginStart(w http.ResponseWriter, r *http.Request) {
	url := s.OAuth.StartFlow(w, auth.IntentLogin, s.secureCookies())
	http.Redirect(w, r, url, http.StatusFound)
}

func (s *Server) handleAccountConnect(w http.ResponseWriter, r *http.Request) {
	url := s.OAuth.StartFlow(w, auth.IntentConnect, s.secureCookies())
	http.Redirect(w, r, url, http.StatusFound)
}

func (s *Server) handleAuthCallback(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if errParam := r.URL.Query().Get("error"); errParam != "" {
		http.Redirect(w, r, "/login?error="+errParam, http.StatusFound)
		return
	}
	intent, err := s.OAuth.VerifyState(w, r)
	if err != nil {
		http.Redirect(w, r, "/login?error=state", http.StatusFound)
		return
	}

	// For "connect" intent, the caller must already be the owner.
	if intent == auth.IntentConnect {
		sess, sessErr := s.Sessions.Read(r)
		if sessErr != nil {
			http.Redirect(w, r, "/login?error=auth", http.StatusFound)
			return
		}
		ok, err := s.TOFU.VerifyOwner(ctx, sess.GoogleSub)
		if err != nil || !ok {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
	}

	tok, err := s.OAuth.Exchange(ctx, r.URL.Query().Get("code"))
	if err != nil {
		s.Log.Error("oauth exchange failed", "err", err)
		http.Redirect(w, r, "/login?error=exchange", http.StatusFound)
		return
	}
	info, err := s.OAuth.FetchUserInfo(ctx, tok)
	if err != nil {
		s.Log.Error("userinfo failed", "err", err)
		http.Redirect(w, r, "/login?error=userinfo", http.StatusFound)
		return
	}
	if tok.RefreshToken == "" {
		// Google only emits a refresh token when prompt=consent + offline.
		// If we don't get one we can't run unattended.
		http.Redirect(w, r, "/login?error=no_refresh", http.StatusFound)
		return
	}

	sealedRefresh, err := s.Sealer.Seal(tok.RefreshToken)
	if err != nil {
		s.Log.Error("seal refresh failed", "err", err)
		http.Error(w, "seal error", http.StatusInternalServerError)
		return
	}
	sealedAccess, err := s.Sealer.Seal(tok.AccessToken)
	if err != nil {
		s.Log.Error("seal access failed", "err", err)
		http.Error(w, "seal error", http.StatusInternalServerError)
		return
	}
	expiry := tok.Expiry
	accountID, err := s.Accounts.Upsert(ctx, info.Sub, info.Email, sealedRefresh, sealedAccess, &expiry)
	if err != nil {
		s.Log.Error("account upsert failed", "err", err)
		http.Error(w, "db error", http.StatusInternalServerError)
		return
	}

	if intent == auth.IntentLogin {
		if err := s.TOFU.Claim(ctx, info.Sub, info.Email); err != nil {
			if errors.Is(err, auth.ErrOwnerMismatch) {
				http.Redirect(w, r, "/login?error=mismatch", http.StatusFound)
				return
			}
			http.Error(w, "claim error", http.StatusInternalServerError)
			return
		}
		s.Sessions.Issue(w, auth.Session{GoogleSub: info.Sub, Email: info.Email})
	}

	// Best-effort first-run discovery so the account isn't empty in the UI.
	if err := s.discoverAndWatchCalendars(ctx, accountID); err != nil {
		s.Log.Warn("calendar discovery failed", "account_id", accountID, "err", err)
	}
	s.Worker.EnqueueAccount(accountID)

	dest := "/"
	if intent == auth.IntentConnect {
		dest = "/accounts"
	}
	http.Redirect(w, r, dest, http.StatusFound)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	s.Sessions.Clear(w)
	http.Redirect(w, r, "/login", http.StatusFound)
}

// ---------------------------------------------------------------------------
// Accounts
// ---------------------------------------------------------------------------

func (s *Server) handleAccountsPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	accounts, err := s.Accounts.List(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	byAcct := map[int64][]db.Calendar{}
	for _, a := range accounts {
		cs, _ := s.Calendars.ListByAccount(ctx, a.ID)
		byAcct[a.ID] = cs
	}
	data := s.pageData(r, "Accounts")
	data["Accounts"] = accounts
	data["CalendarsByAccount"] = byAcct
	s.render(w, "accounts", data)
}

func (s *Server) handleAccountRefresh(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	if err := s.discoverAndWatchCalendars(r.Context(), id); err != nil {
		s.Log.Error("refresh failed", "account_id", id, "err", err)
		http.Error(w, "refresh failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	s.Worker.EnqueueAccount(id)
	http.Redirect(w, r, "/accounts", http.StatusFound)
}

func (s *Server) handleAccountDelete(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	if err := s.Accounts.Delete(r.Context(), id); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/accounts", http.StatusFound)
}

// discoverAndWatchCalendars lists every calendar visible to the account, upserts
// them, and (re-)registers a push channel so we get change notifications.
func (s *Server) discoverAndWatchCalendars(ctx context.Context, accountID int64) error {
	cli, err := s.ClientFor(ctx, accountID)
	if err != nil {
		return fmt.Errorf("client: %w", err)
	}
	entries, err := cli.ListCalendars(ctx)
	if err != nil {
		return fmt.Errorf("list calendars: %w", err)
	}
	for _, e := range entries {
		calID, err := s.Calendars.Upsert(ctx, accountID, e.Id, e.Summary, e.TimeZone, e.BackgroundColor)
		if err != nil {
			s.Log.Warn("calendar upsert failed", "google_id", e.Id, "err", err)
			continue
		}
		if e.Primary {
			_ = s.Accounts.SetPrimaryCalendar(ctx, accountID, e.Id)
		}
		if err := s.Worker.RegisterWatch(ctx, accountID, calID); err != nil {
			s.Log.Warn("watch register failed", "cal_id", calID, "err", err)
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// Sync rules
// ---------------------------------------------------------------------------

type ruleRow struct {
	Rule        db.SyncRule
	SourceLabel string
	TargetLabel string
}

type calendarOption struct {
	ID           int64
	Summary      string
	AccountEmail string
	Enabled      bool
}

// Label is what selectors render — appends "(disabled)" so a user editing an
// existing rule knows the referenced calendar is dormant.
func (o calendarOption) Label() string {
	s := o.AccountEmail + " · " + o.Summary
	if !o.Enabled {
		s += " (disabled)"
	}
	return s
}

func (s *Server) calendarOptions(ctx context.Context) ([]calendarOption, map[int64]calendarOption, error) {
	accounts, err := s.Accounts.List(ctx)
	if err != nil {
		return nil, nil, err
	}
	byAcct := map[int64]string{}
	for _, a := range accounts {
		byAcct[a.ID] = a.Email
	}
	cals, err := s.Calendars.ListAll(ctx)
	if err != nil {
		return nil, nil, err
	}
	out := make([]calendarOption, 0, len(cals))
	idx := map[int64]calendarOption{}
	for _, c := range cals {
		opt := calendarOption{ID: c.ID, Summary: c.Summary, AccountEmail: byAcct[c.AccountID], Enabled: c.Enabled}
		out = append(out, opt)
		idx[c.ID] = opt
	}
	return out, idx, nil
}

func (s *Server) handleRulesPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	rules, err := s.Rules.List(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	_, idx, err := s.calendarOptions(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	rows := make([]ruleRow, 0, len(rules))
	for _, ru := range rules {
		rows = append(rows, ruleRow{
			Rule:        ru,
			SourceLabel: calLabel(idx, ru.SourceCalendarID),
			TargetLabel: calLabel(idx, ru.TargetCalendarID),
		})
	}
	data := s.pageData(r, "Rules")
	data["Rules"] = rows
	s.render(w, "rules", data)
}

func calLabel(idx map[int64]calendarOption, id int64) string {
	c, ok := idx[id]
	if !ok {
		return fmt.Sprintf("[missing #%d]", id)
	}
	return c.AccountEmail + " · " + c.Summary
}

func (s *Server) handleRuleEditPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	rule := &db.SyncRule{
		Direction:      "one_way",
		PrimarySide:    "source",
		Enabled:        true,
		VisibilityMode: "busy_for_all",
		AllDayMode:     "sync_all",
	}
	if idStr := chi.URLParam(r, "id"); idStr != "" {
		id, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil {
			http.Error(w, "bad id", http.StatusBadRequest)
			return
		}
		got, err := s.Rules.Get(ctx, id)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if got == nil {
			http.NotFound(w, r)
			return
		}
		rule = got
	}
	filter, _ := syncengine.ParseFilter(rule.Filter)
	transform, _ := syncengine.ParseTransform(rule.Transform)
	cals, _, err := s.calendarOptions(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	cats, err := s.Categories.List(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	data := s.pageData(r, "Rule")
	data["Rule"] = rule
	data["Filter"] = filter
	data["Transform"] = transform
	data["Calendars"] = cals
	data["Categories"] = cats
	s.render(w, "rule_edit", data)
}

func (s *Server) handleRuleSave(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	idStr := chi.URLParam(r, "id")
	rule := &db.SyncRule{}
	if idStr != "" {
		id, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil {
			http.Error(w, "bad id", http.StatusBadRequest)
			return
		}
		got, err := s.Rules.Get(r.Context(), id)
		if err != nil || got == nil {
			http.NotFound(w, r)
			return
		}
		rule = got
	}

	rule.Name = strings.TrimSpace(r.FormValue("name"))
	rule.SourceCalendarID = parseInt64(r.FormValue("source_calendar_id"))
	rule.TargetCalendarID = parseInt64(r.FormValue("target_calendar_id"))
	rule.Direction = strOr(r.FormValue("direction"), "one_way")
	rule.PrimarySide = strOr(r.FormValue("primary_side"), "source")
	rule.BackfillDays = int(parseInt64(r.FormValue("backfill_days")))
	rule.Enabled = r.FormValue("enabled") != ""
	rule.VisibilityMode = strOr(r.FormValue("visibility_mode"), "busy_for_all")
	rule.AllDayMode = strOr(r.FormValue("all_day_mode"), "sync_all")
	rule.WorkingHoursOnly = r.FormValue("working_hours_only") != ""
	if catID := parseInt64(r.FormValue("category_id")); catID > 0 {
		rule.CategoryID = &catID
	} else {
		rule.CategoryID = nil
	}

	filter := syncengine.Filter{
		TitleRegex:  strings.TrimSpace(r.FormValue("filter_title_regex")),
		ColorIDs:    splitCSV(r.FormValue("filter_color_ids")),
		AttendeeAny: splitCSV(r.FormValue("filter_attendees")),
		FreeBusy:    r.FormValue("filter_free_busy"),
		StartHour:   int(parseInt64(r.FormValue("filter_start_hour"))),
		EndHour:     int(parseInt64(r.FormValue("filter_end_hour"))),
	}
	// Visibility mode now drives the transform; legacy Transform JSON is no
	// longer written from the form. We store an empty {} so older readers
	// still work.
	fb, _ := json.Marshal(filter)
	rule.Filter = fb
	rule.Transform = json.RawMessage(`{}`)

	if rule.Name == "" || rule.SourceCalendarID == 0 || rule.TargetCalendarID == 0 {
		http.Error(w, "name, source, and target are required", http.StatusBadRequest)
		return
	}
	if rule.SourceCalendarID == rule.TargetCalendarID {
		http.Error(w, "source and target must differ", http.StatusBadRequest)
		return
	}

	if rule.ID == 0 {
		newID, err := s.Rules.Create(r.Context(), rule)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		rule.ID = newID
	} else {
		if err := s.Rules.Update(r.Context(), rule); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	http.Redirect(w, r, "/rules", http.StatusFound)
}

func (s *Server) handleRuleDelete(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	if err := s.Rules.Delete(r.Context(), id); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/rules", http.StatusFound)
}

func (s *Server) handleRuleSyncNow(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	rule, err := s.Rules.Get(r.Context(), id)
	if err != nil || rule == nil {
		http.NotFound(w, r)
		return
	}
	cal, err := s.Calendars.Get(r.Context(), rule.SourceCalendarID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.Worker.EnqueueCalendar(cal.AccountID, cal.ID)
	http.Redirect(w, r, "/rules", http.StatusFound)
}

func (s *Server) handleRuleBackfill(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	go func(ruleID int64) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		if err := s.Engine.Backfill(ctx, ruleID); err != nil {
			s.Log.Error("backfill failed", "rule_id", ruleID, "err", err)
		}
	}(id)
	http.Redirect(w, r, "/rules", http.StatusFound)
}

// ---------------------------------------------------------------------------
// Smart blocks
// ---------------------------------------------------------------------------

type blockRow struct {
	Block        db.SmartBlock
	TargetLabel  string
	SourcesLabel string
}

func (s *Server) handleBlocksPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	blocks, err := s.Blocks.List(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	_, idx, err := s.calendarOptions(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	rows := make([]blockRow, 0, len(blocks))
	for _, b := range blocks {
		srcs := make([]string, 0, len(b.SourceCalendarIDs))
		for _, id := range b.SourceCalendarIDs {
			srcs = append(srcs, calLabel(idx, id))
		}
		rows = append(rows, blockRow{
			Block:        b,
			TargetLabel:  calLabel(idx, b.TargetCalendarID),
			SourcesLabel: strings.Join(srcs, ", "),
		})
	}
	data := s.pageData(r, "Smart blocks")
	data["Blocks"] = rows
	s.render(w, "blocks", data)
}

var weekDays = []struct {
	Key   string
	Label string
}{
	{"mon", "Monday"}, {"tue", "Tuesday"}, {"wed", "Wednesday"},
	{"thu", "Thursday"}, {"fri", "Friday"}, {"sat", "Saturday"}, {"sun", "Sunday"},
}

func (s *Server) handleBlockEditPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	block := &db.SmartBlock{
		HorizonDays:     30,
		MinBlockMinutes: 30,
		MergeGapMinutes: 15,
		TitleTemplate:   "Focus",
		Enabled:         true,
	}
	if idStr := chi.URLParam(r, "id"); idStr != "" {
		id, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil {
			http.Error(w, "bad id", http.StatusBadRequest)
			return
		}
		got, err := s.Blocks.Get(ctx, id)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if got == nil {
			http.NotFound(w, r)
			return
		}
		block = got
	}
	wh, _ := hours.Parse(block.WorkingHours)
	if wh.Days == nil {
		wh.Days = map[string][]string{}
	}
	for _, d := range weekDays {
		if _, ok := wh.Days[d.Key]; !ok {
			wh.Days[d.Key] = []string{}
		}
	}
	cals, _, err := s.calendarOptions(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	srcSet := map[int64]bool{}
	for _, id := range block.SourceCalendarIDs {
		srcSet[id] = true
	}
	data := s.pageData(r, "Smart block")
	data["Block"] = block
	data["WH"] = wh
	data["Calendars"] = cals
	data["SourceSet"] = srcSet
	data["Days"] = weekDays
	s.render(w, "block_edit", data)
}

func (s *Server) handleBlockSave(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	block := &db.SmartBlock{}
	if idStr := chi.URLParam(r, "id"); idStr != "" {
		id, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil {
			http.Error(w, "bad id", http.StatusBadRequest)
			return
		}
		got, err := s.Blocks.Get(r.Context(), id)
		if err != nil || got == nil {
			http.NotFound(w, r)
			return
		}
		block = got
	}
	block.Name = strings.TrimSpace(r.FormValue("name"))
	block.TargetCalendarID = parseInt64(r.FormValue("target_calendar_id"))
	srcRaw := r.Form["source_calendar_ids"]
	srcIDs := make([]int64, 0, len(srcRaw))
	for _, v := range srcRaw {
		if id := parseInt64(v); id != 0 {
			srcIDs = append(srcIDs, id)
		}
	}
	block.SourceCalendarIDs = srcIDs
	block.HorizonDays = int(parseInt64(r.FormValue("horizon_days")))
	if block.HorizonDays <= 0 {
		block.HorizonDays = 30
	}
	block.MinBlockMinutes = int(parseInt64(r.FormValue("min_block_minutes")))
	if block.MinBlockMinutes <= 0 {
		block.MinBlockMinutes = 30
	}
	block.MergeGapMinutes = int(parseInt64(r.FormValue("merge_gap_minutes")))
	if block.MergeGapMinutes < 0 {
		block.MergeGapMinutes = 0
	}
	block.TitleTemplate = strOr(strings.TrimSpace(r.FormValue("title_template")), "Focus")
	block.Enabled = r.FormValue("enabled") != ""

	wh := hours.WorkingHours{
		TimeZone: strOr(strings.TrimSpace(r.FormValue("wh_tz")), "UTC"),
		Days:     map[string][]string{},
	}
	for _, d := range weekDays {
		raw := strings.TrimSpace(r.FormValue("wh_" + d.Key))
		ranges := []string{}
		for _, p := range strings.Split(raw, ",") {
			if p = strings.TrimSpace(p); p != "" {
				ranges = append(ranges, p)
			}
		}
		wh.Days[d.Key] = ranges
	}
	whb, _ := json.Marshal(wh)
	block.WorkingHours = whb

	if block.Name == "" || block.TargetCalendarID == 0 || len(block.SourceCalendarIDs) == 0 {
		http.Error(w, "name, target, and at least one source are required", http.StatusBadRequest)
		return
	}

	if block.ID == 0 {
		newID, err := s.Blocks.Create(r.Context(), block)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		block.ID = newID
	} else {
		if err := s.Blocks.Update(r.Context(), block); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	http.Redirect(w, r, "/blocks", http.StatusFound)
}

func (s *Server) handleBlockDelete(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	if err := s.Blocks.Delete(r.Context(), id); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/blocks", http.StatusFound)
}

func (s *Server) handleBlockRecompute(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	go func(blockID int64) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		if err := s.Worker.RecomputeBlock(ctx, blockID); err != nil {
			s.Log.Error("recompute failed", "block_id", blockID, "err", err)
		}
	}(id)
	http.Redirect(w, r, "/blocks", http.StatusFound)
}

// ---------------------------------------------------------------------------
// Audit / settings
// ---------------------------------------------------------------------------

func (s *Server) handleAuditPage(w http.ResponseWriter, r *http.Request) {
	entries, err := s.Audit.Recent(r.Context(), 200)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	data := s.pageData(r, "Audit log")
	data["Entries"] = entries
	s.render(w, "audit", data)
}

func (s *Server) handleSettingsPage(w http.ResponseWriter, r *http.Request) {
	owner, _ := s.TOFU.OwnerEmail(r.Context())
	data := s.pageData(r, "Settings")
	data["OwnerEmail"] = owner
	data["ExternalURL"] = s.Cfg.ExternalURL
	s.render(w, "settings", data)
}

// ---------------------------------------------------------------------------
// Categories
// ---------------------------------------------------------------------------

func (s *Server) handleCategoriesPage(w http.ResponseWriter, r *http.Request) {
	cats, err := s.Categories.List(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	data := s.pageData(r, "Categories")
	data["Categories"] = cats
	s.render(w, "categories", data)
}

func (s *Server) handleCategoriesSave(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	cats, err := s.Categories.List(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	for _, c := range cats {
		idStr := strconv.FormatInt(c.ID, 10)
		name := strings.TrimSpace(r.FormValue("name_" + idStr))
		color := strings.TrimSpace(r.FormValue("color_" + idStr))
		if name == "" {
			name = c.Name
		}
		if color == "" {
			color = c.Color
		}
		if name == c.Name && color == c.Color {
			continue
		}
		if err := s.Categories.UpdateAppearance(r.Context(), c.ID, name, color); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	http.Redirect(w, r, "/settings/categories", http.StatusFound)
}

func (s *Server) handleRewatchAll(w http.ResponseWriter, r *http.Request) {
	cals, err := s.Calendars.ListAll(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	go func(cs []db.Calendar) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		for _, c := range cs {
			if err := s.Worker.RegisterWatch(ctx, c.AccountID, c.ID); err != nil {
				s.Log.Warn("rewatch failed", "cal_id", c.ID, "err", err)
			}
		}
	}(cals)
	http.Redirect(w, r, "/settings", http.StatusFound)
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func parseInt64(s string) int64 {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	v, _ := strconv.ParseInt(s, 10, 64)
	return v
}

func strOr(v, def string) string {
	if strings.TrimSpace(v) == "" {
		return def
	}
	return v
}

func splitCSV(s string) []string {
	out := []string{}
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

