package model

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dev-dami/deck/internal/workspace"
)

func TestInstanceItems(t *testing.T) {
	t.Parallel()

	inserts := func(lines []workspace.AgentLine) []string {
		var out []string
		for _, it := range instanceItems(lines) {
			out = append(out, it.Value.Insert)
		}
		return out
	}

	// Nobody has joined yet: the roster lists installed CLIs as ready.
	require.Empty(t, inserts([]workspace.AgentLine{{ID: "claude", Kind: "claude", Status: "ready"}}))

	// One of a kind: only "start another".
	require.Equal(t, []string{"$claude+"}, inserts([]workspace.AgentLine{{ID: "claude", Kind: "claude", Status: "idle"}}))

	// Two claudes (one more exited) and a codex.
	require.Equal(t,
		[]string{"$claude1", "$claude2", "$claude*", "$claude+", "$codex+"},
		inserts([]workspace.AgentLine{
			{ID: "claude1", Kind: "claude", Status: "working"},
			{ID: "codex", Kind: "codex", Status: "idle"},
			{ID: "claude2", Kind: "claude", Status: "idle"},
			{ID: "claude3", Kind: "claude", Status: "exited"},
		}))
}
