package hub

import (
	"context"
	"encoding/json"
	"github.com/dev-dami/deck/internal/deck/agent"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestBrowserCatalogAndArgumentValidation(t *testing.T) {
	env := testEnv(t)
	require.Contains(t, Search(env, json.RawMessage(`{"query":"browser"}`)), "browser.snapshot")
	called := false
	env.Browser = func(_ context.Context, op string, args json.RawMessage) agent.OpResult {
		called = true
		require.Equal(t, "browser.open", op)
		return agent.TextResult("opened", false)
	}
	bad := ExecuteResult(context.Background(), env, "browser.open", json.RawMessage(`{"url":"http://localhost:3000","owner":"claude"}`))
	require.True(t, bad.IsError)
	require.False(t, called)
	out := ExecuteResult(context.Background(), env, "browser.open", json.RawMessage(`{"url":"http://localhost:3000"}`))
	require.False(t, out.IsError)
	require.True(t, called)
	env.Browser = nil
	require.Contains(t, ExecuteResult(context.Background(), env, "browser.open", json.RawMessage(`{"url":"http://localhost:3000"}`)).Text, "browser_unavailable")
}
