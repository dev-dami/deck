package claude

import (
	"bytes"
	"encoding/json"
	"regexp"
	"strings"
	"time"

	"github.com/dev-dami/deck/internal/deck/agent"
	"github.com/dev-dami/deck/internal/deck/approve"
)

// toolInfo remembers a tool_use so its result can be matched later.
type toolInfo struct {
	name  string
	title string
	input json.RawMessage
}

// translator turns Claude Code stream-json lines into normalized events. It is
// stateful (it matches tool results to the tool calls that produced them) and not
// safe for concurrent use.
type translator struct {
	agentID string
	tools   map[string]toolInfo
	now     func() time.Time

	// Context-window tracking. ctx is the usage of the latest top-level
	// assistant message (what the next request will carry, plus its reply);
	// model is that message's model id; windows remembers the context window
	// the CLI reported per model (result line, modelUsage[model].contextWindow).
	ctx         ctxUsage
	model       string
	windows     map[string]int
	lastEmitted int // ContextTokens of the last mid-turn usage event (dedup)
}

// ctxUsage is the token usage of one assistant message.
type ctxUsage struct {
	input, cacheCreate, cacheRead, output int
}

func (c ctxUsage) total() int { return c.input + c.cacheCreate + c.cacheRead + c.output }

func newTranslator(agentID string) *translator {
	return &translator{agentID: agentID, tools: map[string]toolInfo{}, now: time.Now, windows: map[string]int{}}
}

// Default context windows by model family, used until the CLI has reported
// the real one in a result line's modelUsage.
const (
	defaultContextWindow = 200_000
	longContextWindow    = 1_000_000
)

var longContextRe = regexp.MustCompile(`(?i)\[1m\]|(^|[^a-z0-9])1m($|[^a-z0-9])`)

// fallbackWindow returns the assumed context window of a model id/alias, or 0
// for a model it does not recognise.
func fallbackWindow(model string) int {
	m := strings.ToLower(model)
	if m == "" {
		return 0
	}
	if longContextRe.MatchString(m) {
		return longContextWindow
	}
	for _, fam := range []string{"opus", "sonnet", "haiku", "fable"} {
		if strings.Contains(m, fam) {
			return defaultContextWindow
		}
	}
	return 0
}

// window is the context window for the current model: the CLI-reported value
// if known, else the family fallback, else 0.
func (t *translator) window() int {
	if w := t.windows[t.model]; w > 0 {
		return w
	}
	return fallbackWindow(t.model)
}

type streamLine struct {
	Type            string          `json:"type"`
	Subtype         string          `json:"subtype"`
	SessionID       string          `json:"session_id"`
	Model           string          `json:"model"` // system/init: the model this session runs
	ParentToolUseID *string         `json:"parent_tool_use_id"`
	Event           json.RawMessage `json:"event"`
	Message         json.RawMessage `json:"message"`
	ToolUseResult   json.RawMessage `json:"tool_use_result"`

	// result
	IsError      bool            `json:"is_error"`
	Result       string          `json:"result"`
	TotalCostUSD float64         `json:"total_cost_usd"`
	Usage        json.RawMessage `json:"usage"`
	ModelUsage   map[string]struct {
		ContextWindow int `json:"contextWindow"`
	} `json:"modelUsage"`
}

// wireUsage is the usage object of an assistant message / message_delta.
type wireUsage struct {
	InputTokens  int `json:"input_tokens"`
	CacheCreate  int `json:"cache_creation_input_tokens"`
	CacheRead    int `json:"cache_read_input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

type contentBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	Thinking  string          `json:"thinking"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	Content   json.RawMessage `json:"content"`
	IsError   bool            `json:"is_error"`
}

type messageBody struct {
	Content json.RawMessage `json:"content"`
	Model   string          `json:"model"`
	Usage   *wireUsage      `json:"usage"`
}

