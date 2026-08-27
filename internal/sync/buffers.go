package sync

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	gcal "google.golang.org/api/calendar/v3"

	"github.com/ryakel/skulid/internal/calendar"
	"github.com/ryakel/skulid/internal/db"
)

// BufferEngine writes (and reaps) the visible padding events skulid keeps
// around your meetings on a target calendar, over the next 7 days:
//
//   - "Decompress" — a trailing block after every non-managed meeting with
//     another human on it.
//   - "Travel" — a block either side of a meeting that carries a location,
//     so getting there and back doesn't get scheduled over.
//
// Trigger model: called per-calendar. Cheap on calendars with few meetings,
// heavier on busy ones — the worker debounces calls per calendar.
type BufferEngine struct {
	calendars *db.CalendarRepo
	accounts  *db.AccountRepo
	settings  *db.SettingRepo
	buffers   *db.BufferEventRepo
	audit     *db.AuditRepo
	clientFor ClientFor
	log       *slog.Logger
}

func NewBufferEngine(calendars *db.CalendarRepo, accounts *db.AccountRepo,
	settings *db.SettingRepo, buffers *db.BufferEventRepo, audit *db.AuditRepo,
	clientFor ClientFor, log *slog.Logger) *BufferEngine {
	return &BufferEngine{
		calendars: calendars, accounts: accounts, settings: settings,
		buffers: buffers, audit: audit, clientFor: clientFor, log: log,
	}
}

const bufferHorizon = 7 * 24 * time.Hour

// plannedBuffer is one buffer event the engine wants to exist. Producing these
// is pure — no context, no I/O — so the rules about which meetings earn which
// buffers are exhaustively testable without a calendar.
type plannedBuffer struct {
	Key     db.BufferKey
	Summary string
	Start   time.Time
	End     time.Time
}

