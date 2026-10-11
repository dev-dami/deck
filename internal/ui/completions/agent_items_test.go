package completions

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/stretchr/testify/require"
)

func agentItems() []AgentItem {
	return []AgentItem{
		{Label: "claude opus", Value: AgentCompletionValue{Kind: "claude", Model: "opus", Effort: "high", Current: true}},
		{Label: "codex gpt-5", Value: AgentCompletionValue{Kind: "codex", Model: "gpt-5", Effort: "low"}},
		{Label: "opencode default", Value: AgentCompletionValue{Kind: "opencode"}},
	}
}

func newAgentCompletions() *Completions {
	c := New(lipgloss.NewStyle(), lipgloss.NewStyle(), lipgloss.NewStyle())
	c.SetAgentItems(agentItems())
	return c
}

func TestSetAgentItemsOpensThePopup(t *testing.T) {
	t.Parallel()
	c := newAgentCompletions()
	require.True(t, c.IsOpen())
	require.True(t, c.HasItems())
	require.Empty(t, c.Query())
	require.Len(t, c.filtered, 3)
	for i, it := range c.filtered {
		ci, ok := it.(*CompletionItem)
		require.True(t, ok)
		require.Equal(t, agentItems()[i].Label, ci.Text())
		require.Equal(t, agentItems()[i].Value, ci.Value())
	}
	require.NotEmpty(t, c.Render())
}

func TestSelectAgentItem(t *testing.T) {
	t.Parallel()

	t.Run("enter picks the first", func(t *testing.T) {
		t.Parallel()
		c := newAgentCompletions()
		msg, handled := c.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
		require.True(t, handled)
		sel, ok := msg.(SelectionMsg[AgentCompletionValue])
		require.True(t, ok, "got %T", msg)
		require.Equal(t, "claude", sel.Value.Kind)
		require.Equal(t, "opus", sel.Value.Model)
		require.Equal(t, "high", sel.Value.Effort)
		require.True(t, sel.Value.Current)
		require.False(t, sel.KeepOpen)
		require.False(t, c.IsOpen())
	})

	t.Run("down then enter picks the next", func(t *testing.T) {
		t.Parallel()
		c := newAgentCompletions()
		_, handled := c.Update(tea.KeyPressMsg{Code: tea.KeyDown})
		require.True(t, handled)
		msg, _ := c.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
		sel, ok := msg.(SelectionMsg[AgentCompletionValue])
		require.True(t, ok, "got %T", msg)
		require.Equal(t, AgentCompletionValue{Kind: "codex", Model: "gpt-5", Effort: "low"}, sel.Value)
	})

	t.Run("filter then select", func(t *testing.T) {
		t.Parallel()
		c := newAgentCompletions()
		c.Filter("opencode")
		require.True(t, c.HasItems())
		msg := c.selectCurrent(false)
		sel, ok := msg.(SelectionMsg[AgentCompletionValue])
		require.True(t, ok, "got %T", msg)
		require.Equal(t, "opencode", sel.Value.Kind)
		require.Empty(t, sel.Value.Model)
	})

	t.Run("esc closes without a pick", func(t *testing.T) {
		t.Parallel()
		c := newAgentCompletions()
		msg, handled := c.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
		require.True(t, handled)
		require.IsType(t, ClosedMsg{}, msg)
		require.False(t, c.IsOpen())
	})
}