// Line translates one raw stdout line. Unknown or malformed lines yield nothing.
func (t *translator) Line(line []byte) []agent.Event {
	line = bytes.TrimSpace(line)
	if len(line) == 0 || line[0] != '{' {
		return nil
	}
	var sl streamLine
	if json.Unmarshal(line, &sl) != nil {
		return nil
	}
	raw := json.RawMessage(append([]byte(nil), line...))
	mk := func(k agent.Kind) agent.Event {
		return agent.Event{AgentID: t.agentID, Time: t.now(), Kind: k, Raw: raw}
	}

	switch sl.Type {
	case "system":
		if sl.Subtype == "init" && sl.SessionID != "" {
			ev := mk(agent.KindSessionStarted)
			ev.SessionID = sl.SessionID
			ev.Text = sl.Model // the model this session actually runs
			return []agent.Event{ev}
		}

	case "stream_event":
		if sl.ParentToolUseID != nil && *sl.ParentToolUseID != "" {
			return nil // sub-agent chatter
		}
		var se struct {
			Type  string     `json:"type"`
			Usage *wireUsage `json:"usage"`
			Delta struct {
				Type     string `json:"type"`
				Text     string `json:"text"`
				Thinking string `json:"thinking"`
			} `json:"delta"`
		}
		if json.Unmarshal(sl.Event, &se) != nil {
			return nil
		}
		if se.Type == "message_delta" && se.Usage != nil {
			// Final usage of the message being streamed (the assistant lines
			// carry the message_start usage, whose output_tokens is partial).
			t.noteUsage(*se.Usage, true)
			return t.contextEvent(mk)
		}
		if se.Type != "content_block_delta" {
			return nil
		}
		switch se.Delta.Type {
		case "text_delta":
			if se.Delta.Text == "" {
				return nil
			}
			ev := mk(agent.KindAssistantText)
			ev.Text, ev.Delta = se.Delta.Text, true
			return []agent.Event{ev}
		case "thinking_delta":
			if se.Delta.Thinking == "" {
				return nil
			}
			ev := mk(agent.KindReasoning)
			ev.Text, ev.Delta = se.Delta.Thinking, true
			return []agent.Event{ev}
		}

	case "assistant":
		return t.assistant(sl, mk)

	case "user":
		return t.user(sl, mk)

	case "result":
		var out []agent.Event
		if sl.IsError || strings.HasPrefix(sl.Subtype, "error") {
			ev := mk(agent.KindError)
			ev.Text = sl.Result
			if ev.Text == "" {
				ev.Text = sl.Subtype
			}
			out = append(out, ev)
		}
		var u wireUsage
		_ = json.Unmarshal(sl.Usage, &u)
		// modelUsage[<model>].contextWindow is the CLI's own figure.
		for m, mu := range sl.ModelUsage {
			if mu.ContextWindow > 0 {
				t.windows[m] = mu.ContextWindow
			}
		}
		if _, ok := t.windows[t.model]; !ok && t.model != "" && len(sl.ModelUsage) == 1 {
			// The assistant message names a different id than the modelUsage
			// key (alias vs. canonical): a single entry can only be this model.
			for _, mu := range sl.ModelUsage {
				if mu.ContextWindow > 0 {
					t.windows[t.model] = mu.ContextWindow
				}
			}
		}
		ue := mk(agent.KindUsage)
		ue.Usage = &agent.Usage{
			InputTokens: u.InputTokens, OutputTokens: u.OutputTokens, CachedTokens: u.CacheRead, CostUSD: sl.TotalCostUSD,
			ContextTokens: t.ctx.total(), ContextWindow: t.window(),
		}
		t.lastEmitted = t.ctx.total()
		out = append(out, ue, mk(agent.KindTurnDone))
		return out
	}
	return nil
}

