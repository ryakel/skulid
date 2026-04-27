-- +goose Up
-- +goose StatementBegin

-- Reclaim-style visibility modes. The engine derives a Transform from this
-- preset; per-field transforms remain in transform_jsonb for callers that
-- want full control (default rules use the preset).
ALTER TABLE sync_rule ADD COLUMN visibility_mode TEXT NOT NULL DEFAULT 'busy_for_all';

-- How to handle all-day source events:
--   'skip'      = never mirror
--   'only_busy' = mirror only when source isn't transparent
--   'sync_all'  = mirror every all-day (current behavior)
ALTER TABLE sync_rule ADD COLUMN all_day_mode TEXT NOT NULL DEFAULT 'sync_all';

-- When TRUE, only mirror events whose start lies within the source account's
-- Working hours (account.working_hours_jsonb).
ALTER TABLE sync_rule ADD COLUMN working_hours_only BOOLEAN NOT NULL DEFAULT FALSE;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE sync_rule DROP COLUMN IF EXISTS working_hours_only;
ALTER TABLE sync_rule DROP COLUMN IF EXISTS all_day_mode;
ALTER TABLE sync_rule DROP COLUMN IF EXISTS visibility_mode;
-- +goose StatementEnd
