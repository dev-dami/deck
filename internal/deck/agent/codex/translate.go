package codex

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/dev-dami/deck/internal/deck/agent"
)

// itemState is what the translator remembers about an in-flight thread item.
type itemState struct {
	typ     string
	title   string
	input   json.RawMessage
	out     strings.Builder // command output deltas / reasoning deltas
	text    strings.Builder // agent message deltas
	changes []fileUpdateChange
}

// translator turns codex notifications into agent.Events. It is stateful and
// must be driven from a single goroutine.
type translator struct {
	agentID string
	cwd     string
	now     func() time.Time

	items map[string]*itemState

	// subs are the agents codex spawned with its collab tools, by the id of
	// the spawn call, and child their threads (collab.go).
	subs  map[string]*agent.Subagent
	child map[string]string

	// usage accounting: the protocol reports thread totals; Deck wants
	// per-turn cumulative numbers.
	lastTotal tokenBreakdown
	baseTotal tokenBreakdown

	// limits is the plan's windows (limits.go). Unlike the rest of the
	// translator it is safe for concurrent use.
	limits *limitState

	// onTurn is called with the active turn id ("" when none).
	onTurn func(id string)
	// onTurnDone is called with the id of each turn that completed.
	onTurnDone func(id string)
}

func newTranslator(agentID, cwd string) *translator {
	return &translator{
		agentID: agentID,
		cwd:     cwd,
		now:     time.Now,
		items:   map[string]*itemState{},
		subs:    map[string]*agent.Subagent{},
		child:   map[string]string{},
		limits:  &limitState{},
	}
}

// rawLine yields the protocol line of the message being translated. It is
// called only for an event that keeps its line (agent.KeepsRaw: errors and
// kinds Deck does not know), so the deltas that are most of the stream are
// never encoded or copied for it.
type rawLine func() json.RawMessage

func (t *translator) ev(kind agent.Kind, raw rawLine) agent.Event {
	e := agent.Event{AgentID: t.agentID, Time: t.now(), Kind: kind}
	if raw != nil && agent.KeepsRaw(kind) {
		e.Raw = raw()
	}
	return e
}

// sessionStarted builds the KindSessionStarted event for a thread/start or
// thread/resume response.
func (t *translator) sessionStarted(threadID, model string) agent.Event {
	e := t.ev(agent.KindSessionStarted, nil)
	e.SessionID = threadID
	e.Text = model
	return e
}

func (t *translator) item(id, typ string) *itemState {
	st := t.items[id]
	if st == nil {
		st = &itemState{typ: typ}
		t.items[id] = st
	}
	return st
}

