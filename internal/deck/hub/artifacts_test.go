package hub

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestArtifactCatalogValidationAndOutputBudget(t *testing.T) {
	env := testEnv(t)
	calls := 0
	env.Artifacts = func(_ context.Context, op string, raw json.RawMessage) (string, error) {
		calls++
		var in map[string]any
		require.NoError(t, json.Unmarshal(raw, &in))
		return `{"revision":1}`, nil
	}
	args := json.RawMessage(`{"key":"k","title":"App","kind":"react","entrypoint":"App.tsx","files":[{"path":"App.tsx","content":"export default () => <div/>"}]}`)
	out, isErr := Execute(context.Background(), env, "artifact.create", args)
	require.False(t, isErr)
	require.JSONEq(t, `{"revision":1}`, out)
	for _, extra := range []string{`"agent":"claude"`, `"session_id":"another"`, `"user":true`} {
		bad := json.RawMessage(strings.TrimSuffix(string(args), "}") + "," + extra + "}")
		_, isErr = Execute(context.Background(), env, "artifact.create", bad)
		require.True(t, isErr)
	}
	require.Equal(t, 1, calls)
	require.Contains(t, Search(env, json.RawMessage(`{"query":"artifact"}`)), "artifact.read")
	env.Artifacts = func(context.Context, string, json.RawMessage) (string, error) {
		return strings.Repeat("x", MaxOutput), nil
	}
	out, isErr = Execute(context.Background(), env, "artifact.read", json.RawMessage(`{"id":"artifact"}`))
	require.True(t, isErr)
	require.Contains(t, out, "output budget")
	require.NotContains(t, out, "output truncated")
}
