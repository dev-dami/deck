package opencode

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"time"

	"github.com/dev-dami/deck/internal/deck/agent"
	"github.com/dev-dami/deck/internal/deck/approve"
)

// translator turns session/update notifications into Deck events. Message and
// thought chunks stream as deltas; when the stream moves on (another message,
// a tool call, the end of the turn) the whole text is sent once more, final.
type translator struct {
	agentID, dir string

	text, thought     strings.Builder
	textID, thoughtID string

	tools map[string]*toolState

	ctxUsed, ctxSize int
	cost, costBase   float64 // the session's cost so far, and at the turn's start
}

type toolState struct {
	call  toolCall
	diffs []agent.FileDiff
	done  bool
}

func newTranslator(agentID, dir string) *translator {
	return &translator{agentID: agentID, dir: dir, tools: map[string]*toolState{}}
}

func (t *translator) ev(k agent.Kind) agent.Event {
	return agent.Event{AgentID: t.agentID, Time: time.Now(), Kind: k}
}

// beginTurn marks where the turn's cost starts.
func (t *translator) beginTurn() { t.costBase = t.cost }

// update translates one session/update.
func (t *translator) update(u update) []agent.Event {
	switch u.SessionUpdate {
	case updAgentMessage:
		if u.Content == nil || u.Content.Text == "" {
			return nil
		}
		out := t.flushThought()
		if t.textID != u.MessageID {
			out = append(out, t.flushText()...)
			t.textID = u.MessageID
		}
		t.text.WriteString(u.Content.Text)
		e := t.ev(agent.KindAssistantText)
		e.Text, e.Delta = u.Content.Text, true
		return append(out, e)

	case updAgentThought:
		if u.Content == nil || u.Content.Text == "" {
			return nil
		}
		out := t.flushText()
		if t.thoughtID != u.MessageID {
			out = append(out, t.flushThought()...)
			t.thoughtID = u.MessageID
		}
		t.thought.WriteString(u.Content.Text)
		e := t.ev(agent.KindReasoning)
		e.Text, e.Delta = u.Content.Text, true
		return append(out, e)

	case updToolCall, updToolCallUpdate:
		out := t.flush()
		return append(out, t.tool(u.toolCall)...)

	case updUsage:
		t.ctxUsed, t.ctxSize = u.Used, u.Size
		if u.Cost != nil && (u.Cost.Currency == "" || u.Cost.Currency == "USD") {
			t.cost = u.Cost.Amount
		}
		e := t.ev(agent.KindUsage)
		e.Usage = t.usage()
		return []agent.Event{e}
	}
	return nil
}

func (t *translator) usage() *agent.Usage {
	return &agent.Usage{CostUSD: max(0, t.cost-t.costBase), ContextTokens: t.ctxUsed, ContextWindow: t.ctxSize}
}

// flush ends both streams.
func (t *translator) flush() []agent.Event {
	return append(t.flushThought(), t.flushText()...)
}

func (t *translator) flushText() []agent.Event {
	s := t.text.String()
	t.text.Reset()
	t.textID = ""
	if strings.TrimSpace(s) == "" {
		return nil
	}
	e := t.ev(agent.KindAssistantText)
	e.Text = s
	return []agent.Event{e}
}

func (t *translator) flushThought() []agent.Event {
	s := t.thought.String()
	t.thought.Reset()
	t.thoughtID = ""
	if strings.TrimSpace(s) == "" {
		return nil
	}
	e := t.ev(agent.KindReasoning)
	e.Text = s
	return []agent.Event{e}
}

// merge folds a tool call or update into what is known about it.
func (t *translator) merge(c toolCall) *toolState {
	st := t.tools[c.ToolCallID]
	if st == nil {
		st = &toolState{call: toolCall{ToolCallID: c.ToolCallID}}
		t.tools[c.ToolCallID] = st
	}
	m := &st.call
	if c.Name != "" {
		m.Name = c.Name
	}
	if c.Title != "" {
		m.Title = c.Title
	}
	if c.Kind != "" {
		m.Kind = c.Kind
	}
	if c.Status != "" {
		m.Status = c.Status
	}
	if len(c.Locations) > 0 {
		m.Locations = c.Locations
	}
	if len(c.RawInput) > 0 && string(c.RawInput) != "{}" && string(c.RawInput) != "null" {
		m.RawInput = c.RawInput
	}
	if len(c.Content) > 0 {
		m.Content = c.Content
	}
	for _, d := range diffsOf(c) {
		st.diffs = replaceDiff(st.diffs, d)
	}
	return st
}

