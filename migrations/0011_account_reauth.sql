-- +goose Up
-- +goose StatementBegin

-- Google revokes a refresh token when the user withdraws consent, changes
-- their password, leaves it unused for six months, or -- most commonly --
-- when the OAuth app is still in "Testing" publishing status, where every
-- token dies after 7 days. skulid runs unattended, so a revoked token used
-- to mean sync silently stopped. Track it so the UI can ask for a reconnect.
ALTER TABLE account
    ADD COLUMN needs_reauth        BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN reauth_reason       TEXT    NOT NULL DEFAULT '',
    ADD COLUMN reauth_detected_at  TIMESTAMPTZ;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

ALTER TABLE account
    DROP COLUMN needs_reauth,
    DROP COLUMN reauth_reason,
    DROP COLUMN reauth_detected_at;

-- +goose StatementEnd
