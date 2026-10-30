package chat

import (
	"cmp"
	"encoding/json"
	"regexp"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/dev-dami/deck/internal/message"
	"github.com/dev-dami/deck/internal/ui/common"
	"github.com/dev-dami/deck/internal/ui/styles"
	"github.com/dev-dami/deck/internal/ui/tone"
)

// Hand-offs and subagents are drawn as cards: a rounded hairline box, as
// the desktop app draws them (desktop-shell/src/renderer/handoff.ts and its
// timeline), so a thread reads the same in both apps.

// handoff is a message one agent sent others (the engine's relay): Deck's
// workspace writes it as the sender's message "→ $codex $claude\n\n<text>".
// A task's report to whoever handed it over reads `task t3 "Title" done:
// result`; a sub-thread's report names the sub-thread.
type handoff struct {
	to   []string // the recipients' names, without their sigil
	body string   // what was sent, without the arrow line
	// The task a report is about; id is "" for a plain message.
	taskID, taskTitle, taskState string
	// sub is the title of the sub-thread a report comes from, subOK
	// whether it comes from one.
	sub   string
	subOK bool
}

var (
	handoffHead   = regexp.MustCompile(`^→((?:[ \t]+[@$][^\s@$]+)+)(?:[ \t]+·[ \t]+sub-thread[ \t]+(\S+))?[ \t]*(?:\n|$)`)
	handoffSub    = regexp.MustCompile(`^\[sub-thread "((?:[^"\\]|\\.)*)"\]\s*`)
	handoffReport = regexp.MustCompile(`(?s)^task (\S+) "((?:[^"\\]|\\.)*)" (done|failed|blocked)(?::\s*(.*))?$`)
)

// parseHandoff reads a hand-off from a message's text; ok is false for any
// other message.
func parseHandoff(text string) (h handoff, ok bool) {
	text = strings.TrimSpace(text)
	m := handoffHead.FindStringSubmatch(text)
	if m == nil {
		return h, false
	}
	for _, n := range strings.Fields(m[1]) {
		if n = n[1:]; n != "" {
			h.to = append(h.to, n)
		}
	}
	if len(h.to) == 0 {
		return h, false
	}
	h.body = strings.TrimSpace(text[len(m[0]):])
	h.subOK = m[2] != ""
	if st := handoffSub.FindStringSubmatch(h.body); st != nil {
		h.sub, h.subOK = st[1], true
		h.body = strings.TrimSpace(h.body[len(st[0]):])
	}
	if r := handoffReport.FindStringSubmatch(h.body); r != nil {
		h.taskID, h.taskTitle, h.taskState = r[1], r[2], r[3]
		h.body = strings.TrimSpace(r[4])
	}
	return h, true
}

// agentName is an agent's mark and name in its kind's tint, as the desktop
// writes a member: "✻ claude", ">_ codex-2".
func agentName(name string) string {
	kind := tone.KindOf(name)
	if kind == "" {
		return name
	}
	tint := lipgloss.NewStyle().Foreground(tone.Tint(kind))
	return tint.Render(tone.Mark(kind)) + " " + tint.Bold(true).Render(name)
}

// cardHeader lays a card's first line out: left, and right flush with the
// card's inner edge when it fits.
func cardHeader(left, right string, width int) string {
	rw := lipgloss.Width(right)
	if right == "" || rw+2 > width {
		return ansi.Truncate(left, width, "…")
	}
	left = ansi.Truncate(left, width-rw-1, "…")
	gap := max(1, width-lipgloss.Width(left)-rw)
	return left + strings.Repeat(" ", gap) + right
}

// taskStateStyle is a task's state in its colour.
func taskStateStyle(sty *styles.Styles, state string) lipgloss.Style {
	switch state {
	case "done":
		return sty.Messages.CardDone
	case "failed":
		return sty.Messages.CardFailed
	case "blocked":
		return sty.Messages.CardBlocked
	}
	return sty.Messages.CardMeta
}

// renderHandoffCard draws a hand-off as the desktop does: "to" and the
// recipients over what was sent, a report's task and state on the right.
func renderHandoffCard(sty *styles.Styles, h handoff, width int) string {
	box, inner := planBoxLayout(sty.Messages.Card, width)

	names := make([]string, len(h.to))
	for i, n := range h.to {
		names[i] = agentName(n)
	}
	left := sty.Messages.CardLabel.Render("to ") + strings.Join(names, sty.Messages.CardLabel.Render(", "))
	var right string
	switch {
	case h.taskID != "":
		right = sty.Messages.CardMeta.Render("task "+h.taskID+" · "+h.taskTitle+" · ") +
			taskStateStyle(sty, h.taskState).Render(h.taskState)
	case h.subOK && h.sub != "":
		right = sty.Messages.CardMeta.Render("sub-thread · " + h.sub)
	case h.subOK:
		right = sty.Messages.CardMeta.Render("sub-thread")
	}
	lines := []string{cardHeader(left, right, inner)}

	if h.body != "" {
		renderer := common.MarkdownRenderer(sty, inner)
		mu := common.LockMarkdownRenderer(renderer)
		mu.Lock()
		body, err := renderer.Render(h.body)
		mu.Unlock()
		if err != nil {
			body = h.body
		}
		lines = append(lines, strings.Trim(body, "\n"))
	}
	return renderPlanBox(box, strings.Join(lines, "\n"), width, true)
}

