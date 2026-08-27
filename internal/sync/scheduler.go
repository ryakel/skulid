package sync

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	gcal "google.golang.org/api/calendar/v3"

	"github.com/ryakel/skulid/internal/calendar"
	"github.com/ryakel/skulid/internal/db"
	"github.com/ryakel/skulid/internal/hours"
)

// Scheduler places tasks and habits onto target calendars by finding free
// slots in the target account's effective hours.
type Scheduler struct {
	tasks       *db.TaskRepo
	chunks      *db.TaskChunkRepo
	habits      *db.HabitRepo
	occurrences *db.HabitOccurrenceRepo
	accounts    *db.AccountRepo
	calendars   *db.CalendarRepo
	managed     *db.ManagedWindowRepo
	audit       *db.AuditRepo
	settings    *db.SettingRepo
	clientFor   ClientFor
	log         *slog.Logger
}

func NewScheduler(tasks *db.TaskRepo, chunks *db.TaskChunkRepo, habits *db.HabitRepo, occurrences *db.HabitOccurrenceRepo,
	accounts *db.AccountRepo, calendars *db.CalendarRepo, managed *db.ManagedWindowRepo,
	settings *db.SettingRepo, audit *db.AuditRepo, clientFor ClientFor, log *slog.Logger) *Scheduler {
	return &Scheduler{
		tasks:       tasks,
		chunks:      chunks,
		habits:      habits,
		occurrences: occurrences,
		accounts:    accounts,
		calendars:   calendars,
		managed:     managed,
		audit:       audit,
		settings:    settings,
		clientFor:   clientFor,
		log:         log,
	}
}

// defaultTaskHorizon is the lookahead used when a task has no deadline.
const defaultTaskHorizon = 14 * 24 * time.Hour

// PlaceTask schedules (or reschedules) a single task. If the task is pending
// it gets placed; if it's already scheduled and the existing window is still
// free, no-op; otherwise it's moved.
func (s *Scheduler) PlaceTask(ctx context.Context, taskID int64) error {
	t, err := s.tasks.Get(ctx, taskID)
	if err != nil || t == nil {
		return fmt.Errorf("task not found")
	}
	if t.Status == db.TaskCompleted || t.Status == db.TaskCancelled {
		return nil
	}

	cal, err := s.calendars.Get(ctx, t.TargetCalendarID)
	if err != nil {
		return err
	}
	if !cal.Enabled {
		// Target calendar disabled — leave the task pending so it gets
		// re-placed once the calendar is re-enabled.
		return nil
	}
	cli, err := s.clientFor(ctx, cal.AccountID)
	if err != nil {
		return err
	}
	acct, err := s.accounts.Get(ctx, cal.AccountID)
	if err != nil {
		return err
	}
	if acct == nil {
		return fmt.Errorf("account %d for calendar %d no longer exists", cal.AccountID, cal.ID)
	}

	wh, err := hours.Parse(db.EffectiveCalendarHours(cal, acct, db.HoursWorking))
	if err != nil {
		return fmt.Errorf("parse hours: %w", err)
	}
	loc, err := time.LoadLocation(wh.TimeZone)
	if err != nil {
		return fmt.Errorf("load tz: %w", err)
	}

	now := time.Now().In(loc)
	from := now
	to := now.Add(defaultTaskHorizon)
	if t.DueAt != nil {
		to = t.DueAt.In(loc)
	}
	if !to.After(from) {
		// Past-due task — leave it alone, surface to the user via the UI.
		return nil
	}

	avail := hours.Expand(wh, from, to, loc)
	busy, err := s.busyEverywhere(ctx, from, to, wh.TimeZone)
	if err != nil {
		return err
	}

	existing, err := s.taskChunks(ctx, t.ID)
	if err != nil {
		return err
	}
	// The task's own blocks must not count against it, or a scheduled task
	// would never find room to stay where it is. busyEverywhere already
	// subtracts every managed window including these; this is the backstop for
	// a scheduler wired without the managed-window repo.
	for _, c := range existing {
		busy = excludeBusyExact(busy, c.StartsAt, c.EndsAt)
	}

	dur := time.Duration(t.DurationMinutes) * time.Minute
	plan := hours.ChunkedSlots(avail, busy, dur, s.minChunk(ctx), from, maxTaskChunks)
	if !plan.Fits {
		// Placement is all-or-nothing. Booking 6 of 8 hours and calling the
		// task scheduled would be a lie the user only discovers on Friday;
		// the note says how close it got so they can shorten it, free time
		// up, or move the deadline.
		return s.unschedule(ctx, cli, cal, t, existing, noFitNote(dur, plan.Placeable, t.DueAt, loc))
	}
	return s.applyChunks(ctx, cli, cal, t, existing, plan, wh.TimeZone)
}

