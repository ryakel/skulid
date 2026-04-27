package db

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type CalendarRepo struct {
	pool *pgxpool.Pool
}

func NewCalendarRepo(pool *pgxpool.Pool) *CalendarRepo { return &CalendarRepo{pool: pool} }

const calendarSelectCols = `id, account_id, google_calendar_id, summary, time_zone, color,
	last_synced_at, default_category_id,
	working_hours_jsonb, personal_hours_jsonb, meeting_hours_jsonb, buffers`

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

// SetDefaultCategory pins (or unpins, with id=nil) the category every event on
// this calendar falls back to when the heuristics don't match.
func (r *CalendarRepo) SetDefaultCategory(ctx context.Context, id int64, categoryID *int64) error {
	_, err := r.pool.Exec(ctx, `UPDATE calendar SET default_category_id = $2 WHERE id = $1`, id, categoryID)
	return err
}

// UpdateHours stores the three per-calendar override blobs. Empty inputs are
// stored as SQL NULL so the engine falls back to the account-level value.
func (r *CalendarRepo) UpdateHours(ctx context.Context, id int64, working, personal, meeting json.RawMessage) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE calendar SET working_hours_jsonb = $2, personal_hours_jsonb = $3, meeting_hours_jsonb = $4
		WHERE id = $1`, id, nullableJSON(working), nullableJSON(personal), nullableJSON(meeting))
	return err
}

// UpdateBuffers stores the per-calendar buffer override string. Empty input
// clears the override (the engine then uses the global `setting.buffers`).
func (r *CalendarRepo) UpdateBuffers(ctx context.Context, id int64, buffers string) error {
	var v any = buffers
	if buffers == "" {
		v = nil
	}
	_, err := r.pool.Exec(ctx, `UPDATE calendar SET buffers = $2 WHERE id = $1`, id, v)
	return err
}

func (r *CalendarRepo) Get(ctx context.Context, id int64) (*Calendar, error) {
	row := r.pool.QueryRow(ctx, `SELECT `+calendarSelectCols+` FROM calendar WHERE id = $1`, id)
	return scanCalendar(row)
}

func (r *CalendarRepo) ListByAccount(ctx context.Context, accountID int64) ([]Calendar, error) {
	rows, err := r.pool.Query(ctx, `SELECT `+calendarSelectCols+` FROM calendar WHERE account_id = $1 ORDER BY summary`, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Calendar
	for rows.Next() {
		c, err := scanCalendar(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *c)
	}
	return out, rows.Err()
}

func (r *CalendarRepo) ListAll(ctx context.Context) ([]Calendar, error) {
	rows, err := r.pool.Query(ctx, `SELECT `+calendarSelectCols+` FROM calendar ORDER BY account_id, summary`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Calendar
	for rows.Next() {
		c, err := scanCalendar(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *c)
	}
	return out, rows.Err()
}

func scanCalendar(row rowScanner) (*Calendar, error) {
	var c Calendar
	var buffers *string
	if err := row.Scan(&c.ID, &c.AccountID, &c.GoogleCalendarID, &c.Summary, &c.TimeZone, &c.Color,
		&c.LastSyncedAt, &c.DefaultCategoryID,
		&c.WorkingHours, &c.PersonalHours, &c.MeetingHours, &buffers); err != nil {
		return nil, err
	}
	if buffers != nil {
		c.Buffers = *buffers
	}
	return &c, nil
}

// EffectiveCalendarHours implements the override chain: per-calendar override,
// then per-account default (with personal/meeting falling back to working
// inside the account itself).
func EffectiveCalendarHours(cal *Calendar, acct *Account, kind HoursKind) json.RawMessage {
	if cal != nil {
		switch kind {
		case HoursPersonal:
			if len(cal.PersonalHours) > 0 {
				return cal.PersonalHours
			}
			if len(cal.WorkingHours) > 0 {
				return cal.WorkingHours
			}
		case HoursMeeting:
			if len(cal.MeetingHours) > 0 {
				return cal.MeetingHours
			}
			if len(cal.WorkingHours) > 0 {
				return cal.WorkingHours
			}
		default:
			if len(cal.WorkingHours) > 0 {
				return cal.WorkingHours
			}
		}
	}
	if acct != nil {
		return acct.EffectiveHours(kind)
	}
	return nil
}

// EffectiveCalendarBuffers returns the buffer settings the engine should use
// for this calendar — calendar override, then global. Reads the global setting
// only when the calendar override is empty.
func EffectiveCalendarBuffers(ctx context.Context, settings *SettingRepo, cal *Calendar) BufferSettings {
	if cal != nil && cal.Buffers != "" {
		return parseBuffers(cal.Buffers)
	}
	return LoadBuffers(ctx, settings)
}