// Recompute brings the calendar's buffer events into sync with the user's
// upcoming meetings and the effective buffer-minutes settings.
func (e *BufferEngine) Recompute(ctx context.Context, calendarID int64) error {
	cal, err := e.calendars.Get(ctx, calendarID)
	if err != nil || cal == nil {
		return fmt.Errorf("calendar %d not found: %w", calendarID, err)
	}
	if !cal.Enabled {
		// Disabled calendar — no buffer events should exist on it.
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

	from := time.Now()
	to := from.Add(bufferHorizon)

	existing, err := e.buffers.ListByCalendarInRange(ctx, calendarID, from, to)
	if err != nil {
		return err
	}
	byKey := map[db.BufferKey]db.BufferEvent{}
	for _, b := range existing {
		byKey[b.Key()] = b
	}

	resp, err := cli.Service().Events.List(cal.GoogleCalendarID).
		Context(ctx).SingleEvents(true).
		TimeMin(from.Format(time.RFC3339)).
		TimeMax(to.Format(time.RFC3339)).
		MaxResults(250).OrderBy("startTime").Do()
	if err != nil {
		return fmt.Errorf("events list: %w", err)
	}

	seen := map[db.BufferKey]bool{}
	for _, ev := range resp.Items {
		for _, want := range planBuffers(ev, bufs) {
			seen[want.Key] = true
			if err := e.apply(ctx, cli, cal, want, byKey[want.Key], byKey); err != nil {
				e.log.Warn("buffer write failed", "src", want.Key.SourceEventID,
					"type", want.Key.BufferType, "err", err)
			}
		}
	}

	// Reap orphans: rows whose source meeting no longer earns that buffer —
	// the meeting moved out of the horizon, lost its location, was cancelled,
	// or the minutes were set to zero.
	for key, row := range byKey {
		if seen[key] {
			continue
		}
		if err := cli.DeleteEvent(ctx, cal.GoogleCalendarID, row.TargetEventID); err != nil {
			e.log.Warn("buffer delete failed", "tgt", row.TargetEventID, "err", err)
		}
		_ = e.buffers.Delete(ctx, row.ID)
		e.audit.Write(ctx, db.AuditWrite{Kind: "buffer", TargetEventID: row.TargetEventID,
			Action: "delete", Message: key.BufferType})
	}
	return nil
}

// apply creates or moves a single buffer event so it matches `want`.
func (e *BufferEngine) apply(ctx context.Context, cli *calendar.Client, cal *db.Calendar,
	want plannedBuffer, row db.BufferEvent, byKey map[db.BufferKey]db.BufferEvent) error {
	body := &gcal.Event{
		Summary:      want.Summary,
		Start:        &gcal.EventDateTime{DateTime: want.Start.Format(time.RFC3339), TimeZone: cal.TimeZone},
		End:          &gcal.EventDateTime{DateTime: want.End.Format(time.RFC3339), TimeZone: cal.TimeZone},
		Transparency: "opaque",
		ExtendedProperties: &gcal.EventExtendedProperties{
			Private: calendar.BufferProps(want.Key.BufferType, want.Key.SourceEventID),
		},
	}

	if row.ID != 0 {
		if row.StartsAt.Equal(want.Start) && row.EndsAt.Equal(want.End) {
			return nil
		}
		// Window changed: the meeting moved, or the minutes were changed.
		updated, err := cli.UpdateEvent(ctx, cal.GoogleCalendarID, row.TargetEventID, body)
		if err != nil {
			return err
		}
		if err := e.buffers.UpdateWindow(ctx, row.ID, want.Start, want.End); err != nil {
			return err
		}
		row.StartsAt, row.EndsAt = want.Start, want.End
		byKey[want.Key] = row
		e.audit.Write(ctx, db.AuditWrite{Kind: "buffer", TargetEventID: updated.Id,
			Action: "update", Message: want.Key.BufferType})
		return nil
	}

	saved, err := cli.InsertEvent(ctx, cal.GoogleCalendarID, body)
	if err != nil {
		return err
	}
	id, err := e.buffers.Insert(ctx, &db.BufferEvent{
		CalendarID:    cal.ID,
		SourceEventID: want.Key.SourceEventID,
		TargetEventID: saved.Id,
		BufferType:    want.Key.BufferType,
		Placement:     want.Key.Placement,
		StartsAt:      want.Start,
		EndsAt:        want.End,
	})
	if err != nil {
		return err
	}
	byKey[want.Key] = db.BufferEvent{
		ID: id, CalendarID: cal.ID, SourceEventID: want.Key.SourceEventID,
		TargetEventID: saved.Id, BufferType: want.Key.BufferType,
		Placement: want.Key.Placement, StartsAt: want.Start, EndsAt: want.End,
	}
	e.audit.Write(ctx, db.AuditWrite{Kind: "buffer", TargetEventID: saved.Id,
		Action: "create", Message: want.Key.BufferType})
	return nil
}

// planBuffers returns every buffer event `ev` earns under `bufs`, in a stable
// order. A meeting can earn all three: travel there, decompression after, and
// travel back — decompression sits between the meeting and the travel-after
// block, since you decompress before you drive.
func planBuffers(ev *gcal.Event, bufs db.BufferSettings) []plannedBuffer {
	start, startOK := parseEvStart(ev)
	end, endOK := parseEvEnd(ev)
	if !startOK || !endOK {
		return nil
	}

	var out []plannedBuffer
	travel := time.Duration(bufs.TravelMinutes) * time.Minute
	if travel > 0 && isTravelWorthy(ev) {
		out = append(out, plannedBuffer{
			Key:     db.BufferKey{SourceEventID: ev.Id, BufferType: db.BufferTravel, Placement: db.PlacementBefore},
			Summary: "Travel", Start: start.Add(-travel), End: start,
		})
	}

	decomp := time.Duration(bufs.DecompressionMinutes) * time.Minute
	trailing := end
	if decomp > 0 && isDecompressibleMeeting(ev) {
		out = append(out, plannedBuffer{
			Key:     db.BufferKey{SourceEventID: ev.Id, BufferType: db.BufferDecompression, Placement: db.PlacementAfter},
			Summary: "Decompress", Start: end, End: end.Add(decomp),
		})
		trailing = end.Add(decomp)
	}

	if travel > 0 && isTravelWorthy(ev) {
		out = append(out, plannedBuffer{
			Key:     db.BufferKey{SourceEventID: ev.Id, BufferType: db.BufferTravel, Placement: db.PlacementAfter},
			Summary: "Travel", Start: trailing, End: trailing.Add(travel),
		})
	}
	return out
}

// isTravelWorthy decides whether an event earns travel padding either side.
//
// Google gives us a free-text `location` and nothing else — no origin, no
// distance, no mode of transport. So this is deliberately blunt: a fixed pad
// around any meeting you have to physically be somewhere for. A video-call
// link in the location field is the one case worth excluding, since those are
// common and travelling to them takes no time at all.
func isTravelWorthy(ev *gcal.Event) bool {
	if !isRealCommitment(ev) {
		return false
	}
	loc := strings.TrimSpace(ev.Location)
	return loc != "" && !isVirtualLocation(loc)
}

// virtualLocationHosts are matched as substrings of a lowercased location, so
// they catch both a bare host and a full meeting URL.
var virtualLocationHosts = []string{
	"meet.google.com", "zoom.us", "teams.microsoft.com", "teams.live.com",
	"webex.com", "gotomeeting.com", "whereby.com", "chime.aws",
	"bluejeans.com", "discord.gg", "discord.com", "slack.com",
}

func isVirtualLocation(loc string) bool {
	l := strings.ToLower(loc)
	for _, h := range virtualLocationHosts {
		if strings.Contains(l, h) {
			return true
		}
	}
	return false
}

// isDecompressibleMeeting decides whether an event qualifies for a trailing
// decompression block. Conservative: must be a real commitment with at least
// one other person on it (so solo blocks and personal items don't trigger).
func isDecompressibleMeeting(ev *gcal.Event) bool {
	if !isRealCommitment(ev) {
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

// isRealCommitment is the shared floor for earning any buffer: a live, timed,
// busy event that skulid did not write itself. Padding one of our own buffers
// would compound on every recompute.
func isRealCommitment(ev *gcal.Event) bool {
	if ev == nil || ev.Status == "cancelled" {
		return false
	}
	if calendar.IsManaged(ev) {
		return false
	}
	if ev.Start == nil || ev.Start.DateTime == "" {
		return false
	}
	return ev.Transparency != "transparent"
}

func parseEvStart(ev *gcal.Event) (time.Time, bool) {
	if ev == nil || ev.Start == nil || ev.Start.DateTime == "" {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339, ev.Start.DateTime)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
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