// maxTaskChunks caps how many pieces one task may be broken into. The minimum
// chunk length already bounds this in practice; this is the backstop against a
// pathological calendar producing thirty separate blocks.
const maxTaskChunks = 8

// minChunk is the shortest piece a split task may be broken into.
func (s *Scheduler) minChunk(ctx context.Context) time.Duration {
	return time.Duration(db.TaskMinChunkMinutes(ctx, s.settings)) * time.Minute
}

// taskChunks reads a task's existing blocks. A nil repo means the caller did
// not wire one, in which case a task is never split and behaviour matches what
// it was before.
func (s *Scheduler) taskChunks(ctx context.Context, taskID int64) ([]db.TaskChunk, error) {
	if s.chunks == nil {
		return nil, nil
	}
	out, err := s.chunks.ListByTask(ctx, taskID)
	if err != nil {
		return nil, fmt.Errorf("reading task chunks: %w", err)
	}
	return out, nil
}

// noFitNote explains, in the user's terms, why nothing could be placed. A task
// stuck at pending is the quiet failure SKUL-1 was about; this is the sentence
// that makes it loud.
func noFitNote(want, placeable time.Duration, due *time.Time, loc *time.Location) string {
	if placeable <= 0 {
		if due != nil {
			return fmt.Sprintf("No free time before %s.", due.In(loc).Format("Mon Jan 2, 3:04 PM"))
		}
		return "No free time in your working hours over the next two weeks."
	}
	return fmt.Sprintf("Only %s of the %s needed would fit. Free up time, shorten the task, or move the due date.",
		roundedDuration(placeable), roundedDuration(want))
}

func roundedDuration(d time.Duration) string {
	d = d.Round(time.Minute)
	h := int(d / time.Hour)
	m := int((d % time.Hour) / time.Minute)
	switch {
	case h > 0 && m > 0:
		return fmt.Sprintf("%dh%dm", h, m)
	case h > 0:
		return fmt.Sprintf("%dh", h)
	default:
		return fmt.Sprintf("%dm", m)
	}
}

// unschedule drops every block a task holds and records why, so the user can
// shorten it, free time up, or move the deadline.
func (s *Scheduler) unschedule(ctx context.Context, cli calendar.API, cal *db.Calendar,
	t *db.Task, existing []db.TaskChunk, note string) error {
	for _, c := range existing {
		_ = cli.DeleteEvent(ctx, cal.GoogleCalendarID, c.GoogleEventID)
		if s.chunks != nil {
			_ = s.chunks.Delete(ctx, c.ID)
		}
		_ = s.audit.Write(ctx, db.AuditWrite{Kind: "task", Action: "unscheduled",
			TargetEventID: c.GoogleEventID,
			Message:       fmt.Sprintf("no fit found for task #%d", t.ID)})
	}
	if len(existing) == 0 && t.ScheduledEventID == "" && t.ScheduleNote == note {
		// Nothing was placed, nothing changed, and the reason still reads the
		// same. Don't churn the row.
		return nil
	}
	return s.tasks.UpdateScheduled(ctx, t.ID, "", nil, nil, db.TaskPending, note)
}

