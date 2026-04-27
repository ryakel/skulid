package db

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type SmartBlockRepo struct {
	pool *pgxpool.Pool
}

func NewSmartBlockRepo(pool *pgxpool.Pool) *SmartBlockRepo { return &SmartBlockRepo{pool: pool} }

func (r *SmartBlockRepo) Create(ctx context.Context, b *SmartBlock) (int64, error) {
	if len(b.WorkingHours) == 0 {
		b.WorkingHours = json.RawMessage(`{}`)
	}
	if b.SourceCalendarIDs == nil {
		b.SourceCalendarIDs = []int64{}
	}
	var id int64
	err := r.pool.QueryRow(ctx, `
		INSERT INTO smart_block (name, target_calendar_id, source_calendar_ids, working_hours_jsonb,
		                         horizon_days, min_block_minutes, merge_gap_minutes, title_template, enabled)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9) RETURNING id`,
		b.Name, b.TargetCalendarID, b.SourceCalendarIDs, b.WorkingHours,
		b.HorizonDays, b.MinBlockMinutes, b.MergeGapMinutes, b.TitleTemplate, b.Enabled).Scan(&id)
	return id, err
}

func (r *SmartBlockRepo) Update(ctx context.Context, b *SmartBlock) error {
	if len(b.WorkingHours) == 0 {
		b.WorkingHours = json.RawMessage(`{}`)
	}
	if b.SourceCalendarIDs == nil {
		b.SourceCalendarIDs = []int64{}
	}
	_, err := r.pool.Exec(ctx, `
		UPDATE smart_block
		SET name = $2, target_calendar_id = $3, source_calendar_ids = $4, working_hours_jsonb = $5,
		    horizon_days = $6, min_block_minutes = $7, merge_gap_minutes = $8, title_template = $9, enabled = $10
		WHERE id = $1`,
		b.ID, b.Name, b.TargetCalendarID, b.SourceCalendarIDs, b.WorkingHours,
		b.HorizonDays, b.MinBlockMinutes, b.MergeGapMinutes, b.TitleTemplate, b.Enabled)
	return err
}

func (r *SmartBlockRepo) Delete(ctx context.Context, id int64) error {
	_, err := r.pool.Exec(ctx, `DELETE FROM smart_block WHERE id = $1`, id)
	return err
}

func (r *SmartBlockRepo) Get(ctx context.Context, id int64) (*SmartBlock, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT id, name, target_calendar_id, source_calendar_ids, working_hours_jsonb,
		       horizon_days, min_block_minutes, merge_gap_minutes, title_template, enabled, created_at
		FROM smart_block WHERE id = $1`, id)
	b, err := scanSmartBlock(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return b, err
}

func (r *SmartBlockRepo) List(ctx context.Context) ([]SmartBlock, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, name, target_calendar_id, source_calendar_ids, working_hours_jsonb,
		       horizon_days, min_block_minutes, merge_gap_minutes, title_template, enabled, created_at
		FROM smart_block ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SmartBlock
	for rows.Next() {
		b, err := scanSmartBlock(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *b)
	}
	return out, rows.Err()
}

func (r *SmartBlockRepo) ListBySourceCalendar(ctx context.Context, calendarID int64) ([]SmartBlock, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, name, target_calendar_id, source_calendar_ids, working_hours_jsonb,
		       horizon_days, min_block_minutes, merge_gap_minutes, title_template, enabled, created_at
		FROM smart_block
		WHERE enabled = TRUE AND $1 = ANY(source_calendar_ids)`, calendarID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SmartBlock
	for rows.Next() {
		b, err := scanSmartBlock(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *b)
	}
	return out, rows.Err()
}

func scanSmartBlock(row rowScanner) (*SmartBlock, error) {
	var b SmartBlock
	if err := row.Scan(&b.ID, &b.Name, &b.TargetCalendarID, &b.SourceCalendarIDs, &b.WorkingHours,
		&b.HorizonDays, &b.MinBlockMinutes, &b.MergeGapMinutes, &b.TitleTemplate, &b.Enabled, &b.CreatedAt); err != nil {
		return nil, err
	}
	return &b, nil
}

type ManagedBlockRepo struct {
	pool *pgxpool.Pool
}

func NewManagedBlockRepo(pool *pgxpool.Pool) *ManagedBlockRepo { return &ManagedBlockRepo{pool: pool} }

func (r *ManagedBlockRepo) ListByBlock(ctx context.Context, smartBlockID int64) ([]ManagedBlock, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, smart_block_id, target_account_id, target_calendar_id, target_event_id,
		       starts_at, ends_at, last_synced_at
		FROM managed_block WHERE smart_block_id = $1 ORDER BY starts_at`, smartBlockID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ManagedBlock
	for rows.Next() {
		var m ManagedBlock
		if err := rows.Scan(&m.ID, &m.SmartBlockID, &m.TargetAccountID, &m.TargetCalendarID, &m.TargetEventID,
			&m.StartsAt, &m.EndsAt, &m.LastSyncedAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (r *ManagedBlockRepo) Insert(ctx context.Context, m *ManagedBlock) (int64, error) {
	var id int64
	err := r.pool.QueryRow(ctx, `
		INSERT INTO managed_block (smart_block_id, target_account_id, target_calendar_id, target_event_id, starts_at, ends_at, last_synced_at)
		VALUES ($1, $2, $3, $4, $5, $6, NOW()) RETURNING id`,
		m.SmartBlockID, m.TargetAccountID, m.TargetCalendarID, m.TargetEventID, m.StartsAt, m.EndsAt).Scan(&id)
	return id, err
}

func (r *ManagedBlockRepo) UpdateWindow(ctx context.Context, id int64, starts, ends time.Time) error {
	_, err := r.pool.Exec(ctx, `UPDATE managed_block SET starts_at = $2, ends_at = $3, last_synced_at = NOW() WHERE id = $1`, id, starts, ends)
	return err
}

func (r *ManagedBlockRepo) Delete(ctx context.Context, id int64) error {
	_, err := r.pool.Exec(ctx, `DELETE FROM managed_block WHERE id = $1`, id)
	return err
}
