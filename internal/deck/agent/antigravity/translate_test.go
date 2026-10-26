package antigravity

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/dev-dami/deck/internal/deck/agent"
)

// recordedSession is the verbatim stdout of one real call:
//
//	agy -p "Reply with exactly the word: pineapples. Nothing else." --output-format stream-json
//
// Auth, cwd and tool list are the author's machine; only the event shapes
// matter to the translator.
const recordedSession = `{"event":"init","conversation_id":"67ae5621-c175-433e-b1ed-4153b533310c","init":{"cwd":"C:\\Users\\Erioluwa\\Downloads\\deck","tools":["ask_custom_permission","ask_permission","run_command","write_to_file"],"permission_mode":"request-review"}}
{"event":"step_update","step_update":{"conversation_id":"67ae5621-c175-433e-b1ed-4153b533310c","step_index":0,"state":"DONE","step_type":"user_input"}}
{"event":"step_update","step_update":{"conversation_id":"67ae5621-c175-433e-b1ed-4153b533310c","step_index":1,"state":"ACTIVE","step_type":"agent_response","text_delta":"pineapples"}}
{"event":"step_update","step_update":{"conversation_id":"67ae5621-c175-433e-b1ed-4153b533310c","step_index":1,"state":"DONE","step_type":"agent_response","text_delta":"\n","duration_seconds":2.8359477,"usage":{"input_tokens":13092,"output_tokens":69,"thinking_tokens":67,"cache_read_tokens":0,"total_tokens":13161}}}
{"event":"result","result":{"conversation_id":"67ae5621-c175-433e-b1ed-4153b533310c","status":"SUCCESS","response":"pineapples\n","duration_seconds":3.4403809,"num_turns":1,"usage":{"input_tokens":13092,"output_tokens":69,"thinking_tokens":67,"cache_read_tokens":0,"total_tokens":13161}}}`

func kinds(evs []agent.Event) []agent.Kind {
	out := make([]agent.Kind, len(evs))
	for i, e := range evs {
		out[i] = e.Kind
	}
	return out
}

// TestStreamedFragmentsConcatenate replays the shape of a real long response:
// many ACTIVE fragments, then a DONE step carrying only the tail fragment —
// not a repeat of the full text. Concatenating every AssistantText event must
// rebuild the response exactly once; dropping the DONE tail would lose text,
// emitting a repeat would double it.
func TestStreamedFragmentsConcatenate(t *testing.T) {
	fragments := []string{
		`{"event":"step_update","step_update":{"conversation_id":"c","step_index":1,"state":"ACTIVE","step_type":"agent_response","text_delta":"Thomas had t"}}`,
		`{"event":"step_update","step_update":{"conversation_id":"c","step_index":1,"state":"ACTIVE","step_type":"agent_response","text_delta":"ended the beacon for forty years, mem"}}`,
		`{"event":"step_update","step_update":{"conversation_id":"c","step_index":1,"state":"ACTIVE","step_type":"agent_response","text_delta":"orizing every stone. He knew the ea wind sudd"}}`,
		`{"event":"step_update","step_update":{"conversation_id":"c","step_index":1,"state":"DONE","step_type":"agent_response","text_delta":"enly fall dead silent behind him.\n"}}`,
	}
	tr := newTranslator("antigravity")
	var texts []string
	var deltas []bool
	for _, f := range fragments {
		for _, ev := range tr.Line([]byte(f)) {
			if ev.Kind != agent.KindAssistantText {
				t.Fatalf("kind = %q, want assistant_text", ev.Kind)
			}
			texts = append(texts, ev.Text)
			deltas = append(deltas, ev.Delta)
		}
	}
	if len(texts) != len(fragments) {
		t.Fatalf("got %d text events for %d fragments", len(texts), len(fragments))
	}
	for i, d := range deltas {
		if want := i < len(deltas)-1; d != want {
			t.Fatalf("fragment %d Delta = %v, want %v", i, d, want)
		}
	}
	if got, want := strings.Join(texts, ""), "Thomas had tended the beacon for forty years, memorizing every stone. He knew the ea wind suddenly fall dead silent behind him.\n"; got != want {
		t.Fatalf("concatenated = %q, want %q", got, want)
	}
}

