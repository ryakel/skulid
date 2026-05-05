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
	if !tgtCal.Enabled {
		// Disabled target calendar — leave existing managed blocks alone
		// (they'll get reaped when the user re-enables and the next recompute
		// runs against fresh freebusy).
		return nil
	}
	tgtClient, err := s.clientFor(ctx, tgtCal.AccountID)
	if err != nil {
		return err
	}

	wh, err := hours.Parse(b.WorkingHours)
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
	var allBusy []hours.Window
	for _, srcID := range b.SourceCalendarIDs {
		srcCal, err := s.calendars.Get(ctx, srcID)
		if err != nil {
			s.log.Warn("smart block source cal missing", "cal_id", srcID, "err", err)
			continue
		}
		if !srcCal.Enabled {
			// Disabled source contributes no busy time.
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
				allBusy = append(allBusy, hours.Window{Start: ps, End: pe})
			}
		}
	}
	allBusy = hours.Merge(allBusy)

	// Build availability windows from working hours, then subtract busy time.
	avail := hours.Expand(wh, start, end, loc)
	free := hours.SubtractBusy(avail, allBusy)

	// Apply min duration & merge gap.
	free = hours.MergeWithGap(free, time.Duration(b.MergeGapMinutes)*time.Minute)
	min := time.Duration(b.MinBlockMinutes) * time.Minute
	desired := make([]hours.Window, 0, len(free))
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
			if hours.Overlap(d, hours.Window{Start: e.m.StartsAt, End: e.m.EndsAt}) {
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
