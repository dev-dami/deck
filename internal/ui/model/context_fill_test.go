package model

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dev-dami/deck/internal/workspace"
)

func TestRosterContextPercent(t *testing.T) {
	t.Parallel()

	_, ok := rosterContextPercent(nil)
	require.False(t, ok, "no agents, no figure")

	_, ok = rosterContextPercent([]workspace.AgentLine{{Kind: "claude", Status: "ready"}})
	require.False(t, ok, "an agent that has not reported its context")

	pct, ok := rosterContextPercent([]workspace.AgentLine{
		{Kind: "claude", ContextTokens: 50_000, ContextWindow: 200_000},
		{Kind: "codex", ContextTokens: 160_000, ContextWindow: 400_000},
		{Kind: "opencode", ContextTokens: 10, ContextWindow: 0},
	})
	require.True(t, ok)
	require.Equal(t, 40, pct, "the fullest agent's context")
}