// applyChunks reconciles a task's blocks against the plan: existing events are
// moved where the counts line up, extras are deleted, shortfalls are inserted.
// Reusing events by position keeps the churn on the user's calendar down to
// the blocks that actually moved.
func (s *Scheduler) applyChunks(ctx context.Context, cli calendar.API, cal *db.Calendar,
	t *db.Task, existing []db.TaskChunk, plan hours.ChunkPlan, tz string) error {
	unchanged := len(existing) == len(plan.Slots) && len(existing) > 0
	if unchanged {
		for i, c := range existing {
			if !c.StartsAt.Equal(plan.Slots[i].Start) || !c.EndsAt.Equal(plan.Slots[i].End) {
				unchanged = false
				break
			}
		}
	}
	if unchanged && t.ScheduleNote == "" && t.Status == db.TaskScheduled {
		return nil
	}

	total := len(plan.Slots)
	firstEventID := ""
	for i, slot := range plan.Slots {
		ev := &gcal.Event{
			Summary:      chunkTitle(t.Title, i, total),
			Description:  t.Notes,
			Start:        &gcal.EventDateTime{DateTime: slot.Start.Format(time.RFC3339), TimeZone: tz},
			End:          &gcal.EventDateTime{DateTime: slot.End.Format(time.RFC3339), TimeZone: tz},
			Transparency: "opaque",
			ExtendedProperties: &gcal.EventExtendedProperties{
				Private: calendar.TaskProps(t.ID),
			},
		}

		if i < len(existing) {
			c := existing[i]
			saved, err := cli.UpdateEvent(ctx, cal.GoogleCalendarID, c.GoogleEventID, ev)
			if err != nil {
				return fmt.Errorf("move task block: %w", err)
			}
			if s.chunks != nil {
				if err := s.chunks.UpdateWindow(ctx, c.ID, slot.Start, slot.End); err != nil {
					return err
				}
			}
			if i == 0 {
				firstEventID = saved.Id
			}
			s.auditChunk(ctx, t.ID, saved.Id, "rescheduled", i, total, slot)
			continue
		}

		saved, err := cli.InsertEvent(ctx, cal.GoogleCalendarID, ev)
		if err != nil {
			return fmt.Errorf("place task block: %w", err)
		}
		if s.chunks != nil {
			if _, err := s.chunks.Insert(ctx, &db.TaskChunk{
				TaskID: t.ID, Seq: i, GoogleEventID: saved.Id,
				StartsAt: slot.Start, EndsAt: slot.End,
			}); err != nil {
				return err
			}
		}
		if i == 0 {
			firstEventID = saved.Id
		}
		s.auditChunk(ctx, t.ID, saved.Id, "scheduled", i, total, slot)
	}

	// The plan needs fewer blocks than the task currently holds.
	if len(existing) > len(plan.Slots) {
		for _, c := range existing[len(plan.Slots):] {
			_ = cli.DeleteEvent(ctx, cal.GoogleCalendarID, c.GoogleEventID)
			if s.chunks != nil {
				_ = s.chunks.Delete(ctx, c.ID)
			}
			_ = s.audit.Write(ctx, db.AuditWrite{Kind: "task", Action: "unscheduled",
				TargetEventID: c.GoogleEventID,
				Message:       fmt.Sprintf("task #%d no longer needs this block", t.ID)})
		}
	}

	start := plan.Slots[0].Start
	end := plan.Slots[0].End
	return s.tasks.UpdateScheduled(ctx, t.ID, firstEventID, &start, &end, db.TaskScheduled, "")
}

// chunkTitle labels a split task's blocks so "1/3" on the calendar reads as
// deliberate rather than as three duplicates. A single-block task keeps its
// plain title, so an ordinary task looks exactly as it always did.
func chunkTitle(title string, i, total int) string {
	if total <= 1 {
		return title
	}
	return fmt.Sprintf("%s (%d/%d)", title, i+1, total)
}

func (s *Scheduler) auditChunk(ctx context.Context, taskID int64, eventID, action string, i, total int, slot hours.Window) {
	where := slot.Start.Format(time.RFC3339) + "–" + slot.End.Format(time.RFC3339)
	msg := fmt.Sprintf("task #%d scheduled %s", taskID, where)
	if total > 1 {
		msg = fmt.Sprintf("task #%d block %d/%d scheduled %s", taskID, i+1, total, where)
	}
	_ = s.audit.Write(ctx, db.AuditWrite{
		Kind: "task", TargetEventID: eventID, Action: action, Message: msg,
	})
}

