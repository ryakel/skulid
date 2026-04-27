package sync

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"time"

	gcal "google.golang.org/api/calendar/v3"

	"github.com/ryakel/skulid/internal/calendar"
	"github.com/ryakel/skulid/internal/db"
)

// WorkingHours describes per-weekday availability windows in a specific
// IANA timezone. Each window is HH:MM-HH:MM (24h, local to TimeZone).
type WorkingHours struct {
	TimeZone string              `json:"time_zone"`
	Days     map[string][]string `json:"days"` // "mon" -> ["09:00-12:00","13:00-17:00"]
}

func ParseWorkingHours(raw json.RawMessage) (WorkingHours, error) {
	var w WorkingHours
	if len(raw) == 0 {
		return defaultWorkingHours(), nil
	}
	if err := json.Unmarshal(raw, &w); err != nil {
		return w, err
	}
	if w.TimeZone == "" {
		w.TimeZone = "UTC"
	}
	if w.Days == nil {
		w.Days = map[string][]string{}
	}
	return w, nil
}

func defaultWorkingHours() WorkingHours {
	return WorkingHours{
		TimeZone: "UTC",
		Days: map[string][]string{
			"mon": {"09:00-17:00"},
			"tue": {"09:00-17:00"},
			"wed": {"09:00-17:00"},
			"thu": {"09:00-17:00"},
			"fri": {"09:00-17:00"},
		},
	}
}

type window struct {
	Start time.Time
	End   time.Time
}

type SmartBlockEngine struct {
	blocks    *db.SmartBlockRepo
	managed   *db.ManagedBlockRepo
	calendars *db.CalendarRepo
	audit     *db.AuditRepo
	clientFor ClientFor
	log       *slog.Logger
}

func NewSmartBlockEngine(blocks *db.SmartBlockRepo, managed *db.ManagedBlockRepo, calendars *db.CalendarRepo, audit *db.AuditRepo, clientFor ClientFor, log *slog.Logger) *SmartBlockEngine {
	return &SmartBlockEngine{
		blocks:    blocks,
		managed:   managed,
		calendars: calendars,
		audit:     audit,
		clientFor: clientFor,
		log:       log,
	}
}

