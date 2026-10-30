package session

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/dev-dami/deck/internal/deck/agent"
	"github.com/stretchr/testify/require"
)

func TestArtifactExecutorBindsIdentityAndRejectsPlanWrites(t *testing.T) {
	r := newRig(t, nil, nil)
	tid := r.thread(t)
	id := r.add(t, tid, "codex")
	calls := 0
	r.m.SetArtifacts(func(_ context.Context, thread, who, turn, op string, raw json.RawMessage) (string, error) {
		calls++
		require.Equal(t, tid, thread)
		require.Equal(t, id, who)
		return "accepted", nil
	})
	args := json.RawMessage(`{"key":"k","title":"Doc","kind":"markdown","entrypoint":"doc.md","files":[{"path":"doc.md","content":"# Hi"}]}`)
	require.NoError(t, r.m.SetMode(tid, agent.ModeAsk))
	out := r.m.execOpResult(t.Context(), id, "artifact.create", args)
	require.False(t, out.IsError)
	require.Equal(t, 1, calls)
	require.NoError(t, r.m.SetMode(tid, agent.ModePlan))
	out = r.m.execOpResult(t.Context(), id, "artifact.create", args)
	require.True(t, out.IsError)
	require.Contains(t, out.Text, "plan mode")
	require.Equal(t, 1, calls)
	out = r.m.execOpResult(t.Context(), id, "artifact.list", json.RawMessage(`{}`))
	require.False(t, out.IsError)
	require.Equal(t, 2, calls)
	out = r.m.execOpResult(t.Context(), id, "artifact.read", json.RawMessage(`{"id":"a","agent":"forged"}`))
	require.True(t, out.IsError)
	require.Equal(t, 2, calls)
}
