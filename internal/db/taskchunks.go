package db

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// TaskChunk is one calendar block making up part of a task's duration. A task
// that fits in a single window has exactly one; a long task on a busy calendar
// has several, in `Seq` order.
type TaskChunk struct {
	ID            int64
	TaskID        int64
	Seq           int
	GoogleEventID string
	StartsAt      time.Time
	EndsAt        time.Time
}

type TaskChunkRepo struct{ pool *pgxpool.Pool }

func NewTaskChunkRepo(pool *pgxpool.Pool) *TaskChunkRepo { return &TaskChunkRepo{pool: pool} }

func (r *TaskChunkRepo) ListByTask(ctx context.Context, taskID int64) ([]TaskChunk, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, task_id, seq, google_event_id, starts_at, ends_at
		FROM task_chunk WHERE task_id = $1 ORDER BY seq`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TaskChunk
	for rows.Next() {
		var c TaskChunk
		if err := rows.Scan(&c.ID, &c.TaskID, &c.Seq, &c.GoogleEventID, &c.StartsAt, &c.EndsAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (r *TaskChunkRepo) Insert(ctx context.Context, c *TaskChunk) (int64, error) {
	var id int64
	err := r.pool.QueryRow(ctx, `
		INSERT INTO task_chunk (task_id, seq, google_event_id, starts_at, ends_at)
		VALUES ($1, $2, $3, $4, $5) RETURNING id`,
		c.TaskID, c.Seq, c.GoogleEventID, c.StartsAt, c.EndsAt).Scan(&id)
	return id, err
}

func (r *TaskChunkRepo) UpdateWindow(ctx context.Context, id int64, starts, ends time.Time) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE task_chunk SET starts_at = $2, ends_at = $3 WHERE id = $1`, id, starts, ends)
	return err
}

func (r *TaskChunkRepo) Delete(ctx context.Context, id int64) error {
	_, err := r.pool.Exec(ctx, `DELETE FROM task_chunk WHERE id = $1`, id)
	return err
}

func (r *TaskChunkRepo) DeleteByTask(ctx context.Context, taskID int64) error {
	_, err := r.pool.Exec(ctx, `DELETE FROM task_chunk WHERE task_id = $1`, taskID)
	return err
}
