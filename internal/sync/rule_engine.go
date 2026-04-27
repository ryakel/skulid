// Package sync implements the rule engine and smart-block engine.
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

// ClientFor returns a Google Calendar client for the given account ID.
type ClientFor func(ctx context.Context, accountID int64) (*calendar.Client, error)

type Engine struct {
	rules     *db.SyncRuleRepo
	calendars *db.CalendarRepo
	links     *db.EventLinkRepo
	audit     *db.AuditRepo
	clientFor ClientFor
	log       *slog.Logger
}

func NewEngine(rules *db.SyncRuleRepo, calendars *db.CalendarRepo, links *db.EventLinkRepo, audit *db.AuditRepo, clientFor ClientFor, log *slog.Logger) *Engine {
	return &Engine{
		rules:     rules,
		calendars: calendars,
		links:     links,
		audit:     audit,
		clientFor: clientFor,
		log:       log,
	}
}

// ProcessChange runs all active rules whose source calendar matches the
// supplied calendar, against a single inbound source event. Cancelled events
// trigger removal of the linked target.
func (e *Engine) ProcessChange(ctx context.Context, sourceCalendarID int64, ev *gcal.Event) error {
	rules, err := e.rules.ListBySourceCalendar(ctx, sourceCalendarID)
	if err != nil {
		return fmt.Errorf("list rules: %w", err)
	}
	for _, rule := range rules {
		// Resolve direction relative to this calendar.
		isReverse := false
		if rule.SourceCalendarID != sourceCalendarID {
			// This calendar is the *target* in the rule and the rule is bidirectional.
			isReverse = true
		}
		if err := e.applyRule(ctx, rule, ev, isReverse); err != nil {
			e.log.Error("apply rule failed", "rule_id", rule.ID, "event_id", ev.Id, "err", err)
			_ = e.audit.Write(ctx, db.AuditWrite{
				Kind:          "rule",
				RuleID:        ptrInt64(rule.ID),
				SourceEventID: ev.Id,
				Action:        "error",
				Message:       err.Error(),
			})
		}
	}
	return nil
}

