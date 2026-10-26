package db

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestUpdatedAtTriggers pins which updated_at triggers are left. The
// message and file ones doubled every row write; UpdateMessage sets
// updated_at itself. The session one stays because several session
// writers rely on it.
func TestUpdatedAtTriggers(t *testing.T) {
	t.Parallel()

	conn, err := Connect(t.Context(), t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	rows, err := conn.QueryContext(t.Context(),
		`SELECT name FROM sqlite_master WHERE type = 'trigger' ORDER BY name`)
	require.NoError(t, err)
	defer rows.Close()
	var triggers []string
	for rows.Next() {
		var name string
		require.NoError(t, rows.Scan(&name))
		triggers = append(triggers, name)
	}
	require.NoError(t, rows.Err())
	require.NotContains(t, triggers, "update_messages_updated_at")
	require.NotContains(t, triggers, "update_files_updated_at")
	require.Contains(t, triggers, "update_sessions_updated_at")

	q := New(conn)
	ctx := t.Context()
	_, err = q.CreateSession(ctx, CreateSessionParams{ID: "s", Title: "t"})
	require.NoError(t, err)
	_, err = q.CreateMessage(ctx, CreateMessageParams{ID: "m", SessionID: "s", Role: "assistant", Parts: "[]"})
	require.NoError(t, err)

	// Backdate the row, then update it the way a streaming flush does.
	_, err = conn.ExecContext(ctx, `UPDATE messages SET updated_at = 1 WHERE id = 'm'`)
	require.NoError(t, err)
	require.NoError(t, q.UpdateMessage(ctx, UpdateMessageParams{ID: "m", Parts: `[{"type":"text"}]`}))
	got, err := q.GetMessage(ctx, "m")
	require.NoError(t, err)
	require.Greater(t, got.UpdatedAt, int64(1), "UpdateMessage must set updated_at without the trigger")
}