// SubagentToolMessageItem is a call that starts a subagent: a CLI's own
// (Claude Code's Task or Agent) or Deck's (the agent.spawn op of the deck
// tool). It is drawn as the desktop's subagent card.
type SubagentToolMessageItem struct {
	*baseToolMessageItem
}

var _ ToolMessageItem = (*SubagentToolMessageItem)(nil)

// subagentCall is what a subagent call says about the subagent: its kind
// when Deck starts one, else the CLI's subagent type, and its task.
type subagentCall struct {
	kind, kindLabel, task string
}

// subagentOf reads a subagent call; ok is false for any other call.
func subagentOf(tc message.ToolCall) (subagentCall, bool) {
	switch tc.Name {
	case "Task", "Agent":
		var in struct {
			Description  string `json:"description"`
			SubagentType string `json:"subagent_type"`
			SubagentAlt  string `json:"subagentType"`
			Prompt       string `json:"prompt"`
		}
		_ = json.Unmarshal([]byte(tc.Input), &in)
		return subagentCall{kindLabel: cmp.Or(in.SubagentType, in.SubagentAlt, "subagent"), task: cmp.Or(in.Description, firstLineOf(in.Prompt))}, true
	case "deck":
		var in struct {
			Op   string `json:"op"`
			Args struct {
				Kind  string `json:"kind"`
				Title string `json:"title"`
				Brief string `json:"brief"`
			} `json:"args"`
		}
		if json.Unmarshal([]byte(tc.Input), &in) != nil || in.Op != "agent.spawn" {
			return subagentCall{}, false
		}
		return subagentCall{kind: in.Args.Kind, kindLabel: in.Args.Kind, task: cmp.Or(in.Args.Title, firstLineOf(in.Args.Brief))}, true
	}
	return subagentCall{}, false
}

// NewSubagentToolMessageItem creates a new [SubagentToolMessageItem].
func NewSubagentToolMessageItem(
	sty *styles.Styles,
	toolCall message.ToolCall,
	result *message.ToolResult,
	canceled bool,
) ToolMessageItem {
	return &SubagentToolMessageItem{newBaseToolMessageItem(sty, toolCall, result, &SubagentToolRenderContext{}, canceled)}
}

// SubagentToolRenderContext renders a subagent call as a card: "↳", the
// subagent and its task, its state on the right, and the start of what it
// reported under them.
type SubagentToolRenderContext struct{}

// RenderTool implements the [ToolRenderer] interface.
func (s *SubagentToolRenderContext) RenderTool(sty *styles.Styles, width int, opts *ToolRenderOpts) string {
	call, _ := subagentOf(opts.ToolCall)

	who := sty.Messages.CardMeta.Render(call.kindLabel)
	if call.kind != "" {
		who = agentName(call.kind)
	}
	left := sty.Messages.CardLabel.Render("↳ ") + who
	if call.task != "" {
		left += "  " + sty.Messages.CardTitle.Render(call.task)
	}

	var state string
	switch {
	case opts.IsPending(), opts.Status == ToolStatusRunning, opts.Status == ToolStatusAwaitingPermission:
		state = sty.Messages.CardRunning.Render("running")
	case opts.Status == ToolStatusError:
		state = sty.Messages.CardFailed.Render("failed")
	case opts.IsCanceled():
		state = sty.Messages.CardLabel.Render("stopped")
	case call.kind != "":
		state = sty.Messages.CardMeta.Render("started")
	default:
		state = sty.Messages.CardDone.Render("done")
	}
	if opts.Compact {
		return cardHeader(left, state, width)
	}

	box, inner := planBoxLayout(sty.Messages.Card, width)
	lines := []string{cardHeader(left, state, inner)}
	if opts.HasResult() && opts.Result.Content != "" {
		report := firstLineOf(opts.Result.Content)
		lines = append(lines, "  "+sty.Messages.CardMeta.Render(ansi.Truncate(report, max(1, inner-2), "…")))
	}
	return renderPlanBox(box, strings.Join(lines, "\n"), width, true)
}

// firstLineOf is the first line of s with words on it.
func firstLineOf(s string) string {
	for l := range strings.SplitSeq(s, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			return l
		}
	}
	return ""
}
