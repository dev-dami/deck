package claude

import (
	"bytes"
	"encoding/json"
	"regexp"
	"sort"
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

	// ttft is the first message's time to first token in the turn under
	// way, used when the result line does not carry one.
	ttft int

	// Plan limits (see limits.go): the windows known so far by id, the plan,
	// and whether the get_usage probe failed.
	limits       map[string]agent.LimitWindow
	plan         string
	limitsFailed bool
	// rejected is the window a rate_limit_event of this turn said is used
	// up (status "rejected"), until the turn's result line.
	rejected *agent.LimitHit

	// subs are the subagents started with the Task (Agent) tool that have
	// not reported back, by the id of the call that started them. Their
	// own lines carry that id as parent_tool_use_id.
	subs map[string]*agent.Subagent
}

// ctxUsage is the token usage of one assistant message.
type ctxUsage struct {
	input, cacheCreate, cacheRead, output int
}

func (c ctxUsage) total() int { return c.input + c.cacheCreate + c.cacheRead + c.output }

func newTranslator(agentID string) *translator {
	return &translator{
		agentID: agentID, tools: map[string]toolInfo{}, now: time.Now, windows: map[string]int{},
		limits: map[string]agent.LimitWindow{}, subs: map[string]*agent.Subagent{},
	}
}

// Default context windows by model family, used until the CLI has reported
// the real one in a result line's modelUsage.
const (
	defaultContextWindow = 200_000
	longContextWindow    = 1_000_000
)

var (
	longContextRe = regexp.MustCompile(`(?i)\[1m\]|(^|[^a-z0-9])1m($|[^a-z0-9])`)
	// familyRe finds the family and version of an id: claude-opus-4-6,
	// claude-sonnet-4-5-20250929 (a date is not a minor), or a bare alias.
	familyRe = regexp.MustCompile(`(opus|sonnet|haiku|fable|mythos)(?:[-.]?(\d+)(?:[-.](\d{1,2})\b)?)?`)
	// oldNamingRe is the claude-3-x-<family> naming of models before 4.
	oldNamingRe = regexp.MustCompile(`claude-[0-9]`)
)

// fallbackWindow returns the assumed context window of a model id/alias, or 0
// for a model it does not recognise. Every model from Opus and Sonnet 4.6,
// Haiku 5.5 and Fable on has a 1M window by default; older ones (Haiku 4.5,
// Sonnet 4.5, Opus 4.5 and before) have 200k. A bare alias (opus, sonnet,
// haiku, fable) is the current generation, so 1M.
func fallbackWindow(model string) int {
	m := strings.ToLower(model)
	if m == "" {
		return 0
	}
	if longContextRe.MatchString(m) {
		return longContextWindow
	}
	sub := familyRe.FindStringSubmatch(m)
	if sub == nil {
		return 0
	}
	if oldNamingRe.MatchString(m) {
		return defaultContextWindow
	}
	fam, major, minor := sub[1], atoi(sub[2]), atoi(sub[3])
	switch {
	case sub[2] == "", fam == "fable", fam == "mythos":
		return longContextWindow
	case major > 4, major == 4 && minor >= 6:
		return longContextWindow
	}
	return defaultContextWindow
}

func atoi(s string) int {
	n := 0
	for _, c := range s {
		n = n*10 + int(c-'0')
	}
	return n
}

// window is the context window for the current model: the CLI-reported value
// if known, else the family fallback, else 0.
func (t *translator) window() int {
	if w := t.windows[t.model]; w > 0 {
		return w
	}
	return fallbackWindow(t.model)
}

// source is where the context figures of a usage event come from: the fill
// is always derived from the assistant messages' usage, and the window is the
// CLI's own once a result line reported it, else the family fallback.
func (t *translator) source() string {
	if t.windows[t.model] == 0 && fallbackWindow(t.model) > 0 {
		return agent.ContextFallback
	}
	return agent.ContextDerived
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
		ContextWindow  int `json:"contextWindow"`
		ThinkingTokens int `json:"thinkingTokens"`
	} `json:"modelUsage"`
	DurationMs    int `json:"duration_ms"`
	DurationAPIMs int `json:"duration_api_ms"`

	// result, and a stream_event's message_start: time to first token
	TTFTMs int `json:"ttft_ms"`

	// system/compact_boundary: how the compaction was started
	CompactMetadata *struct {
		Trigger string `json:"trigger"` // "auto" | "manual"
	} `json:"compact_metadata"`
}