// PlaceAllPending walks every active task and (re)places it. Used at startup,
// after working-hours edits, and when a daily tick fires.
func (s *Scheduler) PlaceAllPending(ctx context.Context) {
	tasks, err := s.tasks.ListAllActive(ctx)
	if err != nil {
		s.log.Error("scheduler list failed", "err", err)
		return
	}
	for _, t := range tasks {
		if err := s.PlaceTask(ctx, t.ID); err != nil {
			s.log.Error("place task failed", "task_id", t.ID, "err", err)
		}
	}
}

// busyEverywhere pulls freebusy for every enabled calendar on every connected
// account and merges the result.
//
// Scheduling used to consider only the calendar being written to, which meant
// a personal task would happily land on top of a work meeting: different
// calendar, so as far as the scheduler was concerned the slot was free. It is
// one person's time regardless of which account owns the calendar, so all of
// it counts.
//
// Each calendar's own buffer settings are applied to its own busy windows --
// freebusy is keyed by calendar id, so the per-calendar override chain is
// still honoured rather than flattened to the target's padding.
//
// This fails closed. If any account's freebusy cannot be fetched -- a revoked
// token, an API error -- placement returns an error rather than proceeding on
// a busy set it knows to be incomplete. Scheduling over a real meeting because
// a token expired is a worse outcome than not scheduling at all, and SKUL-1's
// banner already makes a locked-out account visible.
func (s *Scheduler) busyEverywhere(ctx context.Context, from, to time.Time, tz string) ([]hours.Window, error) {
	all, err := s.calendars.ListAll(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing calendars: %w", err)
	}

	var out []hours.Window
	for accountID, cals := range groupByAccount(enabledCalendars(all)) {
		cli, err := s.clientFor(ctx, accountID)
		if err != nil {
			return nil, fmt.Errorf("account %d unavailable, refusing to schedule against an incomplete busy set: %w", accountID, err)
		}

		ids := make([]string, 0, len(cals))
		byGoogleID := make(map[string]db.Calendar, len(cals))
		for _, c := range cals {
			ids = append(ids, c.GoogleCalendarID)
			byGoogleID[c.GoogleCalendarID] = c
		}

		fb, err := cli.FreeBusy(ctx, ids, from, to, tz)
		if err != nil {
			return nil, fmt.Errorf("freebusy for account %d: %w", accountID, err)
		}

		for googleID, periods := range fb {
			windows := periodsToWindows(periods)
			if cal, ok := byGoogleID[googleID]; ok {
				windows = s.applyBufferPadding(ctx, &cal, windows)
			}
			out = append(out, windows...)
		}
	}

	busy := hours.Merge(out)

	// Freebusy cannot tell skulid's own writes from real meetings, so without
	// this a smart block filling your working hours would block every task
	// from ever being placed -- and the only symptom would be tasks sitting
	// at pending. Every window skulid wrote is already recorded locally, so
	// removing them costs one query and no API calls.
	managed, err := s.managedWindows(ctx, from, to)
	if err != nil {
		return nil, err
	}
	return subtractManaged(busy, managed), nil
}

// managedWindows reads back what skulid itself has scheduled in the range.
// A nil repo means the caller did not wire one, in which case nothing is
// subtracted and behaviour matches what it was before.
func (s *Scheduler) managedWindows(ctx context.Context, from, to time.Time) ([]hours.Window, error) {
	if s.managed == nil {
		return nil, nil
	}
	rows, err := s.managed.InRange(ctx, from, to)
	if err != nil {
		return nil, fmt.Errorf("reading managed windows: %w", err)
	}
	out := make([]hours.Window, 0, len(rows))
	for _, w := range rows {
		out = append(out, hours.Window{Start: w.StartsAt, End: w.EndsAt})
	}
	return out, nil
}