func (e *Engine) applyRule(ctx context.Context, rule db.SyncRule, ev *gcal.Event, reverseDirection bool) error {
	// Loop guard: never propagate events we created.
	if calendar.IsManaged(ev) {
		return nil
	}

	srcCalID := rule.SourceCalendarID
	tgtCalID := rule.TargetCalendarID
	if reverseDirection {
		srcCalID, tgtCalID = tgtCalID, srcCalID
	}

	srcCal, err := e.calendars.Get(ctx, srcCalID)
	if err != nil {
		return fmt.Errorf("load source cal: %w", err)
	}
	tgtCal, err := e.calendars.Get(ctx, tgtCalID)
	if err != nil {
		return fmt.Errorf("load target cal: %w", err)
	}

	// Build an effective rule key for event_link. We always store using the
	// canonical (rule.id, source_event_id) — but for reverse passes we use a
	// synthetic key so forward + reverse don't collide.
	linkKeyEventID := ev.Id
	if reverseDirection {
		linkKeyEventID = "rev:" + ev.Id
	}
	existing, err := e.links.Get(ctx, rule.ID, linkKeyEventID)
	if err != nil {
		return fmt.Errorf("lookup event link: %w", err)
	}

	tgtClient, err := e.clientFor(ctx, tgtCal.AccountID)
	if err != nil {
		return fmt.Errorf("target client: %w", err)
	}

	// Cancellation: if source is cancelled and we have a link, delete the mirror.
	if ev.Status == "cancelled" {
		if existing != nil {
			if err := tgtClient.DeleteEvent(ctx, tgtCal.GoogleCalendarID, existing.TargetEventID); err != nil {
				return fmt.Errorf("delete mirror: %w", err)
			}
			_ = e.links.Delete(ctx, existing.ID)
			_ = e.audit.Write(ctx, db.AuditWrite{
				Kind:          "rule",
				RuleID:        ptrInt64(rule.ID),
				SourceEventID: ev.Id,
				TargetEventID: existing.TargetEventID,
				Action:        "delete",
			})
		}
		return nil
	}

	filter, err := ParseFilter(rule.Filter)
	if err != nil {
		return fmt.Errorf("parse filter: %w", err)
	}
	transform, err := ParseTransform(rule.Transform)
	if err != nil {
		return fmt.Errorf("parse transform: %w", err)
	}

	if !filter.Match(ev) {
		// If a mirror exists but the event no longer matches, drop the mirror.
		if existing != nil {
			if err := tgtClient.DeleteEvent(ctx, tgtCal.GoogleCalendarID, existing.TargetEventID); err != nil {
				return fmt.Errorf("delete unmatched mirror: %w", err)
			}
			_ = e.links.Delete(ctx, existing.ID)
			_ = e.audit.Write(ctx, db.AuditWrite{
				Kind:          "rule",
				RuleID:        ptrInt64(rule.ID),
				SourceEventID: ev.Id,
				TargetEventID: existing.TargetEventID,
				Action:        "filter_drop",
			})
		}
		return nil
	}

	mirror := transform.Apply(ev)
	mirror.ExtendedProperties = &gcal.EventExtendedProperties{
		Private: calendar.ManagedProps(rule.ID, ev.Id),
	}

	var saved *gcal.Event
	action := ""
	if existing == nil {
		saved, err = tgtClient.InsertEvent(ctx, tgtCal.GoogleCalendarID, mirror)
		if err != nil {
			return fmt.Errorf("insert mirror: %w", err)
		}
		action = "create"
	} else {
		// For bidirectional rules: if the source hasn't actually changed since
		// we last synced, skip — this prevents the recursive update loop where
		// an outbound mirror update arrives back as a webhook on the other side.
		if rule.Direction == "bidirectional" && ev.Etag != "" && ev.Etag == existing.SourceEtag {
			return nil
		}
		saved, err = tgtClient.UpdateEvent(ctx, tgtCal.GoogleCalendarID, existing.TargetEventID, mirror)
		if err != nil {
			return fmt.Errorf("update mirror: %w", err)
		}
		action = "update"
	}

	_, err = e.links.Upsert(ctx, &db.EventLink{
		RuleID:           rule.ID,
		SourceAccountID:  srcCal.AccountID,
		SourceCalendarID: srcCalID,
		SourceEventID:    linkKeyEventID,
		TargetAccountID:  tgtCal.AccountID,
		TargetCalendarID: tgtCalID,
		TargetEventID:    saved.Id,
		SourceEtag:       ev.Etag,
		TargetEtag:       saved.Etag,
	})
	if err != nil {
		return fmt.Errorf("upsert link: %w", err)
	}

	_ = e.audit.Write(ctx, db.AuditWrite{
		Kind:          "rule",
		RuleID:        ptrInt64(rule.ID),
		SourceEventID: ev.Id,
		TargetEventID: saved.Id,
		Action:        action,
	})
	return nil
}

// Backfill walks the source calendar over the past N days and runs each event
// through the rule engine. Called once per rule when a backfill is requested.
func (e *Engine) Backfill(ctx context.Context, ruleID int64) error {
	rule, err := e.rules.Get(ctx, ruleID)
	if err != nil || rule == nil {
		return fmt.Errorf("rule not found")
	}
	if rule.BackfillDays <= 0 {
		return nil
	}
	srcCal, err := e.calendars.Get(ctx, rule.SourceCalendarID)
	if err != nil {
		return err
	}
	cli, err := e.clientFor(ctx, srcCal.AccountID)
	if err != nil {
		return err
	}
	from := time.Now().AddDate(0, 0, -rule.BackfillDays)
	res, err := cli.IncrementalSync(ctx, srcCal.GoogleCalendarID, "", from)
	if err != nil {
		return err
	}
	for _, ev := range res.Events {
		if err := e.applyRule(ctx, *rule, ev, false); err != nil {
			e.log.Error("backfill apply failed", "event_id", ev.Id, "err", err)
		}
	}
	if err := e.rules.MarkBackfillDone(ctx, ruleID); err != nil {
		return err
	}
	_ = e.audit.Write(ctx, db.AuditWrite{
		Kind:    "rule",
		RuleID:  ptrInt64(ruleID),
		Action:  "backfill_complete",
		Message: fmt.Sprintf("processed %d events", len(res.Events)),
	})
	return nil
}

func ptrInt64(v int64) *int64 { return &v }
