package db

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type DecompressionEvent struct {
	ID            int64
	CalendarID    int64
	SourceEventID string
	TargetEventID string
	StartsAt      time.Time
	EndsAt        time.Time
	LastSeenAt    time.Time
}

type DecompressionRepo struct {
	pool *pgxpool.Pool
}

func NewDecompressionRepo(pool *pgxpool.Pool) *DecompressionRepo {
	return &DecompressionRepo{pool: pool}
}

func (r *DecompressionRepo) GetBySource(ctx context.Context, calendarID int64, sourceEventID string) (*DecompressionEvent, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT id, calendar_id, source_event_id, target_event_id, starts_at, ends_at, last_seen_at
		FROM decompression_event WHERE calendar_id = $1 AND source_event_id = $2`,
		calendarID, sourceEventID)
	var d DecompressionEvent
	if err := row.Scan(&d.ID, &d.CalendarID, &d.SourceEventID, &d.TargetEventID,
		&d.StartsAt, &d.EndsAt, &d.LastSeenAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &d, nil
}

func (r *DecompressionRepo) ListByCalendarInRange(ctx context.Context, calendarID int64, from, to time.Time) ([]DecompressionEvent, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, calendar_id, source_event_id, target_event_id, starts_at, ends_at, last_seen_at
		FROM decompression_event
		WHERE calendar_id = $1 AND ends_at >= $2 AND starts_at < $3
		ORDER BY starts_at`,
		calendarID, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DecompressionEvent
	for rows.Next() {
		var d DecompressionEvent
		if err := rows.Scan(&d.ID, &d.CalendarID, &d.SourceEventID, &d.TargetEventID,
			&d.StartsAt, &d.EndsAt, &d.LastSeenAt); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (r *DecompressionRepo) Insert(ctx context.Context, d *DecompressionEvent) (int64, error) {
	var id int64
	err := r.pool.QueryRow(ctx, `
		INSERT INTO decompression_event (calendar_id, source_event_id, target_event_id, starts_at, ends_at, last_seen_at)
		VALUES ($1, $2, $3, $4, $5, NOW()) RETURNING id`,
		d.CalendarID, d.SourceEventID, d.TargetEventID, d.StartsAt, d.EndsAt).Scan(&id)
	return id, err
}

func (r *DecompressionRepo) UpdateWindow(ctx context.Context, id int64, starts, ends time.Time) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE decompression_event SET starts_at = $2, ends_at = $3, last_seen_at = NOW()
		WHERE id = $1`, id, starts, ends)
	return err
}

func (r *DecompressionRepo) Delete(ctx context.Context, id int64) error {
	_, err := r.pool.Exec(ctx, `DELETE FROM decompression_event WHERE id = $1`, id)
	return err
}
