package session

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/dev-dami/deck/internal/deck/agent/fake"
	"github.com/stretchr/testify/require"
)

func TestHighlightExecutorStampsAuthenticatedCallerAndTurn(t *testing.T) {
	r := newRig(t, nil, []fake.Script{turn(say("working"), finish())})
	tid := r.thread(t)
	id := r.add(t, tid, "codex")
	require.NoError(t, r.m.Post(tid, "@codex begin"))
	waitFor(t, "turn", func() bool { return r.c.turnDones(id) == 1 })
	calls := 0
	r.m.SetHighlights(func(_ context.Context, thread, agent, turn, op string, args json.RawMessage) (string, error) {
		calls++
		require.Equal(t, tid, thread)
		require.Equal(t, id, agent)
		require.NotEmpty(t, turn)
		require.Equal(t, "highlight.add", op)
		return "recorded", nil
	})
	bad := r.m.execOpResult(t.Context(), id, "highlight.add", json.RawMessage(`{"key":"k","title":"Finding","agent":"claude"}`))
	require.True(t, bad.IsError)
	require.Zero(t, calls)
	good := r.m.execOpResult(t.Context(), id, "highlight.add", json.RawMessage(`{"key":"k","title":"Finding"}`))
	require.False(t, good.IsError)
	require.Equal(t, "recorded", good.Text)
	require.Equal(t, 1, calls)
	events := r.c.events(func(e EventMsg) bool { return e.AgentID == id && e.Text == "working" })
	require.NotEmpty(t, events)
	require.NotEmpty(t, events[0].TurnID)
}

func TestHighlightsBriefingRequiresInstalledExecutor(t *testing.T) {
	r := newRig(t, nil, nil)
	tid := r.thread(t)
	r.add(t, tid, "claude")
	before, _ := r.claude.spec("claude")
	lacks(t, before.Instructions, "highlight.add", "summary.update")
	r.m.SetHighlights(func(context.Context, string, string, string, string, json.RawMessage) (string, error) {
		return "", nil
	})
	r.add(t, tid, "codex")
	after, _ := r.codex.spec("codex")
	has(t, after.Instructions, "highlight.add {key,kind,title}", "summary.update {text}", "one-line titles")
	require.LessOrEqual(t, len(after.Instructions), maxInstrChars)
}
