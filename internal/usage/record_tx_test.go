package usage

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dev-dami/deck/internal/db"
)

func TestRecordWithDBWritesSpansInOneTx(t *testing.T) {
	t.Parallel()
	conn, err := db.Connect(t.Context(), t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { conn.Close() })
	svc := NewServiceWithDB(db.New(conn), conn)
	ctx := t.Context()

	spans := []Span{
		{Kind: SpanWait, Dur: time.Second},
		{Kind: SpanThink, Start: time.Second, Dur: 2 * time.Second, Tokens: 40},
		{Kind: SpanTool, Name: "Bash", Start: 3 * time.Second, Dur: time.Second},
	}
	require.NoError(t, svc.Record(ctx, Record{ID: "turn-1", SessionID: "s1", AgentID: "claude-1", Kind: "claude", Spans: spans}))
	require.NoError(t, svc.Record(ctx, Record{ID: "turn-2", SessionID: "s1", AgentID: "claude-1", Kind: "claude"}))

	got, err := svc.Trace(ctx, "turn-1")
	require.NoError(t, err)
	require.Len(t, got, len(spans))
	require.Equal(t, "Bash", got[2].Name)

	traces, err := svc.Traces(ctx, 10)
	require.NoError(t, err)
	require.Len(t, traces, 2)
}
