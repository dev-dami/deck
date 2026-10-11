-- +goose Up
-- +goose StatementBegin
-- One row per finished agent turn, for the local Usage view. Nothing here
-- leaves the machine. Rows outlive their session so totals stay put when a
-- session is deleted.
CREATE TABLE IF NOT EXISTS usage (
    id TEXT PRIMARY KEY,
    session_id TEXT NOT NULL,
    agent_id TEXT NOT NULL,
    kind TEXT NOT NULL,
    model TEXT NOT NULL DEFAULT '',
    input_tokens INTEGER NOT NULL DEFAULT 0 CHECK (input_tokens >= 0),
    output_tokens INTEGER NOT NULL DEFAULT 0 CHECK (output_tokens >= 0),
    cached_tokens INTEGER NOT NULL DEFAULT 0 CHECK (cached_tokens >= 0),
    cost_usd REAL NOT NULL DEFAULT 0.0 CHECK (cost_usd >= 0.0),
    duration_ms INTEGER NOT NULL DEFAULT 0 CHECK (duration_ms >= 0),
    created_at INTEGER NOT NULL   -- Unix timestamp in seconds
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS idx_usage_created_at ON usage (created_at);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS idx_usage_session_id ON usage (session_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_usage_session_id;
-- +goose StatementEnd

-- +goose StatementBegin
DROP INDEX IF EXISTS idx_usage_created_at;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS usage;
-- +goose StatementEnd
