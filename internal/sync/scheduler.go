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

// Scheduler places tasks (and, in a follow-up commit, habits) onto target
// calendars by finding free slots in the target account's effective hours.
type Scheduler struct {
	tasks     *db.TaskRepo
	accounts  *db.AccountRepo
	calendars *db.CalendarRepo
	audit     *db.AuditRepo
	clientFor ClientFor
	log       *slog.Logger
}

func NewScheduler(tasks *db.TaskRepo, accounts *db.AccountRepo, calendars *db.CalendarRepo,
	audit *db.AuditRepo, clientFor ClientFor, log *slog.Logger) *Scheduler {
	return &Scheduler{
		tasks:     tasks,
		accounts:  accounts,
		calendars: calendars,
		audit:     audit,
		clientFor: clientFor,
		log:       log,
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

	wh, err := hours.Parse(acct.EffectiveHours(db.HoursWorking))
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
	busy, err := s.busyOn(ctx, cli, cal.GoogleCalendarID, from, to, wh.TimeZone)
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

// busyOn pulls freebusy for one Google calendar and converts to hours.Window.
func (s *Scheduler) busyOn(ctx context.Context, cli *calendar.Client, googleCalID string,
	from, to time.Time, tz string) ([]hours.Window, error) {
	fb, err := cli.FreeBusy(ctx, []string{googleCalID}, from, to, tz)
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
	return hours.Merge(out), nil
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