func (t *translator) assistant(sl streamLine, mk func(agent.Kind) agent.Event) []agent.Event {
	if sl.ParentToolUseID != nil && *sl.ParentToolUseID != "" {
		return nil
	}
	var mb messageBody
	if json.Unmarshal(sl.Message, &mb) != nil {
		return nil
	}
	var blocks []contentBlock
	if json.Unmarshal(mb.Content, &blocks) != nil {
		return nil
	}
	var out []agent.Event
	if mb.Model != "" {
		t.model = mb.Model
	}
	if mb.Usage != nil {
		t.noteUsage(*mb.Usage, false)
	}
	for _, b := range blocks {
		switch b.Type {
		case "text":
			if b.Text == "" {
				continue
			}
			ev := mk(agent.KindAssistantText)
			ev.Text = b.Text
			out = append(out, ev)
		case "thinking":
			if b.Thinking == "" {
				continue
			}
			ev := mk(agent.KindReasoning)
			ev.Text = b.Thinking
			out = append(out, ev)
		case "tool_use":
			name, title := displayTool(b.Name, b.Input)
			t.tools[b.ID] = toolInfo{name: name, title: title, input: b.Input}
			ev := mk(agent.KindToolCall)
			ev.Tool = &agent.Tool{ID: b.ID, Name: name, Title: title, Input: b.Input, Status: agent.ToolRunning}
			out = append(out, ev)
		}
	}
	return append(out, t.contextEvent(mk)...)
}

// noteUsage records the usage of the latest top-level assistant message. A
// message_delta (final=true) carries the message's final output_tokens; an
// assistant line carries the message_start usage.
func (t *translator) noteUsage(u wireUsage, final bool) {
	if u.InputTokens+u.CacheCreate+u.CacheRead > 0 {
		t.ctx = ctxUsage{input: u.InputTokens, cacheCreate: u.CacheCreate, cacheRead: u.CacheRead, output: u.OutputTokens}
		return
	}
	if final && u.OutputTokens > 0 {
		t.ctx.output = u.OutputTokens
	}
}

// contextEvent returns a mid-turn KindUsage event when the context fill has
// changed since the last one, so the UI's context meter moves during a turn.
//
// Mid-turn usage events set ONLY ContextTokens and ContextWindow; every other
// field (InputTokens, OutputTokens, CachedTokens, CostUSD) is zero. The
// cumulative totals and cost arrive once, on the usage event that precedes
// TurnDone, so a consumer that accumulates or displays those must ignore usage
// events whose totals and CostUSD are all zero (treat them as "no change").
func (t *translator) contextEvent(mk func(agent.Kind) agent.Event) []agent.Event {
	n := t.ctx.total()
	if n == 0 || n == t.lastEmitted {
		return nil
	}
	t.lastEmitted = n
	ev := mk(agent.KindUsage)
	ev.Usage = &agent.Usage{ContextTokens: n, ContextWindow: t.window()}
	return []agent.Event{ev}
}

func (t *translator) user(sl streamLine, mk func(agent.Kind) agent.Event) []agent.Event {
	var mb messageBody
	if json.Unmarshal(sl.Message, &mb) != nil {
		return nil
	}
	var blocks []contentBlock
	if json.Unmarshal(mb.Content, &blocks) != nil {
		return nil // plain-string user content (echo of a prompt)
	}
	var out []agent.Event
	for _, b := range blocks {
		if b.Type != "tool_result" {
			continue
		}
		info := t.tools[b.ToolUseID]
		status := agent.ToolOK
		if b.IsError {
			status = agent.ToolError
		}
		ev := mk(agent.KindToolResult)
		ev.Tool = &agent.Tool{
			ID: b.ToolUseID, Name: info.name, Title: info.title, Input: info.input,
			Output: stringifyContent(b.Content), Status: status,
		}
		out = append(out, ev)

		if status == agent.ToolOK {
			if d := fileDiffFor(info, sl.ToolUseResult); d != nil {
				fe := mk(agent.KindFileChange)
				fe.Diff = d
				out = append(out, fe)
			}
		}
		delete(t.tools, b.ToolUseID)
	}
	return out
}

