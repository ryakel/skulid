package db

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// The tests in this file exist for one reason: every repo is hand-written SQL
// with no compile-time checking, so a column added to a table but missed in a
// select list is a runtime failure on every read. Each test drives a repo's
// write path and then every one of its read paths, which is what makes that
// class of mistake fail here instead of in production.

func TestAccountRepoRoundTrip(t *testing.T) {
	pool := testDB(t)
	ctx := context.Background()
	r := NewAccountRepo(pool)

	expires := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
	id, err := r.Upsert(ctx, "sub-a", "a@example.com", "refresh-sealed", "access-sealed", &expires)
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}

	got, err := r.Get(ctx, id)
	if err != nil || got == nil {
		t.Fatalf("get: %v (got %v)", err, got)
	}
	if got.Email != "a@example.com" || got.GoogleSub != "sub-a" {
		t.Errorf("round trip lost identity: %+v", got)
	}
	if got.RefreshTokenSealed != "refresh-sealed" || got.AccessTokenSealed != "access-sealed" {
		t.Errorf("round trip lost tokens: %+v", got)
	}
	if got.AccessTokenExpiresAt == nil || !got.AccessTokenExpiresAt.Equal(expires) {
		t.Errorf("expiry = %v, want %v", got.AccessTokenExpiresAt, expires)
	}
	if got.NeedsReauth || got.AIExcluded {
		t.Errorf("a fresh account should be neither locked out nor AI-excluded: %+v", got)
	}

	bySub, err := r.GetBySub(ctx, "sub-a")
	if err != nil || bySub == nil || bySub.ID != id {
		t.Fatalf("GetBySub: %v (got %v)", err, bySub)
	}
	// AccountRepo.Get is the one reader that returns pgx.ErrNoRows rather than
	// the repo convention of (nil, nil) -- GetBySub right beside it does the
	// convention. Pinned here rather than fixed, because auth/tokensource.go
	// dereferences the result without a nil check and would panic instead of
	// erroring. Tracked as SKUL-22.
	if _, err := r.Get(ctx, id+9999); !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("missing account: got err %v, want pgx.ErrNoRows", err)
	}
	if missing, err := r.GetBySub(ctx, "no-such-sub"); err != nil || missing != nil {
		t.Errorf("missing sub should be (nil, nil), got (%v, %v)", missing, err)
	}

	// SKUL-1's columns: they must survive a write and come back through the
	// select list, which is the exact thing a missed column breaks.
	if err := r.MarkNeedsReauth(ctx, id, "refresh token revoked"); err != nil {
		t.Fatalf("MarkNeedsReauth: %v", err)
	}
	locked, err := r.ListNeedsReauth(ctx)
	if err != nil {
		t.Fatalf("ListNeedsReauth: %v", err)
	}
	if len(locked) != 1 || locked[0].ID != id {
		t.Fatalf("want the one locked-out account, got %+v", locked)
	}
	if locked[0].ReauthReason != "refresh token revoked" || locked[0].ReauthDetectedAt == nil {
		t.Errorf("reauth detail lost: %+v", locked[0])
	}

	// SKUL-2's column.
	if err := r.SetAIExcluded(ctx, id, true); err != nil {
		t.Fatalf("SetAIExcluded: %v", err)
	}
	if got, _ := r.Get(ctx, id); got == nil || !got.AIExcluded {
		t.Errorf("ai_excluded did not round trip: %+v", got)
	}

	// Hours are nullable JSON; both the set and the cleared state must read.
	hours := json.RawMessage(`{"time_zone":"UTC","days":{"mon":[["09:00","17:00"]]}}`)
	if err := r.UpdateHours(ctx, id, hours, nil, nil); err != nil {
		t.Fatalf("UpdateHours: %v", err)
	}
	got, _ = r.Get(ctx, id)
	if len(got.WorkingHours) == 0 {
		t.Error("working hours did not round trip")
	}
	if len(got.PersonalHours) != 0 {
		t.Errorf("cleared personal hours should read empty, got %s", got.PersonalHours)
	}

	all, err := r.List(ctx)
	if err != nil || len(all) != 1 {
		t.Fatalf("List: %v (got %d)", err, len(all))
	}
}

