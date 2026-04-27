-- +goose Up
-- +goose StatementBegin

CREATE TABLE setting (
    key         TEXT PRIMARY KEY,
    value       TEXT NOT NULL,
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE account (
    id                       BIGSERIAL PRIMARY KEY,
    google_sub               TEXT NOT NULL UNIQUE,
    email                    TEXT NOT NULL,
    refresh_token_sealed     TEXT NOT NULL,
    access_token_sealed      TEXT NOT NULL DEFAULT '',
    access_token_expires_at  TIMESTAMPTZ,
    primary_calendar_id      TEXT NOT NULL DEFAULT '',
    created_at               TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE calendar (
    id                  BIGSERIAL PRIMARY KEY,
    account_id          BIGINT NOT NULL REFERENCES account(id) ON DELETE CASCADE,
    google_calendar_id  TEXT NOT NULL,
    summary             TEXT NOT NULL DEFAULT '',
    time_zone           TEXT NOT NULL DEFAULT '',
    color               TEXT NOT NULL DEFAULT '',
    last_synced_at      TIMESTAMPTZ,
    UNIQUE(account_id, google_calendar_id)
);

CREATE TABLE sync_token (
    id                  BIGSERIAL PRIMARY KEY,
    account_id          BIGINT NOT NULL REFERENCES account(id) ON DELETE CASCADE,
    calendar_id         BIGINT NOT NULL REFERENCES calendar(id) ON DELETE CASCADE,
    sync_token          TEXT NOT NULL DEFAULT '',
    watch_channel_id    TEXT NOT NULL DEFAULT '',
    watch_resource_id   TEXT NOT NULL DEFAULT '',
    watch_token_secret  TEXT NOT NULL DEFAULT '',
    watch_expires_at    TIMESTAMPTZ,
    last_polled_at      TIMESTAMPTZ,
    UNIQUE(account_id, calendar_id)
);

CREATE TABLE sync_rule (
    id                   BIGSERIAL PRIMARY KEY,
    name                 TEXT NOT NULL,
    source_calendar_id   BIGINT NOT NULL REFERENCES calendar(id) ON DELETE CASCADE,
    target_calendar_id   BIGINT NOT NULL REFERENCES calendar(id) ON DELETE CASCADE,
    direction            TEXT NOT NULL DEFAULT 'one_way' CHECK (direction IN ('one_way','bidirectional')),
    primary_side         TEXT NOT NULL DEFAULT 'source' CHECK (primary_side IN ('source','target')),
    filter_jsonb         JSONB NOT NULL DEFAULT '{}'::jsonb,
    transform_jsonb      JSONB NOT NULL DEFAULT '{}'::jsonb,
    backfill_days        INT NOT NULL DEFAULT 0,
    backfill_done        BOOLEAN NOT NULL DEFAULT FALSE,
    enabled              BOOLEAN NOT NULL DEFAULT TRUE,
    created_at           TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE smart_block (
    id                   BIGSERIAL PRIMARY KEY,
    name                 TEXT NOT NULL,
    target_calendar_id   BIGINT NOT NULL REFERENCES calendar(id) ON DELETE CASCADE,
    source_calendar_ids  BIGINT[] NOT NULL DEFAULT '{}',
    working_hours_jsonb  JSONB NOT NULL DEFAULT '{}'::jsonb,
    horizon_days         INT NOT NULL DEFAULT 30,
    min_block_minutes    INT NOT NULL DEFAULT 30,
    merge_gap_minutes    INT NOT NULL DEFAULT 15,
    title_template       TEXT NOT NULL DEFAULT 'Focus',
    enabled              BOOLEAN NOT NULL DEFAULT TRUE,
    created_at           TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE event_link (
    id                  BIGSERIAL PRIMARY KEY,
    rule_id             BIGINT NOT NULL REFERENCES sync_rule(id) ON DELETE CASCADE,
    source_account_id   BIGINT NOT NULL REFERENCES account(id) ON DELETE CASCADE,
    source_calendar_id  BIGINT NOT NULL REFERENCES calendar(id) ON DELETE CASCADE,
    source_event_id     TEXT NOT NULL,
    target_account_id   BIGINT NOT NULL REFERENCES account(id) ON DELETE CASCADE,
    target_calendar_id  BIGINT NOT NULL REFERENCES calendar(id) ON DELETE CASCADE,
    target_event_id     TEXT NOT NULL,
    source_etag         TEXT NOT NULL DEFAULT '',
    target_etag         TEXT NOT NULL DEFAULT '',
    last_synced_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(rule_id, source_event_id)
);

CREATE INDEX event_link_target_idx ON event_link(target_account_id, target_calendar_id, target_event_id);

CREATE TABLE managed_block (
    id                   BIGSERIAL PRIMARY KEY,
    smart_block_id       BIGINT NOT NULL REFERENCES smart_block(id) ON DELETE CASCADE,
    target_account_id    BIGINT NOT NULL REFERENCES account(id) ON DELETE CASCADE,
    target_calendar_id   BIGINT NOT NULL REFERENCES calendar(id) ON DELETE CASCADE,
    target_event_id      TEXT NOT NULL,
    starts_at            TIMESTAMPTZ NOT NULL,
    ends_at              TIMESTAMPTZ NOT NULL,
    last_synced_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(smart_block_id, target_event_id)
);

CREATE INDEX managed_block_window_idx ON managed_block(smart_block_id, starts_at);

CREATE TABLE audit_log (
    id              BIGSERIAL PRIMARY KEY,
    ts              TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    kind            TEXT NOT NULL,
    rule_id         BIGINT REFERENCES sync_rule(id) ON DELETE SET NULL,
    smart_block_id  BIGINT REFERENCES smart_block(id) ON DELETE SET NULL,
    source_event_id TEXT NOT NULL DEFAULT '',
    target_event_id TEXT NOT NULL DEFAULT '',
    action          TEXT NOT NULL DEFAULT '',
    message         TEXT NOT NULL DEFAULT ''
);

CREATE INDEX audit_log_ts_idx ON audit_log(ts DESC);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS audit_log;
DROP TABLE IF EXISTS managed_block;
DROP TABLE IF EXISTS event_link;
DROP TABLE IF EXISTS smart_block;
DROP TABLE IF EXISTS sync_rule;
DROP TABLE IF EXISTS sync_token;
DROP TABLE IF EXISTS calendar;
DROP TABLE IF EXISTS account;
DROP TABLE IF EXISTS setting;
-- +goose StatementEnd
