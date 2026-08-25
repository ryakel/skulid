package db

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type AccountRepo struct {
	pool *pgxpool.Pool
}

func NewAccountRepo(pool *pgxpool.Pool) *AccountRepo { return &AccountRepo{pool: pool} }

const accountSelectCols = `id, google_sub, email, refresh_token_sealed, access_token_sealed,
	       access_token_expires_at, primary_calendar_id, created_at,
	       working_hours_jsonb, personal_hours_jsonb, meeting_hours_jsonb,
	       needs_reauth, reauth_reason, reauth_detected_at, ai_excluded`

func (r *AccountRepo) Upsert(ctx context.Context, sub, email, refreshSealed, accessSealed string, accessExpires *time.Time) (int64, error) {
	var id int64
	err := r.pool.QueryRow(ctx, `
		INSERT INTO account (google_sub, email, refresh_token_sealed, access_token_sealed, access_token_expires_at)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (google_sub) DO UPDATE SET
			email = EXCLUDED.email,
			refresh_token_sealed = CASE WHEN EXCLUDED.refresh_token_sealed = '' THEN account.refresh_token_sealed ELSE EXCLUDED.refresh_token_sealed END,
			access_token_sealed = EXCLUDED.access_token_sealed,
			access_token_expires_at = EXCLUDED.access_token_expires_at,
			needs_reauth = FALSE,
			reauth_reason = '',
			reauth_detected_at = NULL
		RETURNING id`, sub, email, refreshSealed, accessSealed, accessExpires).Scan(&id)
	return id, err
}

func (r *AccountRepo) UpdateAccessToken(ctx context.Context, id int64, sealed string, expires time.Time) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE account SET access_token_sealed = $2, access_token_expires_at = $3 WHERE id = $1`,
		id, sealed, expires)
	return err
}

func (r *AccountRepo) UpdateRefreshToken(ctx context.Context, id int64, sealed string) error {
	_, err := r.pool.Exec(ctx, `UPDATE account SET refresh_token_sealed = $2 WHERE id = $1`, id, sealed)
	return err
}

func (r *AccountRepo) SetPrimaryCalendar(ctx context.Context, id int64, primaryID string) error {
	_, err := r.pool.Exec(ctx, `UPDATE account SET primary_calendar_id = $2 WHERE id = $1`, id, primaryID)
	return err
}

// UpdateHours stores the three working-hours JSON blobs for an account. Empty
// inputs are stored as SQL NULL, which the readers treat as "use the default
// or fall back to working".
func (r *AccountRepo) UpdateHours(ctx context.Context, id int64, working, personal, meeting json.RawMessage) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE account SET working_hours_jsonb = $2, personal_hours_jsonb = $3, meeting_hours_jsonb = $4
		WHERE id = $1`, id, nullableJSON(working), nullableJSON(personal), nullableJSON(meeting))
	return err
}

func (r *AccountRepo) Get(ctx context.Context, id int64) (*Account, error) {
	row := r.pool.QueryRow(ctx, `SELECT `+accountSelectCols+` FROM account WHERE id = $1`, id)
	return scanAccount(row)
}

func (r *AccountRepo) GetBySub(ctx context.Context, sub string) (*Account, error) {
	row := r.pool.QueryRow(ctx, `SELECT `+accountSelectCols+` FROM account WHERE google_sub = $1`, sub)
	a, err := scanAccount(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return a, err
}

func (r *AccountRepo) List(ctx context.Context) ([]Account, error) {
	rows, err := r.pool.Query(ctx, `SELECT `+accountSelectCols+` FROM account ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Account
	for rows.Next() {
		a, err := scanAccount(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *a)
	}
	return out, rows.Err()
}

// MarkNeedsReauth flags an account whose refresh token Google will never
// honour again. Idempotent: re-flagging keeps the original detection time so
// the UI can say how long sync has actually been down.
func (r *AccountRepo) MarkNeedsReauth(ctx context.Context, id int64, reason string) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE account
		SET needs_reauth = TRUE,
		    reauth_reason = $2,
		    reauth_detected_at = COALESCE(reauth_detected_at, NOW())
		WHERE id = $1`, id, reason)
	return err
}

// ClearNeedsReauth resets the flag after a refresh succeeds again. It only
// touches rows that are actually flagged so the common path is a no-op.
func (r *AccountRepo) ClearNeedsReauth(ctx context.Context, id int64) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE account
		SET needs_reauth = FALSE, reauth_reason = '', reauth_detected_at = NULL
		WHERE id = $1 AND needs_reauth`, id)
	return err
}

// ListNeedsReauth returns every account currently locked out, for the
// banner the layout renders on each page.
func (r *AccountRepo) ListNeedsReauth(ctx context.Context) ([]Account, error) {
	rows, err := r.pool.Query(ctx, `SELECT `+accountSelectCols+` FROM account WHERE needs_reauth ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Account
	for rows.Next() {
		a, err := scanAccount(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *a)
	}
	return out, rows.Err()
}