// notification translates one server notification. Unknown or uninteresting
// methods yield no events.
func (t *translator) notification(method string, params json.RawMessage, raw rawLine) []agent.Event {
	switch method {
	case noteTurnStarted:
		var n turnNotification
		if json.Unmarshal(params, &n) != nil || t.childOf(n.ThreadID) != "" {
			return nil
		}
		t.baseTotal = t.lastTotal
		if t.onTurn != nil {
			t.onTurn(n.Turn.ID)
		}
		return nil

	case noteTurnCompleted:
		var n turnNotification
		if json.Unmarshal(params, &n) != nil || t.childOf(n.ThreadID) != "" {
			return nil // a spawned agent's turn is not codex's
		}
		if t.onTurnDone != nil {
			t.onTurnDone(n.Turn.ID)
		}
		if t.onTurn != nil {
			t.onTurn("")
		}
		var out []agent.Event
		if n.Turn.Status == "failed" {
			e := t.ev(agent.KindError, raw)
			e.Text = "turn failed"
			if n.Turn.Error != nil && n.Turn.Error.Message != "" {
				e.Text = n.Turn.Error.Message
			}
			if n.Turn.Error != nil {
				e.LimitHit = t.limitHit(*n.Turn.Error)
			}
			out = append(out, e)
		}
		done := t.ev(agent.KindTurnDone, raw)
		if n.Turn.Status != "completed" {
			done.Text = n.Turn.Status // "interrupted" | "failed"
		}
		return append(out, done)

	case noteItemStarted, noteItemCompleted:
		var n itemNotification
		if json.Unmarshal(params, &n) != nil {
			return nil
		}
		var evs []agent.Event
		if method == noteItemStarted {
			evs = t.itemStarted(n.Item, raw)
		} else {
			evs = t.itemCompleted(n.Item, raw)
		}
		if sub := t.childOf(n.ThreadID); sub != "" {
			// An item of a spawned agent's thread: its work, not codex's
			// (its prompt is the spawn's).
			kept := evs[:0]
			for _, e := range evs {
				switch e.Kind {
				case agent.KindUserText:
					continue
				case agent.KindSubagent:
				default:
					e.Sub = sub
				}
				kept = append(kept, e)
			}
			evs = kept
		}
		return evs

	case noteAgentDelta:
		var d deltaNotification
		if json.Unmarshal(params, &d) != nil || d.Delta == "" {
			return nil
		}
		d.Delta = fixMojibake(d.Delta)
		t.item(d.ItemID, "agentMessage").text.WriteString(d.Delta)
		if t.childOf(d.ThreadID) != "" {
			return nil // a spawned agent's: its item's end carries the text
		}
		e := t.ev(agent.KindAssistantText, raw)
		e.Text, e.Delta = d.Delta, true
		return []agent.Event{e}

	case noteReasoningText, noteReasoningSummary:
		var d deltaNotification
		if json.Unmarshal(params, &d) != nil || d.Delta == "" {
			return nil
		}
		d.Delta = fixMojibake(d.Delta)
		t.item(d.ItemID, "reasoning").out.WriteString(d.Delta)
		if t.childOf(d.ThreadID) != "" {
			return nil
		}
		e := t.ev(agent.KindReasoning, raw)
		e.Text, e.Delta = d.Delta, true
		return []agent.Event{e}

	case noteCommandOutput:
		var d deltaNotification
		if json.Unmarshal(params, &d) != nil {
			return nil
		}
		t.item(d.ItemID, "commandExecution").out.WriteString(d.Delta)
		return nil

	case noteFilePatchUpdated:
		var n patchUpdatedNotification
		if json.Unmarshal(params, &n) != nil {
			return nil
		}
		t.item(n.ItemID, "fileChange").changes = n.Changes
		return nil

	case noteTokenUsage:
		var n tokenUsageNotification
		if json.Unmarshal(params, &n) != nil {
			return nil
		}
		tot := n.TokenUsage.Total
		t.lastTotal = tot
		e := t.ev(agent.KindUsage, raw)
		e.Usage = &agent.Usage{
			InputTokens:      nonNeg(tot.InputTokens - t.baseTotal.InputTokens),
			OutputTokens:     nonNeg(tot.OutputTokens - t.baseTotal.OutputTokens),
			CachedTokens:     nonNeg(tot.CachedInputTokens - t.baseTotal.CachedInputTokens),
			CacheWriteTokens: nonNeg(tot.CacheWriteInputTokens - t.baseTotal.CacheWriteInputTokens),
			ReasoningTokens:  nonNeg(tot.ReasoningOutputTokens - t.baseTotal.ReasoningOutputTokens),
		}
		// Context fullness is the latest model request (`last`), not the
		// thread's cumulative `total`. Like codex itself, reasoning tokens are
		// excluded: they are not carried into the next request.
		last := n.TokenUsage.Last
		ctxTok := last.TotalTokens - last.ReasoningOutputTokens
		if last.TotalTokens == 0 {
			ctxTok = last.InputTokens + last.OutputTokens - last.ReasoningOutputTokens
		}
		e.Usage.ContextTokens = nonNeg(ctxTok)
		e.Usage.ContextSource = agent.ContextCLI
		if w := n.TokenUsage.ModelContextWindow; w != nil {
			e.Usage.ContextWindow = nonNeg(*w)
		}
		return []agent.Event{e}

	case noteCompacted:
		// The thread's history was summarised. Its params are not decoded:
		// the context use after it comes with the next token usage update.
		return []agent.Event{t.ev(agent.KindCompacted, raw)}

	case noteRateLimits:
		var n rateLimitsNotification
		if json.Unmarshal(params, &n) != nil || !t.limits.merge(n.RateLimits) {
			return nil
		}
		return []agent.Event{t.limitsEvent()}

	case noteError:
		var n errorNotification
		if json.Unmarshal(params, &n) != nil {
			return nil
		}
		e := t.ev(agent.KindError, raw)
		e.Text = n.Error.Message
		if n.WillRetry {
			e.Text += " (retrying)"
		} else {
			e.LimitHit = t.limitHit(n.Error)
		}
		return []agent.Event{e}
	}
	return nil
}

