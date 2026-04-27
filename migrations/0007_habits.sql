-- +goose Up
-- +goose StatementBegin

CREATE TABLE habit (
    id                 BIGSERIAL PRIMARY KEY,
    title              TEXT NOT NULL,
    duration_minutes   INT  NOT NULL DEFAULT 60,
    -- HH:MM in the target account's hours timezone.
    ideal_time         TEXT NOT NULL DEFAULT '12:00',
    -- Maximum drift, in minutes, from ideal_time the scheduler will allow.
    flex_minutes       INT  NOT NULL DEFAULT 90,
    -- Set of weekday keys: 'mon','tue','wed','thu','fri','sat','sun'.
    days_of_week       TEXT[] NOT NULL DEFAULT '{}',
    -- Which account-level hours window the habit lives inside.
    -- One of: working | personal | meeting. Personal/meeting fall back to
    -- working when those columns on the account are NULL.
    hours_kind         TEXT NOT NULL DEFAULT 'personal',
    target_calendar_id BIGINT NOT NULL REFERENCES calendar(id) ON DELETE CASCADE,
    category_id        BIGINT REFERENCES category(id) ON DELETE SET NULL,
    -- How many days into the future the scheduler maintains occurrences for.
    horizon_days       INT  NOT NULL DEFAULT 14,
    enabled            BOOLEAN NOT NULL DEFAULT TRUE,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- One row per scheduled occurrence of a habit. occurs_on is the local date
-- the occurrence covers; the unique constraint stops double-booking.
CREATE TABLE habit_occurrence (
    id              BIGSERIAL PRIMARY KEY,
    habit_id        BIGINT NOT NULL REFERENCES habit(id) ON DELETE CASCADE,
    target_event_id TEXT   NOT NULL,
    occurs_on       DATE   NOT NULL,
    starts_at       TIMESTAMPTZ NOT NULL,
    ends_at         TIMESTAMPTZ NOT NULL,
    last_synced_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (habit_id, occurs_on)
);

CREATE INDEX habit_occurrence_window_idx ON habit_occurrence(habit_id, starts_at);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS habit_occurrence;
DROP TABLE IF EXISTS habit;
-- +goose StatementEnd
