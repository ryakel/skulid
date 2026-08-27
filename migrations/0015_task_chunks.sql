-- +goose Up
-- +goose StatementBegin

-- A task's duration can now be spread across several calendar blocks instead
-- of needing one window big enough for all of it. Each block is a row here,
-- much as habit_occurrence is a row per day of a habit.
--
-- task.scheduled_event_id / scheduled_starts_at / scheduled_ends_at are kept
-- in step with the FIRST chunk, so everything that reads "when is this task"
-- -- the tasks list, the AI tools, the planner -- keeps working unchanged.
-- This table is the full truth; those columns are the summary.
CREATE TABLE task_chunk (
    id              BIGSERIAL PRIMARY KEY,
    task_id         BIGINT NOT NULL REFERENCES task(id) ON DELETE CASCADE,
    seq             INT    NOT NULL,
    google_event_id TEXT   NOT NULL,
    starts_at       TIMESTAMPTZ NOT NULL,
    ends_at         TIMESTAMPTZ NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (task_id, seq)
);

CREATE INDEX task_chunk_task_idx  ON task_chunk(task_id, seq);
CREATE INDEX task_chunk_range_idx ON task_chunk(starts_at, ends_at);

-- Adopt every existing single placement as chunk 0, so no already-scheduled
-- task is orphaned and the scheduler's managed-window query can read this
-- table alone rather than unioning both shapes.
INSERT INTO task_chunk (task_id, seq, google_event_id, starts_at, ends_at)
SELECT id, 0, scheduled_event_id, scheduled_starts_at, scheduled_ends_at
FROM task
WHERE scheduled_event_id <> ''
  AND scheduled_starts_at IS NOT NULL
  AND scheduled_ends_at IS NOT NULL;

-- Why a task isn't on the calendar, in the user's words rather than inferred
-- from an empty day. Cleared on every successful placement.
ALTER TABLE task ADD COLUMN schedule_note TEXT NOT NULL DEFAULT '';

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE task DROP COLUMN schedule_note;
DROP TABLE IF EXISTS task_chunk;
-- +goose StatementEnd
