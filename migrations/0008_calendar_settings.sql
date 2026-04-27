-- +goose Up
-- +goose StatementBegin

-- Per-calendar overrides for hours and buffers. NULL = use the account's
-- value (for hours) or the global setting (for buffers). Stored as JSON to
-- match the existing account columns and avoid a join-heavy normalized form.
ALTER TABLE calendar ADD COLUMN working_hours_jsonb  JSONB;
ALTER TABLE calendar ADD COLUMN personal_hours_jsonb JSONB;
ALTER TABLE calendar ADD COLUMN meeting_hours_jsonb  JSONB;

-- Buffer overrides. Same comma-separated string format as the global
-- setting: "task_habit_break,decompression,travel". NULL = use global.
ALTER TABLE calendar ADD COLUMN buffers TEXT;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE calendar DROP COLUMN IF EXISTS buffers;
ALTER TABLE calendar DROP COLUMN IF EXISTS meeting_hours_jsonb;
ALTER TABLE calendar DROP COLUMN IF EXISTS personal_hours_jsonb;
ALTER TABLE calendar DROP COLUMN IF EXISTS working_hours_jsonb;
-- +goose StatementEnd
