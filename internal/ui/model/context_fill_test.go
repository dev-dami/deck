package model

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dev-dami/deck/internal/workspace"
)

func TestRosterContextPercent(t *testing.T) {
	t.Parallel()

	_, _, ok := rosterContextPercent(nil)
	require.False(t, ok, "no agents, no figure")

	_, _, ok = rosterContextPercent([]workspace.AgentLine{{Kind: "claude", Status: "ready"}})
	require.False(t, ok, "an agent that has not reported its context")

	pct, approx, ok := rosterContextPercent([]workspace.AgentLine{
		{Kind: "claude", ContextTokens: 50_000, ContextWindow: 200_000, ContextSource: "derived"},
		{Kind: "codex", ContextTokens: 160_000, ContextWindow: 400_000, ContextSource: "cli"},
		{Kind: "opencode", ContextTokens: 10, ContextWindow: 0},
	})
	require.True(t, ok)
	require.False(t, approx)
	require.Equal(t, 40, pct, "the fullest agent's context")

	// An estimate that is the fullest is shown, but marked as one.
	pct, approx, ok = rosterContextPercent([]workspace.AgentLine{
		{Kind: "codex", ContextTokens: 100_000, ContextWindow: 400_000, ContextSource: "cli"},
		{Kind: "antigravity", ContextTokens: 600_000, ContextWindow: 1_000_000, ContextSource: "estimated"},
	})
	require.True(t, ok)
	require.True(t, approx, "the fullest figure is an estimate")
	require.Equal(t, 60, pct)

	// A CLI's figure wins a tie with an estimate.
	_, approx, _ = rosterContextPercent([]workspace.AgentLine{
		{Kind: "claude", ContextTokens: 100_000, ContextWindow: 1_000_000, ContextSource: "fallback"},
		{Kind: "codex", ContextTokens: 40_000, ContextWindow: 400_000, ContextSource: "cli"},
	})
	require.False(t, approx)
}
