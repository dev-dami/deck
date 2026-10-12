package chat

import (
	"testing"

	"github.com/dev-dami/deck/internal/message"
	"github.com/dev-dami/deck/internal/ui/styles"
	"github.com/stretchr/testify/require"
)

// TestAgentTreeCache asserts a running agent tool reuses its tree between
// spinner frames, renders exactly what an uncached render does, and
// rebuilds the tree when a nested call arrives.
func TestAgentTreeCache(t *testing.T) {
	t.Parallel()

	sty := styles.DeckDark()
	tc := message.ToolCall{ID: "agent", Name: "agent", Input: `{"prompt":"look around\nand report"}`, Finished: true}
	item := NewAgentToolMessageItem(&sty, tc, nil, false)
	child := NewToolMessageItem(&sty, "msg", message.ToolCall{ID: "c1", Name: "bash", Input: `{"command":"ls"}`, Finished: true}, &message.ToolResult{ToolCallID: "c1", Content: "ok"}, false, "")
	item.AddNestedTool(child)

	first := item.RawRender(100)
	require.True(t, item.tree.ok, "the tree is cached")
	cached := item.tree.tree

	item.Advance()
	second := item.RawRender(100)
	require.Equal(t, cached, item.tree.tree, "a spinner frame reuses the tree")

	// An uncached render of the same frame is identical.
	item.tree = agentTreeCache{}
	require.Equal(t, second, item.RawRender(100))
	require.NotEmpty(t, first)

	item.AddNestedTool(NewToolMessageItem(&sty, "msg", message.ToolCall{ID: "c2", Name: "bash", Input: `{"command":"pwd"}`, Finished: true}, nil, false, ""))
	_ = item.RawRender(100)
	require.NotEqual(t, cached, item.tree.tree, "a new nested call rebuilds the tree")
	require.Len(t, item.tree.children, 2)
}