func replaceDiff(ds []agent.FileDiff, d agent.FileDiff) []agent.FileDiff {
	for i := range ds {
		if ds[i].Path == d.Path {
			ds[i] = d
			return ds
		}
	}
	return append(ds, d)
}

func (t *translator) tool(c toolCall) []agent.Event {
	if c.ToolCallID == "" {
		return nil
	}
	st := t.merge(c)
	if st.done {
		return nil
	}
	tool := t.agentTool(st.call)
	switch st.call.Status {
	case "completed", "failed":
		st.done = true
		var out []agent.Event
		if st.call.Status == "completed" {
			diffs := st.diffs
			if len(diffs) == 0 {
				diffs = t.inputDiffs(st.call)
			}
			for i := range diffs {
				e := t.ev(agent.KindFileChange)
				e.Diff = &diffs[i]
				out = append(out, e)
			}
			tool.Status = agent.ToolOK
		} else {
			tool.Status = agent.ToolError
		}
		tool.Output = outputOf(st.call)
		e := t.ev(agent.KindToolResult)
		e.Tool = &tool
		// Keep only a marker, so a late update is still ignored, and let the
		// call's input, output and diffs go: a long session runs many tools.
		t.tools[c.ToolCallID] = &toolState{call: toolCall{ToolCallID: c.ToolCallID}, done: true}
		return append(out, e)
	}
	tool.Status = agent.ToolRunning
	e := t.ev(agent.KindToolCall)
	e.Tool = &tool
	return []agent.Event{e}
}

// toolNames maps OpenCode's tool names to the names Deck shows; Deck's own MCP
// tools (registered as server "deck") are mapped by deckTool.
var toolNames = map[string]string{
	"bash": "Bash", "shell": "Bash", "edit": "Edit", "write": "Write", "patch": "Edit", "apply_patch": "Edit",
	"read": "Read", "grep": "Grep", "glob": "Glob", "list": "LS", "webfetch": "WebFetch", "websearch": "WebSearch",
	"todowrite": "TodoWrite", "todoread": "TodoRead", "task": "Task",
}

var kindNames = map[string]string{
	"execute": "Bash", "edit": "Edit", "delete": "Edit", "move": "Edit", "read": "Read", "search": "Grep", "fetch": "WebFetch",
}

// deckToolPrefixes are how OpenCode may name the tools of Deck's MCP server
// (registered as "deck"), longest first.
var deckToolPrefixes = []string{"mcp__deck__", "deck__", "deck_"}

// deckTool maps a call of Deck's own search or execute tool to its display
// name and title (a thread.send is a "message", a thread.read a "read").
// OpenCode's code mode runs a script under its own bare "execute" tool; a
// script that calls Deck's execute with exactly one of the thread ops is
// shown as that op, and one that only searches Deck as "deck".
func deckTool(c toolCall) (name, title string, ok bool) {
	n := strings.ToLower(c.Name)
	for _, p := range deckToolPrefixes {
		if bare, found := strings.CutPrefix(n, p); found {
			return agent.DeckToolDisplay(bare, c.RawInput)
		}
	}
	if n != "execute" { // not OpenCode's code mode either
		return "", "", false
	}
	var code struct {
		Code string `json:"code"`
	}
	if json.Unmarshal(c.RawInput, &code) != nil || code.Code == "" {
		return "", "", false
	}
	in := code.Code
	callsExec := strings.Contains(in, "deck.execute") || strings.Contains(in, "deck_execute")
	callsSearch := strings.Contains(in, "deck.search") || strings.Contains(in, "deck_search")
	send, read := strings.Contains(in, agent.OpThreadSend), strings.Contains(in, agent.OpThreadRead)
	switch {
	case callsExec && send && !read:
		return "message", "", true
	case callsExec && read && !send:
		return "read", "shared thread", true
	case callsSearch && !callsExec:
		return "deck", "search", true
	}
	return "", "", false
}

