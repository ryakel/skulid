package db

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Buffer kinds. These are the values of the `skulidBufferType` extended
// property on the Google event as well as the `buffer_type` column, so the
// two stay readable against each other.
const (
	BufferDecompression = "decompression"
	BufferTravel        = "travel"
)

// Which side of the source meeting a buffer sits on.
const (
	PlacementBefore = "before"
	PlacementAfter  = "after"
)

// BufferEvent is one visible padding event skulid wrote to Google, tied to the
// meeting that justifies it. A source meeting owns at most one row per
// (type, placement) pair: a trailing decompression block plus travel either
// side of it, at most.
type BufferEvent struct {
	ID            int64
	CalendarID    int64
	SourceEventID string
	TargetEventID string
	BufferType    string
	Placement     string
	StartsAt      time.Time
	EndsAt        time.Time
	LastSeenAt    time.Time
}

// Key identifies the buffer a row is, independent of where it currently sits.
// The reconciler diffs desired against existing on this.
func (b BufferEvent) Key() BufferKey {
	return BufferKey{SourceEventID: b.SourceEventID, BufferType: b.BufferType, Placement: b.Placement}
}

// BufferKey is a comparable identity for a buffer, usable as a map key.
type BufferKey struct {
	SourceEventID string
	BufferType    string
	Placement     string
}

type BufferEventRepo struct {
	pool *pgxpool.Pool
}

func NewBufferEventRepo(pool *pgxpool.Pool) *BufferEventRepo {
	return &BufferEventRepo{pool: pool}
}

const bufferEventCols = `id, calendar_id, source_event_id, target_event_id,
	buffer_type, placement, starts_at, ends_at, last_seen_at`

func scanBufferEvent(row pgx.Row, b *BufferEvent) error {
	return row.Scan(&b.ID, &b.CalendarID, &b.SourceEventID, &b.TargetEventID,
		&b.BufferType, &b.Placement, &b.StartsAt, &b.EndsAt, &b.LastSeenAt)
}

func (r *BufferEventRepo) Get(ctx context.Context, calendarID int64, key BufferKey) (*BufferEvent, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT `+bufferEventCols+` FROM buffer_event
		WHERE calendar_id = $1 AND source_event_id = $2
		  AND buffer_type = $3 AND placement = $4`,
		calendarID, key.SourceEventID, key.BufferType, key.Placement)
	var b BufferEvent
	if err := scanBufferEvent(row, &b); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &b, nil
}

func (r *BufferEventRepo) ListByCalendarInRange(ctx context.Context, calendarID int64, from, to time.Time) ([]BufferEvent, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT `+bufferEventCols+` FROM buffer_event
		WHERE calendar_id = $1 AND ends_at >= $2 AND starts_at < $3
		ORDER BY starts_at`,
		calendarID, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []BufferEvent
	for rows.Next() {
		var b BufferEvent
		if err := scanBufferEvent(rows, &b); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

func (r *BufferEventRepo) Insert(ctx context.Context, b *BufferEvent) (int64, error) {
	var id int64
	err := r.pool.QueryRow(ctx, `
		INSERT INTO buffer_event
			(calendar_id, source_event_id, target_event_id, buffer_type, placement, starts_at, ends_at, last_seen_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, NOW()) RETURNING id`,
		b.CalendarID, b.SourceEventID, b.TargetEventID, b.BufferType, b.Placement,
		b.StartsAt, b.EndsAt).Scan(&id)
	return id, err
}

func (r *BufferEventRepo) UpdateWindow(ctx context.Context, id int64, starts, ends time.Time) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE buffer_event SET starts_at = $2, ends_at = $3, last_seen_at = NOW()
		WHERE id = $1`, id, starts, ends)
	return err
}

func (r *BufferEventRepo) Delete(ctx context.Context, id int64) error {
	_, err := r.pool.Exec(ctx, `DELETE FROM buffer_event WHERE id = $1`, id)
	return err
}
