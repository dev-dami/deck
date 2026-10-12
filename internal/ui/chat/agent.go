package chat

import (
	"encoding/json"
	"slices"
	"strings"

	"charm.land/lipgloss/v2"
	"charm.land/lipgloss/v2/tree"
	"github.com/dev-dami/deck/internal/message"
	tools "github.com/dev-dami/deck/internal/toolspec"
	"github.com/dev-dami/deck/internal/ui/common"
	"github.com/dev-dami/deck/internal/ui/styles"
)

// -----------------------------------------------------------------------------
// Agent Tool
// -----------------------------------------------------------------------------

// NestedToolContainer is an interface for tool items that can contain nested tool calls.
type NestedToolContainer interface {
	NestedTools() []ToolMessageItem
	SetNestedTools(tools []ToolMessageItem)
	AddNestedTool(tool ToolMessageItem)
}

// AgentToolMessageItem is a message item that represents an agent tool call.
type AgentToolMessageItem struct {
	*baseToolMessageItem

	nestedTools []ToolMessageItem
	// tree keeps the rendered tree between animation frames.
	tree agentTreeCache
}

var (
	_ ToolMessageItem     = (*AgentToolMessageItem)(nil)
	_ NestedToolContainer = (*AgentToolMessageItem)(nil)
)

// NewAgentToolMessageItem creates a new [AgentToolMessageItem].
func NewAgentToolMessageItem(
	sty *styles.Styles,
	toolCall message.ToolCall,
	result *message.ToolResult,
	canceled bool,
) *AgentToolMessageItem {
	t := &AgentToolMessageItem{}
	t.baseToolMessageItem = newBaseToolMessageItem(sty, toolCall, result, &AgentToolRenderContext{agent: t}, canceled)
	// For the agent tool we keep spinning until the tool call is finished.
	t.spinningFunc = func(state SpinningState) bool {
		return !state.HasResult() && !state.IsCanceled()
	}
	return t
}

// Advance implements [Animatable].
//
// Advances the parent's own spinner and every spinning nested tool in
// one frame, bumping the parent's F6 list-cache version. Nested tools
// are not list entries of their own — their IDs map to this parent's
// index in idInxMap and their renders are embedded inline in this
// parent's output — so the list only checks the parent's version.
// Without the bump, the list cache would serve the previously rendered
// frame indefinitely and the spinner would appear frozen.
func (a *AgentToolMessageItem) Advance() bool {
	if a.result != nil || a.Status() == ToolStatusCanceled {
		return false
	}
	changed := a.anim.Advance()
	changed = advanceNested(a.nestedTools) || changed
	if changed {
		a.Bump()
	}
	return changed
}

// restyleAnim implements [animRestyler]. It also reaches nested tools,
// which are not list entries of their own and therefore never receive
// the call directly.
func (a *AgentToolMessageItem) restyleAnim() {
	a.baseToolMessageItem.restyleAnim()
	for _, nested := range a.nestedTools {
		if ar, ok := nested.(animRestyler); ok {
			ar.restyleAnim()
		}
	}
}

// advanceNested advances every spinning animatable tool in tools and
// reports whether any of them changed.
func advanceNested(tools []ToolMessageItem) bool {
	changed := false
	for _, nestedTool := range tools {
		if s, ok := nestedTool.(Animatable); ok && s.Spinning() && s.Advance() {
			changed = true
		}
	}
	return changed
}

// NestedTools returns the nested tools.
func (a *AgentToolMessageItem) NestedTools() []ToolMessageItem {
	return a.nestedTools
}

// SetNestedTools sets the nested tools.
//
// SetNestedTools always bumps the version. The previous design
// deduped when the slice's length and element pointers were
// unchanged, but the live update path in internal/ui/model/ui.go
// mutates existing children in place (SetToolCall / SetResult on the
// same pointers) and then calls SetNestedTools with the same slice.
// Pointer-equality dedupe in that case skips the parent Bump even
// though the parent's rendered output (which embeds the children
// inline) has changed, leaving a stale parent entry in the list
// cache. Always bumping is cheap (one uint64 increment) and called
// at most once per agent event; in the rare case the slice is
// truly unchanged the worst case is one extra parent re-render
// while every child cache hit stays warm.
func (a *AgentToolMessageItem) SetNestedTools(tools []ToolMessageItem) {
	a.nestedTools = tools
	a.clearCache()
	a.Bump()
}

// AddNestedTool adds a nested tool.
func (a *AgentToolMessageItem) AddNestedTool(tool ToolMessageItem) {
	// Mark nested tools as simple (compact) rendering.
	if s, ok := tool.(Compactable); ok {
		s.SetCompact(true)
	}
	a.nestedTools = append(a.nestedTools, tool)
	a.clearCache()
	a.Bump()
}

// AgentToolRenderContext renders agent tool messages.
type AgentToolRenderContext struct {
	agent *AgentToolMessageItem
}

