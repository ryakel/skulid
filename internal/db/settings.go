package db

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	SettingOwnerEmail     = "owner_email"
	SettingOwnerGoogleSub = "owner_google_sub"
	SettingExternalURL    = "external_url"
	SettingSchemaVersion  = "schema_version"
	// Buffer minutes — comma-separated `task_habit_break,decompression,travel`,
	// e.g. "30,30,30". Single string so we don't need a new table for one row.
	SettingBuffers        = "buffers"
	// Planner preferences. PlannerTimezone is an IANA name (defaults to the
	// first connected account's working-hours timezone, then UTC); the
	// week-start is "0".."6" with Sunday=0 per Go's time.Weekday convention.
	// PlannerDefaultView is "day" / "3day" / "week" / "month" (defaults to
	// "week" when unset).
	SettingPlannerTimezone   = "planner_timezone"
	SettingPlannerWeekStart  = "planner_week_start"
	SettingPlannerDefaultView = "planner_default_view"
)

type SettingRepo struct {
	pool *pgxpool.Pool
}

func NewSettingRepo(pool *pgxpool.Pool) *SettingRepo { return &SettingRepo{pool: pool} }

func (r *SettingRepo) Get(ctx context.Context, key string) (string, bool, error) {
	var v string
	err := r.pool.QueryRow(ctx, `SELECT value FROM setting WHERE key = $1`, key).Scan(&v)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return v, true, nil
}

func (r *SettingRepo) Set(ctx context.Context, key, value string) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO setting (key, value, updated_at) VALUES ($1, $2, NOW())
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = NOW()`, key, value)
	return err
}