// SetAIExcluded controls whether the AI assistant may see this account.
func (r *AccountRepo) SetAIExcluded(ctx context.Context, id int64, excluded bool) error {
	_, err := r.pool.Exec(ctx, `UPDATE account SET ai_excluded = $2 WHERE id = $1`, id, excluded)
	return err
}

// AIExcludedIDs returns the set of accounts the assistant must not touch.
// Returned as a set so callers can filter a calendar list in one pass.
func (r *AccountRepo) AIExcludedIDs(ctx context.Context) (map[int64]bool, error) {
	rows, err := r.pool.Query(ctx, `SELECT id FROM account WHERE ai_excluded`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]bool{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}

// IsAIExcluded reports whether one account is off-limits to the assistant.
// Missing accounts report true: refusing to hand out a client for an account
// we cannot verify is the safe direction to fail.
func (r *AccountRepo) IsAIExcluded(ctx context.Context, id int64) (bool, error) {
	var excluded bool
	err := r.pool.QueryRow(ctx, `SELECT ai_excluded FROM account WHERE id = $1`, id).Scan(&excluded)
	if errors.Is(err, pgx.ErrNoRows) {
		return true, nil
	}
	if err != nil {
		return true, err
	}
	return excluded, nil
}

func (r *AccountRepo) Delete(ctx context.Context, id int64) error {
	_, err := r.pool.Exec(ctx, `DELETE FROM account WHERE id = $1`, id)
	return err
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanAccount(row rowScanner) (*Account, error) {
	var a Account
	if err := row.Scan(&a.ID, &a.GoogleSub, &a.Email, &a.RefreshTokenSealed,
		&a.AccessTokenSealed, &a.AccessTokenExpiresAt, &a.PrimaryCalendarID, &a.CreatedAt,
		&a.WorkingHours, &a.PersonalHours, &a.MeetingHours,
		&a.NeedsReauth, &a.ReauthReason, &a.ReauthDetectedAt, &a.AIExcluded); err != nil {
		return nil, err
	}
	return &a, nil
}

// nullableJSON converts an empty/nil RawMessage to a typed nil so pgx writes
// SQL NULL rather than the literal string "null".
func nullableJSON(j json.RawMessage) any {
	if len(j) == 0 {
		return nil
	}
	return []byte(j)
}

// HoursKind names the three supported per-account hour windows.
type HoursKind string

const (
	HoursWorking  HoursKind = "working"
	HoursPersonal HoursKind = "personal"
	HoursMeeting  HoursKind = "meeting"
)

// EffectiveHours returns the JSON blob a caller should use for the requested
// hour kind on this account, applying the documented fallbacks: personal and
// meeting both fall back to working when their own column is NULL/empty.
// Returns an empty RawMessage if even working is unset (caller can then use
// hours.Default()).
func (a *Account) EffectiveHours(kind HoursKind) json.RawMessage {
	switch kind {
	case HoursPersonal:
		if len(a.PersonalHours) > 0 {
			return a.PersonalHours
		}
		return a.WorkingHours
	case HoursMeeting:
		if len(a.MeetingHours) > 0 {
			return a.MeetingHours
		}
		return a.WorkingHours
	default:
		return a.WorkingHours
	}
}