// agentTreeCache keeps an agent tool's rendered tree (header, tagged
// prompt and nested tool calls) between animation frames. While the agent
// runs, each frame only turns the spinner under the tree, yet the header,
// the wrapped prompt and the tree were all built again. It is keyed by
// everything the tree is drawn from; the nested tools' renders are part
// of the key, so a nested spinner or a new nested call rebuilds it.
type agentTreeCache struct {
	ok       bool
	width    int
	expanded bool
	status   ToolStatus
	input    string
	gen      uint64
	children []string
	tree     string
}

// get returns the cached tree when it was drawn from the same inputs.
func (c *agentTreeCache) get(width int, opts *ToolRenderOpts, children []string) (string, bool) {
	if !c.ok || c.width != width || c.expanded != opts.ExpandedContent || c.status != opts.Status ||
		c.input != opts.ToolCall.Input || c.gen != common.StyleGeneration() || !slices.Equal(c.children, children) {
		return "", false
	}
	return c.tree, true
}

// set caches tree as drawn from the given inputs.
func (c *agentTreeCache) set(width int, opts *ToolRenderOpts, children []string, tree string) {
	*c = agentTreeCache{
		ok:       true,
		width:    width,
		expanded: opts.ExpandedContent,
		status:   opts.Status,
		input:    opts.ToolCall.Input,
		gen:      common.StyleGeneration(),
		children: children,
		tree:     tree,
	}
}

// renderNested renders each nested tool at width.
func renderNested(nested []ToolMessageItem, width int) []string {
	out := make([]string, len(nested))
	for i, t := range nested {
		out[i] = t.Render(width)
	}
	return out
}

// RenderTool implements the [ToolRenderer] interface.
func (r *AgentToolRenderContext) RenderTool(sty *styles.Styles, width int, opts *ToolRenderOpts) string {
	cappedWidth := cappedMessageWidth(width)
	if !opts.ToolCall.Finished && !opts.IsCanceled() && len(r.agent.nestedTools) == 0 {
		return pendingTool(sty, "Agent", opts.Anim, opts.Compact)
	}

	if opts.Compact {
		return toolHeader(sty, opts.Status, "Agent", cappedWidth, opts)
	}

	// Build the task tag and prompt.
	taskTag := sty.Tool.AgentTaskTag.Render("Task")
	taskTagWidth := lipgloss.Width(taskTag)

	// Calculate remaining width for prompt.
	remainingWidth := min(cappedWidth-taskTagWidth-3, maxTextWidth-taskTagWidth-3) // -3 for spacing

	children := renderNested(r.agent.nestedTools, remainingWidth)
	treeView, ok := r.agent.tree.get(cappedWidth, opts, children)
	if !ok {
		var params tools.AgentParams
		_ = json.Unmarshal([]byte(opts.ToolCall.Input), &params)

		prompt := params.Prompt
		if !opts.ExpandedContent {
			prompt = strings.ReplaceAll(prompt, "\n", " ")
		}

		header := toolHeader(sty, opts.Status, "Agent", cappedWidth, opts)
		promptText := sty.Tool.AgentPrompt.Width(remainingWidth).Render(prompt)

		header = lipgloss.JoinVertical(
			lipgloss.Left,
			header,
			"",
			lipgloss.JoinHorizontal(
				lipgloss.Left,
				taskTag,
				" ",
				promptText,
			),
		)

		// Build tree with nested tool calls.
		childTools := tree.Root(header)
		for _, childView := range children {
			childTools.Child(childView)
		}
		treeView = roundedTree(childTools, taskTagWidth-5).String()
		r.agent.tree.set(cappedWidth, opts, children, treeView)
	}

	// Build parts.
	var parts []string
	parts = append(parts, treeView)

	// Show animation if still running.
	if !opts.HasResult() && !opts.IsCanceled() {
		parts = append(parts, "", opts.Anim.Render())
	}

	result := lipgloss.JoinVertical(lipgloss.Left, parts...)

	// Add body content when completed.
	if opts.HasResult() && opts.Result.Content != "" {
		body := toolOutputMarkdownContent(sty, opts.Result.Content, cappedWidth-toolBodyLeftPaddingTotal, opts.ExpandedContent)
		return joinToolParts(result, body)
	}

	return result
}

// -----------------------------------------------------------------------------
// Agentic Fetch Tool
// -----------------------------------------------------------------------------

// AgenticFetchToolMessageItem is a message item that represents an agentic fetch tool call.
type AgenticFetchToolMessageItem struct {
	*baseToolMessageItem

	nestedTools []ToolMessageItem
	// tree keeps the rendered tree between animation frames.
	tree agentTreeCache
}

var (
	_ ToolMessageItem     = (*AgenticFetchToolMessageItem)(nil)
	_ NestedToolContainer = (*AgenticFetchToolMessageItem)(nil)
)

// NewAgenticFetchToolMessageItem creates a new [AgenticFetchToolMessageItem].
func NewAgenticFetchToolMessageItem(
	sty *styles.Styles,
	toolCall message.ToolCall,
	result *message.ToolResult,
	canceled bool,
) *AgenticFetchToolMessageItem {
	t := &AgenticFetchToolMessageItem{}
	t.baseToolMessageItem = newBaseToolMessageItem(sty, toolCall, result, &AgenticFetchToolRenderContext{fetch: t}, canceled)
	// For the agentic fetch tool we keep spinning until the tool call is finished.
	t.spinningFunc = func(state SpinningState) bool {
		return !state.HasResult() && !state.IsCanceled()
	}
	return t
}