func nonNeg(n int) int {
	if n < 0 {
		return 0
	}
	return n
}

func (t *translator) itemStarted(it threadItem, raw rawLine) []agent.Event {
	switch it.Type {
	case "commandExecution":
		st := t.item(it.ID, it.Type)
		st.title = commandTitle(it)
		st.input, _ = json.Marshal(map[string]string{"command": it.Command, "cwd": it.Cwd})
		e := t.ev(agent.KindToolCall, raw)
		e.Tool = &agent.Tool{ID: it.ID, Name: "Bash", Title: st.title, Input: st.input, Status: agent.ToolRunning}
		return []agent.Event{e}

	case "fileChange":
		st := t.item(it.ID, it.Type)
		st.changes = it.Changes
		st.title = t.changesTitle(it.Changes)
		e := t.ev(agent.KindToolCall, raw)
		e.Tool = &agent.Tool{ID: it.ID, Name: "Edit", Title: st.title, Status: agent.ToolRunning}
		return []agent.Event{e}

	case "mcpToolCall":
		st := t.item(it.ID, it.Type)
		st.title = it.Server + "/" + it.Tool
		st.input = it.Arguments
		name := it.Tool
		if n, title, ok := deckToolDisplay(it); ok {
			name, st.title = n, title
		}
		e := t.ev(agent.KindToolCall, raw)
		e.Tool = &agent.Tool{ID: it.ID, Name: name, Title: st.title, Input: it.Arguments, Status: agent.ToolRunning}
		return []agent.Event{e}

	case "webSearch":
		st := t.item(it.ID, it.Type)
		st.title = it.Query
		e := t.ev(agent.KindToolCall, raw)
		e.Tool = &agent.Tool{ID: it.ID, Name: "WebSearch", Title: it.Query, Status: agent.ToolRunning}
		return []agent.Event{e}

	case "dynamicToolCall":
		st := t.item(it.ID, it.Type)
		st.title = it.Tool
		st.input = it.Arguments
		e := t.ev(agent.KindToolCall, raw)
		e.Tool = &agent.Tool{ID: it.ID, Name: it.Tool, Title: it.Tool, Input: it.Arguments, Status: agent.ToolRunning}
		return []agent.Event{e}

	case "agentMessage", "reasoning":
		t.item(it.ID, it.Type)

	case "collabAgentToolCall":
		return t.collabStarted(it, raw)
	}
	return nil
}

