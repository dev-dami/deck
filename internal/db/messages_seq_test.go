package db

import (
	"testing"

	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/require"
)

// TestSeqMigrationNumbersExistingRows: rows written before messages had a
// seq are numbered per session by created_at, then by insertion order
// within a second, so an upgraded database keeps the order it had.
func TestSeqMigrationNumbersExistingRows(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	conn, err := Connect(ctx, t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	require.NoError(t, goose.DownTo(conn, "migrations", 20261026000000))
	_, err = conn.ExecContext(ctx, `INSERT INTO sessions (id, title, created_at, updated_at) VALUES ('a', 'a', 1, 1), ('b', 'b', 1, 1)`)
	require.NoError(t, err)
	// Ids sort the other way from insertion; two rows share a second.
	_, err = conn.ExecContext(ctx, `INSERT INTO messages (id, session_id, role, parts, created_at, updated_at) VALUES
		('z', 'a', 'user', '[]', 10, 10),
		('y', 'a', 'user', '[]', 10, 10),
		('x', 'a', 'user', '[]', 5, 5),
		('w', 'b', 'user', '[]', 10, 10)`)
	require.NoError(t, err)
	require.NoError(t, goose.Up(conn, "migrations"))

	rows, err := New(conn).ListMessagesBySession(ctx, "a")
	require.NoError(t, err)
	var ids []string
	var seqs []int64
	for _, r := range rows {
		ids = append(ids, r.ID)
		seqs = append(seqs, r.Seq)
	}
	require.Equal(t, []string{"x", "z", "y"}, ids)
	require.Equal(t, []int64{1, 2, 3}, seqs)

	// New rows continue the session's numbering.
	m, err := New(conn).CreateMessage(ctx, CreateMessageParams{ID: "v", SessionID: "a", Role: "user", Parts: "[]"})
	require.NoError(t, err)
	require.Equal(t, int64(4), m.Seq)
	b, err := New(conn).ListMessagesBySession(ctx, "b")
	require.NoError(t, err)
	require.Equal(t, int64(1), b[0].Seq)
}
