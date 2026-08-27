package db

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// ManagedWindow is a span of time skulid itself wrote to Google.
type ManagedWindow struct {
	StartsAt time.Time
	EndsAt   time.Time
}

// ManagedWindowRepo reads back the windows skulid has written, across every
// subsystem that writes one.
type ManagedWindowRepo struct {
	pool *pgxpool.Pool
}

func NewManagedWindowRepo(pool *pgxpool.Pool) *ManagedWindowRepo {
	return &ManagedWindowRepo{pool: pool}
}

// InRange returns every window skulid wrote that overlaps [from, to).
//
// Freebusy cannot distinguish skulid's own smart blocks and buffers from real
// meetings -- it returns opaque busy periods with no extendedProperties. But
// skulid recorded the window of every event it wrote at the time it wrote it,
// so the same answer is available locally for the cost of one query and no
// Google API calls at all.
//
// Tasks come from task_chunk rather than task.scheduled_*: a task can occupy
// several blocks, and reading only the summary columns would leave its later
// chunks reading as busy against itself.
//
// Travel buffers are deliberately absent. A decompression block is subtracted
// because the protection it represents is already applied a second way -- the
// scheduler pads every busy window by the decompression minutes -- so counting
// the visible block too would double it. Travel has no such padding: the
// visible block IS the protection, so it has to keep reading as busy.
//
// Overlap is half-open on both sides: a window ending exactly at `from`, or
// starting exactly at `to`, does not overlap.
func (r *ManagedWindowRepo) InRange(ctx context.Context, from, to time.Time) ([]ManagedWindow, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT starts_at, ends_at FROM managed_block
		WHERE ends_at > $1 AND starts_at < $2
		UNION ALL
		SELECT starts_at, ends_at FROM habit_occurrence
		WHERE ends_at > $1 AND starts_at < $2
		UNION ALL
		SELECT starts_at, ends_at FROM buffer_event
		WHERE buffer_type = 'decompression' AND ends_at > $1 AND starts_at < $2
		UNION ALL
		SELECT starts_at, ends_at FROM task_chunk
		WHERE ends_at > $1 AND starts_at < $2`, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []ManagedWindow
	for rows.Next() {
		var w ManagedWindow
		if err := rows.Scan(&w.StartsAt, &w.EndsAt); err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}
