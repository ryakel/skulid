package db

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type SyncTokenRepo struct {
	pool *pgxpool.Pool
}

func NewSyncTokenRepo(pool *pgxpool.Pool) *SyncTokenRepo { return &SyncTokenRepo{pool: pool} }

func (r *SyncTokenRepo) Get(ctx context.Context, accountID, calendarID int64) (*SyncToken, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT id, account_id, calendar_id, sync_token, watch_channel_id, watch_resource_id,
		       watch_token_secret, watch_expires_at, last_polled_at
		FROM sync_token WHERE account_id = $1 AND calendar_id = $2`, accountID, calendarID)
	var t SyncToken
	if err := row.Scan(&t.ID, &t.AccountID, &t.CalendarID, &t.SyncToken, &t.WatchChannelID,
		&t.WatchResourceID, &t.WatchTokenSecret, &t.WatchExpiresAt, &t.LastPolledAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &t, nil
}

func (r *SyncTokenRepo) Ensure(ctx context.Context, accountID, calendarID int64) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO sync_token (account_id, calendar_id) VALUES ($1, $2)
		ON CONFLICT (account_id, calendar_id) DO NOTHING`, accountID, calendarID)
	return err
}

func (r *SyncTokenRepo) UpdateSyncToken(ctx context.Context, accountID, calendarID int64, token string) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO sync_token (account_id, calendar_id, sync_token, last_polled_at) VALUES ($1, $2, $3, NOW())
		ON CONFLICT (account_id, calendar_id) DO UPDATE SET sync_token = EXCLUDED.sync_token, last_polled_at = NOW()`,
		accountID, calendarID, token)
	return err
}

func (r *SyncTokenRepo) UpdateWatch(ctx context.Context, accountID, calendarID int64, channelID, resourceID, tokenSecret string, expires time.Time) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE sync_token
		SET watch_channel_id = $3, watch_resource_id = $4, watch_token_secret = $5, watch_expires_at = $6
		WHERE account_id = $1 AND calendar_id = $2`,
		accountID, calendarID, channelID, resourceID, tokenSecret, expires)
	return err
}

func (r *SyncTokenRepo) ClearWatch(ctx context.Context, accountID, calendarID int64) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE sync_token SET watch_channel_id = '', watch_resource_id = '',
		watch_token_secret = '', watch_expires_at = NULL
		WHERE account_id = $1 AND calendar_id = $2`, accountID, calendarID)
	return err
}

func (r *SyncTokenRepo) FindByChannelID(ctx context.Context, channelID string) (*SyncToken, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT id, account_id, calendar_id, sync_token, watch_channel_id, watch_resource_id,
		       watch_token_secret, watch_expires_at, last_polled_at
		FROM sync_token WHERE watch_channel_id = $1`, channelID)
	var t SyncToken
	if err := row.Scan(&t.ID, &t.AccountID, &t.CalendarID, &t.SyncToken, &t.WatchChannelID,
		&t.WatchResourceID, &t.WatchTokenSecret, &t.WatchExpiresAt, &t.LastPolledAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &t, nil
}

func (r *SyncTokenRepo) ListExpiringSoon(ctx context.Context, within time.Duration) ([]SyncToken, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, account_id, calendar_id, sync_token, watch_channel_id, watch_resource_id,
		       watch_token_secret, watch_expires_at, last_polled_at
		FROM sync_token
		WHERE watch_expires_at IS NOT NULL AND watch_expires_at < NOW() + $1::interval`,
		within)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanSyncTokens(rows)
}

func (r *SyncTokenRepo) ListNeedingPoll(ctx context.Context, staleAfter time.Duration) ([]SyncToken, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, account_id, calendar_id, sync_token, watch_channel_id, watch_resource_id,
		       watch_token_secret, watch_expires_at, last_polled_at
		FROM sync_token
		WHERE last_polled_at IS NULL OR last_polled_at < NOW() - $1::interval
		   OR watch_expires_at IS NULL OR watch_expires_at < NOW()`, staleAfter)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanSyncTokens(rows)
}

func scanSyncTokens(rows pgx.Rows) ([]SyncToken, error) {
	var out []SyncToken
	for rows.Next() {
		var t SyncToken
		if err := rows.Scan(&t.ID, &t.AccountID, &t.CalendarID, &t.SyncToken, &t.WatchChannelID,
			&t.WatchResourceID, &t.WatchTokenSecret, &t.WatchExpiresAt, &t.LastPolledAt); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}
