-- name: GetMessage :one
SELECT *
FROM messages
WHERE id = ? LIMIT 1;

-- name: ListMessagesBySession :many
-- seq is the order the rows were created in; created_at has one-second
-- resolution and cannot break ties.
SELECT *
FROM messages
WHERE session_id = ?
ORDER BY seq ASC;

-- name: ListMessagesBySessionFromSummary :many
-- Messages from the summary onward, which is all a compacted session sends.
SELECT m.*
FROM messages m
WHERE m.session_id = ?
  AND m.seq >= (SELECT s.seq FROM messages s WHERE s.id = ?)
ORDER BY m.seq ASC;

-- name: CreateMessage :one
-- seq is one more than the session's last: SQLite runs the statement as one
-- write, so two rows of a session never get the same number.
INSERT INTO messages (
    id,
    session_id,
    role,
    parts,
    model,
    provider,
    is_summary_message,
    seq,
    created_at,
    updated_at
) VALUES (
    @id, @session_id, @role, @parts, @model, @provider, @is_summary_message,
    (SELECT COALESCE(MAX(m.seq), 0) + 1 FROM messages m WHERE m.session_id = @session_id),
    strftime('%s', 'now'), strftime('%s', 'now')
)
RETURNING *;

-- name: UpdateMessage :exec
UPDATE messages
SET
    parts = ?,
    prism_model_id = ?,
    prism_model_name = ?,
    prism_hypercredit_savings = ?,
    prism_dollar_savings = ?,
    finished_at = ?,
    updated_at = strftime('%s', 'now')
WHERE id = ?;


-- name: DeleteMessage :exec
DELETE FROM messages
WHERE id = ?;

-- name: DeleteSessionMessages :exec
DELETE FROM messages
WHERE session_id = ?;

-- name: ListUserMessagesBySession :many
-- Backs prompt history, which steps back one entry at a time.
SELECT *
FROM messages
WHERE session_id = ? AND role = 'user'
ORDER BY seq DESC
LIMIT 200;

-- name: ListAllUserMessages :many
-- Backs prompt history when no session is open. Needs
-- idx_messages_role_created_at to seek rather than scan the table.
SELECT *
FROM messages
WHERE role = 'user'
ORDER BY created_at DESC
LIMIT 200;

-- name: GetLastAssistantMessageBySession :one
SELECT *
FROM messages
WHERE session_id = ? AND role = 'assistant' AND is_summary_message = 0
ORDER BY seq DESC
LIMIT 1;