func TestCalendarRepoRoundTrip(t *testing.T) {
	pool := testDB(t)
	ctx := context.Background()
	accountID, calendarID := seedCalendar(t, pool)
	r := NewCalendarRepo(pool)

	got, err := r.Get(ctx, calendarID)
	if err != nil || got == nil {
		t.Fatalf("get: %v (got %v)", err, got)
	}
	if got.Summary != "Primary" || got.TimeZone != "UTC" || got.Color != "#ff0000" {
		t.Errorf("round trip lost fields: %+v", got)
	}
	// Discovery must not switch calendars on behind the owner's back.
	if got.Enabled {
		t.Error("a newly discovered calendar should arrive disabled")
	}

	if err := r.SetEnabled(ctx, calendarID, true); err != nil {
		t.Fatalf("SetEnabled: %v", err)
	}
	if err := r.UpdateBuffers(ctx, calendarID, "15,20,25"); err != nil {
		t.Fatalf("UpdateBuffers: %v", err)
	}
	got, _ = r.Get(ctx, calendarID)
	if !got.Enabled {
		t.Error("enabled did not round trip")
	}
	if got.Buffers != "15,20,25" {
		t.Errorf("buffers = %q, want \"15,20,25\"", got.Buffers)
	}

	// Re-running discovery updates the label but must leave `enabled` alone.
	if _, err := r.Upsert(ctx, accountID, "primary", "Renamed", "UTC", "#00ff00"); err != nil {
		t.Fatalf("re-upsert: %v", err)
	}
	got, _ = r.Get(ctx, calendarID)
	if got.Summary != "Renamed" {
		t.Errorf("summary = %q, want the rediscovered name", got.Summary)
	}
	if !got.Enabled {
		t.Error("re-running discovery must not disable a calendar the owner turned on")
	}

	byAccount, err := r.ListByAccount(ctx, accountID)
	if err != nil || len(byAccount) != 1 {
		t.Fatalf("ListByAccount: %v (got %d)", err, len(byAccount))
	}
	enabled, err := r.ListEnabledByAccount(ctx, accountID)
	if err != nil || len(enabled) != 1 {
		t.Fatalf("ListEnabledByAccount: %v (got %d)", err, len(enabled))
	}
	all, err := r.ListAll(ctx)
	if err != nil || len(all) != 1 {
		t.Fatalf("ListAll: %v (got %d)", err, len(all))
	}
}

