package db

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type AccountRepo struct {
	pool *pgxpool.Pool
}

func NewAccountRepo(pool *pgxpool.Pool) *AccountRepo { return &AccountRepo{pool: pool} }

func (r *AccountRepo) Upsert(ctx context.Context, sub, email, refreshSealed, accessSealed string, accessExpires *time.Time) (int64, error) {
	var id int64
	err := r.pool.QueryRow(ctx, `
		INSERT INTO account (google_sub, email, refresh_token_sealed, access_token_sealed, access_token_expires_at)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (google_sub) DO UPDATE SET
			email = EXCLUDED.email,
			refresh_token_sealed = CASE WHEN EXCLUDED.refresh_token_sealed = '' THEN account.refresh_token_sealed ELSE EXCLUDED.refresh_token_sealed END,
			access_token_sealed = EXCLUDED.access_token_sealed,
			access_token_expires_at = EXCLUDED.access_token_expires_at
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

func (r *AccountRepo) Get(ctx context.Context, id int64) (*Account, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT id, google_sub, email, refresh_token_sealed, access_token_sealed,
		       access_token_expires_at, primary_calendar_id, created_at
		FROM account WHERE id = $1`, id)
	return scanAccount(row)
}

func (r *AccountRepo) GetBySub(ctx context.Context, sub string) (*Account, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT id, google_sub, email, refresh_token_sealed, access_token_sealed,
		       access_token_expires_at, primary_calendar_id, created_at
		FROM account WHERE google_sub = $1`, sub)
	a, err := scanAccount(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return a, err
}

func (r *AccountRepo) List(ctx context.Context) ([]Account, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, google_sub, email, refresh_token_sealed, access_token_sealed,
		       access_token_expires_at, primary_calendar_id, created_at
		FROM account ORDER BY id`)
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
		&a.AccessTokenSealed, &a.AccessTokenExpiresAt, &a.PrimaryCalendarID, &a.CreatedAt); err != nil {
		return nil, err
	}
	return &a, nil
}
