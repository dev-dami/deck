package antigravity

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/dev-dami/deck/internal/deck/agent"
)

// translator turns `agy --output-format stream-json` lines into normalized
// events. It is stateful (it matches DONE tool steps to the calls they
// complete) and not safe for concurrent use.
//
// Wire shapes (antigravity.google/docs/cli/headless):
//
//	{"event":"init","conversation_id":"...","init":{"cwd":...,"tools":[...],"permission_mode":"request-review"|"always-proceed"}}
//	{"event":"step_update","step_update":{"conversation_id":...,"step_index":N,"state":"ACTIVE"|"DONE","step_type":"user_input"|"agent_response"|"tool"|"checkpoint"|...,
//	  "tool_name":...,"text_delta":...,"tool_info":{"name":...,"parameters":{...},"output":...,"error":{...}},"subagent_info":{"subagents":[...]},"usage":{...}}}
//	{"event":"result","result":{"conversation_id":...,"status":"SUCCESS"|...,"response":...,"error":...,"usage":{...},"num_turns":N}}
//
// Unknown event names, step types and statuses yield no panic: statuses map to
// a visible error (or turn-done), unknown steps are skipped. Every emitted
// event carries the original line in Raw so nothing is lost.
type translator struct {
	agentID string
	// pending remembers tool calls announced by an ACTIVE step so the DONE
	// step emits only the result. agy usually reports a tool only once, on
	// DONE; then the call and result are emitted back to back.
	pending map[int]pendingTool
	now     func() time.Time
}

type pendingTool struct {
	id    string
	name  string
	title string
	input json.RawMessage
}

func newTranslator(agentID string) *translator {
	return &translator{agentID: agentID, pending: map[int]pendingTool{}, now: time.Now}
}

// wireUsage is agy's token object, on DONE steps and result events.
type wireUsage struct {
	InputTokens     int `json:"input_tokens"`
	OutputTokens    int `json:"output_tokens"`
	ThinkingTokens  int `json:"thinking_tokens"`
	CacheReadTokens int `json:"cache_read_tokens"`
	TotalTokens     int `json:"total_tokens"`
}

type wireInit struct {
	Cwd            string          `json:"cwd"`
	Tools          []string        `json:"tools"`
	PermissionMode string          `json:"permission_mode"`
	Model          string          `json:"model"`
	Agent          string          `json:"agent"`
	JSONSchema     json.RawMessage `json:"json_schema"`
}

