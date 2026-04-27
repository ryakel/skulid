package db

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type HabitRepo struct{ pool *pgxpool.Pool }

func NewHabitRepo(pool *pgxpool.Pool) *HabitRepo { return &HabitRepo{pool: pool} }

const habitSelectCols = `id, title, duration_minutes, ideal_time, flex_minutes, days_of_week,
	       hours_kind, target_calendar_id, category_id, horizon_days, enabled, created_at`

func (r *HabitRepo) Create(ctx context.Context, h *Habit) (int64, error) {
	if h.DurationMinutes <= 0 {
		h.DurationMinutes = 60
	}
	if h.HorizonDays <= 0 {
		h.HorizonDays = 14
	}
	if h.HoursKind == "" {
		h.HoursKind = string(HoursPersonal)
	}
	if h.IdealTime == "" {
		h.IdealTime = "12:00"
	}
	if h.FlexMinutes <= 0 {
		h.FlexMinutes = 90
	}
	if h.DaysOfWeek == nil {
		h.DaysOfWeek = []string{}
	}
	var id int64
	err := r.pool.QueryRow(ctx, `
		INSERT INTO habit (title, duration_minutes, ideal_time, flex_minutes, days_of_week,
		                   hours_kind, target_calendar_id, category_id, horizon_days, enabled)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10) RETURNING id`,
		h.Title, h.DurationMinutes, h.IdealTime, h.FlexMinutes, h.DaysOfWeek,
		h.HoursKind, h.TargetCalendarID, h.CategoryID, h.HorizonDays, h.Enabled).Scan(&id)
	return id, err
}

func (r *HabitRepo) Update(ctx context.Context, h *Habit) error {
	if h.DaysOfWeek == nil {
		h.DaysOfWeek = []string{}
	}
	_, err := r.pool.Exec(ctx, `
		UPDATE habit SET title = $2, duration_minutes = $3, ideal_time = $4, flex_minutes = $5,
		    days_of_week = $6, hours_kind = $7, target_calendar_id = $8, category_id = $9,
		    horizon_days = $10, enabled = $11
		WHERE id = $1`,
		h.ID, h.Title, h.DurationMinutes, h.IdealTime, h.FlexMinutes, h.DaysOfWeek,
		h.HoursKind, h.TargetCalendarID, h.CategoryID, h.HorizonDays, h.Enabled)
	return err
}

func (r *HabitRepo) Delete(ctx context.Context, id int64) error {
	_, err := r.pool.Exec(ctx, `DELETE FROM habit WHERE id = $1`, id)
	return err
}

func (r *HabitRepo) Get(ctx context.Context, id int64) (*Habit, error) {
	row := r.pool.QueryRow(ctx, `SELECT `+habitSelectCols+` FROM habit WHERE id = $1`, id)
	h, err := scanHabit(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return h, err
}

func (r *HabitRepo) List(ctx context.Context) ([]Habit, error) {
	rows, err := r.pool.Query(ctx, `SELECT `+habitSelectCols+` FROM habit ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectHabits(rows)
}

func (r *HabitRepo) ListEnabled(ctx context.Context) ([]Habit, error) {
	rows, err := r.pool.Query(ctx, `SELECT `+habitSelectCols+` FROM habit WHERE enabled = TRUE ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectHabits(rows)
}

func collectHabits(rows pgx.Rows) ([]Habit, error) {
	var out []Habit
	for rows.Next() {
		h, err := scanHabit(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *h)
	}
	return out, rows.Err()
}

func scanHabit(row rowScanner) (*Habit, error) {
	var h Habit
	if err := row.Scan(&h.ID, &h.Title, &h.DurationMinutes, &h.IdealTime, &h.FlexMinutes,
		&h.DaysOfWeek, &h.HoursKind, &h.TargetCalendarID, &h.CategoryID, &h.HorizonDays,
		&h.Enabled, &h.CreatedAt); err != nil {
		return nil, err
	}
	return &h, nil
}

// ---------------------------------------------------------------------------

type HabitOccurrenceRepo struct{ pool *pgxpool.Pool }

func NewHabitOccurrenceRepo(pool *pgxpool.Pool) *HabitOccurrenceRepo {
	return &HabitOccurrenceRepo{pool: pool}
}

func (r *HabitOccurrenceRepo) Upsert(ctx context.Context, o *HabitOccurrence) (int64, error) {
	var id int64
	err := r.pool.QueryRow(ctx, `
		INSERT INTO habit_occurrence (habit_id, target_event_id, occurs_on, starts_at, ends_at)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (habit_id, occurs_on) DO UPDATE SET
		    target_event_id = EXCLUDED.target_event_id,
		    starts_at = EXCLUDED.starts_at,
		    ends_at = EXCLUDED.ends_at,
		    last_synced_at = NOW()
		RETURNING id`,
		o.HabitID, o.TargetEventID, o.OccursOn, o.StartsAt, o.EndsAt).Scan(&id)
	return id, err
}

func (r *HabitOccurrenceRepo) ListByHabit(ctx context.Context, habitID int64) ([]HabitOccurrence, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, habit_id, target_event_id, occurs_on, starts_at, ends_at, last_synced_at
		FROM habit_occurrence WHERE habit_id = $1 ORDER BY occurs_on`, habitID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []HabitOccurrence
	for rows.Next() {
		var o HabitOccurrence
		if err := rows.Scan(&o.ID, &o.HabitID, &o.TargetEventID, &o.OccursOn,
			&o.StartsAt, &o.EndsAt, &o.LastSyncedAt); err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// DeleteFromDate removes occurrences whose date is on or after the cutoff.
// Used when a habit is edited or disabled — the scheduler can then refresh
// only the future window.
func (r *HabitOccurrenceRepo) DeleteFromDate(ctx context.Context, habitID int64, cutoff time.Time) error {
	_, err := r.pool.Exec(ctx, `
		DELETE FROM habit_occurrence WHERE habit_id = $1 AND occurs_on >= $2`,
		habitID, cutoff)
	return err
}

func (r *HabitOccurrenceRepo) DeleteByID(ctx context.Context, id int64) error {
	_, err := r.pool.Exec(ctx, `DELETE FROM habit_occurrence WHERE id = $1`, id)
	return err
}
