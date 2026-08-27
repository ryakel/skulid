package db

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type TaskRepo struct{ pool *pgxpool.Pool }

func NewTaskRepo(pool *pgxpool.Pool) *TaskRepo { return &TaskRepo{pool: pool} }

const taskSelectCols = `id, title, notes, priority, duration_minutes, due_at, status,
	       target_calendar_id, category_id, scheduled_event_id,
	       scheduled_starts_at, scheduled_ends_at, schedule_note, created_at, updated_at`

func (r *TaskRepo) Create(ctx context.Context, t *Task) (int64, error) {
	if t.Priority == "" {
		t.Priority = PriorityMedium
	}
	if t.Status == "" {
		t.Status = TaskPending
	}
	if t.DurationMinutes <= 0 {
		t.DurationMinutes = 30
	}
	var id int64
	err := r.pool.QueryRow(ctx, `
		INSERT INTO task (title, notes, priority, duration_minutes, due_at, status,
		                  target_calendar_id, category_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8) RETURNING id`,
		t.Title, t.Notes, t.Priority, t.DurationMinutes, t.DueAt, t.Status,
		t.TargetCalendarID, t.CategoryID).Scan(&id)
	return id, err
}

func (r *TaskRepo) Update(ctx context.Context, t *Task) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE task SET title = $2, notes = $3, priority = $4, duration_minutes = $5,
		    due_at = $6, status = $7, target_calendar_id = $8, category_id = $9,
		    updated_at = NOW()
		WHERE id = $1`,
		t.ID, t.Title, t.Notes, t.Priority, t.DurationMinutes, t.DueAt,
		t.Status, t.TargetCalendarID, t.CategoryID)
	return err
}

// UpdateScheduled writes the summary of a task's placement: the first chunk's
// event and window, the resulting status, and why it is where it is. `note`
// is empty on success and carries the reason when nothing could be placed.
func (r *TaskRepo) UpdateScheduled(ctx context.Context, id int64, eventID string, starts, ends *time.Time, status, note string) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE task SET scheduled_event_id = $2, scheduled_starts_at = $3,
		    scheduled_ends_at = $4, status = $5, schedule_note = $6, updated_at = NOW()
		WHERE id = $1`,
		id, eventID, starts, ends, status, note)
	return err
}

func (r *TaskRepo) Delete(ctx context.Context, id int64) error {
	_, err := r.pool.Exec(ctx, `DELETE FROM task WHERE id = $1`, id)
	return err
}

func (r *TaskRepo) Get(ctx context.Context, id int64) (*Task, error) {
	row := r.pool.QueryRow(ctx, `SELECT `+taskSelectCols+` FROM task WHERE id = $1`, id)
	t, err := scanTask(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return t, err
}

func (r *TaskRepo) List(ctx context.Context) ([]Task, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT `+taskSelectCols+` FROM task
		ORDER BY CASE priority
		  WHEN 'critical' THEN 0
		  WHEN 'high' THEN 1
		  WHEN 'medium' THEN 2
		  WHEN 'low' THEN 3
		  ELSE 4 END,
		  due_at NULLS LAST, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectTasks(rows)
}

// ListUnscheduled returns tasks the scheduler should try to place: anything
// not yet completed/cancelled. Excludes already-scheduled tasks unless the
// caller wants to re-evaluate them; the scheduler does that with ListAllActive.
func (r *TaskRepo) ListUnscheduled(ctx context.Context) ([]Task, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT `+taskSelectCols+` FROM task
		WHERE status = 'pending' ORDER BY due_at NULLS LAST, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectTasks(rows)
}

// ListAllActive returns tasks the scheduler considers (pending or scheduled).
// Cancelled and completed are excluded.
func (r *TaskRepo) ListAllActive(ctx context.Context) ([]Task, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT `+taskSelectCols+` FROM task
		WHERE status IN ('pending','scheduled') ORDER BY due_at NULLS LAST, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectTasks(rows)
}

func collectTasks(rows pgx.Rows) ([]Task, error) {
	var out []Task
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *t)
	}
	return out, rows.Err()
}

func scanTask(row rowScanner) (*Task, error) {
	var t Task
	if err := row.Scan(&t.ID, &t.Title, &t.Notes, &t.Priority, &t.DurationMinutes,
		&t.DueAt, &t.Status, &t.TargetCalendarID, &t.CategoryID,
		&t.ScheduledEventID, &t.ScheduledStartsAt, &t.ScheduledEndsAt,
		&t.ScheduleNote, &t.CreatedAt, &t.UpdatedAt); err != nil {
		return nil, err
	}
	return &t, nil
}

// DefaultTaskMinChunkMinutes is how short a piece of a split task may be when
// the operator hasn't said otherwise. Long enough to get something done, short
// enough to fit between meetings.
const DefaultTaskMinChunkMinutes = 30

// TaskMinChunkMinutes resolves the configured minimum block length for a split
// task, falling back to the default when unset or unparseable.
func TaskMinChunkMinutes(ctx context.Context, r *SettingRepo) int {
	if r == nil {
		return DefaultTaskMinChunkMinutes
	}
	v, ok, err := r.Get(ctx, SettingTaskMinChunkMinutes)
	if err != nil || !ok {
		return DefaultTaskMinChunkMinutes
	}
	if n := atoiOr(strings.TrimSpace(v), 0); n > 0 {
		return n
	}
	return DefaultTaskMinChunkMinutes
}
