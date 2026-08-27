-- +goose Up
-- +goose StatementBegin

-- The buffer engine now writes two kinds of event, not one: the existing
-- trailing "Decompress" block, and "Travel" blocks either side of a meeting
-- that carries a location. Generalise the table it reconciles against.
--
-- A source meeting can own more than one buffer now (travel before AND after),
-- so the old UNIQUE (calendar_id, source_event_id) has to widen to include
-- what kind of buffer a row is and which side of the meeting it sits on.
-- Dropping it by its generated name deliberately has no IF EXISTS: if the name
-- ever differed, silently leaving the old constraint in place would reject
-- every second travel row at runtime rather than here.
ALTER TABLE decompression_event RENAME TO buffer_event;
ALTER INDEX decompression_event_pkey RENAME TO buffer_event_pkey;
ALTER INDEX decompression_event_calendar_idx RENAME TO buffer_event_calendar_idx;

-- Existing rows are all trailing decompression blocks.
ALTER TABLE buffer_event ADD COLUMN buffer_type TEXT NOT NULL DEFAULT 'decompression';
ALTER TABLE buffer_event ADD COLUMN placement   TEXT NOT NULL DEFAULT 'after';

ALTER TABLE buffer_event
    DROP CONSTRAINT decompression_event_calendar_id_source_event_id_key;
ALTER TABLE buffer_event ADD CONSTRAINT buffer_event_unique_key
    UNIQUE (calendar_id, source_event_id, buffer_type, placement);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

-- Only decompression rows fit the old shape; travel rows have no home there.
-- Their Google events are orphaned by this, but they carry skulidManaged=1
-- and skulidBufferType=travel, so they stay recognisable for manual cleanup.
DELETE FROM buffer_event WHERE buffer_type <> 'decompression';

ALTER TABLE buffer_event DROP CONSTRAINT buffer_event_unique_key;
ALTER TABLE buffer_event DROP COLUMN placement;
ALTER TABLE buffer_event DROP COLUMN buffer_type;
ALTER TABLE buffer_event ADD CONSTRAINT decompression_event_calendar_id_source_event_id_key
    UNIQUE (calendar_id, source_event_id);
ALTER INDEX buffer_event_calendar_idx RENAME TO decompression_event_calendar_idx;
ALTER INDEX buffer_event_pkey RENAME TO decompression_event_pkey;
ALTER TABLE buffer_event RENAME TO decompression_event;

-- +goose StatementEnd