type wireToolInfo struct {
	Name       string          `json:"name"`
	Parameters json.RawMessage `json:"parameters"`
	Output     string          `json:"output"`
	Error      *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

type wireSubagent struct {
	TypeName       string   `json:"type_name"`
	Role           string   `json:"role"`
	ConversationID string   `json:"conversation_id"`
	LogURI         string   `json:"log_uri"`
	WorkspaceURIs  []string `json:"workspace_uris"`
}

type wireStep struct {
	ConversationID string        `json:"conversation_id"`
	StepIndex      int           `json:"step_index"`
	State          string        `json:"state"`
	StepType       string        `json:"step_type"`
	ToolName       string        `json:"tool_name"`
	TextDelta      string        `json:"text_delta"`
	ToolInfo       *wireToolInfo `json:"tool_info"`
	SubagentInfo   *struct {
		Subagents []wireSubagent `json:"subagents"`
	} `json:"subagent_info"`
	Usage *wireUsage `json:"usage"`
}

type wireResult struct {
	ConversationID string     `json:"conversation_id"`
	Status         string     `json:"status"`
	Response       string     `json:"response"`
	Error          string     `json:"error"`
	DurationSecs   float64    `json:"duration_seconds"`
	NumTurns       int        `json:"num_turns"`
	Usage          *wireUsage `json:"usage"`
}

type wireLine struct {
	Event          string      `json:"event"`
	ConversationID string      `json:"conversation_id"`
	Init           *wireInit   `json:"init"`
	StepUpdate     *wireStep   `json:"step_update"`
	Result         *wireResult `json:"result"`
}

// Line translates one raw stdout line. Unknown or malformed lines yield
// nothing; it never panics on new shapes.
func (t *translator) Line(line []byte) []agent.Event {
	line = bytes.TrimSpace(line)
	if len(line) == 0 || line[0] != '{' {
		return nil
	}
	var wl wireLine
	if json.Unmarshal(line, &wl) != nil {
		return nil
	}
	raw := json.RawMessage(append([]byte(nil), line...))
	mk := func(k agent.Kind) agent.Event {
		return agent.Event{AgentID: t.agentID, Time: t.now(), Kind: k, Raw: raw}
	}

	switch wl.Event {
	case "init":
		if wl.ConversationID == "" {
			return nil
		}
		ev := mk(agent.KindSessionStarted)
		ev.SessionID = wl.ConversationID
		if wl.Init != nil {
			// The model/agent the CLI runs, when overridden on the command line.
			ev.Text = strings.TrimSpace(wl.Init.Model + " " + wl.Init.Agent)
		}
		return []agent.Event{ev}

	case "step_update":
		if wl.StepUpdate == nil {
			return nil
		}
		return t.step(wl.StepUpdate, mk)

	case "result":
		if wl.Result == nil {
			return nil
		}
		return t.result(wl.Result, mk)
	}
	return nil
}

func (t *translator) step(s *wireStep, mk func(agent.Kind) agent.Event) []agent.Event {
	active := s.State == "ACTIVE"
	switch s.StepType {
	case "user_input":
		// Deck already echoes every prompt it sends (KindUserText at Send);
		// the wire echo carries no text to add.
		return nil

	case "agent_response":
		if s.TextDelta == "" {
			return nil
		}
		ev := mk(agent.KindAssistantText)
		ev.Text, ev.Delta = s.TextDelta, active
		return []agent.Event{ev}

	case "tool":
		var out []agent.Event
		name := s.ToolName
		if name == "" && s.ToolInfo != nil {
			name = s.ToolInfo.Name
		}
		if name == "" {
			name = "tool"
		}
		var info *wireToolInfo
		if s.ToolInfo != nil {
			info = s.ToolInfo
		}
		if active {
			// Streaming start of a call; the DONE step brings the result.
			id := toolID(s.StepIndex)
			title, input := toolTitleInput(name, nil)
			if info != nil {
				title, input = toolTitleInput(name, info.Parameters)
			}
			t.pending[s.StepIndex] = pendingTool{id: id, name: name, title: title, input: input}
			ev := mk(agent.KindToolCall)
			ev.Tool = &agent.Tool{ID: id, Name: displayName(name), Title: title, Input: input, Status: agent.ToolRunning}
			return []agent.Event{ev}
		}
		p, announced := t.pending[s.StepIndex]
		delete(t.pending, s.StepIndex)
		if !announced {
			// The usual case: agy reports the call once, on completion.
			title, input := toolTitleInput(name, nil)
			if info != nil {
				title, input = toolTitleInput(name, info.Parameters)
			}
			p = pendingTool{id: toolID(s.StepIndex), name: name, title: title, input: input}
			ev := mk(agent.KindToolCall)
			ev.Tool = &agent.Tool{ID: p.id, Name: displayName(p.name), Title: p.title, Input: p.input, Status: agent.ToolRunning}
			out = append(out, ev)
		} else if info != nil {
			// The ACTIVE step announced the call before its parameters
			// were known; the result carries the full picture.
			p.title, p.input = toolTitleInput(name, info.Parameters)
		}
		res := mk(agent.KindToolResult)
		status := agent.ToolOK
		output := ""
		if info != nil {
			output = info.Output
			if info.Error != nil {
				status = agent.ToolError
				if output == "" {
					output = info.Error.Message
				} else {
					output += "\n" + info.Error.Message
				}
			}
		}
		res.Tool = &agent.Tool{ID: p.id, Name: displayName(p.name), Title: p.title, Input: p.input, Output: output, Status: status}
		out = append(out, res)
		out = append(out, t.subagents(s, mk)...)
		return out

	case "checkpoint":
		// Per-step usage is informational; the authoritative cumulative
		// usage arrives on the result event, which is the only usage Deck
		// counts, so checkpoints emit nothing.
		return append([]agent.Event(nil), t.subagents(s, mk)...)

	case "":
		return nil

	default:
		// New step types the CLI may add: never panic, never guess. Steps
		// that still carry subagent info are surfaced as tool entries; text
		// deltas on unknown steps are ignored to avoid misattribution.
		return append([]agent.Event(nil), t.subagents(s, mk)...)
	}
}

// subagents emits subagent invocations as tool-call entries: one running call
// plus its result per subagent, so they show in Deck like any other tool.
func (t *translator) subagents(s *wireStep, mk func(agent.Kind) agent.Event) []agent.Event {
	if s.SubagentInfo == nil || len(s.SubagentInfo.Subagents) == 0 {
		return nil
	}
	var out []agent.Event
	for i, sa := range s.SubagentInfo.Subagents {
		id := fmt.Sprintf("subagent-%d-%d", s.StepIndex, i)
		title := sa.Role
		if title == "" {
			title = sa.TypeName
		}
		if title == "" {
			title = "subagent"
		}
		call := mk(agent.KindToolCall)
		call.Tool = &agent.Tool{ID: id, Name: "subagent", Title: title, Status: agent.ToolRunning}
		res := mk(agent.KindToolResult)
		var sb strings.Builder
		if sa.ConversationID != "" {
			sb.WriteString(sa.ConversationID)
		}
		if len(sa.WorkspaceURIs) > 0 {
			if sb.Len() > 0 {
				sb.WriteString("\n")
			}
			sb.WriteString(strings.Join(sa.WorkspaceURIs, "\n"))
		}
		res.Tool = &agent.Tool{ID: id, Name: "subagent", Title: title, Output: sb.String(), Status: agent.ToolOK}
		out = append(out, call, res)
	}
	return out
}

func (t *translator) result(r *wireResult, mk func(agent.Kind) agent.Event) []agent.Event {
	var out []agent.Event
	usageOf := func() *agent.Usage {
		u := &agent.Usage{}
		if r.Usage != nil {
			u.InputTokens, u.OutputTokens, u.CachedTokens = r.Usage.InputTokens, r.Usage.OutputTokens, r.Usage.CacheReadTokens
		}
		return u
	}
	turnDone := func(text string) {
		ue := mk(agent.KindUsage)
		ue.Usage = usageOf()
		out = append(out, ue)
		td := mk(agent.KindTurnDone)
		td.Text = text
		if r.ConversationID != "" {
			td.SessionID = r.ConversationID
		}
		out = append(out, td)
	}

	switch r.Status {
	case "SUCCESS":
		// The response text duplicates the streamed agent_response deltas,
		// which were already emitted, so only usage and turn-done follow.
		turnDone("")
	case "ERROR":
		ev := mk(agent.KindError)
		ev.Text = r.Error
		if ev.Text == "" {
			ev.Text = r.Response
		}
		if ev.Text == "" {
			ev.Text = "antigravity run failed"
		}
		out = append(out, ev)
		turnDone("")
	case "CANCELED", "INTERRUPTED":
		turnDone(strings.ToLower(r.Status))
	case "WAITING", "RUNNING", "INVALID":
		// The run stopped without producing a response.
		ev := mk(agent.KindError)
		msg := strings.ToLower(r.Status)
		if r.Error != "" {
			msg += ": " + r.Error
		} else {
			msg = "antigravity run ended while " + msg
		}
		ev.Text = msg
		out = append(out, ev)
		turnDone("")
	default:
		ev := mk(agent.KindError)
		if r.Status == "" {
			ev.Text = "antigravity run ended with an unknown status"
		} else {
			ev.Text = fmt.Sprintf("antigravity run ended with status %q", r.Status)
		}
		if r.Error != "" {
			ev.Text += ": " + r.Error
		}
		out = append(out, ev)
		turnDone("")
	}
	return out
}

func toolID(step int) string { return fmt.Sprintf("step-%d", step) }

// toolTitleInput summarizes a tool call for Deck's display. Only run_command
// has a documented parameter shape (CommandLine); everything else falls back
// to compacted JSON so new or undocumented tools still show something useful.
func toolTitleInput(name string, params json.RawMessage) (string, json.RawMessage) {
	if len(params) == 0 {
		return "", nil
	}
	if name == "run_command" {
		var p struct {
			CommandLine string `json:"CommandLine"`
		}
		if json.Unmarshal(params, &p) == nil && p.CommandLine != "" {
			return firstLine(p.CommandLine, 160), params
		}
	}
	title := compactJSON(params)
	if i := strings.IndexByte(title, '\n'); i >= 0 {
		title = title[:i] + " ..."
	}
	if r := []rune(title); len(r) > 160 {
		title = string(r[:157]) + "..."
	}
	return title, params
}

func firstLine(s string, max int) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i] + " ..."
	}
	if r := []rune(s); len(r) > max {
		s = string(r[:max-3]) + "..."
	}
	return s
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

// displayName maps agy's tool names to Deck's display names. Deck's own
// messaging tools arrive through the deck MCP server under an unknown tool
// prefix (agy's MCP tool naming is undocumented), so they are matched by
// suffix: anything ending in send_message becomes "message".
func displayName(name string) string {
	switch {
	case name == "send_message" || strings.HasSuffix(name, "__send_message") || strings.HasSuffix(name, "_send_message"):
		return "message"
	case name == "read_thread" || strings.HasSuffix(name, "__read_thread") || strings.HasSuffix(name, "_read_thread"):
		return "read"
	}
	if strings.HasSuffix(name, "__approve") || strings.HasSuffix(name, "_approve") || name == "approve" {
		return "approve"
	}
	return name
}