func TestRecordedSession(t *testing.T) {
	tr := newTranslator("antigravity")
	var got []agent.Event
	for line := range strings.Lines(strings.TrimSpace(recordedSession)) {
		got = append(got, tr.Line([]byte(line))...)
	}
	want := []agent.Kind{
		agent.KindSessionStarted,
		agent.KindLimits, // once: agy reports no plan windows
		// user_input echo: skipped, Deck echoes at Send.
		agent.KindAssistantText, // ACTIVE delta
		agent.KindAssistantText, // DONE delta
		agent.KindUsage,
		agent.KindTurnDone,
	}
	if len(got) != len(want) {
		t.Fatalf("got %d events %v, want %d %v", len(got), kinds(got), len(want), want)
	}
	for i, k := range want {
		if got[i].Kind != k {
			t.Fatalf("event %d: got %q, want %q", i, got[i].Kind, k)
		}
	}
	if got[0].SessionID != "67ae5621-c175-433e-b1ed-4153b533310c" {
		t.Fatalf("session id = %q", got[0].SessionID)
	}
	if l := got[1].Limits; l == nil || l.Unavailable != agent.LimitsUnsupported || len(l.Windows) != 0 {
		t.Fatalf("limits = %+v", got[1])
	}
	if !got[2].Delta || got[2].Text != "pineapples" {
		t.Fatalf("streaming delta = %+v", got[2])
	}
	if got[3].Delta {
		t.Fatalf("final delta should not be marked streaming: %+v", got[3])
	}
	u := got[4].Usage
	if u == nil || u.InputTokens != 13092 || u.OutputTokens != 69 || u.CachedTokens != 0 || u.ReasoningTokens != 67 || u.DurationMs != 3440 {
		t.Fatalf("usage = %+v", u)
	}
	// agy reports no context: Deck estimates the fill from the last step's
	// usage. The init line names no model, so the window is unknown.
	if u.ContextTokens != 13161 || u.ContextWindow != 0 || u.ContextSource != agent.ContextEstimated {
		t.Fatalf("context estimate = %d/%d %q", u.ContextTokens, u.ContextWindow, u.ContextSource)
	}
	// A second init (the next turn's process) does not repeat the limits.
	if evs := tr.Line([]byte(`{"event":"init","conversation_id":"67ae5621-c175-433e-b1ed-4153b533310c"}`)); len(evs) != 1 || evs[0].Kind != agent.KindSessionStarted {
		t.Fatalf("second init = %v", kinds(evs))
	}
	for i, e := range got {
		if e.Kind == agent.KindLimits {
			continue // about the account, not a protocol line
		}
		if len(e.Raw) == 0 {
			t.Fatalf("event %d carries no Raw", i)
		}
		var raw map[string]any
		if err := json.Unmarshal(e.Raw, &raw); err != nil {
			t.Fatalf("event %d Raw is not JSON: %v", i, err)
		}
	}
}

func TestToolStep(t *testing.T) {
	// Tool shape follows the documented stream-json example (a run_command
	// call); only DONE steps carry tool_info, so call and result arrive
	// together.
	tr := newTranslator("antigravity")
	evs := tr.Line([]byte(`{"event":"step_update","step_update":{"conversation_id":"c","step_index":4,"state":"DONE","step_type":"tool","tool_name":"run_command","duration_seconds":0.07,"tool_info":{"name":"run_command","parameters":{"CommandLine":"echo hello_headless_demo"},"output":"hello_headless_demo\r\n"}}}`))
	if len(evs) != 2 || evs[0].Kind != agent.KindToolCall || evs[1].Kind != agent.KindToolResult {
		t.Fatalf("kinds = %v", kinds(evs))
	}
	call, res := evs[0].Tool, evs[1].Tool
	if call.ID != res.ID || call.ID == "" {
		t.Fatalf("call/result ids = %q/%q", call.ID, res.ID)
	}
	if call.Name != "run_command" || call.Status != agent.ToolRunning {
		t.Fatalf("call = %+v", call)
	}
	if call.Title != "echo hello_headless_demo" {
		t.Fatalf("title = %q", call.Title)
	}
	if res.Status != agent.ToolOK || res.Output != "hello_headless_demo\r\n" {
		t.Fatalf("result = %+v", res)
	}
	if string(res.Input) != `{"CommandLine":"echo hello_headless_demo"}` {
		t.Fatalf("input = %s", res.Input)
	}
}

func TestToolError(t *testing.T) {
	tr := newTranslator("antigravity")
	evs := tr.Line([]byte(`{"event":"step_update","step_update":{"conversation_id":"c","step_index":7,"state":"DONE","step_type":"tool","tool_name":"run_command","tool_info":{"name":"run_command","parameters":{"CommandLine":"exit 3"},"output":"","error":{"type":"command_failed","message":"exit status 3"}}}}`))
	if len(evs) != 2 || evs[1].Kind != agent.KindToolResult {
		t.Fatalf("kinds = %v", kinds(evs))
	}
	if evs[1].Tool.Status != agent.ToolError {
		t.Fatalf("result = %+v", evs[1].Tool)
	}
	if !strings.Contains(evs[1].Tool.Output, "exit status 3") {
		t.Fatalf("output = %q", evs[1].Tool.Output)
	}
}