func (t *translator) itemCompleted(it threadItem, raw rawLine) []agent.Event {
	st := t.items[it.ID]
	delete(t.items, it.ID)
	if st == nil {
		st = &itemState{typ: it.Type}
	}
	switch it.Type {
	case "collabAgentToolCall":
		return t.collabCompleted(it, raw)

	case "userMessage":
		var parts []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		_ = json.Unmarshal(it.Content, &parts)
		var sb strings.Builder
		for _, p := range parts {
			if p.Type == "text" {
				sb.WriteString(p.Text)
			}
		}
		text := stripPlanNote(sb.String())
		if text == "" {
			return nil
		}
		e := t.ev(agent.KindUserText, raw)
		e.Text = text
		return []agent.Event{e}

	case "agentMessage":
		text := it.Text
		if text == "" {
			text = st.text.String()
		}
		text = fixMojibake(text)
		if text == "" {
			return nil
		}
		e := t.ev(agent.KindAssistantText, raw)
		e.Text = text
		return []agent.Event{e}

	case "reasoning":
		text := strings.Join(it.Summary, "\n\n")
		if text == "" {
			var content []string
			_ = json.Unmarshal(it.Content, &content)
			text = strings.Join(content, "\n\n")
		}
		if text == "" {
			text = st.out.String()
		}
		text = fixMojibake(text)
		if text == "" {
			return nil
		}
		e := t.ev(agent.KindReasoning, raw)
		e.Text = text
		return []agent.Event{e}

	case "commandExecution":
		title := st.title
		if title == "" {
			title = commandTitle(it)
		}
		out := st.out.String()
		if it.AggregatedOutput != nil {
			out = *it.AggregatedOutput
		}
		status := agent.ToolOK
		if it.Status != "completed" || (it.ExitCode != nil && *it.ExitCode != 0) {
			status = agent.ToolError
		}
		input := st.input
		if input == nil {
			input, _ = json.Marshal(map[string]string{"command": it.Command, "cwd": it.Cwd})
		}
		e := t.ev(agent.KindToolResult, raw)
		e.Tool = &agent.Tool{ID: it.ID, Name: "Bash", Title: title, Input: input, Output: out, Status: status}
		return []agent.Event{e}

	case "fileChange":
		changes := it.Changes
		if len(changes) == 0 {
			changes = st.changes
		}
		var out []agent.Event
		status := agent.ToolOK
		if it.Status == "completed" {
			for _, c := range changes {
				d := t.fileDiff(c)
				e := t.ev(agent.KindFileChange, raw)
				e.Diff = &d
				out = append(out, e)
			}
		} else {
			status = agent.ToolError
		}
		res := t.ev(agent.KindToolResult, raw)
		res.Tool = &agent.Tool{ID: it.ID, Name: "Edit", Title: t.changesTitle(changes), Status: status}
		if status == agent.ToolError {
			res.Tool.Output = it.Status // failed | declined
		}
		return append(out, res)

	case "mcpToolCall", "dynamicToolCall", "webSearch":
		status := agent.ToolOK
		if it.Status == "failed" || it.Status == "declined" {
			status = agent.ToolError
		}
		name := st.title
		switch it.Type {
		case "mcpToolCall":
			name = it.Tool
		case "dynamicToolCall":
			name = it.Tool
		case "webSearch":
			name = "WebSearch"
		}
		out := it.resultText()
		if n, title, ok := deckToolDisplay(it); ok {
			name, out = n, deckResultText(it)
			if st.title == "" || st.title == it.Server+"/"+it.Tool {
				st.title = title
			}
			if st.input == nil {
				st.input = it.Arguments
			}
		}
		e := t.ev(agent.KindToolResult, raw)
		e.Tool = &agent.Tool{ID: it.ID, Name: name, Title: st.title, Input: st.input, Output: out, Status: status}
		return []agent.Event{e}
	}
	return nil
}

// resultText is a best-effort one-string rendering of an mcp/dynamic tool result.
func (it threadItem) resultText() string {
	switch {
	case len(it.Error) > 0 && string(it.Error) != "null":
		return string(it.Error)
	case len(it.Result) > 0 && string(it.Result) != "null":
		return string(it.Result)
	}
	return ""
}

// commandTitle picks the one-line description of a command. Codex wraps shell
// commands (powershell.exe -Command '...'); its parsed commandActions carry the
// inner command, which reads far better.
func commandTitle(it threadItem) string {
	var inner []string
	for _, a := range it.CommandActions {
		if strings.TrimSpace(a.Command) == "" {
			inner = nil
			break
		}
		inner = append(inner, a.Command)
	}
	if len(inner) > 0 {
		return strings.Join(inner, " ; ")
	}
	return it.Command
}

func (t *translator) rel(p string) string {
	if t.cwd == "" || !filepath.IsAbs(p) {
		return p
	}
	r, err := filepath.Rel(t.cwd, p)
	if err != nil || r == ".." || strings.HasPrefix(r, ".."+string(filepath.Separator)) {
		return p
	}
	return r
}

func (t *translator) changesTitle(cs []fileUpdateChange) string {
	paths := make([]string, 0, len(cs))
	for _, c := range cs {
		paths = append(paths, t.rel(c.Path))
	}
	return strings.Join(paths, ", ")
}