// Advance implements [Animatable]. See [AgentToolMessageItem.Advance]
// for the parent-bump rationale; without an override the embedded base
// Advance would never advance the nested children.
func (a *AgenticFetchToolMessageItem) Advance() bool {
	if a.result != nil || a.Status() == ToolStatusCanceled {
		return false
	}
	changed := a.anim.Advance()
	changed = advanceNested(a.nestedTools) || changed
	if changed {
		a.Bump()
	}
	return changed
}

// restyleAnim implements [animRestyler]. See
// [AgentToolMessageItem.restyleAnim] for the nested-tool rationale.
func (a *AgenticFetchToolMessageItem) restyleAnim() {
	a.baseToolMessageItem.restyleAnim()
	for _, nested := range a.nestedTools {
		if ar, ok := nested.(animRestyler); ok {
			ar.restyleAnim()
		}
	}
}

// NestedTools returns the nested tools.
func (a *AgenticFetchToolMessageItem) NestedTools() []ToolMessageItem {
	return a.nestedTools
}

// SetNestedTools sets the nested tools. Always bumps the version;
// see [AgentToolMessageItem.SetNestedTools] for the rationale.
func (a *AgenticFetchToolMessageItem) SetNestedTools(tools []ToolMessageItem) {
	a.nestedTools = tools
	a.clearCache()
	a.Bump()
}

// AddNestedTool adds a nested tool.
func (a *AgenticFetchToolMessageItem) AddNestedTool(tool ToolMessageItem) {
	// Mark nested tools as simple (compact) rendering.
	if s, ok := tool.(Compactable); ok {
		s.SetCompact(true)
	}
	a.nestedTools = append(a.nestedTools, tool)
	a.clearCache()
	a.Bump()
}

// AgenticFetchToolRenderContext renders agentic fetch tool messages.
type AgenticFetchToolRenderContext struct {
	fetch *AgenticFetchToolMessageItem
}

// agenticFetchParams matches tools.AgenticFetchParams.
type agenticFetchParams struct {
	URL    string `json:"url,omitempty"`
	Prompt string `json:"prompt"`
}

// RenderTool implements the [ToolRenderer] interface.
func (r *AgenticFetchToolRenderContext) RenderTool(sty *styles.Styles, width int, opts *ToolRenderOpts) string {
	cappedWidth := cappedMessageWidth(width)
	if !opts.ToolCall.Finished && !opts.IsCanceled() && len(r.fetch.nestedTools) == 0 {
		return pendingTool(sty, "Agentic Fetch", opts.Anim, opts.Compact)
	}

	var params agenticFetchParams
	_ = json.Unmarshal([]byte(opts.ToolCall.Input), &params)

	// Build header with optional URL param.
	var toolParams []string
	if params.URL != "" {
		toolParams = append(toolParams, params.URL)
	}

	if opts.Compact {
		return toolHeader(sty, opts.Status, "Agentic Fetch", cappedWidth, opts, toolParams...)
	}

	// Build the prompt tag.
	promptTag := sty.Tool.AgenticFetchPromptTag.Render("Prompt")
	promptTagWidth := lipgloss.Width(promptTag)

	// Calculate remaining width for prompt text.
	remainingWidth := min(cappedWidth-promptTagWidth-3, maxTextWidth-promptTagWidth-3) // -3 for spacing

	children := renderNested(r.fetch.nestedTools, remainingWidth)
	treeView, ok := r.fetch.tree.get(cappedWidth, opts, children)
	if !ok {
		prompt := params.Prompt
		if !opts.ExpandedContent {
			prompt = strings.ReplaceAll(prompt, "\n", " ")
		}

		header := toolHeader(sty, opts.Status, "Agentic Fetch", cappedWidth, opts, toolParams...)
		promptText := sty.Tool.AgentPrompt.Width(remainingWidth).Render(prompt)

		header = lipgloss.JoinVertical(
			lipgloss.Left,
			header,
			"",
			lipgloss.JoinHorizontal(
				lipgloss.Left,
				promptTag,
				" ",
				promptText,
			),
		)

		// Build tree with nested tool calls.
		childTools := tree.Root(header)
		for _, childView := range children {
			childTools.Child(childView)
		}
		treeView = roundedTree(childTools, promptTagWidth-5).String()
		r.fetch.tree.set(cappedWidth, opts, children, treeView)
	}

	// Build parts.
	var parts []string
	parts = append(parts, treeView)

	// Show animation if still running.
	if !opts.HasResult() && !opts.IsCanceled() {
		parts = append(parts, "", opts.Anim.Render())
	}

	result := lipgloss.JoinVertical(lipgloss.Left, parts...)

	// Add body content when completed.
	if opts.HasResult() && opts.Result.Content != "" {
		body := toolOutputMarkdownContent(sty, opts.Result.Content, cappedWidth-toolBodyLeftPaddingTotal, opts.ExpandedContent)
		return joinToolParts(result, body)
	}

	return result
}
