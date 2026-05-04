-- +goose Up
-- +goose StatementBegin

-- Three distinct windows per account, all WorkingHours-shaped JSON:
--   working  = when work tasks/meetings can land
--   personal = when habits like Lunch can land
--   meeting  = when external meetings can be booked (subset of working)
-- All nullable; Personal and Meeting fall back to Working when null.
ALTER TABLE account ADD COLUMN working_hours_jsonb  JSONB;
ALTER TABLE account ADD COLUMN personal_hours_jsonb JSONB;
ALTER TABLE account ADD COLUMN meeting_hours_jsonb  JSONB;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE account DROP COLUMN IF EXISTS meeting_hours_jsonb;
ALTER TABLE account DROP COLUMN IF EXISTS personal_hours_jsonb;
ALTER TABLE account DROP COLUMN IF EXISTS working_hours_jsonb;
-- +goose StatementEnd