// wireUsage is the usage object of an assistant message / message_delta.
type wireUsage struct {
	InputTokens   int `json:"input_tokens"`
	CacheCreate   int `json:"cache_creation_input_tokens"`
	CacheRead     int `json:"cache_read_input_tokens"`
	OutputTokens  int `json:"output_tokens"`
	OutputDetails struct {
		ThinkingTokens int `json:"thinking_tokens"`
	} `json:"output_tokens_details"`
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

// streamEvent is the event of a stream_event line (a token delta, or the
// usage of the message being streamed).
type streamEvent struct {
	Type  string     `json:"type"`
	Usage *wireUsage `json:"usage"`
	Delta struct {
		Type     string `json:"type"`
		Text     string `json:"text"`
		Thinking string `json:"thinking"`
	} `json:"delta"`
}

// streamLineDecoded is a streamLine whose event is decoded in the same pass
// (its Event shadows streamLine's raw one). stream_event lines are the bulk
// of the output, one per token delta, so this saves a copy and a second
// decode of each.
type streamLineDecoded struct {
	streamLine
	Event *streamEvent `json:"event"`
}

// Line translates one raw stdout line. Unknown or malformed lines yield
// nothing. line is only valid for the duration of the call (the reader
// reuses its buffer): anything kept must be copied.
func (t *translator) Line(line []byte) []agent.Event {
	line = bytes.TrimSpace(line)
	if len(line) == 0 || line[0] != '{' {
		return nil
	}
	var sl streamLine
	var ev0 *streamEvent
	var dec streamLineDecoded
	if json.Unmarshal(line, &dec) == nil {
		sl, ev0 = dec.streamLine, dec.Event
	} else if json.Unmarshal(line, &sl) != nil {
		// The one-pass decode is stricter about "event"; only a line that
		// fails both is malformed.
		return nil
	}
	// The line is copied for Raw only when an event keeps it: most lines
	// (stream deltas, sub-agent chatter) yield none that does.
	var raw json.RawMessage
	mk := func(k agent.Kind) agent.Event {
		if raw == nil {
			raw = json.RawMessage(append([]byte(nil), line...))
		}
		return agent.Event{AgentID: t.agentID, Time: t.now(), Kind: k, Raw: raw}
	}
	// Streamed text carries no Raw: Deck drops it for these kinds anyway
	// (agent.KeepsRaw), and deltas are most of the stream.
	mkDelta := func(k agent.Kind, text string) agent.Event {
		return agent.Event{AgentID: t.agentID, Time: t.now(), Kind: k, Text: text, Delta: true}
	}

	switch sl.Type {
	case "system":
		if sl.Subtype == "init" && sl.SessionID != "" {
			ev := mk(agent.KindSessionStarted)
			ev.SessionID = sl.SessionID
			ev.Text = sl.Model // the model this session actually runs
			return []agent.Event{ev}
		}
		if sl.Subtype == "compact_boundary" && (sl.ParentToolUseID == nil || *sl.ParentToolUseID == "") {
			// The conversation was summarised: what the last message
			// filled no longer stands; the next one reports the new use.
			t.ctx, t.lastEmitted = ctxUsage{}, 0
			ev := mk(agent.KindCompacted)
			if sl.CompactMetadata != nil {
				ev.Text = sl.CompactMetadata.Trigger
			}
			return []agent.Event{ev}
		}

	case "stream_event":
		if sl.ParentToolUseID != nil && *sl.ParentToolUseID != "" {
			return nil // sub-agent chatter
		}
		var se streamEvent
		if ev0 != nil {
			se = *ev0
		} else if json.Unmarshal(sl.Event, &se) != nil {
			return nil
		}
		if se.Type == "message_start" && t.ttft == 0 && sl.TTFTMs > 0 {
			t.ttft = sl.TTFTMs
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
			return []agent.Event{mkDelta(agent.KindAssistantText, se.Delta.Text)}
		case "thinking_delta":
			if se.Delta.Thinking == "" {
				return nil
			}
			return []agent.Event{mkDelta(agent.KindReasoning, se.Delta.Thinking)}
		}

	case "assistant":
		return t.assistant(sl, mk)

	case "user":
		return t.user(sl, mk)

	case "result":
		var out []agent.Event
		failed := sl.IsError || strings.HasPrefix(sl.Subtype, "error")
		hit := t.limitHit(sl.Result, failed)
		if failed || hit != nil {
			ev := mk(agent.KindError)
			ev.Text = sl.Result
			if ev.Text == "" {
				ev.Text = sl.Subtype
			}
			ev.LimitHit = hit
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
		thinking := u.OutputDetails.ThinkingTokens
		if thinking == 0 {
			for _, mu := range sl.ModelUsage {
				thinking += mu.ThinkingTokens
			}
		}
		ttft := sl.TTFTMs
		if ttft == 0 {
			ttft = t.ttft
		}
		t.ttft = 0
		ue := mk(agent.KindUsage)
		ue.Usage = &agent.Usage{
			InputTokens: u.InputTokens, OutputTokens: u.OutputTokens, CachedTokens: u.CacheRead, CostUSD: sl.TotalCostUSD,
			ContextTokens: t.ctx.total(), ContextWindow: t.window(), ContextSource: t.source(),
			CacheWriteTokens: u.CacheCreate, ReasoningTokens: thinking,
			TTFTms: ttft, APIms: sl.DurationAPIMs, DurationMs: sl.DurationMs,
		}
		t.lastEmitted = t.ctx.total()
		out = append(out, t.stopSubs(mk)...)
		out = append(out, ue, mk(agent.KindTurnDone))
		return out

	case "rate_limit_event":
		if t.rateLimit(line) {
			return t.limitsEvent(mk)
		}

	case "control_response":
		if t.probeAnswer(line) {
			return t.limitsEvent(mk)
		}
	}
	return nil
}

// parentOf is the subagent a line belongs to: its parent_tool_use_id, ""
// for the agent's own lines.
func parentOf(sl streamLine) string {
	if sl.ParentToolUseID == nil {
		return ""
	}
	return *sl.ParentToolUseID
}

func (t *translator) assistant(sl streamLine, mk func(agent.Kind) agent.Event) []agent.Event {
	if p := parentOf(sl); p != "" {
		return t.subWork(p, t.subAssistant(sl, mk), mk)
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
			if s := agent.SubagentOf(ev.Tool); s != nil {
				// The Task call starts a subagent: its lines follow,
				// tagged with this call's id, until the call's result.
				t.subs[b.ID] = s
				out = append(out, t.subEvent(mk, *s))
			}
		}
	}
	return append(out, t.contextEvent(mk)...)
}

// subAssistant translates an assistant line of a subagent: its text,
// thinking and tool calls. Its usage is its own context, not the agent's,
// and its model is not the one the agent runs.
func (t *translator) subAssistant(sl streamLine, mk func(agent.Kind) agent.Event) []agent.Event {
	var mb messageBody
	if json.Unmarshal(sl.Message, &mb) != nil {
		return nil
	}
	var blocks []contentBlock
	if json.Unmarshal(mb.Content, &blocks) != nil {
		return nil
	}
	var out []agent.Event
	for _, b := range blocks {
		switch b.Type {
		case "text":
			if strings.TrimSpace(b.Text) != "" {
				ev := mk(agent.KindAssistantText)
				ev.Text = b.Text
				out = append(out, ev)
			}
		case "thinking":
			if b.Thinking != "" {
				ev := mk(agent.KindReasoning)
				ev.Text = b.Thinking
				out = append(out, ev)
			}
		case "tool_use":
			name, title := displayTool(b.Name, b.Input)
			t.tools[b.ID] = toolInfo{name: name, title: title, input: b.Input}
			ev := mk(agent.KindToolCall)
			ev.Tool = &agent.Tool{ID: b.ID, Name: name, Title: title, Input: b.Input, Status: agent.ToolRunning}
			out = append(out, ev)
		}
	}
	return out
}

// subWork tags evs as the work of the subagent the Task call parent
// started. A subagent whose start was not seen (its agent's turn began
// before Deck read the stream) starts here.
func (t *translator) subWork(parent string, evs []agent.Event, mk func(agent.Kind) agent.Event) []agent.Event {
	if len(evs) == 0 {
		return nil
	}
	var out []agent.Event
	if t.subs[parent] == nil {
		s := &agent.Subagent{ID: parent, Status: agent.SubRunning}
		t.subs[parent] = s
		out = append(out, t.subEvent(mk, *s))
	}
	for i := range evs {
		evs[i].Sub = parent
	}
	return append(out, evs...)
}

// subEvent is the KindSubagent event of s as it stands.
func (t *translator) subEvent(mk func(agent.Kind) agent.Event, s agent.Subagent) agent.Event {
	ev := mk(agent.KindSubagent)
	ev.Raw = nil // the line is the Task call's or its result's, kept there
	ev.Subagent = &s
	return ev
}

// stopSubs ends the subagents still running when the agent's turn ends: the
// turn was interrupted, or failed, before they reported back.
func (t *translator) stopSubs(mk func(agent.Kind) agent.Event) []agent.Event {
	if len(t.subs) == 0 {
		return nil
	}
	ids := make([]string, 0, len(t.subs))
	for id := range t.subs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	var out []agent.Event
	for _, id := range ids {
		s := *t.subs[id]
		s.Status = agent.SubStopped
		out = append(out, t.subEvent(mk, s))
		delete(t.subs, id)
	}
	return out
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
	ev.Usage = &agent.Usage{ContextTokens: n, ContextWindow: t.window(), ContextSource: t.source()}
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
	parent := parentOf(sl)
	for _, b := range blocks {
		if b.Type != "tool_result" {
			continue
		}
		info := t.tools[b.ToolUseID]
		status := agent.ToolOK
		if b.IsError {
			status = agent.ToolError
		}
		// The diff goes before its result, as other adapters emit it:
		// consumers attach a path's latest diff to the result that follows.
		if status == agent.ToolOK {
			if d := fileDiffFor(info, sl.ToolUseResult); d != nil {
				fe := mk(agent.KindFileChange)
				fe.Diff = d
				out = append(out, fe)
			}
		}
		ev := mk(agent.KindToolResult)
		ev.Tool = &agent.Tool{
			ID: b.ToolUseID, Name: info.name, Title: info.title, Input: info.input,
			Output: stringifyContent(b.Content), Status: status,
		}
		if s := t.subs[b.ToolUseID]; s != nil && parent == "" {
			// The Task call's result: the subagent reported back, or failed.
			out = append(out, t.subEvent(mk, agent.SubagentEnd(*s, ev.Tool)))
			delete(t.subs, b.ToolUseID)
		}
		out = append(out, ev)
		delete(t.tools, b.ToolUseID)
	}
	if parent != "" {
		return t.subWork(parent, out, mk)
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
// own search and execute tools render as agent.DeckToolDisplay says: a
// thread.send is a "message" ("to <to>: <text>"), a thread.read a "read".
func displayTool(name string, input json.RawMessage) (string, string) {
	if bare, ok := strings.CutPrefix(name, deckToolPrefix); ok {
		if n, title, ok := agent.DeckToolDisplay(bare, input); ok {
			return n, title
		}
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
	case "AskUserQuestion":
		var in struct {
			Questions []struct {
				Question string `json:"question"`
			} `json:"questions"`
		}
		if json.Unmarshal(input, &in) == nil && len(in.Questions) > 0 {
			title = in.Questions[0].Question
		}
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
