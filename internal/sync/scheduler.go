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
	habits      *db.HabitRepo
	occurrences *db.HabitOccurrenceRepo
	accounts    *db.AccountRepo
	calendars   *db.CalendarRepo
	audit       *db.AuditRepo
	settings    *db.SettingRepo
	clientFor   ClientFor
	log         *slog.Logger
}

func NewScheduler(tasks *db.TaskRepo, habits *db.HabitRepo, occurrences *db.HabitOccurrenceRepo,
	accounts *db.AccountRepo, calendars *db.CalendarRepo, settings *db.SettingRepo,
	audit *db.AuditRepo, clientFor ClientFor, log *slog.Logger) *Scheduler {
	return &Scheduler{
		tasks:       tasks,
		habits:      habits,
		occurrences: occurrences,
		accounts:    accounts,
		calendars:   calendars,
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
	cli, err := s.clientFor(ctx, cal.AccountID)
	if err != nil {
		return err
	}
	acct, err := s.accounts.Get(ctx, cal.AccountID)
	if err != nil {
		return err
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
	busy, err := s.busyOn(ctx, cli, cal, from, to, wh.TimeZone)
	if err != nil {
		return err
	}

	// If the task already has a scheduled slot, exclude it from the busy set
	// so the slot is rediscoverable as "free for this task" — otherwise we'd
	// kick our own existing block out.
	if t.ScheduledStartsAt != nil && t.ScheduledEndsAt != nil {
		busy = excludeBusyExact(busy, *t.ScheduledStartsAt, *t.ScheduledEndsAt)
	}

	dur := time.Duration(t.DurationMinutes) * time.Minute
	slot, ok := hours.FirstFitSlot(avail, busy, dur, from)
	if !ok {
		// No room. Leave pending if it was; if it was scheduled, drop the old
		// event (the user can manually move/reschedule the task).
		if t.ScheduledEventID != "" {
			_ = cli.DeleteEvent(ctx, cal.GoogleCalendarID, t.ScheduledEventID)
			_ = s.tasks.UpdateScheduled(ctx, t.ID, "", nil, nil, db.TaskPending)
			_ = s.audit.Write(ctx, db.AuditWrite{Kind: "task", Action: "unscheduled",
				TargetEventID: t.ScheduledEventID,
				Message:       fmt.Sprintf("no fit found for task #%d", t.ID)})
		}
		return nil
	}

	// If we already have an event and the slot is identical, no-op.
	if t.ScheduledEventID != "" && t.ScheduledStartsAt != nil && t.ScheduledStartsAt.Equal(slot.Start) &&
		t.ScheduledEndsAt != nil && t.ScheduledEndsAt.Equal(slot.End) {
		return nil
	}

	ev := &gcal.Event{
		Summary: t.Title,
		Description: t.Notes,
		Start: &gcal.EventDateTime{DateTime: slot.Start.Format(time.RFC3339), TimeZone: wh.TimeZone},
		End:   &gcal.EventDateTime{DateTime: slot.End.Format(time.RFC3339), TimeZone: wh.TimeZone},
		Transparency: "opaque",
		ExtendedProperties: &gcal.EventExtendedProperties{
			Private: calendar.TaskProps(t.ID),
		},
	}

	var saved *gcal.Event
	action := "scheduled"
	if t.ScheduledEventID != "" {
		saved, err = cli.UpdateEvent(ctx, cal.GoogleCalendarID, t.ScheduledEventID, ev)
		action = "rescheduled"
	} else {
		saved, err = cli.InsertEvent(ctx, cal.GoogleCalendarID, ev)
	}
	if err != nil {
		return fmt.Errorf("place task: %w", err)
	}
	start := slot.Start
	end := slot.End
	if err := s.tasks.UpdateScheduled(ctx, t.ID, saved.Id, &start, &end, db.TaskScheduled); err != nil {
		return err
	}
	_ = s.audit.Write(ctx, db.AuditWrite{
		Kind:          "task",
		TargetEventID: saved.Id,
		Action:        action,
		Message:       fmt.Sprintf("task #%d scheduled %s–%s", t.ID, slot.Start.Format(time.RFC3339), slot.End.Format(time.RFC3339)),
	})
	return nil
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

// busyOn pulls freebusy for one calendar and converts to hours.Window. The
// result is post-padded by the calendar's effective buffer settings (per-
// calendar override → global setting), rolled up into one universal padding
// since freebusy output doesn't distinguish task-break from decompression.
func (s *Scheduler) busyOn(ctx context.Context, cli *calendar.Client, cal *db.Calendar,
	from, to time.Time, tz string) ([]hours.Window, error) {
	fb, err := cli.FreeBusy(ctx, []string{cal.GoogleCalendarID}, from, to, tz)
	if err != nil {
		return nil, fmt.Errorf("freebusy: %w", err)
	}
	var out []hours.Window
	for _, periods := range fb {
		for _, p := range periods {
			ps, err := time.Parse(time.RFC3339, p.Start)
			if err != nil {
				continue
			}
			pe, err := time.Parse(time.RFC3339, p.End)
			if err != nil {
				continue
			}
			out = append(out, hours.Window{Start: ps, End: pe})
		}
	}
	out = s.applyBufferPadding(ctx, cal, out)
	return hours.Merge(out), nil
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
	cli, err := s.clientFor(ctx, cal.AccountID)
	if err != nil {
		return err
	}
	acct, err := s.accounts.Get(ctx, cal.AccountID)
	if err != nil {
		return err
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
		busy, err := s.busyOn(ctx, cli, cal, dayStart, dayEnd, wh.TimeZone)
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
