-- +goose Up
-- +goose StatementBegin

-- Cosmetic only: a hidden calendar is collapsed out of the Accounts list so
-- its Enable and Settings controls can't be hit by accident. It is NOT the
-- same as `enabled` -- a hidden calendar that is enabled still syncs, still
-- renews its watch, and still appears in rule/block/task selectors. Nothing
-- outside the Accounts page reads this column. Default false so every
-- existing calendar stays visible after migrating.
ALTER TABLE calendar ADD COLUMN hidden BOOLEAN NOT NULL DEFAULT FALSE;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE calendar DROP COLUMN IF EXISTS hidden;
-- +goose StatementEnd
