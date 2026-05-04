package db

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type SyncRuleRepo struct {
	pool *pgxpool.Pool
}

func NewSyncRuleRepo(pool *pgxpool.Pool) *SyncRuleRepo { return &SyncRuleRepo{pool: pool} }

const syncRuleSelectCols = `id, name, source_calendar_id, target_calendar_id, direction, primary_side,
	       filter_jsonb, transform_jsonb, backfill_days, backfill_done, enabled, created_at,
	       category_id, visibility_mode, all_day_mode, working_hours_only`

func (r *SyncRuleRepo) Create(ctx context.Context, ru *SyncRule) (int64, error) {
	if len(ru.Filter) == 0 {
		ru.Filter = json.RawMessage(`{}`)
	}
	if len(ru.Transform) == 0 {
		ru.Transform = json.RawMessage(`{}`)
	}
	if ru.VisibilityMode == "" {
		ru.VisibilityMode = "busy_for_all"
	}
	if ru.AllDayMode == "" {
		ru.AllDayMode = "sync_all"
	}
	var id int64
	err := r.pool.QueryRow(ctx, `
		INSERT INTO sync_rule (name, source_calendar_id, target_calendar_id, direction, primary_side,
		                       filter_jsonb, transform_jsonb, backfill_days, enabled,
		                       category_id, visibility_mode, all_day_mode, working_hours_only)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13) RETURNING id`,
		ru.Name, ru.SourceCalendarID, ru.TargetCalendarID, ru.Direction, ru.PrimarySide,
		ru.Filter, ru.Transform, ru.BackfillDays, ru.Enabled,
		ru.CategoryID, ru.VisibilityMode, ru.AllDayMode, ru.WorkingHoursOnly).Scan(&id)
	return id, err
}

func (r *SyncRuleRepo) Update(ctx context.Context, ru *SyncRule) error {
	if len(ru.Filter) == 0 {
		ru.Filter = json.RawMessage(`{}`)
	}
	if len(ru.Transform) == 0 {
		ru.Transform = json.RawMessage(`{}`)
	}
	if ru.VisibilityMode == "" {
		ru.VisibilityMode = "busy_for_all"
	}
	if ru.AllDayMode == "" {
		ru.AllDayMode = "sync_all"
	}
	_, err := r.pool.Exec(ctx, `
		UPDATE sync_rule
		SET name = $2, source_calendar_id = $3, target_calendar_id = $4, direction = $5,
		    primary_side = $6, filter_jsonb = $7, transform_jsonb = $8, backfill_days = $9, enabled = $10,
		    category_id = $11, visibility_mode = $12, all_day_mode = $13, working_hours_only = $14
		WHERE id = $1`,
		ru.ID, ru.Name, ru.SourceCalendarID, ru.TargetCalendarID, ru.Direction,
		ru.PrimarySide, ru.Filter, ru.Transform, ru.BackfillDays, ru.Enabled,
		ru.CategoryID, ru.VisibilityMode, ru.AllDayMode, ru.WorkingHoursOnly)
	return err
}

func (r *SyncRuleRepo) MarkBackfillDone(ctx context.Context, id int64) error {
	_, err := r.pool.Exec(ctx, `UPDATE sync_rule SET backfill_done = TRUE WHERE id = $1`, id)
	return err
}

func (r *SyncRuleRepo) ResetBackfill(ctx context.Context, id int64) error {
	_, err := r.pool.Exec(ctx, `UPDATE sync_rule SET backfill_done = FALSE WHERE id = $1`, id)
	return err
}

func (r *SyncRuleRepo) Delete(ctx context.Context, id int64) error {
	_, err := r.pool.Exec(ctx, `DELETE FROM sync_rule WHERE id = $1`, id)
	return err
}

func (r *SyncRuleRepo) Get(ctx context.Context, id int64) (*SyncRule, error) {
	row := r.pool.QueryRow(ctx, `SELECT `+syncRuleSelectCols+` FROM sync_rule WHERE id = $1`, id)
	ru, err := scanRule(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return ru, err
}

func (r *SyncRuleRepo) List(ctx context.Context) ([]SyncRule, error) {
	rows, err := r.pool.Query(ctx, `SELECT `+syncRuleSelectCols+` FROM sync_rule ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectRules(rows)
}

func (r *SyncRuleRepo) ListBySourceCalendar(ctx context.Context, calendarID int64) ([]SyncRule, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT `+syncRuleSelectCols+`
		FROM sync_rule
		WHERE enabled = TRUE
		  AND (source_calendar_id = $1 OR (direction = 'bidirectional' AND target_calendar_id = $1))`,
		calendarID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectRules(rows)
}

func collectRules(rows pgx.Rows) ([]SyncRule, error) {
	var out []SyncRule
	for rows.Next() {
		ru, err := scanRule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *ru)
	}
	return out, rows.Err()
}

func scanRule(row rowScanner) (*SyncRule, error) {
	var ru SyncRule
	if err := row.Scan(&ru.ID, &ru.Name, &ru.SourceCalendarID, &ru.TargetCalendarID, &ru.Direction,
		&ru.PrimarySide, &ru.Filter, &ru.Transform, &ru.BackfillDays, &ru.BackfillDone, &ru.Enabled, &ru.CreatedAt,
		&ru.CategoryID, &ru.VisibilityMode, &ru.AllDayMode, &ru.WorkingHoursOnly); err != nil {
		return nil, err
	}
	return &ru, nil
}
