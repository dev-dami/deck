-- +goose Up
-- +goose StatementBegin
-- seq is a message's place in its session: 1, 2, 3, ... in the order the
-- rows were created. created_at has one-second resolution and ids are
-- random, so ordering by them put rows made in the same second in random
-- order, and each client in a different one.
ALTER TABLE messages ADD COLUMN seq INTEGER NOT NULL DEFAULT 0;
-- +goose StatementEnd

-- +goose StatementBegin
-- Existing rows are numbered by created_at, then by the order they were
-- inserted in (rowid) within a second. The numbering goes through a
-- temporary table so the update is one indexed lookup per row.
CREATE TEMP TABLE message_seq_backfill AS
SELECT id, ROW_NUMBER() OVER (PARTITION BY session_id ORDER BY created_at, rowid) AS n
FROM messages;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE UNIQUE INDEX message_seq_backfill_id ON message_seq_backfill (id);
-- +goose StatementEnd

-- +goose StatementBegin
UPDATE messages
SET seq = (SELECT b.n FROM message_seq_backfill b WHERE b.id = messages.id);
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE message_seq_backfill;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS idx_messages_session_seq ON messages (session_id, seq);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_messages_session_seq;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE messages DROP COLUMN seq;
-- +goose StatementEnd
