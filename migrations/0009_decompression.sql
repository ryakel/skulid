-- +goose Up
-- +goose StatementBegin

-- Tracks the visible "Decompress" events the buffer engine writes after
-- non-managed meetings. One row per (calendar, source meeting). The decompress
-- event itself lives on Google with skulidManaged=1 and skulidBufferType
-- properties for loop-guard recognition.
CREATE TABLE decompression_event (
    id              BIGSERIAL PRIMARY KEY,
    calendar_id     BIGINT NOT NULL REFERENCES calendar(id) ON DELETE CASCADE,
    source_event_id TEXT   NOT NULL,
    target_event_id TEXT   NOT NULL,
    starts_at       TIMESTAMPTZ NOT NULL,
    ends_at         TIMESTAMPTZ NOT NULL,
    last_seen_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (calendar_id, source_event_id)
);

CREATE INDEX decompression_event_calendar_idx ON decompression_event(calendar_id, starts_at);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS decompression_event;
-- +goose StatementEnd
