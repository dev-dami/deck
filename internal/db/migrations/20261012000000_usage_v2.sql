-- +goose Up
-- Usage v2: more of what each turn used, how the user steered the agents,
-- where each turn's time went, and the plan windows the CLIs report. Like the
-- usage table, nothing here leaves the machine and no prompt text is kept.

-- +goose StatementBegin
ALTER TABLE usage ADD COLUMN cache_write_tokens INTEGER NOT NULL DEFAULT 0 CHECK (cache_write_tokens >= 0);
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE usage ADD COLUMN reasoning_tokens INTEGER NOT NULL DEFAULT 0 CHECK (reasoning_tokens >= 0);
-- +goose StatementEnd

-- +goose StatementBegin
-- done | interrupted | failed
ALTER TABLE usage ADD COLUMN status TEXT NOT NULL DEFAULT 'done';
-- +goose StatementEnd

-- +goose StatementBegin
-- Time to the first token and time spent in model requests; 0 = not known.
ALTER TABLE usage ADD COLUMN ttft_ms INTEGER NOT NULL DEFAULT 0 CHECK (ttft_ms >= 0);
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE usage ADD COLUMN api_ms INTEGER NOT NULL DEFAULT 0 CHECK (api_ms >= 0);
-- +goose StatementEnd

-- +goose StatementBegin
-- What started the turn: user | relay | mention | steer; '' = not known.
ALTER TABLE usage ADD COLUMN "trigger" TEXT NOT NULL DEFAULT '';
-- +goose StatementEnd

-- +goose StatementBegin
-- The thread's permission mode during the turn: plan | ask | yolo.
ALTER TABLE usage ADD COLUMN mode TEXT NOT NULL DEFAULT '';
-- +goose StatementEnd

-- +goose StatementBegin
-- One row per thing the user (or Deck on their behalf) did to steer the
-- agents: interrupts, steers, approvals, relays, mode and model switches,
-- errors. detail qualifies kind (manual/auto, tool/mention, the new mode...).
CREATE TABLE IF NOT EXISTS behavior (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id TEXT NOT NULL DEFAULT '',
    agent_id TEXT NOT NULL DEFAULT '',
    kind TEXT NOT NULL,
    detail TEXT NOT NULL DEFAULT '',
    value INTEGER NOT NULL DEFAULT 1,
    created_at INTEGER NOT NULL   -- Unix timestamp in seconds
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS idx_behavior_created_at ON behavior (created_at);
-- +goose StatementEnd

-- +goose StatementBegin
-- Where one turn's time went: think, text, tool and wait spans, relative to
-- the start of the turn. turn_id is the usage row's id.
CREATE TABLE IF NOT EXISTS trace_span (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    turn_id TEXT NOT NULL,
    session_id TEXT NOT NULL DEFAULT '',
    agent_id TEXT NOT NULL DEFAULT '',
    kind TEXT NOT NULL,
    name TEXT NOT NULL DEFAULT '',
    start_ms INTEGER NOT NULL DEFAULT 0 CHECK (start_ms >= 0),
    dur_ms INTEGER NOT NULL DEFAULT 0 CHECK (dur_ms >= 0),
    tokens INTEGER NOT NULL DEFAULT 0 CHECK (tokens >= 0),
    created_at INTEGER NOT NULL   -- Unix timestamp in seconds
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS idx_trace_span_turn_id ON trace_span (turn_id);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS idx_trace_span_created_at ON trace_span (created_at);
-- +goose StatementEnd

-- +goose StatementBegin
-- The signed-in account's plan windows as a CLI reported them. A row is
-- written only when a window moved (used_pct by 1 or more, or a new reset).
CREATE TABLE IF NOT EXISTS limit_snapshot (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    agent_kind TEXT NOT NULL,
    window_id TEXT NOT NULL,
    kind TEXT NOT NULL DEFAULT 'other',   -- session | weekly | monthly | other
    label TEXT NOT NULL DEFAULT '',
    used_pct REAL NOT NULL DEFAULT 0.0,   -- 0-100
    resets_at INTEGER NOT NULL DEFAULT 0, -- Unix seconds; 0 = not known
    duration_mins INTEGER NOT NULL DEFAULT 0,
    plan TEXT NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL   -- Unix timestamp in seconds
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS idx_limit_snapshot_created_at ON limit_snapshot (created_at);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS idx_limit_snapshot_window ON limit_snapshot (agent_kind, window_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_limit_snapshot_window;
-- +goose StatementEnd

-- +goose StatementBegin
DROP INDEX IF EXISTS idx_limit_snapshot_created_at;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS limit_snapshot;
-- +goose StatementEnd

-- +goose StatementBegin
DROP INDEX IF EXISTS idx_trace_span_created_at;
-- +goose StatementEnd

-- +goose StatementBegin
DROP INDEX IF EXISTS idx_trace_span_turn_id;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS trace_span;
-- +goose StatementEnd

-- +goose StatementBegin
DROP INDEX IF EXISTS idx_behavior_created_at;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS behavior;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE usage DROP COLUMN mode;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE usage DROP COLUMN "trigger";
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE usage DROP COLUMN api_ms;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE usage DROP COLUMN ttft_ms;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE usage DROP COLUMN status;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE usage DROP COLUMN reasoning_tokens;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE usage DROP COLUMN cache_write_tokens;
-- +goose StatementEnd