// Recompute regenerates the focus blocks for a single smart_block.
func (s *SmartBlockEngine) Recompute(ctx context.Context, blockID int64) error {
	b, err := s.blocks.Get(ctx, blockID)
	if err != nil || b == nil {
		return fmt.Errorf("smart block not found")
	}
	if !b.Enabled {
		return nil
	}

	tgtCal, err := s.calendars.Get(ctx, b.TargetCalendarID)
	if err != nil {
		return err
	}
	tgtClient, err := s.clientFor(ctx, tgtCal.AccountID)
	if err != nil {
		return err
	}

	wh, err := ParseWorkingHours(b.WorkingHours)
	if err != nil {
		return fmt.Errorf("parse working hours: %w", err)
	}
	loc, err := time.LoadLocation(wh.TimeZone)
	if err != nil {
		return fmt.Errorf("load tz %q: %w", wh.TimeZone, err)
	}
	horizon := b.HorizonDays
	if horizon <= 0 {
		horizon = 30
	}

	now := time.Now().In(loc)
	start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	end := start.AddDate(0, 0, horizon)

	// Aggregate busy windows from each source calendar (use that calendar's account).
	var allBusy []window
	for _, srcID := range b.SourceCalendarIDs {
		srcCal, err := s.calendars.Get(ctx, srcID)
		if err != nil {
			s.log.Warn("smart block source cal missing", "cal_id", srcID, "err", err)
			continue
		}
		srcClient, err := s.clientFor(ctx, srcCal.AccountID)
		if err != nil {
			return err
		}
		fb, err := srcClient.FreeBusy(ctx, []string{srcCal.GoogleCalendarID}, start, end, wh.TimeZone)
		if err != nil {
			return fmt.Errorf("freebusy %s: %w", srcCal.GoogleCalendarID, err)
		}
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
				allBusy = append(allBusy, window{ps, pe})
			}
		}
	}
	allBusy = mergeWindows(allBusy)

	// Build availability windows from working hours, then subtract busy time.
	avail := workingWindows(wh, start, end, loc)
	free := subtractBusy(avail, allBusy)

	// Apply min duration & merge gap.
	free = mergeWithGap(free, time.Duration(b.MergeGapMinutes)*time.Minute)
	min := time.Duration(b.MinBlockMinutes) * time.Minute
	desired := make([]window, 0, len(free))
	for _, w := range free {
		if w.End.Sub(w.Start) >= min {
			desired = append(desired, w)
		}
	}

	// Diff against existing managed blocks for this smart_block, in the horizon range.
	existing, err := s.managed.ListByBlock(ctx, b.ID)
	if err != nil {
		return err
	}
	// Filter existing to those overlapping [start, end] — outside-horizon ones we leave alone.
	type managedWithIdx struct {
		idx int
		m   db.ManagedBlock
	}
	inRange := []managedWithIdx{}
	for i, m := range existing {
		if m.EndsAt.After(start) && m.StartsAt.Before(end) {
			inRange = append(inRange, managedWithIdx{i, m})
		}
	}

	// Greedy match: for each desired window, find an exact-or-overlapping existing
	// to update; otherwise insert. Anything in-range and unmatched gets deleted.
	matchedExisting := make(map[int]bool, len(inRange))
	for _, d := range desired {
		var matchIdx = -1
		for j, e := range inRange {
			if matchedExisting[j] {
				continue
			}
			if windowsOverlap(d, window{e.m.StartsAt, e.m.EndsAt}) {
				matchIdx = j
				break
			}
		}
		if matchIdx == -1 {
			ev, err := tgtClient.InsertEvent(ctx, tgtCal.GoogleCalendarID, &gcal.Event{
				Summary:      b.TitleTemplate,
				Start:        &gcal.EventDateTime{DateTime: d.Start.Format(time.RFC3339), TimeZone: wh.TimeZone},
				End:          &gcal.EventDateTime{DateTime: d.End.Format(time.RFC3339), TimeZone: wh.TimeZone},
				Transparency: "opaque",
				ExtendedProperties: &gcal.EventExtendedProperties{
					Private: calendar.SmartBlockProps(b.ID),
				},
			})
			if err != nil {
				return fmt.Errorf("insert block: %w", err)
			}
			if _, err := s.managed.Insert(ctx, &db.ManagedBlock{
				SmartBlockID:     b.ID,
				TargetAccountID:  tgtCal.AccountID,
				TargetCalendarID: tgtCal.ID,
				TargetEventID:    ev.Id,
				StartsAt:         d.Start,
				EndsAt:           d.End,
			}); err != nil {
				return err
			}
			_ = s.audit.Write(ctx, db.AuditWrite{
				Kind:          "smart_block",
				SmartBlockID:  ptrInt64(b.ID),
				TargetEventID: ev.Id,
				Action:        "create",
			})
			continue
		}
		matched := inRange[matchIdx]
		matchedExisting[matchIdx] = true
		if matched.m.StartsAt.Equal(d.Start) && matched.m.EndsAt.Equal(d.End) {
			continue
		}
		_, err := tgtClient.UpdateEvent(ctx, tgtCal.GoogleCalendarID, matched.m.TargetEventID, &gcal.Event{
			Summary:      b.TitleTemplate,
			Start:        &gcal.EventDateTime{DateTime: d.Start.Format(time.RFC3339), TimeZone: wh.TimeZone},
			End:          &gcal.EventDateTime{DateTime: d.End.Format(time.RFC3339), TimeZone: wh.TimeZone},
			Transparency: "opaque",
			ExtendedProperties: &gcal.EventExtendedProperties{
				Private: calendar.SmartBlockProps(b.ID),
			},
		})
		if err != nil {
			return fmt.Errorf("update block: %w", err)
		}
		if err := s.managed.UpdateWindow(ctx, matched.m.ID, d.Start, d.End); err != nil {
			return err
		}
		_ = s.audit.Write(ctx, db.AuditWrite{
			Kind:          "smart_block",
			SmartBlockID:  ptrInt64(b.ID),
			TargetEventID: matched.m.TargetEventID,
			Action:        "update",
		})
	}
	// Delete leftovers.
	for j, m := range inRange {
		if matchedExisting[j] {
			continue
		}
		if err := tgtClient.DeleteEvent(ctx, tgtCal.GoogleCalendarID, m.m.TargetEventID); err != nil {
			s.log.Warn("delete block failed", "event_id", m.m.TargetEventID, "err", err)
		}
		_ = s.managed.Delete(ctx, m.m.ID)
		_ = s.audit.Write(ctx, db.AuditWrite{
			Kind:          "smart_block",
			SmartBlockID:  ptrInt64(b.ID),
			TargetEventID: m.m.TargetEventID,
			Action:        "delete",
		})
	}
	return nil
}

