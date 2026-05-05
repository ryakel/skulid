-- +goose Up
-- +goose StatementBegin

-- A calendar can be turned off without disconnecting the account. Disabled
-- calendars don't sync, don't renew watch channels, and disappear from new
-- selectors (existing rule/block/task/habit references still work but are
-- effectively dormant). Default true so every existing calendar stays on
-- after migrating.
ALTER TABLE calendar ADD COLUMN enabled BOOLEAN NOT NULL DEFAULT TRUE;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE calendar DROP COLUMN IF EXISTS enabled;
-- +goose StatementEnd