func TestSyncRuleRepoRoundTrip(t *testing.T) {
	pool := testDB(t)
	ctx := context.Background()
	_, calendarID := seedCalendar(t, pool)
	r := NewSyncRuleRepo(pool)

	id, err := r.Create(ctx, &SyncRule{
		Name:             "work to personal",
		SourceCalendarID: calendarID,
		TargetCalendarID: calendarID,
		Direction:        "one_way",
		PrimarySide:      "source",
		BackfillDays:     30,
		Enabled:          true,
		WorkingHoursOnly: true,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	got, err := r.Get(ctx, id)
	if err != nil || got == nil {
		t.Fatalf("get: %v (got %v)", err, got)
	}
	if got.Name != "work to personal" || got.BackfillDays != 30 || !got.WorkingHoursOnly {
		t.Errorf("round trip lost fields: %+v", got)
	}
	// Create fills these in when the caller leaves them blank.
	if got.VisibilityMode != "busy_for_all" || got.AllDayMode != "sync_all" {
		t.Errorf("defaults not applied: mode=%q allday=%q", got.VisibilityMode, got.AllDayMode)
	}
	if got.BackfillDone {
		t.Error("a new rule should not be marked backfilled")
	}

	// The MarkBackfillDone / ResetBackfill pair behind the Re-run button.
	if err := r.MarkBackfillDone(ctx, id); err != nil {
		t.Fatalf("MarkBackfillDone: %v", err)
	}
	if got, _ := r.Get(ctx, id); got == nil || !got.BackfillDone {
		t.Fatalf("backfill_done did not stick: %+v", got)
	}
	if err := r.ResetBackfill(ctx, id); err != nil {
		t.Fatalf("ResetBackfill: %v", err)
	}
	if got, _ := r.Get(ctx, id); got == nil || got.BackfillDone {
		t.Fatalf("ResetBackfill did not clear the flag: %+v", got)
	}

	bySource, err := r.ListBySourceCalendar(ctx, calendarID)
	if err != nil || len(bySource) != 1 {
		t.Fatalf("ListBySourceCalendar: %v (got %d)", err, len(bySource))
	}
	all, err := r.List(ctx)
	if err != nil || len(all) != 1 {
		t.Fatalf("List: %v (got %d)", err, len(all))
	}
}

func TestTaskRepoAndChunksRoundTrip(t *testing.T) {
	pool := testDB(t)
	ctx := context.Background()
	_, calendarID := seedCalendar(t, pool)
	tasks := NewTaskRepo(pool)
	chunks := NewTaskChunkRepo(pool)

	due := time.Now().Add(48 * time.Hour).UTC().Truncate(time.Second)
	id, err := tasks.Create(ctx, &Task{
		Title:            "Write the report",
		Notes:            "the long one",
		Priority:         PriorityHigh,
		DurationMinutes:  240,
		DueAt:            &due,
		TargetCalendarID: calendarID,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	got, err := tasks.Get(ctx, id)
	if err != nil || got == nil {
		t.Fatalf("get: %v (got %v)", err, got)
	}
	if got.Title != "Write the report" || got.DurationMinutes != 240 {
		t.Errorf("round trip lost fields: %+v", got)
	}
	if got.Status != TaskPending || got.ScheduleNote != "" {
		t.Errorf("a new task should be pending with no note: %+v", got)
	}

	// A task split across two blocks, as the scheduler writes it.
	start := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
	windows := [][2]time.Time{
		{start, start.Add(2 * time.Hour)},
		{start.Add(4 * time.Hour), start.Add(6 * time.Hour)},
	}
	for i, w := range windows {
		if _, err := chunks.Insert(ctx, &TaskChunk{
			TaskID: id, Seq: i, GoogleEventID: "ev-" + string(rune('a'+i)),
			StartsAt: w[0], EndsAt: w[1],
		}); err != nil {
			t.Fatalf("inserting chunk %d: %v", i, err)
		}
	}
	if err := tasks.UpdateScheduled(ctx, id, "ev-a", &windows[0][0], &windows[0][1], TaskScheduled, ""); err != nil {
		t.Fatalf("UpdateScheduled: %v", err)
	}

	list, err := chunks.ListByTask(ctx, id)
	if err != nil || len(list) != 2 {
		t.Fatalf("ListByTask: %v (got %d)", err, len(list))
	}
	if list[0].Seq != 0 || list[1].Seq != 1 {
		t.Errorf("chunks must come back in seq order, got %d then %d", list[0].Seq, list[1].Seq)
	}

	// The summary columns must track the first chunk, since that is what the
	// tasks list, the planner and the AI tools read.
	got, _ = tasks.Get(ctx, id)
	if got.ScheduledEventID != "ev-a" || got.ScheduledStartsAt == nil || !got.ScheduledStartsAt.Equal(windows[0][0]) {
		t.Errorf("summary columns don't match the first chunk: %+v", got)
	}

	// The reason a task isn't placed has to survive a write, or the whole
	// point of surfacing it is lost.
	if err := tasks.UpdateScheduled(ctx, id, "", nil, nil, TaskPending, "Only 3h of the 4h needed would fit."); err != nil {
		t.Fatalf("UpdateScheduled (unschedule): %v", err)
	}
	got, _ = tasks.Get(ctx, id)
	if got.ScheduleNote != "Only 3h of the 4h needed would fit." {
		t.Errorf("schedule_note = %q", got.ScheduleNote)
	}
	if got.ScheduledStartsAt != nil {
		t.Errorf("unscheduling should clear the window, got %v", got.ScheduledStartsAt)
	}

	if _, err := tasks.List(ctx); err != nil {
		t.Errorf("List: %v", err)
	}
	if _, err := tasks.ListUnscheduled(ctx); err != nil {
		t.Errorf("ListUnscheduled: %v", err)
	}
	if _, err := tasks.ListAllActive(ctx); err != nil {
		t.Errorf("ListAllActive: %v", err)
	}

	// Deleting the task must take its chunks with it, or the scheduler's
	// managed-window query keeps seeing blocks for a task that no longer
	// exists.
	if err := tasks.Delete(ctx, id); err != nil {
		t.Fatalf("delete: %v", err)
	}
	orphans, err := chunks.ListByTask(ctx, id)
	if err != nil {
		t.Fatalf("ListByTask after delete: %v", err)
	}
	if len(orphans) != 0 {
		t.Errorf("task_chunk rows outlived their task: %+v", orphans)
	}
}

func TestBufferEventRepoRoundTrip(t *testing.T) {
	pool := testDB(t)
	ctx := context.Background()
	_, calendarID := seedCalendar(t, pool)
	r := NewBufferEventRepo(pool)

	start := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
	// One meeting owning travel-before, decompress, and travel-back: the case
	// the old UNIQUE (calendar_id, source_event_id) could not represent.
	want := []BufferEvent{
		{SourceEventID: "meeting-1", TargetEventID: "buf-1", BufferType: BufferTravel, Placement: PlacementBefore,
			StartsAt: start.Add(-20 * time.Minute), EndsAt: start},
		{SourceEventID: "meeting-1", TargetEventID: "buf-2", BufferType: BufferDecompression, Placement: PlacementAfter,
			StartsAt: start.Add(time.Hour), EndsAt: start.Add(75 * time.Minute)},
		{SourceEventID: "meeting-1", TargetEventID: "buf-3", BufferType: BufferTravel, Placement: PlacementAfter,
			StartsAt: start.Add(75 * time.Minute), EndsAt: start.Add(95 * time.Minute)},
	}
	for i := range want {
		want[i].CalendarID = calendarID
		if _, err := r.Insert(ctx, &want[i]); err != nil {
			t.Fatalf("inserting buffer %d: %v", i, err)
		}
	}

	got, err := r.Get(ctx, calendarID, BufferKey{
		SourceEventID: "meeting-1", BufferType: BufferTravel, Placement: PlacementBefore,
	})
	if err != nil || got == nil {
		t.Fatalf("Get: %v (got %v)", err, got)
	}
	if got.TargetEventID != "buf-1" {
		t.Errorf("Get returned the wrong buffer: %+v", got)
	}

	missing, err := r.Get(ctx, calendarID, BufferKey{
		SourceEventID: "nope", BufferType: BufferTravel, Placement: PlacementBefore,
	})
	if err != nil || missing != nil {
		t.Errorf("a missing buffer should be (nil, nil), got (%v, %v)", missing, err)
	}

	inRange, err := r.ListByCalendarInRange(ctx, calendarID, start.Add(-time.Hour), start.Add(2*time.Hour))
	if err != nil || len(inRange) != 3 {
		t.Fatalf("ListByCalendarInRange: %v (got %d)", err, len(inRange))
	}
	// Key() is what the reconciler maps on; three buffers on one meeting must
	// produce three distinct keys.
	keys := map[BufferKey]bool{}
	for _, b := range inRange {
		if keys[b.Key()] {
			t.Fatalf("duplicate key %+v", b.Key())
		}
		keys[b.Key()] = true
	}

	if err := r.UpdateWindow(ctx, inRange[0].ID, start, start.Add(30*time.Minute)); err != nil {
		t.Fatalf("UpdateWindow: %v", err)
	}
	if err := r.Delete(ctx, inRange[0].ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	left, _ := r.ListByCalendarInRange(ctx, calendarID, start.Add(-time.Hour), start.Add(2*time.Hour))
	if len(left) != 2 {
		t.Errorf("after deleting one, want 2 left, got %d", len(left))
	}
}

func TestManagedWindowRepoExcludesTravel(t *testing.T) {
	pool := testDB(t)
	ctx := context.Background()
	_, calendarID := seedCalendar(t, pool)

	start := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
	buffers := NewBufferEventRepo(pool)
	for _, b := range []BufferEvent{
		{CalendarID: calendarID, SourceEventID: "m1", TargetEventID: "d1",
			BufferType: BufferDecompression, Placement: PlacementAfter,
			StartsAt: start, EndsAt: start.Add(15 * time.Minute)},
		{CalendarID: calendarID, SourceEventID: "m1", TargetEventID: "t1",
			BufferType: BufferTravel, Placement: PlacementAfter,
			StartsAt: start.Add(15 * time.Minute), EndsAt: start.Add(35 * time.Minute)},
	} {
		if _, err := buffers.Insert(ctx, &b); err != nil {
			t.Fatalf("inserting buffer: %v", err)
		}
	}

	got, err := NewManagedWindowRepo(pool).InRange(ctx, start.Add(-time.Hour), start.Add(2*time.Hour))
	if err != nil {
		t.Fatalf("InRange: %v", err)
	}
	// The decompression block is subtracted from busy because the scheduler
	// already pads the source meeting by the same minutes. Travel has no such
	// padding behind it, so it must keep reading as busy -- which means it
	// must NOT appear here.
	if len(got) != 1 {
		t.Fatalf("want only the decompression window, got %d: %+v", len(got), got)
	}
	if !got[0].StartsAt.Equal(start) || !got[0].EndsAt.Equal(start.Add(15*time.Minute)) {
		t.Errorf("wrong window came back: %+v", got[0])
	}
}

func TestManagedWindowRepoIncludesEveryTaskChunk(t *testing.T) {
	pool := testDB(t)
	ctx := context.Background()
	_, calendarID := seedCalendar(t, pool)

	id, err := NewTaskRepo(pool).Create(ctx, &Task{
		Title: "Split task", DurationMinutes: 240, TargetCalendarID: calendarID,
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	start := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
	chunks := NewTaskChunkRepo(pool)
	for i := 0; i < 3; i++ {
		off := time.Duration(i*3) * time.Hour
		if _, err := chunks.Insert(ctx, &TaskChunk{
			TaskID: id, Seq: i, GoogleEventID: "ev",
			StartsAt: start.Add(off), EndsAt: start.Add(off + 2*time.Hour),
		}); err != nil {
			t.Fatalf("inserting chunk: %v", err)
		}
	}

	got, err := NewManagedWindowRepo(pool).InRange(ctx, start.Add(-time.Hour), start.Add(24*time.Hour))
	if err != nil {
		t.Fatalf("InRange: %v", err)
	}
	// Reading only task.scheduled_* would return one window and leave the
	// task's own later blocks reading as busy against itself.
	if len(got) != 3 {
		t.Fatalf("want all 3 chunks, got %d: %+v", len(got), got)
	}
}

func TestSettingRepoRoundTrip(t *testing.T) {
	pool := testDB(t)
	ctx := context.Background()
	r := NewSettingRepo(pool)

	if _, ok, err := r.Get(ctx, SettingPlannerDayWindow); err != nil || ok {
		t.Fatalf("an unset key should be (\"\", false, nil), got ok=%v err=%v", ok, err)
	}
	if err := r.Set(ctx, SettingPlannerDayWindow, "5,23"); err != nil {
		t.Fatalf("set: %v", err)
	}
	v, ok, err := r.Get(ctx, SettingPlannerDayWindow)
	if err != nil || !ok || v != "5,23" {
		t.Fatalf("get after set: %q ok=%v err=%v", v, ok, err)
	}
	// Set is an upsert; a second write must replace rather than conflict.
	if err := r.Set(ctx, SettingPlannerDayWindow, "6,22"); err != nil {
		t.Fatalf("re-set: %v", err)
	}
	if v, _, _ := r.Get(ctx, SettingPlannerDayWindow); v != "6,22" {
		t.Errorf("second write did not take: %q", v)
	}

	// The helpers layered on top of the raw key/value store.
	if got := TaskMinChunkMinutes(ctx, r); got != DefaultTaskMinChunkMinutes {
		t.Errorf("unset min chunk = %d, want the default %d", got, DefaultTaskMinChunkMinutes)
	}
	if err := r.Set(ctx, SettingTaskMinChunkMinutes, "45"); err != nil {
		t.Fatalf("set min chunk: %v", err)
	}
	if got := TaskMinChunkMinutes(ctx, r); got != 45 {
		t.Errorf("min chunk = %d, want 45", got)
	}
	// A garbage value must fall back rather than produce a zero minimum, which
	// would let a task shatter into arbitrarily small pieces.
	if err := r.Set(ctx, SettingTaskMinChunkMinutes, "not a number"); err != nil {
		t.Fatalf("set garbage: %v", err)
	}
	if got := TaskMinChunkMinutes(ctx, r); got != DefaultTaskMinChunkMinutes {
		t.Errorf("garbage min chunk = %d, want the default", got)
	}

	if err := SaveBuffers(ctx, r, BufferSettings{
		TaskHabitBreakMinutes: 10, DecompressionMinutes: 15, TravelMinutes: 20,
	}); err != nil {
		t.Fatalf("SaveBuffers: %v", err)
	}
	if got := LoadBuffers(ctx, r); got.TravelMinutes != 20 || got.DecompressionMinutes != 15 {
		t.Errorf("buffers did not round trip: %+v", got)
	}
}

func TestEventLinkRepoForwardAndReverseCoexist(t *testing.T) {
	pool := testDB(t)
	ctx := context.Background()
	accountID, calendarID := seedCalendar(t, pool)

	ruleID, err := NewSyncRuleRepo(pool).Create(ctx, &SyncRule{
		Name: "bidi", SourceCalendarID: calendarID, TargetCalendarID: calendarID,
		Direction: "bidirectional", PrimarySide: "source", Enabled: true,
	})
	if err != nil {
		t.Fatalf("create rule: %v", err)
	}

	r := NewEventLinkRepo(pool)
	link := func(key string) *EventLink {
		return &EventLink{
			RuleID:          ruleID,
			SourceAccountID: accountID, SourceCalendarID: calendarID, SourceEventID: key,
			TargetAccountID: accountID, TargetCalendarID: calendarID, TargetEventID: "mirror-" + key,
			SourceEtag: "etag-1",
		}
	}
	// A bidirectional rule stores the reverse pass under a synthetic
	// "rev:" key. Both must be able to exist at once, or the unique index
	// turns every reverse sync into a conflict.
	for _, key := range []string{"src-event", "rev:src-event"} {
		if _, err := r.Upsert(ctx, link(key)); err != nil {
			t.Fatalf("upsert %q: %v", key, err)
		}
	}

	links, err := r.ListByRule(ctx, ruleID)
	if err != nil || len(links) != 2 {
		t.Fatalf("ListByRule: %v (got %d)", err, len(links))
	}

	fwd, err := r.Get(ctx, ruleID, "src-event")
	if err != nil || fwd == nil || fwd.TargetEventID != "mirror-src-event" {
		t.Fatalf("forward link: %v (got %v)", err, fwd)
	}
	rev, err := r.Get(ctx, ruleID, "rev:src-event")
	if err != nil || rev == nil || rev.TargetEventID != "mirror-rev:src-event" {
		t.Fatalf("reverse link: %v (got %v)", err, rev)
	}
	if fwd.ID == rev.ID {
		t.Error("forward and reverse collided on one row")
	}

	// Upsert must update the etag in place -- that is what the bidirectional
	// dedup compares against to break the webhook loop.
	updated := link("src-event")
	updated.SourceEtag = "etag-2"
	if _, err := r.Upsert(ctx, updated); err != nil {
		t.Fatalf("re-upsert: %v", err)
	}
	fwd, _ = r.Get(ctx, ruleID, "src-event")
	if fwd == nil || fwd.SourceEtag != "etag-2" {
		t.Errorf("etag did not update: %+v", fwd)
	}
	if again, _ := r.ListByRule(ctx, ruleID); len(again) != 2 {
		t.Errorf("re-upsert added a row instead of updating: %d", len(again))
	}

	if err := r.DeleteByRuleAndSource(ctx, ruleID, "rev:src-event"); err != nil {
		t.Fatalf("DeleteByRuleAndSource: %v", err)
	}
	if left, _ := r.ListByRule(ctx, ruleID); len(left) != 1 {
		t.Errorf("want 1 link left, got %d", len(left))
	}
}

func TestAuditRepoRetention(t *testing.T) {
	pool := testDB(t)
	ctx := context.Background()
	r := NewAuditRepo(pool)

	for i := 0; i < 3; i++ {
		if err := r.Write(ctx, AuditWrite{Kind: "task", Action: "scheduled", Message: "one"}); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	recent, err := r.Recent(ctx, 10)
	if err != nil || len(recent) != 3 {
		t.Fatalf("Recent: %v (got %d)", err, len(recent))
	}

	// Age one row past the cutoff and prove the sweep takes it.
	if _, err := pool.Exec(ctx,
		`UPDATE audit_log SET ts = NOW() - INTERVAL '90 days' WHERE id = $1`, recent[0].ID); err != nil {
		t.Fatalf("aging a row: %v", err)
	}
	n, err := r.DeleteOlderThan(ctx, 30*24*time.Hour)
	if err != nil {
		t.Fatalf("DeleteOlderThan: %v", err)
	}
	if n != 1 {
		t.Errorf("deleted %d rows, want 1", n)
	}
	if left, _ := r.Recent(ctx, 10); len(left) != 2 {
		t.Errorf("want 2 entries left, got %d", len(left))
	}
}

func TestCategoryRepoSeedsBuiltIns(t *testing.T) {
	pool := testDB(t)
	ctx := context.Background()
	r := NewCategoryRepo(pool)

	all, err := r.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(all) == 0 {
		t.Fatal("migrations should seed the built-in categories")
	}
	bySlug, err := r.GetBySlug(ctx, CategoryTravel)
	if err != nil || bySlug == nil {
		t.Fatalf("GetBySlug(%q): %v (got %v)", CategoryTravel, err, bySlug)
	}
	byID, err := r.Get(ctx, bySlug.ID)
	if err != nil || byID == nil || byID.Slug != CategoryTravel {
		t.Fatalf("Get: %v (got %v)", err, byID)
	}
	if missing, err := r.GetBySlug(ctx, "not-a-category"); err != nil || missing != nil {
		t.Errorf("a missing slug should be (nil, nil), got (%v, %v)", missing, err)
	}
}
