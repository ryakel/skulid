-- +goose Up
-- +goose StatementBegin

CREATE TABLE category (
    id          BIGSERIAL PRIMARY KEY,
    slug        TEXT NOT NULL UNIQUE,            -- stable machine identifier
    name        TEXT NOT NULL,                   -- human label
    color       TEXT NOT NULL DEFAULT '#9ca3af', -- hex; rendered in the planner
    sort_order  INT  NOT NULL DEFAULT 0,
    builtin     BOOLEAN NOT NULL DEFAULT FALSE,  -- seeded categories can't be deleted
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Reclaim's default category palette. Colors loosely match the Reclaim UI but
-- are intentionally muted — users can override via /settings/categories.
INSERT INTO category (slug, name, color, sort_order, builtin) VALUES
    ('focus',        'Focus',            '#3b82f6', 10, TRUE),
    ('one_on_one',   '1:1 meetings',     '#10b981', 20, TRUE),
    ('team',         'Team meetings',    '#22c55e', 30, TRUE),
    ('external',     'External meetings','#ef4444', 40, TRUE),
    ('personal',     'Personal',         '#f97316', 50, TRUE),
    ('travel',       'Travel & breaks',  '#f59e0b', 60, TRUE),
    ('other',        'Other work',       '#6b7280', 70, TRUE),
    ('free',         'Free',             '#a3e635', 80, TRUE);

-- Sync rules and smart blocks can now carry a fixed category for everything
-- they write. NULL means "use the auto-categorizer".
ALTER TABLE sync_rule ADD COLUMN category_id BIGINT REFERENCES category(id) ON DELETE SET NULL;
ALTER TABLE smart_block ADD COLUMN category_id BIGINT REFERENCES category(id) ON DELETE SET NULL;

-- A calendar can declare a default category — every event read from it falls
-- back to this when the heuristics don't pin anything. Useful for pinning a
-- shared "Family" calendar to Personal.
ALTER TABLE calendar ADD COLUMN default_category_id BIGINT REFERENCES category(id) ON DELETE SET NULL;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE calendar DROP COLUMN IF EXISTS default_category_id;
ALTER TABLE smart_block DROP COLUMN IF EXISTS category_id;
ALTER TABLE sync_rule DROP COLUMN IF EXISTS category_id;
DROP TABLE IF EXISTS category;
-- +goose StatementEnd