// fileDiffFor builds the diff for a successful Edit/Write/MultiEdit/NotebookEdit.
// toolUseResult is Claude Code's structured result when present; it lets Write
// distinguish creating a file from overwriting one.
func fileDiffFor(info toolInfo, toolUseResult json.RawMessage) *agent.FileDiff {
	var in struct {
		FilePath     string `json:"file_path"`
		NotebookPath string `json:"notebook_path"`
		OldString    string `json:"old_string"`
		NewString    string `json:"new_string"`
		Content      string `json:"content"`
		NewSource    string `json:"new_source"`
		Edits        []struct {
			OldString string `json:"old_string"`
			NewString string `json:"new_string"`
		} `json:"edits"`
	}
	if json.Unmarshal(info.input, &in) != nil {
		return nil
	}
	switch info.name {
	case "Edit":
		return approve.BuildEditDiff(in.FilePath, in.OldString, in.NewString)
	case "MultiEdit":
		d := &agent.FileDiff{Path: in.FilePath}
		for _, e := range in.Edits {
			ed := approve.BuildEditDiff(in.FilePath, e.OldString, e.NewString)
			d.Added += ed.Added
			d.Removed += ed.Removed
			d.Hunks = append(d.Hunks, ed.Hunks...)
		}
		return d
	case "Write":
		var tr struct {
			Type         string  `json:"type"`
			OriginalFile *string `json:"originalFile"`
		}
		_ = json.Unmarshal(toolUseResult, &tr)
		if tr.OriginalFile != nil && *tr.OriginalFile != "" {
			return approve.BuildEditDiff(in.FilePath, *tr.OriginalFile, in.Content)
		}
		d := approve.BuildEditDiff(in.FilePath, "", in.Content)
		d.New = true
		return d
	case "NotebookEdit":
		p := in.NotebookPath
		if p == "" {
			p = in.FilePath
		}
		d := approve.BuildEditDiff(p, "", in.NewSource)
		d.New = false
		return d
	}
	return nil
}

func stringifyContent(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &parts) == nil {
		var sb strings.Builder
		for i, p := range parts {
			if p.Type != "text" && p.Text == "" {
				continue
			}
			if i > 0 && sb.Len() > 0 {
				sb.WriteByte('\n')
			}
			sb.WriteString(p.Text)
		}
		return sb.String()
	}
	return string(raw)
}

// displayTool maps Claude's tool name to Deck's display name and title. Deck's
// own messaging tools become "message" ("to <to>: <text>") and "read".
func displayTool(name string, input json.RawMessage) (string, string) {
	switch name {
	case toolSendMessage:
		var in struct {
			To      string `json:"to"`
			Message string `json:"message"`
		}
		_ = json.Unmarshal(input, &in)
		text := strings.Join(strings.Fields(in.Message), " ")
		if r := []rune(text); len(r) > 60 {
			text = string(r[:60])
		}
		return "message", "to " + in.To + ": " + text
	case toolReadThread:
		return "read", "shared thread"
	}
	return name, toolTitle(name, input)
}

func toolTitle(name string, input json.RawMessage) string {
	var m map[string]any
	_ = json.Unmarshal(input, &m)
	str := func(k string) string {
		s, _ := m[k].(string)
		return s
	}
	var title string
	switch name {
	case "Bash", "PowerShell":
		title = str("command")
	case "Read", "Edit", "Write", "MultiEdit":
		title = str("file_path")
	case "NotebookEdit":
		title = str("notebook_path")
	case "Grep", "Glob":
		title = str("pattern")
	case "WebFetch":
		title = str("url")
	case "WebSearch":
		title = str("query")
	case "Task", "Agent":
		title = str("description")
	}
	if title == "" {
		title = compactJSON(input)
	}
	if i := strings.IndexByte(title, '\n'); i >= 0 {
		title = title[:i] + " ..."
	}
	if r := []rune(title); len(r) > 160 {
		title = string(r[:157]) + "..."
	}
	return title
}

func compactJSON(in json.RawMessage) string {
	var buf bytes.Buffer
	if json.Compact(&buf, in) != nil {
		return strings.TrimSpace(string(in))
	}
	s := buf.String()
	if s == "{}" || s == "null" {
		return ""
	}
	return s
}