// workingWindows expands per-weekday WorkingHours into concrete time windows
// across [from, to). All times are in the working-hours timezone.
func workingWindows(wh WorkingHours, from, to time.Time, loc *time.Location) []window {
	var out []window
	day := from
	for day.Before(to) {
		key := dayKey(day.Weekday())
		ranges := wh.Days[key]
		for _, r := range ranges {
			start, end, ok := parseRange(r, day, loc)
			if !ok {
				continue
			}
			if end.Before(from) || start.After(to) {
				continue
			}
			if start.Before(from) {
				start = from
			}
			if end.After(to) {
				end = to
			}
			out = append(out, window{start, end})
		}
		day = day.AddDate(0, 0, 1)
	}
	return out
}

func dayKey(d time.Weekday) string {
	switch d {
	case time.Monday:
		return "mon"
	case time.Tuesday:
		return "tue"
	case time.Wednesday:
		return "wed"
	case time.Thursday:
		return "thu"
	case time.Friday:
		return "fri"
	case time.Saturday:
		return "sat"
	case time.Sunday:
		return "sun"
	}
	return ""
}

func parseRange(r string, day time.Time, loc *time.Location) (time.Time, time.Time, bool) {
	// Strict HH:MM-HH:MM with no trailing or leading junk. Anything else is
	// ignored so users get a recompute that does what the form said it would.
	var sh, sm, eh, em int
	var trailing rune
	n, _ := fmt.Sscanf(r, "%d:%d-%d:%d%c", &sh, &sm, &eh, &em, &trailing)
	if n != 4 {
		return time.Time{}, time.Time{}, false
	}
	if sh < 0 || sh > 23 || eh < 0 || eh > 23 || sm < 0 || sm > 59 || em < 0 || em > 59 {
		return time.Time{}, time.Time{}, false
	}
	start := time.Date(day.Year(), day.Month(), day.Day(), sh, sm, 0, 0, loc)
	end := time.Date(day.Year(), day.Month(), day.Day(), eh, em, 0, 0, loc)
	if !end.After(start) {
		return time.Time{}, time.Time{}, false
	}
	return start, end, true
}

func mergeWindows(in []window) []window {
	if len(in) == 0 {
		return nil
	}
	sort.Slice(in, func(i, j int) bool { return in[i].Start.Before(in[j].Start) })
	out := []window{in[0]}
	for _, w := range in[1:] {
		last := &out[len(out)-1]
		if !w.Start.After(last.End) {
			if w.End.After(last.End) {
				last.End = w.End
			}
			continue
		}
		out = append(out, w)
	}
	return out
}

func subtractBusy(avail, busy []window) []window {
	var out []window
	for _, a := range avail {
		segs := []window{a}
		for _, b := range busy {
			var next []window
			for _, s := range segs {
				if !windowsOverlap(s, b) {
					next = append(next, s)
					continue
				}
				if s.Start.Before(b.Start) {
					next = append(next, window{s.Start, b.Start})
				}
				if s.End.After(b.End) {
					next = append(next, window{b.End, s.End})
				}
			}
			segs = next
			if len(segs) == 0 {
				break
			}
		}
		out = append(out, segs...)
	}
	return out
}

func mergeWithGap(in []window, gap time.Duration) []window {
	if len(in) == 0 {
		return nil
	}
	sort.Slice(in, func(i, j int) bool { return in[i].Start.Before(in[j].Start) })
	out := []window{in[0]}
	for _, w := range in[1:] {
		last := &out[len(out)-1]
		if w.Start.Sub(last.End) <= gap {
			if w.End.After(last.End) {
				last.End = w.End
			}
			continue
		}
		out = append(out, w)
	}
	return out
}

func windowsOverlap(a, b window) bool {
	return a.Start.Before(b.End) && b.Start.Before(a.End)
}
