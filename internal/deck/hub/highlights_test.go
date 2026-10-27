package hub

import (
	"context"
	"encoding/json"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestHighlightCatalogPreservesJSONSourcesAndRejectsIdentityFields(t *testing.T) {
	env := testEnv(t)
	calls := 0
	env.Highlights = func(_ context.Context, op string, raw json.RawMessage) (string, error) {
		calls++
		require.Equal(t, "highlight.add", op)
		var args map[string]any
		require.NoError(t, json.Unmarshal(raw, &args))
		require.Equal(t, map[string]any{"message_id": "message"}, args["source"])
		return "created", nil
	}
	text, isErr := Execute(context.Background(), env, "highlight.add", json.RawMessage(`{"key":"k","title":"Finding","source":{"message_id":"message"}}`))
	require.False(t, isErr)
	require.Equal(t, "created", text)
	_, isErr = Execute(context.Background(), env, "highlight.add", json.RawMessage(`{"key":"k","title":"Finding","agent":"claude"}`))
	require.True(t, isErr)
	require.Equal(t, 1, calls)
	require.Contains(t, Search(env, json.RawMessage(`{"query":"summary.update"}`)), "summary.update")
}