// applyBufferPadding extends every busy window's end by the calendar's
// effective padding minutes. Caller still merges, so adjacent paddings collapse.
func (s *Scheduler) applyBufferPadding(ctx context.Context, cal *db.Calendar, in []hours.Window) []hours.Window {
	if s.settings == nil || len(in) == 0 {
		return in
	}
	pad := time.Duration(db.EffectiveCalendarBuffers(ctx, s.settings, cal).PaddingMinutes()) * time.Minute
	if pad <= 0 {
		return in
	}
	out := make([]hours.Window, len(in))
	for i, w := range in {
		out[i] = hours.Window{Start: w.Start, End: w.End.Add(pad)}
	}
	return out
}

// excludeBusyExact removes any busy windows that exactly match start/end. Used
// to ignore the task's own existing block when computing where it could go.
func excludeBusyExact(in []hours.Window, start, end time.Time) []hours.Window {
	out := make([]hours.Window, 0, len(in))
	for _, w := range in {
		if w.Start.Equal(start) && w.End.Equal(end) {
			continue
		}
		out = append(out, w)
	}
	return out
}

// PlaceHabit walks the habit's horizon and ensures every matching weekday has
// a scheduled occurrence near the ideal time. Existing occurrences are kept
// when their slot is still free; otherwise they're moved (or deleted if no
// fit exists). Days that don't match the habit's days_of_week are left alone.
func (s *Scheduler) PlaceHabit(ctx context.Context, habitID int64) error {
	h, err := s.habits.Get(ctx, habitID)
	if err != nil || h == nil {
		return fmt.Errorf("habit not found")
	}
	if !h.Enabled {
		return nil
	}

	cal, err := s.calendars.Get(ctx, h.TargetCalendarID)
	if err != nil {
		return err
	}
	if !cal.Enabled {
		// Target calendar disabled — skip placement and let the maintenance
		// tick retry once the calendar is back on.
		return nil
	}
	cli, err := s.clientFor(ctx, cal.AccountID)
	if err != nil {
		return err
	}
	acct, err := s.accounts.Get(ctx, cal.AccountID)
	if err != nil {
		return err
	}
	if acct == nil {
		return fmt.Errorf("account %d for calendar %d no longer exists", cal.AccountID, cal.ID)
	}

	wh, err := hours.Parse(db.EffectiveCalendarHours(cal, acct, db.HoursKind(h.HoursKind)))
	if err != nil {
		return fmt.Errorf("parse hours: %w", err)
	}
	loc, err := time.LoadLocation(wh.TimeZone)
	if err != nil {
		return fmt.Errorf("load tz: %w", err)
	}

	idealH, idealM, ok := splitHHMM(h.IdealTime)
	if !ok {
		return fmt.Errorf("invalid ideal_time %q", h.IdealTime)
	}
	dur := time.Duration(h.DurationMinutes) * time.Minute
	flex := time.Duration(h.FlexMinutes) * time.Minute
	dowSet := map[string]bool{}
	for _, d := range h.DaysOfWeek {
		dowSet[d] = true
	}
	if len(dowSet) == 0 {
		return nil
	}

	now := time.Now().In(loc)
	startDay := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	horizon := h.HorizonDays
	if horizon <= 0 {
		horizon = 14
	}

	// Index existing occurrences by date so we can quickly check if a day is
	// already placed. Anything outside the horizon stays where it is.
	existing, err := s.occurrences.ListByHabit(ctx, h.ID)
	if err != nil {
		return err
	}
	occByDate := map[string]db.HabitOccurrence{}
	for _, o := range existing {
		occByDate[o.OccursOn.Format("2006-01-02")] = o
	}

	for i := 0; i < horizon; i++ {
		day := startDay.AddDate(0, 0, i)
		if !dowSet[hours.DayKey(day.Weekday())] {
			continue
		}
		key := day.Format("2006-01-02")
		ideal := time.Date(day.Year(), day.Month(), day.Day(), idealH, idealM, 0, 0, loc)

		// Build avail just for this day (so we don't pull a 14-day freebusy
		// every time — Google supports it but it's wasteful).
		dayStart := day
		dayEnd := day.AddDate(0, 0, 1)
		avail := hours.Expand(wh, dayStart, dayEnd, loc)
		busy, err := s.busyEverywhere(ctx, dayStart, dayEnd, wh.TimeZone)
		if err != nil {
			s.log.Warn("habit busy fetch failed", "habit_id", h.ID, "day", key, "err", err)
			continue
		}
		// Don't let the occurrence's own existing block kick itself out.
		if prev, ok := occByDate[key]; ok {
			busy = excludeBusyExact(busy, prev.StartsAt, prev.EndsAt)
		}

		slot, ok := hours.NearestFitSlot(avail, busy, dur, flex, ideal)
		if !ok {
			// No fit today. If a stale occurrence exists, drop it.
			if prev, ok := occByDate[key]; ok {
				_ = cli.DeleteEvent(ctx, cal.GoogleCalendarID, prev.TargetEventID)
				_ = s.occurrences.DeleteByID(ctx, prev.ID)
				_ = s.audit.Write(ctx, db.AuditWrite{
					Kind:          "habit",
					TargetEventID: prev.TargetEventID,
					Action:        "drop",
					Message:       fmt.Sprintf("habit #%d no fit on %s", h.ID, key),
				})
			}
			continue
		}

		// Skip if the occurrence is already in this exact slot.
		if prev, ok := occByDate[key]; ok && prev.StartsAt.Equal(slot.Start) && prev.EndsAt.Equal(slot.End) {
			continue
		}

		ev := &gcal.Event{
			Summary:      h.Title,
			Start:        &gcal.EventDateTime{DateTime: slot.Start.Format(time.RFC3339), TimeZone: wh.TimeZone},
			End:          &gcal.EventDateTime{DateTime: slot.End.Format(time.RFC3339), TimeZone: wh.TimeZone},
			Transparency: "opaque",
			ExtendedProperties: &gcal.EventExtendedProperties{
				Private: calendar.HabitProps(h.ID),
			},
		}

		var saved *gcal.Event
		action := "scheduled"
		if prev, ok := occByDate[key]; ok && prev.TargetEventID != "" {
			saved, err = cli.UpdateEvent(ctx, cal.GoogleCalendarID, prev.TargetEventID, ev)
			action = "rescheduled"
		} else {
			saved, err = cli.InsertEvent(ctx, cal.GoogleCalendarID, ev)
		}
		if err != nil {
			s.log.Warn("habit place failed", "habit_id", h.ID, "day", key, "err", err)
			continue
		}
		if _, err := s.occurrences.Upsert(ctx, &db.HabitOccurrence{
			HabitID:       h.ID,
			TargetEventID: saved.Id,
			OccursOn:      day,
			StartsAt:      slot.Start,
			EndsAt:        slot.End,
		}); err != nil {
			s.log.Error("habit occurrence upsert failed", "habit_id", h.ID, "day", key, "err", err)
			continue
		}
		_ = s.audit.Write(ctx, db.AuditWrite{
			Kind:          "habit",
			TargetEventID: saved.Id,
			Action:        action,
			Message:       fmt.Sprintf("habit #%d %s %s", h.ID, key, slot.Start.Format("15:04")),
		})
	}
	return nil
}

// PlaceAllHabits is the daily-tick equivalent for habits — useful at startup.
func (s *Scheduler) PlaceAllHabits(ctx context.Context) {
	hs, err := s.habits.ListEnabled(ctx)
	if err != nil {
		s.log.Error("scheduler list habits failed", "err", err)
		return
	}
	for _, h := range hs {
		if err := s.PlaceHabit(ctx, h.ID); err != nil {
			s.log.Error("place habit failed", "habit_id", h.ID, "err", err)
		}
	}
}

// splitHHMM parses "HH:MM" and returns hour, minute, ok.
func splitHHMM(s string) (int, int, bool) {
	var h, m int
	var trail rune
	n, _ := fmt.Sscanf(s, "%d:%d%c", &h, &m, &trail)
	if n != 2 {
		return 0, 0, false
	}
	if h < 0 || h > 23 || m < 0 || m > 59 {
		return 0, 0, false
	}
	return h, m, true
}
