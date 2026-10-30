package chat

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"

	"github.com/dev-dami/deck/internal/message"
	"github.com/dev-dami/deck/internal/ui/styles"
)

// The hand-offs Deck's workspace writes read as the desktop's handoff.ts
// reads them.
func TestParseHandoff(t *testing.T) {
	t.Parallel()

	h, ok := parseHandoff("→ $codex $claude\n\nAdd a test.")
	require.True(t, ok)
	require.Equal(t, []string{"codex", "claude"}, h.to)
	require.Equal(t, "Add a test.", h.body)
	require.Empty(t, h.taskID)
	require.False(t, h.subOK)

	h, ok = parseHandoff("→ $claude\n\ntask t1 \"limiter test\" done: Added the test.")
	require.True(t, ok)
	require.Equal(t, "t1", h.taskID)
	require.Equal(t, "limiter test", h.taskTitle)
	require.Equal(t, "done", h.taskState)
	require.Equal(t, "Added the test.", h.body)

	h, ok = parseHandoff("→ $claude · sub-thread s-42\n\n[sub-thread \"Docs pass\"] All fixed.")
	require.True(t, ok)
	require.True(t, h.subOK)
	require.Equal(t, "Docs pass", h.sub)
	require.Equal(t, "All fixed.", h.body)

	for _, text := range []string{"plain reply", "→ nobody", "→\n\nbody", "see → $codex"} {
		_, ok := parseHandoff(text)
		require.False(t, ok, text)
	}
}

func TestHandoffCardIsARoundedBox(t *testing.T) {
	t.Parallel()
	sty := styles.DeckDark()
	h, _ := parseHandoff("→ $claude\n\ntask t1 \"limiter test\" failed: it did not build")
	out := renderHandoffCard(&sty, h, 60)
	lines := strings.Split(ansi.Strip(out), "\n")
	require.True(t, strings.HasPrefix(lines[0], "╭"))
	require.True(t, strings.HasPrefix(lines[len(lines)-1], "╰"))
	require.Contains(t, lines[1], "to ✻ claude")
	require.Contains(t, lines[1], "task t1 · limiter test · failed")
	require.Contains(t, ansi.Strip(out), "it did not build")
	for _, l := range lines {
		require.Equal(t, 60, lipgloss.Width(l), l)
	}
}

func TestSubagentOf(t *testing.T) {
	t.Parallel()

	call, ok := subagentOf(message.ToolCall{Name: "Task", Input: `{"description":"find callers","subagent_type":"Explore"}`})
	require.True(t, ok)
	require.Equal(t, subagentCall{kindLabel: "Explore", task: "find callers"}, call)

	call, ok = subagentOf(message.ToolCall{Name: "deck", Input: `{"op":"agent.spawn","args":{"kind":"codex","brief":"write the test\nand run it"}}`})
	require.True(t, ok)
	require.Equal(t, subagentCall{kind: "codex", kindLabel: "codex", task: "write the test"}, call)

	_, ok = subagentOf(message.ToolCall{Name: "deck", Input: `{"op":"thread.read"}`})
	require.False(t, ok)
	_, ok = subagentOf(message.ToolCall{Name: "Bash", Input: `{}`})
	require.False(t, ok)
}