func toolName(c toolCall) string {
	if name, _, ok := deckTool(c); ok {
		return name
	}
	n := strings.ToLower(c.Name)
	if v, ok := toolNames[n]; ok {
		return v
	}
	if c.Name != "" {
		return c.Name
	}
	if v, ok := kindNames[c.Kind]; ok {
		return v
	}
	return "Tool"
}

func (t *translator) agentTool(c toolCall) agent.Tool {
	if name, title, ok := deckTool(c); ok {
		if title == "" {
			title = t.title(c)
		}
		return agent.Tool{ID: c.ToolCallID, Name: name, Title: title, Input: c.RawInput}
	}
	return agent.Tool{ID: c.ToolCallID, Name: toolName(c), Title: t.title(c), Input: c.RawInput}
}

// rawInput is the input fields Deck reads for titles and diffs.
type rawInput struct {
	Command   string `json:"command"`
	Path      string `json:"path"`
	FilePath  string `json:"filePath"`
	Content   string `json:"content"`
	OldString string `json:"oldString"`
	NewString string `json:"newString"`
	Pattern   string `json:"pattern"`
	URL       string `json:"url"`
}

func inputOf(c toolCall) rawInput {
	var in rawInput
	_ = json.Unmarshal(c.RawInput, &in)
	return in
}

// title is the tool's one-line summary: the command, the file, or what
// OpenCode calls it.
func (t *translator) title(c toolCall) string {
	in := inputOf(c)
	switch {
	case in.Command != "":
		return in.Command
	case in.FilePath != "":
		return t.rel(in.FilePath)
	case in.Path != "" && c.Kind != "execute":
		return t.rel(in.Path)
	case in.Pattern != "":
		return in.Pattern
	case in.URL != "":
		return in.URL
	}
	if c.Kind != "execute" && len(c.Locations) > 0 && c.Locations[0].Path != "" {
		return t.rel(c.Locations[0].Path)
	}
	if c.Title != "" && !strings.EqualFold(c.Title, c.Name) {
		return c.Title
	}
	return ""
}

// rel shows a path relative to the thread's folder when it is inside it.
func (t *translator) rel(p string) string {
	if t.dir == "" || !filepath.IsAbs(p) {
		return filepath.ToSlash(p)
	}
	if r, err := filepath.Rel(t.dir, p); err == nil && !strings.HasPrefix(r, "..") {
		return filepath.ToSlash(r)
	}
	return filepath.ToSlash(p)
}

func outputOf(c toolCall) string {
	var parts []string
	for _, it := range c.Content {
		if it.Type == "content" && it.Content != nil && it.Content.Text != "" {
			parts = append(parts, it.Content.Text)
		}
	}
	return strings.TrimRight(strings.Join(parts, "\n"), "\n")
}

// diffsOf is the diffs a tool call carries.
func diffsOf(c toolCall) []agent.FileDiff {
	var out []agent.FileDiff
	for _, it := range c.Content {
		if it.Type != "diff" || it.Path == "" {
			continue
		}
		old := ""
		if it.OldText != nil {
			old = *it.OldText
		}
		if d := approve.BuildEditDiff(it.Path, old, it.NewText); d != nil {
			out = append(out, *d)
		}
	}
	return out
}

// inputDiffs is the diff an edit's input describes, when no diff came with it.
func (t *translator) inputDiffs(c toolCall) []agent.FileDiff {
	in := inputOf(c)
	path := in.FilePath
	if path == "" {
		path = in.Path
	}
	if path == "" || (in.OldString == "" && in.NewString == "") {
		return nil
	}
	if d := approve.BuildEditDiff(path, in.OldString, in.NewString); d != nil {
		return []agent.FileDiff{*d}
	}
	return nil
}

// diffFor is the diff known for a tool call, for its approval.
func (t *translator) diffFor(c toolCall) *agent.FileDiff {
	st := t.merge(c)
	if len(st.diffs) > 0 {
		d := st.diffs[0]
		return &d
	}
	if ds := t.inputDiffs(st.call); len(ds) > 0 {
		return &ds[0]
	}
	return nil
}
