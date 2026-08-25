-- +goose Up
-- +goose StatementBegin

-- The AI assistant reaches every connected account, and its conversations --
-- which quote whatever calendar data it read -- are sent to Anthropic and
-- kept for 30 days. That is the only path by which an account's data leaves
-- this host, so an account whose calendars must not travel (an employer's,
-- typically) needs to be excluded from the assistant without disconnecting
-- it from sync.
ALTER TABLE account ADD COLUMN ai_excluded BOOLEAN NOT NULL DEFAULT FALSE;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE account DROP COLUMN ai_excluded;
-- +goose StatementEnd
