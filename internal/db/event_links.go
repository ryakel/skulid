package db

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type EventLinkRepo struct {
	pool *pgxpool.Pool
}

func NewEventLinkRepo(pool *pgxpool.Pool) *EventLinkRepo { return &EventLinkRepo{pool: pool} }

func (r *EventLinkRepo) Get(ctx context.Context, ruleID int64, sourceEventID string) (*EventLink, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT id, rule_id, source_account_id, source_calendar_id, source_event_id,
		       target_account_id, target_calendar_id, target_event_id,
		       source_etag, target_etag, last_synced_at
		FROM event_link WHERE rule_id = $1 AND source_event_id = $2`, ruleID, sourceEventID)
	return scanEventLink(row)
}

func (r *EventLinkRepo) Upsert(ctx context.Context, e *EventLink) (int64, error) {
	var id int64
	err := r.pool.QueryRow(ctx, `
		INSERT INTO event_link (rule_id, source_account_id, source_calendar_id, source_event_id,
		                        target_account_id, target_calendar_id, target_event_id,
		                        source_etag, target_etag, last_synced_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, NOW())
		ON CONFLICT (rule_id, source_event_id) DO UPDATE SET
		    target_account_id = EXCLUDED.target_account_id,
		    target_calendar_id = EXCLUDED.target_calendar_id,
		    target_event_id = EXCLUDED.target_event_id,
		    source_etag = EXCLUDED.source_etag,
		    target_etag = EXCLUDED.target_etag,
		    last_synced_at = NOW()
		RETURNING id`,
		e.RuleID, e.SourceAccountID, e.SourceCalendarID, e.SourceEventID,
		e.TargetAccountID, e.TargetCalendarID, e.TargetEventID,
		e.SourceEtag, e.TargetEtag).Scan(&id)
	return id, err
}

func (r *EventLinkRepo) Delete(ctx context.Context, id int64) error {
	_, err := r.pool.Exec(ctx, `DELETE FROM event_link WHERE id = $1`, id)
	return err
}

func (r *EventLinkRepo) DeleteByRuleAndSource(ctx context.Context, ruleID int64, sourceEventID string) error {
	_, err := r.pool.Exec(ctx, `DELETE FROM event_link WHERE rule_id = $1 AND source_event_id = $2`, ruleID, sourceEventID)
	return err
}

func (r *EventLinkRepo) ListByRule(ctx context.Context, ruleID int64) ([]EventLink, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, rule_id, source_account_id, source_calendar_id, source_event_id,
		       target_account_id, target_calendar_id, target_event_id,
		       source_etag, target_etag, last_synced_at
		FROM event_link WHERE rule_id = $1`, ruleID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []EventLink
	for rows.Next() {
		e, err := scanEventLink(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *e)
	}
	return out, rows.Err()
}

func scanEventLink(row rowScanner) (*EventLink, error) {
	var e EventLink
	if err := row.Scan(&e.ID, &e.RuleID, &e.SourceAccountID, &e.SourceCalendarID, &e.SourceEventID,
		&e.TargetAccountID, &e.TargetCalendarID, &e.TargetEventID,
		&e.SourceEtag, &e.TargetEtag, &e.LastSyncedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &e, nil
}