// fileDiff converts one protocol change to an agent.FileDiff.
func (t *translator) fileDiff(c fileUpdateChange) agent.FileDiff {
	var fd agent.FileDiff
	if parsed := agent.ParseUnified(c.Diff); len(parsed) > 0 && len(parsed[0].Hunks) > 0 {
		fd = parsed[0]
		for _, extra := range parsed[1:] { // defensive: merge stray extra files
			fd.Hunks = append(fd.Hunks, extra.Hunks...)
			fd.Added += extra.Added
			fd.Removed += extra.Removed
		}
	} else if c.Diff != "" && (c.Kind.Type == "add" || c.Kind.Type == "delete") {
		// For adds/deletes codex may give the file content rather than a diff.
		op := agent.OpAdd
		if c.Kind.Type == "delete" {
			op = agent.OpDel
		}
		lines := strings.Split(strings.ReplaceAll(c.Diff, "\r\n", "\n"), "\n")
		if n := len(lines); n > 0 && lines[n-1] == "" {
			lines = lines[:n-1]
		}
		h := agent.Hunk{}
		for _, l := range lines {
			h.Lines = append(h.Lines, agent.DiffLine{Op: op, Text: l})
		}
		if op == agent.OpAdd {
			h.NewStart, h.NewLines = 1, len(lines)
			fd.Added = len(lines)
		} else {
			h.OldStart, h.OldLines = 1, len(lines)
			fd.Removed = len(lines)
		}
		fd.Hunks = []agent.Hunk{h}
	}
	fd.OldPath = ""
	fd.Path = t.rel(c.Path)
	switch c.Kind.Type {
	case "add":
		fd.New = true
	case "delete":
		fd.Deleted = true
	case "update":
		if c.Kind.MovePath != "" {
			fd.OldPath = t.rel(c.Path)
			fd.Path = t.rel(c.Kind.MovePath)
		}
	}
	return fd
}

// fileDiffsFor returns the diffs of a (possibly in-flight) fileChange item.
func (t *translator) fileDiffsFor(itemID string) []agent.FileDiff {
	st := t.items[itemID]
	if st == nil {
		return nil
	}
	out := make([]agent.FileDiff, 0, len(st.changes))
	for _, c := range st.changes {
		out = append(out, t.fileDiff(c))
	}
	return out
}

// cp1252 maps the Windows-1252 specials back to their byte values.
var cp1252 = map[rune]byte{
	0x20AC: 0x80, 0x201A: 0x82, 0x0192: 0x83, 0x201E: 0x84, 0x2026: 0x85, 0x2020: 0x86,
	0x2021: 0x87, 0x02C6: 0x88, 0x2030: 0x89, 0x0160: 0x8A, 0x2039: 0x8B, 0x0152: 0x8C,
	0x017D: 0x8E, 0x2018: 0x91, 0x2019: 0x92, 0x201C: 0x93, 0x201D: 0x94, 0x2022: 0x95,
	0x2013: 0x96, 0x2014: 0x97, 0x02DC: 0x98, 0x2122: 0x99, 0x0161: 0x9A, 0x203A: 0x9B,
	0x0153: 0x9C, 0x017E: 0x9E, 0x0178: 0x9F,
}

// fixMojibake repairs UTF-8 text that codex on Windows sometimes emits having
// been decoded as Windows-1252 and re-encoded (for example the right single
// quote U+2019 arriving as "\u00e2\u20ac\u2122"). It only rewrites a string
// when every rune maps back to a byte and the resulting bytes form valid
// UTF-8, so well-formed text is left untouched.
func fixMojibake(s string) string {
	ascii := true
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			ascii = false
			break
		}
	}
	if ascii {
		return s
	}
	buf := make([]byte, 0, len(s))
	for _, r := range s {
		switch {
		case r < 0x80:
			buf = append(buf, byte(r))
		case r < 0x100:
			buf = append(buf, byte(r))
		default:
			b, ok := cp1252[r]
			if !ok {
				return s
			}
			buf = append(buf, b)
		}
	}
	if !utf8.Valid(buf) || string(buf) == s {
		return s
	}
	return string(buf)
}