func TestActiveThenDoneTool(t *testing.T) {
	tr := newTranslator("antigravity")
	first := tr.Line([]byte(`{"event":"step_update","step_update":{"conversation_id":"c","step_index":2,"state":"ACTIVE","step_type":"tool","tool_name":"write_to_file"}}`))
	if len(first) != 1 || first[0].Kind != agent.KindToolCall {
		t.Fatalf("active kinds = %v", kinds(first))
	}
	second := tr.Line([]byte(`{"event":"step_update","step_update":{"conversation_id":"c","step_index":2,"state":"DONE","step_type":"tool","tool_name":"write_to_file","tool_info":{"name":"write_to_file","parameters":{"file":"a.txt"},"output":"ok"}}}`))
	// The DONE step emits only the result: the call was already announced.
	if len(second) != 1 || second[0].Kind != agent.KindToolResult {
		t.Fatalf("done kinds = %v", kinds(second))
	}
	if second[0].Tool.ID != first[0].Tool.ID {
		t.Fatalf("ids = %q/%q", first[0].Tool.ID, second[0].Tool.ID)
	}
}

func TestSubagentStep(t *testing.T) {
	tr := newTranslator("antigravity")
	evs := tr.Line([]byte(`{"event":"step_update","step_update":{"conversation_id":"c","step_index":5,"state":"DONE","step_type":"tool","tool_name":"invoke_subagent","tool_info":{"name":"invoke_subagent","parameters":{},"output":"done"},"subagent_info":{"subagents":[{"type_name":"explore","role":"researcher","conversation_id":"sub-1","log_uri":"/tmp/x","workspace_uris":["/tmp/w"]}]}}}`))
	// Tool call + result, then the subagent as its own call + result pair.
	if len(evs) != 4 {
		t.Fatalf("kinds = %v", kinds(evs))
	}
	sub, subRes := evs[2], evs[3]
	if sub.Kind != agent.KindToolCall || subRes.Kind != agent.KindToolResult {
		t.Fatalf("kinds = %v", kinds(evs))
	}
	if sub.Tool.Name != "subagent" || sub.Tool.Title != "researcher" {
		t.Fatalf("subagent call = %+v", sub.Tool)
	}
	if !strings.Contains(subRes.Tool.Output, "sub-1") || !strings.Contains(subRes.Tool.Output, "/tmp/w") {
		t.Fatalf("subagent result = %+v", subRes.Tool)
	}
}

func TestResultStatuses(t *testing.T) {
	mk := func(status, errMsg string) []agent.Event {
		r := map[string]any{"conversation_id": "c", "status": status}
		if errMsg != "" {
			r["error"] = errMsg
		}
		line, _ := json.Marshal(map[string]any{"event": "result", "result": r})
		return newTranslator("antigravity").Line(line)
	}
	cases := []struct {
		status   string
		err      string
		kinds    []agent.Kind
		contains string // required substring of the first event's text ("" = none)
	}{
		{"ERROR", "boom", []agent.Kind{agent.KindError, agent.KindUsage, agent.KindTurnDone}, "boom"},
		{"ERROR", "", []agent.Kind{agent.KindError, agent.KindUsage, agent.KindTurnDone}, ""},
		{"CANCELED", "", []agent.Kind{agent.KindUsage, agent.KindTurnDone}, ""},
		{"INTERRUPTED", "", []agent.Kind{agent.KindUsage, agent.KindTurnDone}, ""},
		{"WAITING", "", []agent.Kind{agent.KindError, agent.KindUsage, agent.KindTurnDone}, "waiting"},
		{"RUNNING", "", []agent.Kind{agent.KindError, agent.KindUsage, agent.KindTurnDone}, "running"},
		{"INVALID", "bad state", []agent.Kind{agent.KindError, agent.KindUsage, agent.KindTurnDone}, "bad state"},
		{"SOMETHING_NEW", "", []agent.Kind{agent.KindError, agent.KindUsage, agent.KindTurnDone}, "SOMETHING_NEW"},
		{"", "", []agent.Kind{agent.KindError, agent.KindUsage, agent.KindTurnDone}, ""},
	}
	for _, c := range cases {
		evs := mk(c.status, c.err)
		if len(evs) != len(c.kinds) {
			t.Fatalf("status %q: kinds = %v, want %v", c.status, kinds(evs), c.kinds)
		}
		for i, k := range c.kinds {
			if evs[i].Kind != k {
				t.Fatalf("status %q: event %d = %q, want %q", c.status, i, evs[i].Kind, k)
			}
		}
		if c.contains != "" && !strings.Contains(evs[0].Text, c.contains) {
			t.Fatalf("status %q: text %q lacks %q", c.status, evs[0].Text, c.contains)
		}
	}
}

