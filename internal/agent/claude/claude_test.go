package claude

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dev-dami/deck/internal/agent"
	"github.com/dev-dami/deck/internal/approve"
)

func replay(t *testing.T, path string) []agent.Event {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	tr := newTranslator("c1")
	var evs []agent.Event
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	for sc.Scan() {
		evs = append(evs, tr.Line(sc.Bytes())...)
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return evs
}

func kinds(evs []agent.Event) []agent.Kind {
	var k []agent.Kind
	for _, e := range evs {
		k = append(k, e.Kind)
	}
	return k
}

func TestReplaySession1(t *testing.T) {
	evs := replay(t, filepath.Join("testdata", "session1.jsonl"))
	want := []agent.Kind{
		agent.KindSessionStarted,
		agent.KindToolCall, agent.KindToolResult, agent.KindFileChange,
		agent.KindAssistantText, agent.KindAssistantText, // deltas "D" "ONE"
		agent.KindAssistantText, // final DONE
		agent.KindUsage, agent.KindTurnDone,
	}
	got := kinds(evs)
	if len(got) != len(want) {
		t.Fatalf("kinds = %v\nwant   %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("kinds = %v\nwant   %v", got, want)
		}
	}
	if evs[0].SessionID == "" {
		t.Error("no session id")
	}
	call, res, fc := evs[1], evs[2], evs[3]
	if call.Tool.Name != "Write" || call.Tool.Status != agent.ToolRunning || !strings.HasSuffix(call.Tool.Title, "hello.txt") {
		t.Errorf("tool call %+v", call.Tool)
	}
	if res.Tool.ID != call.Tool.ID || res.Tool.Status != agent.ToolOK || !strings.Contains(res.Tool.Output, "created") || res.Tool.Name != "Write" {
		t.Errorf("tool result %+v", res.Tool)
	}
	if !fc.Diff.New || fc.Diff.Added != 1 || fc.Diff.Removed != 0 {
		t.Errorf("diff %+v", fc.Diff)
	}
	if !evs[4].Delta || evs[4].Text != "D" || !evs[5].Delta || evs[5].Text != "ONE" {
		t.Errorf("deltas %+v %+v", evs[4], evs[5])
	}
	if evs[6].Delta || evs[6].Text != "DONE" {
		t.Errorf("final text %+v", evs[6])
	}
	u := evs[7].Usage
	if u == nil || u.OutputTokens != 153 || u.InputTokens != 4 || u.CachedTokens != 67466 || u.CostUSD < 0.11 {
		t.Errorf("usage %+v", u)
	}
	if len(evs[0].Raw) == 0 {
		t.Error("raw not kept")
	}
}

func TestTranslateSynthetic(t *testing.T) {
	tr := newTranslator("c1")
	lines := []string{
		`{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"hmm"}}}`,
		`{"type":"stream_event","parent_tool_use_id":"toolu_x","event":{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"sub"}}}`,
		`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"t1","name":"Edit","input":{"file_path":"a.go","old_string":"x\ny\n","new_string":"x\nz\nw\n"}},{"type":"tool_use","id":"t2","name":"Bash","input":{"command":"go test ./..."}},{"type":"tool_use","id":"t3","name":"Grep","input":{"pattern":"foo","path":"."}},{"type":"tool_use","id":"t4","name":"Task","input":{}}]}}`,
		`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"t1","content":"ok"},{"type":"tool_result","tool_use_id":"t2","content":[{"type":"text","text":"line1"},{"type":"text","text":"line2"}],"is_error":true}]}}`,
		`not json`,
		`{"type":"mystery"}`,
		`{"type":"result","subtype":"error_during_execution","is_error":true,"result":"boom","total_cost_usd":0.5,"usage":{"input_tokens":1,"output_tokens":2,"cache_read_input_tokens":3}}`,
	}
	var evs []agent.Event
	for _, l := range lines {
		evs = append(evs, tr.Line([]byte(l))...)
	}
	k := kinds(evs)
	want := []agent.Kind{
		agent.KindReasoning,
		agent.KindToolCall, agent.KindToolCall, agent.KindToolCall, agent.KindToolCall,
		agent.KindToolResult, agent.KindFileChange, agent.KindToolResult,
		agent.KindError, agent.KindUsage, agent.KindTurnDone,
	}
	if len(k) != len(want) {
		t.Fatalf("kinds %v want %v", k, want)
	}
	for i := range want {
		if k[i] != want[i] {
			t.Fatalf("kinds %v want %v", k, want)
		}
	}
	if !evs[0].Delta || evs[0].Text != "hmm" {
		t.Errorf("reasoning %+v", evs[0])
	}
	titles := []string{"a.go", "go test ./...", "foo", ""}
	for i, ti := range titles[:3] {
		if evs[1+i].Tool.Title != ti {
			t.Errorf("title %d = %q want %q", i, evs[1+i].Tool.Title, ti)
		}
	}
	if evs[6].Diff.Added != 2 || evs[6].Diff.Removed != 1 || evs[6].Diff.New {
		t.Errorf("edit diff %+v", evs[6].Diff)
	}
	if evs[7].Tool.Status != agent.ToolError || evs[7].Tool.Output != "line1\nline2" || evs[7].Tool.Name != "Bash" {
		t.Errorf("error result %+v", evs[7].Tool)
	}
	if evs[8].Text != "boom" || evs[9].Usage.CostUSD != 0.5 {
		t.Errorf("result events %+v %+v", evs[8], evs[9].Usage)
	}
}

// ---- adapter tests against a fake claude (this test binary) ----

func init() {
	if os.Getenv("DECK_FAKE_CLAUDE") == "1" {
		fakeClaude()
		os.Exit(0)
	}
}

func fakeClaude() {
	if f := os.Getenv("DECK_FAKE_ARGS_FILE"); f != "" {
		fh, _ := os.OpenFile(f, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		b, _ := json.Marshal(os.Args[1:])
		fh.Write(append(b, '\n'))
		fh.Close()
		os.WriteFile(f+".env", []byte(os.Getenv("DECK_APPROVE_TOKEN")), 0o600)
	}
	data, _ := os.ReadFile(filepath.Join(os.Getenv("DECK_FAKE_DIR"), "testdata", "session1.jsonl"))
	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	for sc.Scan() {
		if strings.Contains(sc.Text(), "HANG") {
			time.Sleep(time.Hour)
		}
		os.Stdout.Write(data)
	}
}

func waitKind(t *testing.T, r agent.Run, k agent.Kind) []agent.Event {
	t.Helper()
	var evs []agent.Event
	timeout := time.After(15 * time.Second)
	for {
		select {
		case ev, ok := <-r.Events():
			if !ok {
				t.Fatalf("events closed before %s; got %v", k, kinds(evs))
			}
			evs = append(evs, ev)
			if ev.Kind == k {
				return evs
			}
		case <-timeout:
			t.Fatalf("timeout waiting for %s; got %v", k, kinds(evs))
		}
	}
}

func newFake(t *testing.T) (agent.Adapter, agent.Spec, string) {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	broker, err := approve.NewBroker()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(broker.Close)
	wd, _ := os.Getwd()
	argsFile := filepath.Join(t.TempDir(), "args.jsonl")
	spec := agent.Spec{
		ID: "claude7", Dir: t.TempDir(), Model: "sonnet", Binary: self,
		UserName: "Dami", TellName: true,
		Env: map[string]string{"DECK_FAKE_CLAUDE": "1", "DECK_FAKE_ARGS_FILE": argsFile, "DECK_FAKE_DIR": wd},
	}
	return New("", self, broker), spec, argsFile
}

func readArgs(t *testing.T, f string) [][]string {
	t.Helper()
	b, err := os.ReadFile(f)
	if err != nil {
		t.Fatal(err)
	}
	var out [][]string
	for _, l := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		var a []string
		if err := json.Unmarshal([]byte(l), &a); err != nil {
			t.Fatal(err)
		}
		out = append(out, a)
	}
	return out
}

func argAfter(args []string, flag string) (string, bool) {
	for i, a := range args {
		if a == flag && i+1 < len(args) {
			return args[i+1], true
		}
	}
	return "", false
}

func TestAdapterStartSendClose(t *testing.T) {
	t.Setenv("DECK_APPROVE_TOKEN", "leaked-from-parent-env")
	a, spec, argsFile := newFake(t)
	if a.Name() != "claude" {
		t.Fatal(a.Name())
	}
	r, err := a.Start(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Send("hello"); err != nil {
		t.Fatal(err)
	}
	evs := waitKind(t, r, agent.KindTurnDone)
	if evs[0].Kind != agent.KindUserText || evs[0].Text != "hello" {
		t.Errorf("first event %+v", evs[0])
	}
	if r.SessionID() == "" {
		t.Error("session id not tracked")
	}

	args := readArgs(t, argsFile)[0]
	for _, f := range []string{"-p", "--verbose", "--include-partial-messages"} {
		if !contains(args, f) {
			t.Errorf("missing %s in %v", f, args)
		}
	}
	if v, _ := argAfter(args, "--input-format"); v != "stream-json" {
		t.Errorf("input-format %q", v)
	}
	if v, _ := argAfter(args, "--output-format"); v != "stream-json" {
		t.Errorf("output-format %q", v)
	}
	if v, _ := argAfter(args, "--permission-prompt-tool"); v != "mcp__deck__approve" {
		t.Errorf("permission-prompt-tool %q", v)
	}
	if v, _ := argAfter(args, "--model"); v != "sonnet" {
		t.Errorf("model %q", v)
	}
	if v, _ := argAfter(args, "--append-system-prompt"); v != "The user's name is Dami." {
		t.Errorf("system prompt %q", v)
	}
	if contains(args, "--resume") {
		t.Error("unexpected --resume")
	}
	cfg, ok := argAfter(args, "--mcp-config")
	if !ok {
		t.Fatal("no --mcp-config")
	}
	raw, err := os.ReadFile(cfg)
	if err != nil {
		t.Fatalf("config missing while running: %v", err)
	}
	var parsed struct {
		MCPServers map[string]struct {
			Command string            `json:"command"`
			Args    []string          `json:"args"`
			Env     map[string]string `json:"env"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatal(err)
	}
	d := parsed.MCPServers["deck"]
	if d.Command == "" || len(d.Args) != 5 || d.Args[0] != "mcp-approve" || d.Args[4] != "claude7" {
		t.Errorf("mcp config %+v", d)
	}
	if d.Env[approve.TokenEnv] == "" || strings.Contains(strings.Join(args, " "), d.Env[approve.TokenEnv]) || strings.Contains(strings.Join(d.Args, " "), d.Env[approve.TokenEnv]) {
		t.Errorf("token must be in env only: env=%v args=%v", d.Env, d.Args)
	}

	if b, err := os.ReadFile(argsFile + ".env"); err != nil || len(b) != 0 {
		t.Errorf("claude env check failed (err=%v) token=%q", err, b)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(cfg); !os.IsNotExist(err) {
		t.Errorf("config not deleted: %v", err)
	}
	if err := r.Send("x"); err == nil {
		t.Error("send after close should fail")
	}
	// events channel is closed
	for range r.Events() {
	}
}

func TestAdapterNoNameWhenNotAllowed(t *testing.T) {
	a, spec, argsFile := newFake(t)
	spec.TellName = false
	spec.Model = ""
	spec.ResumeID = "sess-123"
	spec.FirstPrompt = "go"
	r, err := a.Start(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	waitKind(t, r, agent.KindTurnDone)
	args := readArgs(t, argsFile)[0]
	if contains(args, "--append-system-prompt") || contains(args, "--model") {
		t.Errorf("unexpected flags %v", args)
	}
	if v, _ := argAfter(args, "--resume"); v != "sess-123" {
		t.Errorf("resume %q", v)
	}
}

func TestAdapterInterruptRestartsWithResume(t *testing.T) {
	a, spec, argsFile := newFake(t)
	r, err := a.Start(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if err := r.Send("first"); err != nil {
		t.Fatal(err)
	}
	waitKind(t, r, agent.KindTurnDone)
	sid := r.SessionID()
	if sid == "" {
		t.Fatal("no session id")
	}

	if err := r.Send("HANG please"); err != nil {
		t.Fatal(err)
	}
	waitKind(t, r, agent.KindUserText)
	if err := r.Interrupt(); err != nil {
		t.Fatal(err)
	}
	evs := waitKind(t, r, agent.KindTurnDone)
	for _, e := range evs {
		if e.Kind == agent.KindExited {
			t.Fatal("interrupt must not report exit")
		}
	}
	// agent is usable again, resumed on the same session
	if err := r.Send("again"); err != nil {
		t.Fatal(err)
	}
	waitKind(t, r, agent.KindTurnDone)
	all := readArgs(t, argsFile)
	if len(all) != 2 {
		t.Fatalf("expected 2 launches, got %d", len(all))
	}
	if v, _ := argAfter(all[1], "--resume"); v != sid {
		t.Errorf("restart resume = %q want %q", v, sid)
	}
}

func TestAdapterMissingBinary(t *testing.T) {
	broker, _ := approve.NewBroker()
	defer broker.Close()
	a := New("definitely-not-a-real-binary-xyz", "deck", broker)
	if _, err := a.Start(context.Background(), agent.Spec{ID: "c", Dir: t.TempDir()}); err == nil {
		t.Fatal("expected error")
	}
}

func TestAdapterUnexpectedExit(t *testing.T) {
	a, spec, _ := newFake(t)
	spec.Env["DECK_FAKE_CLAUDE"] = "" // plain test binary: exits quickly, non-claude output
	r, err := a.Start(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	waitKind(t, r, agent.KindExited)
}

func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}
