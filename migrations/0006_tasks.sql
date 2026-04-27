-- +goose Up
-- +goose StatementBegin

CREATE TABLE task (
    id                 BIGSERIAL PRIMARY KEY,
    title              TEXT NOT NULL,
    notes              TEXT NOT NULL DEFAULT '',
    -- One of: critical | high | medium | low. Drives Priorities Kanban placement.
    priority           TEXT NOT NULL DEFAULT 'medium',
    duration_minutes   INT  NOT NULL DEFAULT 30,
    due_at             TIMESTAMPTZ,
    -- One of: pending (not yet placed) | scheduled (event written) | completed | cancelled
    status             TEXT NOT NULL DEFAULT 'pending',
    target_calendar_id BIGINT NOT NULL REFERENCES calendar(id) ON DELETE CASCADE,
    category_id        BIGINT REFERENCES category(id) ON DELETE SET NULL,
    -- The currently scheduled placement on Google Calendar. Empty when status is
    -- pending or completed.
    scheduled_event_id   TEXT NOT NULL DEFAULT '',
    scheduled_starts_at  TIMESTAMPTZ,
    scheduled_ends_at    TIMESTAMPTZ,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX task_status_priority_idx ON task(status, priority, due_at);
CREATE INDEX task_target_calendar_idx ON task(target_calendar_id);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS task;
-- +goose StatementEnd
