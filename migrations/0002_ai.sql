-- +goose Up
-- +goose StatementBegin

CREATE TABLE ai_conversation (
    id          BIGSERIAL PRIMARY KEY,
    title       TEXT NOT NULL DEFAULT '',
    state       TEXT NOT NULL DEFAULT 'idle',  -- 'idle' | 'awaiting_confirmation' | 'thinking'
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX ai_conversation_updated_at_idx ON ai_conversation(updated_at);

CREATE TABLE ai_message (
    id              BIGSERIAL PRIMARY KEY,
    conversation_id BIGINT NOT NULL REFERENCES ai_conversation(id) ON DELETE CASCADE,
    role            TEXT NOT NULL,             -- 'user' | 'assistant'
    content_jsonb   JSONB NOT NULL,             -- array of Anthropic content blocks
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX ai_message_conversation_idx ON ai_message(conversation_id, created_at);

CREATE TABLE ai_pending_action (
    id              BIGSERIAL PRIMARY KEY,
    conversation_id BIGINT NOT NULL REFERENCES ai_conversation(id) ON DELETE CASCADE,
    message_id      BIGINT NOT NULL REFERENCES ai_message(id) ON DELETE CASCADE,
    tool_use_id     TEXT NOT NULL,             -- the id Anthropic gave the tool_use block
    tool_name       TEXT NOT NULL,
    tool_input_jsonb JSONB NOT NULL,
    status          TEXT NOT NULL DEFAULT 'pending', -- 'pending' | 'applied' | 'rejected'
    result_jsonb    JSONB,                      -- tool result content (text or error)
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    resolved_at     TIMESTAMPTZ
);

CREATE INDEX ai_pending_action_conversation_idx ON ai_pending_action(conversation_id, status);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS ai_pending_action;
DROP TABLE IF EXISTS ai_message;
DROP TABLE IF EXISTS ai_conversation;
-- +goose StatementEnd
