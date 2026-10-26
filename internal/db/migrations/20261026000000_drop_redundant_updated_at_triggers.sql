-- +goose Up
-- +goose StatementBegin
-- These triggers rewrote updated_at after every UPDATE, so each message
-- flush (up to ~30 a second per agent while streaming) wrote the row twice.
-- UpdateMessage sets updated_at itself, and nothing updates files rows.
-- update_sessions_updated_at stays: UpdateSession, SetSessionChannel and
-- RenameSession rely on it, and so does the message-count trigger that
-- bumps a session to the top of the list when a message is added.
DROP TRIGGER IF EXISTS update_messages_updated_at;
DROP TRIGGER IF EXISTS update_files_updated_at;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
CREATE TRIGGER IF NOT EXISTS update_files_updated_at
AFTER UPDATE ON files
BEGIN
UPDATE files SET updated_at = strftime('%s', 'now')
WHERE id = new.id;
END;

CREATE TRIGGER IF NOT EXISTS update_messages_updated_at
AFTER UPDATE ON messages
BEGIN
UPDATE messages SET updated_at = strftime('%s', 'now')
WHERE id = new.id;
END;
-- +goose StatementEnd
