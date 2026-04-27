package db

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type CalendarRepo struct {
	pool *pgxpool.Pool
}

func NewCalendarRepo(pool *pgxpool.Pool) *CalendarRepo { return &CalendarRepo{pool: pool} }

func (r *CalendarRepo) Upsert(ctx context.Context, accountID int64, googleID, summary, tz, color string) (int64, error) {
	var id int64
	err := r.pool.QueryRow(ctx, `
		INSERT INTO calendar (account_id, google_calendar_id, summary, time_zone, color)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (account_id, google_calendar_id) DO UPDATE SET
			summary = EXCLUDED.summary,
			time_zone = EXCLUDED.time_zone,
			color = EXCLUDED.color
		RETURNING id`, accountID, googleID, summary, tz, color).Scan(&id)
	return id, err
}

func (r *CalendarRepo) MarkSynced(ctx context.Context, id int64, t time.Time) error {
	_, err := r.pool.Exec(ctx, `UPDATE calendar SET last_synced_at = $2 WHERE id = $1`, id, t)
	return err
}

func (r *CalendarRepo) Get(ctx context.Context, id int64) (*Calendar, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT id, account_id, google_calendar_id, summary, time_zone, color, last_synced_at
		FROM calendar WHERE id = $1`, id)
	var c Calendar
	if err := row.Scan(&c.ID, &c.AccountID, &c.GoogleCalendarID, &c.Summary, &c.TimeZone, &c.Color, &c.LastSyncedAt); err != nil {
		return nil, err
	}
	return &c, nil
}

func (r *CalendarRepo) ListByAccount(ctx context.Context, accountID int64) ([]Calendar, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, account_id, google_calendar_id, summary, time_zone, color, last_synced_at
		FROM calendar WHERE account_id = $1 ORDER BY summary`, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Calendar
	for rows.Next() {
		var c Calendar
		if err := rows.Scan(&c.ID, &c.AccountID, &c.GoogleCalendarID, &c.Summary, &c.TimeZone, &c.Color, &c.LastSyncedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (r *CalendarRepo) ListAll(ctx context.Context) ([]Calendar, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, account_id, google_calendar_id, summary, time_zone, color, last_synced_at
		FROM calendar ORDER BY account_id, summary`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Calendar
	for rows.Next() {
		var c Calendar
		if err := rows.Scan(&c.ID, &c.AccountID, &c.GoogleCalendarID, &c.Summary, &c.TimeZone, &c.Color, &c.LastSyncedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
