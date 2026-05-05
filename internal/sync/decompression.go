package sync

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	gcal "google.golang.org/api/calendar/v3"

	"github.com/ryakel/skulid/internal/calendar"
	"github.com/ryakel/skulid/internal/db"
)

// DecompressionEngine writes (and reaps) "Decompress" buffer events on a
// target calendar after every non-managed meeting in the next 7 days.
//
// Trigger model: called per-calendar. Cheap on calendars with few meetings,
// heavier on busy ones — the worker debounces calls per calendar.
type DecompressionEngine struct {
	calendars *db.CalendarRepo
	accounts  *db.AccountRepo
	settings  *db.SettingRepo
	decomp    *db.DecompressionRepo
	audit     *db.AuditRepo
	clientFor ClientFor
	log       *slog.Logger
}

func NewDecompressionEngine(calendars *db.CalendarRepo, accounts *db.AccountRepo,
	settings *db.SettingRepo, decomp *db.DecompressionRepo, audit *db.AuditRepo,
	clientFor ClientFor, log *slog.Logger) *DecompressionEngine {
	return &DecompressionEngine{
		calendars: calendars, accounts: accounts, settings: settings,
		decomp: decomp, audit: audit, clientFor: clientFor, log: log,
	}
}

const decompressionHorizon = 7 * 24 * time.Hour

// Recompute brings the calendar's decompress events into sync with the user's
// upcoming meetings and the effective decompression-minutes setting.
func (e *DecompressionEngine) Recompute(ctx context.Context, calendarID int64) error {
	cal, err := e.calendars.Get(ctx, calendarID)
	if err != nil || cal == nil {
		return fmt.Errorf("calendar %d not found: %w", calendarID, err)
	}
	if !cal.Enabled {
		// Disabled calendar — no decompress events should exist on it.
		// Drop any rows we previously created so the diff stays clean if the
		// user re-enables later.
		_ = e.audit
		return nil
	}
	cli, err := e.clientFor(ctx, cal.AccountID)
	if err != nil {
		return err
	}
	bufs := db.EffectiveCalendarBuffers(ctx, e.settings, cal)
	dur := time.Duration(bufs.DecompressionMinutes) * time.Minute

	from := time.Now()
	to := from.Add(decompressionHorizon)

	existing, err := e.decomp.ListByCalendarInRange(ctx, calendarID, from, to)
	if err != nil {
		return err
	}
	bySource := map[string]db.DecompressionEvent{}
	for _, d := range existing {
		bySource[d.SourceEventID] = d
	}

	resp, err := cli.Service().Events.List(cal.GoogleCalendarID).
		Context(ctx).SingleEvents(true).
		TimeMin(from.Format(time.RFC3339)).
		TimeMax(to.Format(time.RFC3339)).
		MaxResults(250).OrderBy("startTime").Do()
	if err != nil {
		return fmt.Errorf("events list: %w", err)
	}

	seen := map[string]bool{}
	for _, ev := range resp.Items {
		if !isDecompressibleMeeting(ev) {
			continue
		}
		if dur <= 0 {
			// Decompression turned off — skip; the cleanup pass below will
			// reap any leftover buffer events.
			continue
		}
		end, ok := parseEvEnd(ev)
		if !ok {
			continue
		}
		decompStart := end
		decompEnd := end.Add(dur)

		if existingRow, ok := bySource[ev.Id]; ok {
			seen[ev.Id] = true
			if existingRow.StartsAt.Equal(decompStart) && existingRow.EndsAt.Equal(decompEnd) {
				continue
			}
			// Window changed (meeting moved or decompression-minutes changed).
			updated, err := cli.UpdateEvent(ctx, cal.GoogleCalendarID, existingRow.TargetEventID, &gcal.Event{
				Summary:      "Decompress",
				Start:        &gcal.EventDateTime{DateTime: decompStart.Format(time.RFC3339), TimeZone: cal.TimeZone},
				End:          &gcal.EventDateTime{DateTime: decompEnd.Format(time.RFC3339), TimeZone: cal.TimeZone},
				Transparency: "opaque",
				ExtendedProperties: &gcal.EventExtendedProperties{
					Private: calendar.BufferProps("decompression", ev.Id),
				},
			})
			if err != nil {
				e.log.Warn("decompress update failed", "src", ev.Id, "err", err)
				continue
			}
			_ = e.decomp.UpdateWindow(ctx, existingRow.ID, decompStart, decompEnd)
			e.audit.Write(ctx, db.AuditWrite{Kind: "buffer", TargetEventID: updated.Id, Action: "update", Message: "decompression"})
			continue
		}

		// New decompress event.
		saved, err := cli.InsertEvent(ctx, cal.GoogleCalendarID, &gcal.Event{
			Summary:      "Decompress",
			Start:        &gcal.EventDateTime{DateTime: decompStart.Format(time.RFC3339), TimeZone: cal.TimeZone},
			End:          &gcal.EventDateTime{DateTime: decompEnd.Format(time.RFC3339), TimeZone: cal.TimeZone},
			Transparency: "opaque",
			ExtendedProperties: &gcal.EventExtendedProperties{
				Private: calendar.BufferProps("decompression", ev.Id),
			},
		})
		if err != nil {
			e.log.Warn("decompress insert failed", "src", ev.Id, "err", err)
			continue
		}
		if _, err := e.decomp.Insert(ctx, &db.DecompressionEvent{
			CalendarID:    cal.ID,
			SourceEventID: ev.Id,
			TargetEventID: saved.Id,
			StartsAt:      decompStart,
			EndsAt:        decompEnd,
		}); err != nil {
			e.log.Warn("decompress row insert failed", "src", ev.Id, "err", err)
			continue
		}
		seen[ev.Id] = true
		e.audit.Write(ctx, db.AuditWrite{Kind: "buffer", TargetEventID: saved.Id, Action: "create", Message: "decompression"})
	}

	// Reap orphans: existing rows whose source meeting no longer qualifies.
	for src, row := range bySource {
		if seen[src] {
			continue
		}
		if err := cli.DeleteEvent(ctx, cal.GoogleCalendarID, row.TargetEventID); err != nil {
			e.log.Warn("decompress delete failed", "tgt", row.TargetEventID, "err", err)
		}
		_ = e.decomp.Delete(ctx, row.ID)
		e.audit.Write(ctx, db.AuditWrite{Kind: "buffer", TargetEventID: row.TargetEventID, Action: "delete", Message: "decompression"})
	}
	return nil
}

// isDecompressibleMeeting decides whether an event qualifies for a trailing
// decompression block. Conservative: must be a timed busy event with at least
// one external attendee (so solo blocks and personal items don't trigger).
func isDecompressibleMeeting(ev *gcal.Event) bool {
	if ev == nil || ev.Status == "cancelled" {
		return false
	}
	if calendar.IsManaged(ev) {
		return false
	}
	if ev.Start == nil || ev.Start.DateTime == "" {
		return false
	}
	if ev.Transparency == "transparent" {
		return false
	}
	// Require at least 2 attendees (you + at least one other person, ignoring rooms).
	count := 0
	for _, a := range ev.Attendees {
		if a == nil || a.Resource {
			continue
		}
		count++
		if count >= 2 {
			return true
		}
	}
	return false
}

func parseEvEnd(ev *gcal.Event) (time.Time, bool) {
	if ev == nil || ev.End == nil || ev.End.DateTime == "" {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339, ev.End.DateTime)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}
