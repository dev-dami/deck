package acp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dev-dami/deck/internal/deck/agent"
	"github.com/dev-dami/deck/internal/deck/agent/acp/acptest"
)

// The test binary doubles as a scripted ACP agent (acptest) when it is
// started as one.
func TestMain(m *testing.M) {
	acptest.Main()
	os.Exit(m.Run())
}

// testConfig is an agent like OpenCode, run as the test binary.
var testConfig = Config{Kind: "test", Command: os.Args[0]}

type fake struct {
	log string
	dir string
}

func newFake(t *testing.T, flavor string) fake {
	t.Helper()
	f := fake{log: filepath.Join(t.TempDir(), "acp.log"), dir: t.TempDir()}
	t.Setenv(acptest.EnvLog, f.log)
	t.Setenv(acptest.EnvDir, f.dir)
	t.Setenv(acptest.EnvFlavor, flavor)
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

func (f fake) start(t *testing.T, cfg Config, spec agent.Spec) agent.Run {
	t.Helper()
	spec.ID, spec.Dir = "test", f.dir
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	t.Cleanup(cancel)
	r, err := New("", cfg).Start(ctx, spec)
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

// brief is one line per event: its kind and what matters of it.
func brief(evs []agent.Event) string {
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
	return strings.Join(got, "\n")
}

func TestStartAppliesModelEffortAndMode(t *testing.T) {
	f := newFake(t, "opencode")
	r := f.start(t, testConfig, agent.Spec{Model: "p/b", Effort: "high", Mode: agent.ModePlan})
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
	if f.has(t, "missing jsonrpc") {
		t.Error("a message went out without jsonrpc 2.0")
	}
}

func TestATurnStreamsAsksAndEnds(t *testing.T) {
	f := newFake(t, "opencode")
	r := f.start(t, testConfig, agent.Spec{Instructions: "BRIEF"})
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
	want := []string{
		"user_text=hi", "reasoning~think", "reasoning~ing", "reasoning=thinking",
		"tool_call:Edit//running/", "file_change:a.go", "tool_result:Edit/a.go/ok/Edit applied",
		"assistant_text~Hel", "assistant_text~lo", "usage:0/10/100/0.5",
		"assistant_text=Hello", "usage:4/10/100/0.5", "turn_done",
	}
	if got := brief(evs); got != strings.Join(want, "\n") {
		t.Errorf("events:\n%s\nwant:\n%s", got, strings.Join(want, "\n"))
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
	f := newFake(t, "opencode")
	r := f.start(t, testConfig, agent.Spec{})
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
	if !f.has(t, `session/cancel {"sessionId":"ses_1"}`) {
		t.Error("no session/cancel went out")
	}
}

// TestACrashEndsTheRun covers an agent that dies mid-turn: the turn ends in
// an error, then the run exits with what the agent last said on stderr.
func TestACrashEndsTheRun(t *testing.T) {
	f := newFake(t, "opencode")
	r := f.start(t, testConfig, agent.Spec{})
	<-r.Events()
	if err := r.Send("CRASH"); err != nil {
		t.Fatal(err)
	}
	var exited *agent.Event
	timeout := time.After(30 * time.Second)
	for exited == nil {
		select {
		case e, ok := <-r.Events():
			if !ok {
				t.Fatal("events closed before exited")
			}
			if e.Kind == agent.KindExited {
				exited = &e
			}
		case <-timeout:
			t.Fatal("no exit")
		}
	}
	if !strings.Contains(exited.Text, "exit status 3") || !strings.Contains(exited.Text, "fake agent crashed") {
		t.Errorf("exited = %q", exited.Text)
	}
	for range r.Events() { // closed after exited
	}
	if _, ok := <-r.Approvals(); ok {
		t.Error("approvals stay open")
	}
}

// TestTerminalOutput covers a command whose output streams in _meta, as
// pi-acp sends it: the output is the result's, and a failing exit an error.
func TestTerminalOutput(t *testing.T) {
	f := newFake(t, "pi")
	r := f.start(t, testConfig, agent.Spec{})
	<-r.Events()
	if err := r.Send("BASH"); err != nil {
		t.Fatal(err)
	}
	evs, _ := turn(t, r, agent.Decision{})
	got := brief(evs)
	if !strings.Contains(got, "tool_call:Bash/go test ./.../running/\n") || !strings.Contains(got, "tool_result:Bash/go test ./.../error/ok  \tpkg\nFAIL\n") {
		t.Errorf("events:\n%s", got)
	}
}

func TestResumeAndSwitches(t *testing.T) {
	f := newFake(t, "opencode")
	r := f.start(t, testConfig, agent.Spec{ResumeID: "ses_1"})
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
	if err := r.(agent.ModeSetter).SetMode(agent.ModePlan); err != nil || !f.has(t, `session/set_config_option {"sessionId":"ses_1","configId":"mode","value":"plan"}`) {
		t.Errorf("plan mode: %v", err)
	}
	if err := r.(agent.ModeSetter).SetMode(agent.ModeYolo); err != nil || !f.has(t, `session/set_config_option {"sessionId":"ses_1","configId":"mode","value":"build"}`) {
		t.Errorf("yolo runs the build agent: %v", err)
	}
}

// TestLoadDropsTheHistory covers an agent that resumes only by session/load,
// which replays the history as updates: Deck has its own, so they are
// dropped.
func TestLoadDropsTheHistory(t *testing.T) {
	f := newFake(t, "pi")
	r := f.start(t, testConfig, agent.Spec{ResumeID: "ses_1"})
	if ev := <-r.Events(); ev.Kind != agent.KindSessionStarted {
		t.Fatalf("first event = %+v", ev)
	}
	if !f.has(t, `session/load {"sessionId":"ses_1"`) {
		t.Fatalf("load:\n%s", strings.Join(f.lines(t), "\n"))
	}
	if err := r.Send("hi"); err != nil {
		t.Fatal(err)
	}
	evs, _ := turn(t, r, agent.Decision{Allow: true})
	if got := brief(evs); strings.Contains(got, "old reply") {
		t.Errorf("the history came through:\n%s", got)
	}
}

// TestBriefedResumeSkipsInstructions covers a resumed session that already
// had Deck's instructions: they are not sent into it again.
func TestBriefedResumeSkipsInstructions(t *testing.T) {
	f := newFake(t, "opencode")
	r := f.start(t, testConfig, agent.Spec{ResumeID: "ses_1", Instructions: "BRIEF", Briefed: true})
	<-r.Events()
	if err := r.Send("hi"); err != nil {
		t.Fatal(err)
	}
	turn(t, r, agent.Decision{Allow: true})
	if !f.has(t, `session/prompt {"sessionId":"ses_1","prompt":[{"type":"text","text":"hi"}]}`) {
		t.Errorf("the prompt went out with the instructions again:\n%s", strings.Join(f.lines(t), "\n"))
	}
}

// TestLabelsAndVideo covers attachments: each block follows its reference
// line, an image goes as its bytes and a video as a resource_link to the
// file, which only an agent that takes video is sent.
func TestLabelsAndVideo(t *testing.T) {
	f := newFake(t, "opencode")
	img := filepath.Join(t.TempDir(), "shot.png")
	vid := filepath.Join(t.TempDir(), "clip.mp4")
	if err := os.WriteFile(img, []byte("\x89PNG"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(vid, []byte("video"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := testConfig
	cfg.Video = func(model string) bool { return model == "p/b" }
	r := f.start(t, cfg, agent.Spec{})
	<-r.Events()
	err := r.(agent.ImageSender).SendWithImages("compare them", []agent.Image{
		{Path: img, MediaType: "image/png", Label: "login page error"},
		{Path: vid, MediaType: "video/mp4", Label: "the crash"},
	})
	if err != nil {
		t.Fatal(err)
	}
	turn(t, r, agent.Decision{Allow: true})
	want := `session/prompt {"sessionId":"ses_1","prompt":[{"type":"text","text":"[Image 1: login page error]"},` +
		`{"type":"image","mimeType":"image/png","data":"iVBORw=="},{"type":"text","text":"[Video 1: the crash]"},` +
		`{"type":"resource_link","mimeType":"video/mp4","uri":"file://` + filepath.ToSlash(vid) + `","name":"the crash"},`
	if !f.has(t, want) {
		t.Errorf("prompt:\n%s\nwant prefix:\n%s", strings.Join(f.lines(t), "\n"), want)
	}

	ad := New("", cfg)
	if !ad.TakesVideo("p/b") || ad.TakesVideo("p/a") || New("", testConfig).TakesVideo("p/b") {
		t.Error("TakesVideo follows Config.Video")
	}
	ms, err := ad.Models(context.Background())
	if err != nil || len(ms) != 2 || ms[0].Video || !ms[1].Video {
		t.Errorf("models say which take video: %+v (%v)", ms, err)
	}
	r2 := f.start(t, testConfig, agent.Spec{})
	<-r2.Events()
	if err := r2.(agent.ImageSender).SendWithImages("x", []agent.Image{{Path: vid, MediaType: "video/mp4"}}); err == nil {
		t.Error("an agent that takes no video is sent none")
	}
}

func TestModelsListEveryProviderWithEfforts(t *testing.T) {
	f := newFake(t, "opencode")
	ms, err := New("", testConfig).Models(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(ms) != 2 || ms[0].ID != "p/a" || !ms[0].Default || ms[0].Name != "Model A" || ms[0].Description != "p" || len(ms[0].Efforts) != 0 {
		t.Fatalf("models = %+v", ms)
	}
	if e := ms[1].Efforts; len(e) != 2 || e[0].Level != "low" || e[1].Level != "high" || e[1].Description == "" || ms[1].DefaultEffort != "" {
		t.Errorf("p/b efforts = %+v (default %q)", e, ms[1].DefaultEffort)
	}
	if !f.has(t, "session/delete") {
		t.Error("the listing session is deleted")
	}
}

func TestAMissingCommandSaysHowToInstall(t *testing.T) {
	_, err := New("", Config{Kind: "x", Command: "no-such-acp-agent", Install: "npm i -g x"}).Start(context.Background(), agent.Spec{Dir: t.TempDir()})
	if err == nil || !strings.Contains(err.Error(), "npm i -g x") || !strings.HasPrefix(err.Error(), "x: ") {
		t.Fatalf("err = %v", err)
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
		{toolCall{Name: "find"}, "Glob"},
	} {
		if got := toolName(c.call); got != c.want {
			t.Errorf("toolName(%+v) = %q, want %q", c.call, got, c.want)
		}
	}
	// pi-acp names a tool by its title alone.
	tr := newTranslator("pi", "/w")
	st := tr.merge(toolCall{ToolCallID: "r", Title: "read", Kind: "read", RawInput: json.RawMessage(`{"path":"/w/a.go"}`)})
	if got := tr.agentTool(st.call); got.Name != "Read" || got.Title != "a.go" {
		t.Errorf("pi read = %+v", got)
	}
}

// TestTaskSubagent: OpenCode's task tool starts a subagent; ACP streams only
// the parent session, so Deck sees it start and end as KindSubagent.
func TestTaskSubagent(t *testing.T) {
	tr := newTranslator("oc", "/w")
	var failed toolCall
	if err := json.Unmarshal([]byte(`{"toolCallId":"k1","status":"failed","content":[{"type":"content","content":{"type":"text","text":"it crashed"}}]}`), &failed); err != nil {
		t.Fatal(err)
	}
	var evs []agent.Event
	for _, c := range []toolCall{
		{ToolCallID: "k1", Name: "task", Status: "pending"},
		{ToolCallID: "k1", Status: "in_progress", RawInput: json.RawMessage(`{"description":"Review PR #296","prompt":"look","subagent_type":"general"}`)},
		failed,
	} {
		evs = append(evs, tr.update(update{SessionUpdate: updToolCallUpdate, toolCall: c})...)
	}
	var got []string
	for _, e := range evs {
		if e.Kind == agent.KindSubagent {
			got = append(got, e.Subagent.ID+" "+string(e.Subagent.Status)+" "+e.Subagent.Description+" "+e.Subagent.Type+" "+e.Subagent.Error)
		}
	}
	want := []string{"k1 running   ", "k1 running Review PR #296 general ", "k1 failed Review PR #296 general it crashed"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("subagents %q\nwant %q", got, want)
	}
}
