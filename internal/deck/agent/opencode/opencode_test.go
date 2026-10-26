package opencode

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dev-dami/deck/internal/deck/agent"
)

// The test binary doubles as a scripted `opencode acp` when DECK_FAKE_ACP is
// set: it answers the handshake, keeps a session's options, plays one turn
// with a permission request, and appends every request it gets to the file
// named by DECK_FAKE_ACP.
func TestMain(m *testing.M) {
	if log := os.Getenv("DECK_FAKE_ACP"); log != "" && len(os.Args) > 1 && os.Args[len(os.Args)-1] == "acp" {
		fakeACP(log)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func fakeACP(logPath string) {
	logf, _ := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	defer logf.Close()
	var wmu sync.Mutex
	send := func(v any) {
		b, _ := json.Marshal(v)
		wmu.Lock()
		os.Stdout.Write(append(b, '\n'))
		wmu.Unlock()
	}
	note := func(s string) { fmt.Fprintln(logf, s) }
	note("env " + os.Getenv("OPENCODE_CONFIG_CONTENT"))

	model, effortOn := "p/a", false
	options := func() []map[string]any {
		opts := []map[string]any{
			{"id": "model", "category": "model", "currentValue": model, "options": []map[string]string{
				{"value": "p/a", "name": "p/Model A"}, {"value": "p/b", "name": "p/Model B"}}},
			{"id": "mode", "category": "mode", "currentValue": "build", "options": []map[string]string{{"value": "build"}, {"value": "plan"}}},
		}
		if effortOn {
			opts = append(opts, map[string]any{"id": "effort", "category": "thought_level", "currentValue": "default", "options": []map[string]string{
				{"value": "low", "name": "Low"}, {"value": "high", "name": "High"}, {"value": "default", "name": "Default"}}})
		}
		return opts
	}
	update := func(u map[string]any) {
		send(map[string]any{"jsonrpc": "2.0", "method": "session/update", "params": map[string]any{"sessionId": "ses_1", "update": u}})
	}
	responses := make(chan map[string]any, 4)
	cancels := make(chan struct{}, 4)

	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		var m struct {
			V      string          `json:"jsonrpc"`
			ID     *int            `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
			Result json.RawMessage `json:"result"`
		}
		if json.Unmarshal(sc.Bytes(), &m) != nil {
			continue
		}
		if m.V != "2.0" {
			note("missing jsonrpc 2.0")
		}
		if m.Method == "" { // a response to our permission request
			var r map[string]any
			_ = json.Unmarshal(m.Result, &r)
			responses <- r
			continue
		}
		note(m.Method + " " + string(m.Params))
		reply := func(res any) { send(map[string]any{"jsonrpc": "2.0", "id": *m.ID, "result": res}) }
		switch m.Method {
		case "initialize":
			reply(map[string]any{"protocolVersion": 1, "agentCapabilities": map[string]any{
				"loadSession": true, "promptCapabilities": map[string]any{"image": true},
				"sessionCapabilities": map[string]any{"resume": map[string]any{}, "delete": map[string]any{}}}})
		case "session/new":
			reply(map[string]any{"sessionId": "ses_1", "configOptions": options()})
		case "session/resume", "session/delete":
			reply(map[string]any{"configOptions": options()})
		case "session/set_config_option":
			var p setOptionParams
			_ = json.Unmarshal(m.Params, &p)
			if p.ConfigID == "model" {
				model, effortOn = p.Value, p.Value == "p/b"
			}
			reply(map[string]any{"configOptions": options()})
		case "session/cancel":
			cancels <- struct{}{}
		case "session/prompt":
			id := *m.ID
			slow := strings.Contains(string(m.Params), "SLOW")
			go func() {
				if slow {
					<-cancels
					send(map[string]any{"jsonrpc": "2.0", "id": id, "result": map[string]any{"stopReason": "cancelled"}})
					return
				}
				update(map[string]any{"sessionUpdate": "agent_thought_chunk", "messageId": "r1", "content": map[string]any{"type": "text", "text": "think"}})
				update(map[string]any{"sessionUpdate": "agent_thought_chunk", "messageId": "r1", "content": map[string]any{"type": "text", "text": "ing"}})
				update(map[string]any{"sessionUpdate": "tool_call", "toolCallId": "t1", "name": "edit", "title": "edit", "kind": "edit", "status": "pending", "rawInput": map[string]any{}})
				send(map[string]any{"jsonrpc": "2.0", "id": 100, "method": "session/request_permission", "params": map[string]any{
					"sessionId": "ses_1",
					"toolCall": map[string]any{"toolCallId": "t1", "name": "edit", "kind": "edit", "status": "pending",
						"rawInput": map[string]any{"filePath": filepath.Join(os.Getenv("DECK_FAKE_DIR"), "a.go"), "oldString": "x", "newString": "y"},
						"content":  []map[string]any{{"type": "diff", "path": "a.go", "oldText": "x\n", "newText": "y\n"}}},
					"options": []map[string]string{{"optionId": "once", "kind": "allow_once"}, {"optionId": "always", "kind": "allow_always"}, {"optionId": "reject", "kind": "reject_once"}}}})
				r := <-responses
				b, _ := json.Marshal(r)
				note("permission " + string(b))
				update(map[string]any{"sessionUpdate": "tool_call_update", "toolCallId": "t1", "status": "completed",
					"content": []map[string]any{{"type": "content", "content": map[string]any{"type": "text", "text": "Edit applied"}}}})
				update(map[string]any{"sessionUpdate": "agent_message_chunk", "messageId": "m1", "content": map[string]any{"type": "text", "text": "Hel"}})
				update(map[string]any{"sessionUpdate": "agent_message_chunk", "messageId": "m1", "content": map[string]any{"type": "text", "text": "lo"}})
				update(map[string]any{"sessionUpdate": "usage_update", "used": 10, "size": 100, "cost": map[string]any{"amount": 0.5, "currency": "USD"}})
				send(map[string]any{"jsonrpc": "2.0", "id": id, "result": map[string]any{"stopReason": "end_turn",
					"usage": map[string]any{"inputTokens": 3, "outputTokens": 4, "cachedReadTokens": 1}}})
			}()
		default:
			send(map[string]any{"jsonrpc": "2.0", "id": *m.ID, "error": map[string]any{"code": -32601, "message": "no"}})
		}
	}
}

type fake struct {
	log string
	dir string
}

func newFake(t *testing.T) fake {
	t.Helper()
	f := fake{log: filepath.Join(t.TempDir(), "acp.log"), dir: t.TempDir()}
	t.Setenv("DECK_FAKE_ACP", f.log)
	t.Setenv("DECK_FAKE_DIR", f.dir)
	return f
}

func (f fake) lines(t *testing.T) []string {
	t.Helper()
	b, _ := os.ReadFile(f.log)
	return strings.Split(strings.TrimSpace(string(b)), "\n")
}

func (f fake) has(t *testing.T, prefix string) bool {
	for _, l := range f.lines(t) {
		if strings.HasPrefix(l, prefix) {
			return true
		}
	}
	return false
}

func (f fake) start(t *testing.T, spec agent.Spec) agent.Run {
	t.Helper()
	spec.ID, spec.Dir, spec.Binary = "opencode", f.dir, os.Args[0]
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	t.Cleanup(cancel)
	r, err := New("").Start(ctx, spec)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })
	return r
}

// turn collects one turn's events, answering approvals with d.
func turn(t *testing.T, r agent.Run, d agent.Decision) ([]agent.Event, []agent.ApprovalRequest) {
	t.Helper()
	var evs []agent.Event
	var reqs []agent.ApprovalRequest
	timeout := time.After(30 * time.Second)
	for {
		select {
		case e := <-r.Events():
			evs = append(evs, e)
			if e.Kind == agent.KindTurnDone || e.Kind == agent.KindExited {
				return evs, reqs
			}
		case req := <-r.Approvals():
			reqs = append(reqs, req)
			req.Respond(d)
		case <-timeout:
			t.Fatalf("no turn end; got %d events", len(evs))
		}
	}
}

func TestStartAppliesModelEffortModeAndAsksFirst(t *testing.T) {
	f := newFake(t)
	t.Setenv("OPENCODE_CONFIG_CONTENT", `{"theme":"x","permission":{"read":"allow"}}`)
	r := f.start(t, agent.Spec{Model: "p/b", Effort: "high", Mode: agent.ModePlan})
	if ev := <-r.Events(); ev.Kind != agent.KindSessionStarted || ev.SessionID != "ses_1" || ev.Text != "p/b" {
		t.Fatalf("first event = %+v", ev)
	}
	for _, want := range []string{
		`session/set_config_option {"sessionId":"ses_1","configId":"model","value":"p/b"}`,
		`session/set_config_option {"sessionId":"ses_1","configId":"effort","value":"high"}`,
		`session/set_config_option {"sessionId":"ses_1","configId":"mode","value":"plan"}`,
	} {
		if !f.has(t, want) {
			t.Errorf("missing %s in\n%s", want, strings.Join(f.lines(t), "\n"))
		}
	}
	var env map[string]any
	_ = json.Unmarshal([]byte(strings.TrimPrefix(f.lines(t)[0], "env ")), &env)
	perm, _ := env["permission"].(map[string]any)
	if env["theme"] != "x" || perm["read"] != "allow" || perm["edit"] != "ask" || perm["bash"] != "ask" {
		t.Errorf("inline config = %v", env)
	}
	if f.has(t, "missing jsonrpc") {
		t.Error("a message went out without jsonrpc 2.0")
	}
}

func TestATurnStreamsAsksAndEnds(t *testing.T) {
	f := newFake(t)
	r := f.start(t, agent.Spec{Instructions: "BRIEF"})
	<-r.Events() // session started
	if ev := <-r.Events(); ev.Kind != agent.KindLimits || ev.Limits == nil || ev.Limits.Unavailable != agent.LimitsUnsupported {
		t.Fatalf("limits = %+v", ev)
	}
	if err := r.Send("hi"); err != nil {
		t.Fatal(err)
	}
	evs, reqs := turn(t, r, agent.Decision{Allow: true})
	if len(reqs) != 1 || reqs[0].Kind != agent.ApproveEdit || reqs[0].Diff == nil || reqs[0].Summary != "a.go" {
		t.Fatalf("approvals = %+v", reqs)
	}
	if !f.has(t, `permission {"outcome":{"optionId":"once","outcome":"selected"}}`) {
		t.Errorf("the approval went back as allow once:\n%s", strings.Join(f.lines(t), "\n"))
	}
	var got []string
	for _, e := range evs {
		s := string(e.Kind)
		switch {
		case e.Delta:
			s += "~" + e.Text
		case e.Kind == agent.KindAssistantText || e.Kind == agent.KindReasoning || e.Kind == agent.KindUserText:
			s += "=" + e.Text
		case e.Tool != nil:
			s += ":" + e.Tool.Name + "/" + e.Tool.Title + "/" + string(e.Tool.Status) + "/" + e.Tool.Output
		case e.Kind == agent.KindUsage:
			s += fmt.Sprintf(":%d/%d/%d/%.1f", e.Usage.OutputTokens, e.Usage.ContextTokens, e.Usage.ContextWindow, e.Usage.CostUSD)
		case e.Diff != nil:
			s += ":" + e.Diff.Path
		}
		got = append(got, s)
	}
	want := []string{
		"user_text=hi", "reasoning~think", "reasoning~ing", "reasoning=thinking",
		"tool_call:Edit//running/", "file_change:a.go", "tool_result:Edit/a.go/ok/Edit applied",
		"assistant_text~Hel", "assistant_text~lo", "usage:0/10/100/0.5",
		"assistant_text=Hello", "usage:4/10/100/0.5", "turn_done",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("events:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if first := f.lines(t)[3]; !strings.HasPrefix(first, "session/prompt") || !strings.Contains(first, "deck-instructions") || !strings.Contains(first, "BRIEF") {
		t.Errorf("the first prompt carries the instructions:\n%s", strings.Join(f.lines(t), "\n"))
	}

	// the second turn: no instructions, its own cost
	if err := r.Send("again"); err != nil {
		t.Fatal(err)
	}
	evs, _ = turn(t, r, agent.Decision{Allow: false})
	if !f.has(t, `session/prompt {"sessionId":"ses_1","prompt":[{"type":"text","text":"again"}]}`) {
		t.Error("later prompts go out as typed")
	}
	if !f.has(t, `permission {"outcome":{"optionId":"reject","outcome":"selected"}}`) {
		t.Error("a denial goes back as reject")
	}
	for _, e := range evs {
		if e.Kind == agent.KindUsage && e.Usage.CostUSD != 0 {
			t.Errorf("the session's cost did not grow, so the turn cost nothing: %+v", e.Usage)
		}
	}
}

func TestInterruptCancelsTheTurn(t *testing.T) {
	f := newFake(t)
	r := f.start(t, agent.Spec{})
	<-r.Events()
	if err := r.Send("SLOW"); err != nil {
		t.Fatal(err)
	}
	if err := r.Interrupt(); err != nil {
		t.Fatal(err)
	}
	evs, _ := turn(t, r, agent.Decision{})
	if last := evs[len(evs)-1]; last.Kind != agent.KindTurnDone || last.Text != "interrupted" {
		t.Fatalf("last = %+v", last)
	}
}

func TestResumeAndSwitches(t *testing.T) {
	f := newFake(t)
	r := f.start(t, agent.Spec{ResumeID: "ses_1"})
	<-r.Events()
	if !f.has(t, `session/resume {"sessionId":"ses_1"`) {
		t.Fatalf("resume:\n%s", strings.Join(f.lines(t), "\n"))
	}
	if err := r.(agent.EffortSetter).SetEffort("low"); err == nil {
		t.Error("model A has no effort setting")
	}
	if err := r.(agent.ModelSetter).SetModel("p/b"); err != nil {
		t.Fatal(err)
	}
	if err := r.(agent.EffortSetter).SetEffort(""); err != nil {
		t.Fatal(err)
	}
	if !f.has(t, `session/set_config_option {"sessionId":"ses_1","configId":"effort","value":"default"}`) {
		t.Error("no effort means the model's default")
	}
	if err := r.(agent.ModeSetter).SetMode(agent.ModeYolo); err != nil || !f.has(t, `session/set_config_option {"sessionId":"ses_1","configId":"mode","value":"build"}`) {
		t.Errorf("yolo runs the build agent: %v", err)
	}
}

// TestBriefedResumeSkipsInstructions covers a resumed session that already
// had Deck's instructions: they are not sent into it again.
func TestBriefedResumeSkipsInstructions(t *testing.T) {
	f := newFake(t)
	r := f.start(t, agent.Spec{ResumeID: "ses_1", Instructions: "BRIEF", Briefed: true})
	<-r.Events()
	if err := r.Send("hi"); err != nil {
		t.Fatal(err)
	}
	turn(t, r, agent.Decision{Allow: true})
	if !f.has(t, `session/prompt {"sessionId":"ses_1","prompt":[{"type":"text","text":"hi"}]}`) {
		t.Errorf("the prompt went out with the instructions again:\n%s", strings.Join(f.lines(t), "\n"))
	}
}

func TestModelsListEveryProviderWithEfforts(t *testing.T) {
	f := newFake(t)
	ms, err := New(os.Args[0]).(agent.ModelLister).Models(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(ms) != 2 || ms[0].ID != "p/a" || !ms[0].Default || ms[0].Name != "Model A" || ms[0].Description != "p" || len(ms[0].Efforts) != 0 {
		t.Fatalf("models = %+v", ms)
	}
	if e := ms[1].Efforts; len(e) != 2 || e[0].Level != "low" || e[1].Level != "high" || e[1].Description == "" {
		t.Errorf("p/b efforts = %+v", e)
	}
	if !f.has(t, "session/delete") {
		t.Error("the listing session is deleted")
	}
}

func TestDeckToolTitles(t *testing.T) {
	tr := newTranslator("oc", t.TempDir())
	got := tr.agentTool(toolCall{ToolCallID: "x", Name: "deck_execute", RawInput: json.RawMessage(`{"op":"thread.send","args":{"to":"claude","message":"look at\nthis"}}`)})
	if got.Name != "message" || got.Title != "to claude: look at this" {
		t.Errorf("thread.send = %+v", got)
	}
	got = tr.agentTool(toolCall{ToolCallID: "y", Name: "deck_search", RawInput: json.RawMessage(`{"query":"git log"}`)})
	if got.Name != "deck" || got.Title != "search git log" {
		t.Errorf("search = %+v", got)
	}
}

func TestToolNames(t *testing.T) {
	for _, c := range []struct {
		call toolCall
		want string
	}{
		{toolCall{Name: "shell"}, "Bash"},
		{toolCall{Name: "deck_execute", RawInput: json.RawMessage(`{"op":"thread.send","args":{"to":"claude","message":"hi"}}`)}, "message"},
		{toolCall{Name: "deck_execute", RawInput: json.RawMessage(`{"op":"thread.read"}`)}, "read"},
		{toolCall{Name: "deck_execute", RawInput: json.RawMessage(`{"op":"git.status"}`)}, "deck"},
		{toolCall{Name: "deck_search", RawInput: json.RawMessage(`{"query":"pdf"}`)}, "deck"},
		{toolCall{Name: "brave_search"}, "brave_search"},
		{toolCall{Name: "execute", RawInput: json.RawMessage(`{"code":"await deck.execute({op: 'thread.read'})"}`)}, "read"},
		{toolCall{Name: "execute", RawInput: json.RawMessage(`{"code":"await deck.execute({op: 'thread.send', args: {to: 'codex', message: 'hi'}})"}`)}, "message"},
		{toolCall{Name: "execute", RawInput: json.RawMessage(`{"code":"await tools.grep({})"}`)}, "execute"},
		{toolCall{Kind: "search"}, "Grep"},
	} {
		if got := toolName(c.call); got != c.want {
			t.Errorf("toolName(%+v) = %q, want %q", c.call, got, c.want)
		}
	}
}