func TestUnknownShapesNeverPanic(t *testing.T) {
	tr := newTranslator("antigravity")
	lines := []string{
		``,
		`not json`,
		`[1,2,3]`,
		`{"event":"future_thing","stuff":{}}`,
		`{"event":"step_update"}`,
		`{"event":"step_update","step_update":{"step_type":"teleport","state":"DONE"}}`,
		`{"event":"step_update","step_update":{"step_type":"","state":"DONE"}}`,
		`{"event":"result"}`,
		`{"event":"init"}`,
	}
	for _, l := range lines {
		_ = tr.Line([]byte(l)) // must not panic
	}
}

func TestDisplayTool(t *testing.T) {
	send := json.RawMessage(`{"op":"thread.send","args":{"to":"claude","message":"hi"}}`)
	read := json.RawMessage(`{"op":"thread.read"}`)
	for _, c := range []struct {
		name      string
		input     json.RawMessage
		wantName  string
		wantTitle string
	}{
		{"run_command", nil, "run_command", "orig"},
		{"grep_search", json.RawMessage(`{"query":"x"}`), "grep_search", "orig"},
		{"mcp__deck__execute", send, "message", "to claude: hi"},
		{"deck-antigravity_execute", send, "message", "to claude: hi"},
		{"deck_execute", read, "read", "shared thread"},
		{"deck-antigravity__execute", json.RawMessage(`{"op":"git.log"}`), "deck", "git.log"},
		{"deck-antigravity_search", json.RawMessage(`{"query":"pdf"}`), "deck", "search pdf"},
		{"execute", read, "read", "shared thread"},
		{"execute", json.RawMessage(`{"code":"x"}`), "execute", "orig"},
		{"search", json.RawMessage(`{"query":"pdf"}`), "search", "orig"},
		{"deck-antigravity_approve", nil, "approve", "orig"},
	} {
		n, title := displayTool(c.name, "orig", c.input)
		if n != c.wantName || title != c.wantTitle {
			t.Errorf("displayTool(%q, %s) = %q, %q; want %q, %q", c.name, c.input, n, title, c.wantName, c.wantTitle)
		}
	}
}

// TestContextEstimate covers the estimate of the fill (the turn's last model
// request, not its cumulative total) and the window from the model table.
func TestContextEstimate(t *testing.T) {
	tr := newTranslator("antigravity")
	tr.slug = func() string { return "gemini-3.8-flash-high" }
	lines := []string{
		`{"event":"init","conversation_id":"c1","init":{"cwd":"/w"}}`,
		`{"event":"step_update","step_update":{"conversation_id":"c1","step_index":1,"state":"DONE","step_type":"tool","tool_name":"run_command","usage":{"input_tokens":20000,"output_tokens":100,"total_tokens":20100}}}`,
		`{"event":"step_update","step_update":{"conversation_id":"c1","step_index":2,"state":"DONE","step_type":"agent_response","text_delta":"ok","usage":{"input_tokens":21000,"output_tokens":50,"total_tokens":21050}}}`,
		`{"event":"result","result":{"conversation_id":"c1","status":"SUCCESS","usage":{"input_tokens":41000,"output_tokens":150,"total_tokens":41150}}}`,
	}
	var u *agent.Usage
	for _, l := range lines {
		for _, e := range tr.Line([]byte(l)) {
			if e.Kind == agent.KindUsage {
				u = e.Usage
			}
		}
	}
	if u == nil || u.ContextTokens != 21050 || u.ContextWindow != geminiWindow || u.ContextSource != agent.ContextEstimated {
		t.Fatalf("usage = %+v", u)
	}
	if u.InputTokens != 41000 {
		t.Fatalf("the turn's totals must stay the result's: %+v", u)
	}
	// The next turn without step usage falls back to the result's own.
	for _, e := range tr.Line([]byte(`{"event":"result","result":{"conversation_id":"c1","status":"SUCCESS","usage":{"input_tokens":30000,"output_tokens":10}}}`)) {
		if e.Kind == agent.KindUsage {
			u = e.Usage
		}
	}
	if u.ContextTokens != 30010 {
		t.Fatalf("estimate without steps = %d", u.ContextTokens)
	}
	if windowFor("gpt-oss-120b-medium") != 0 || windowFor("") != 0 {
		t.Fatal("a model Deck does not know has no assumed window")
	}
}
