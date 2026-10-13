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

	"github.com/dev-dami/deck/internal/deck/agent"
	"github.com/dev-dami/deck/internal/deck/approve"
	"github.com/dev-dami/deck/internal/deck/brand"
	"github.com/dev-dami/deck/internal/gitutil"
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

// isMidTurnUsage reports whether ev is a context-only usage event.
func isMidTurnUsage(ev agent.Event) bool {
	u := ev.Usage
	return ev.Kind == agent.KindUsage && u != nil && u.InputTokens == 0 && u.OutputTokens == 0 && u.CachedTokens == 0 && u.CostUSD == 0
}

// withoutMidTurnUsage drops the context-only usage events.
func withoutMidTurnUsage(evs []agent.Event) []agent.Event {
	var out []agent.Event
	for _, e := range evs {
		if !isMidTurnUsage(e) {
			out = append(out, e)
		}
	}
	return out
}

func TestReplaySession1(t *testing.T) {
	evs := withoutMidTurnUsage(replay(t, filepath.Join("testdata", "session1.jsonl")))
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
		if strings.Contains(sc.Text(), `"control_request"`) {
			// Deck's get_usage probe: the fake has no answer.
			if f := os.Getenv("DECK_FAKE_PROBE_FILE"); f != "" {
				if fh, err := os.OpenFile(f, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600); err == nil {
					fh.WriteString(sc.Text() + "\n")
					fh.Close()
				}
			}
			continue
		}
		if f := os.Getenv("DECK_FAKE_STDIN_FILE"); f != "" {
			// record each stdin line (one per user message), across restarts
			if fh, err := os.OpenFile(f, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600); err == nil {
				fh.WriteString(sc.Text() + "\n")
				fh.Close()
			}
		}
		if strings.Contains(sc.Text(), "HANG") {
			time.Sleep(time.Hour)
		}
		if strings.Contains(sc.Text(), "SLOW") {
			time.Sleep(700 * time.Millisecond)
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
	if !contains(args, "--strict-mcp-config") {
		t.Error("no --strict-mcp-config: the user's own MCP servers would load too")
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
	if d.Command == "" || len(d.Args) != 6 || d.Args[0] != "mcp-approve" || d.Args[4] != "claude7" || d.Args[5] != approve.ApproveToolFlag {
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
	probes := filepath.Join(t.TempDir(), "probes.jsonl")
	spec.Env["DECK_FAKE_PROBE_FILE"] = probes
	defer func() {
		// One get_usage probe per run, not per process.
		b, _ := os.ReadFile(probes)
		if n := strings.Count(string(b), "\n"); n != 1 || !strings.Contains(string(b), `"get_usage"`) {
			t.Errorf("probes sent: %q", b)
		}
	}()
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

func TestAdapterAllowedToolsAndInstructions(t *testing.T) {
	a, spec, argsFile := newFake(t)
	spec.Instructions = "You share a thread with codex."
	spec.FirstPrompt = "go"
	r, err := a.Start(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	waitKind(t, r, agent.KindTurnDone)
	args := readArgs(t, argsFile)[0]
	if v, _ := argAfter(args, "--allowedTools"); v != "mcp__deck__search,mcp__deck__execute" {
		t.Errorf("allowedTools %q", v)
	}
	if v, _ := argAfter(args, "--permission-prompt-tool"); v != "mcp__deck__approve" {
		t.Errorf("permission-prompt-tool %q", v)
	}
	if v, _ := argAfter(args, "--append-system-prompt"); v != "The user's name is Dami.\n\nYou share a thread with codex." {
		t.Errorf("system prompt %q", v)
	}
	// Instructions alone (name not allowed)
	spec.TellName = false
	r2, err := a.Start(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	defer r2.Close()
	waitKind(t, r2, agent.KindTurnDone)
	all := readArgs(t, argsFile)
	if v, _ := argAfter(all[len(all)-1], "--append-system-prompt"); v != "You share a thread with codex." {
		t.Errorf("instructions-only system prompt %q", v)
	}
}

func TestAdapterUsesSpecMCP(t *testing.T) {
	a, spec, argsFile := newFake(t)
	spec.MCP = &agent.MCPServer{
		Name:    "deck",
		Command: "/opt/deck",
		Args:    []string{"mcp-approve", "--addr", "127.0.0.1:9", "--agent", "claude7"},
		Env:     map[string]string{approve.TokenEnv: "spec-token"},
	}
	spec.FirstPrompt = "go"
	r, err := a.Start(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	waitKind(t, r, agent.KindTurnDone)
	args := readArgs(t, argsFile)[0]
	cfg, _ := argAfter(args, "--mcp-config")
	raw, err := os.ReadFile(cfg)
	if err != nil {
		t.Fatal(err)
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
	if d.Command != "/opt/deck" || len(d.Args) != 6 || d.Args[5] != approve.ApproveToolFlag || d.Args[2] != "127.0.0.1:9" || d.Env[approve.TokenEnv] != "spec-token" {
		t.Errorf("config does not reflect spec.MCP: %+v", d)
	}
	if len(spec.MCP.Args) != 5 {
		t.Errorf("the caller's spec.MCP was changed: %v", spec.MCP.Args)
	}
	if strings.Contains(strings.Join(args, " "), "spec-token") {
		t.Error("token on the command line")
	}
}

func TestTranslateMessagingTools(t *testing.T) {
	tr := newTranslator("c1")
	long := strings.Repeat("abcdefghij", 10)
	lines := []string{
		`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"m1","name":"mcp__deck__execute","input":{"op":"thread.send","args":{"to":"codex","message":"` + long + `"}}},{"type":"tool_use","id":"m2","name":"mcp__deck__execute","input":{"op":"thread.read"}}]}}`,
		`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"m1","content":"delivered to codex"},{"type":"tool_result","tool_use_id":"m2","content":"thread"}]}}`,
		`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"m3","name":"mcp__deck__execute","input":{"op":"git.log","args":{"n":5}}},{"type":"tool_use","id":"m4","name":"mcp__deck__search","input":{"query":"pdf"}}]}}`,
	}
	var evs []agent.Event
	for _, l := range lines {
		evs = append(evs, tr.Line([]byte(l))...)
	}
	if len(evs) != 6 {
		t.Fatalf("events %v", kinds(evs))
	}
	if c := evs[0].Tool; evs[0].Kind != agent.KindToolCall || c.Name != "message" || c.Title != "to codex: "+long[:60] {
		t.Errorf("thread.send call %+v", c)
	}
	if c := evs[1].Tool; c.Name != "read" || c.Title != "shared thread" {
		t.Errorf("thread.read call %+v", c)
	}
	if c := evs[2].Tool; c.Name != "message" || c.Output != "delivered to codex" || c.Status != agent.ToolOK {
		t.Errorf("thread.send result %+v", c)
	}
	if c := evs[3].Tool; c.Name != "read" {
		t.Errorf("thread.read result %+v", c)
	}
	if c := evs[4].Tool; c.Name != "deck" || c.Title != `git.log {"n":5}` {
		t.Errorf("other op %+v", c)
	}
	if c := evs[5].Tool; c.Name != "deck" || c.Title != "search pdf" {
		t.Errorf("search %+v", c)
	}
}

func TestContextUsageFromFixture(t *testing.T) {
	evs := replay(t, filepath.Join("testdata", "session1.jsonl"))
	var mid, final []*agent.Usage
	for _, e := range evs {
		switch {
		case isMidTurnUsage(e):
			mid = append(mid, e.Usage)
		case e.Kind == agent.KindUsage:
			final = append(final, e.Usage)
		}
	}
	// Per assistant message: input + cache creation + cache read + output.
	// msg 1: 2+10308+28579+16 (message_start), then output 109 (message_delta).
	// msg 2: 2+2117+38887+8, then output 44. The repeated assistant line of
	// msg 2 changes nothing and yields no event.
	wantCtx := []int{38905, 38998, 41014, 41050}
	if len(mid) != len(wantCtx) {
		t.Fatalf("mid-turn usage events = %d want %d", len(mid), len(wantCtx))
	}
	for i, u := range mid {
		if u.ContextTokens != wantCtx[i] {
			t.Errorf("mid[%d].ContextTokens = %d want %d", i, u.ContextTokens, wantCtx[i])
		}
		// no modelUsage seen yet: family fallback for claude-opus-*
		if u.ContextWindow != 200000 {
			t.Errorf("mid[%d].ContextWindow = %d want 200000 (fallback)", i, u.ContextWindow)
		}
	}
	if len(final) != 1 {
		t.Fatalf("final usage events = %d", len(final))
	}
	f := final[0]
	if f.ContextTokens != 41050 {
		t.Errorf("final ContextTokens = %d want 41050 (latest message, not the cumulative sum)", f.ContextTokens)
	}
	if f.ContextWindow != 1000000 {
		t.Errorf("final ContextWindow = %d want 1000000 (modelUsage.contextWindow)", f.ContextWindow)
	}
	if f.OutputTokens != 153 || f.CostUSD < 0.11 {
		t.Errorf("existing fields lost: %+v", f)
	}
}

func TestContextWindowKnownAfterFirstResult(t *testing.T) {
	// Replaying the session twice through one translator: the second turn's
	// mid-turn events already carry the window the first result reported.
	path := filepath.Join("testdata", "session1.jsonl")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	tr := newTranslator("c1")
	var second []agent.Event
	for pass := 0; pass < 2; pass++ {
		for _, l := range strings.Split(string(b), "\n") {
			evs := tr.Line([]byte(l))
			if pass == 1 {
				second = append(second, evs...)
			}
		}
	}
	for _, e := range second {
		if e.Kind == agent.KindUsage && e.Usage.ContextWindow != 1000000 {
			t.Errorf("second turn window = %d want 1000000", e.Usage.ContextWindow)
		}
	}
}

func TestFallbackWindow(t *testing.T) {
	cases := map[string]int{
		"claude-opus-4-1":            200000,
		"sonnet":                     200000,
		"haiku":                      200000,
		"claude-fable-1":             200000,
		"claude-sonnet-4-5[1m]":      1000000,
		"sonnet[1m]":                 1000000,
		"opus-1m":                    1000000,
		"claude-sonnet-4-5-20250929": 200000,
		"":                           0,
		"gpt-5":                      0,
	}
	for m, want := range cases {
		if got := fallbackWindow(m); got != want {
			t.Errorf("fallbackWindow(%q) = %d want %d", m, got, want)
		}
	}
}

func TestContextWindowFromModelUsageAndSubagentIgnored(t *testing.T) {
	tr := newTranslator("c1")
	lines := []string{
		// sub-agent chatter must not move the meter
		`{"type":"assistant","parent_tool_use_id":"toolu_x","message":{"model":"claude-haiku-9","usage":{"input_tokens":999999},"content":[{"type":"text","text":"sub"}]}}`,
		`{"type":"assistant","message":{"model":"claude-sonnet-4-5","usage":{"input_tokens":10,"cache_creation_input_tokens":20,"cache_read_input_tokens":30,"output_tokens":5},"content":[{"type":"text","text":"hi"}]}}`,
		`{"type":"result","subtype":"success","total_cost_usd":0.01,"usage":{"input_tokens":50,"output_tokens":9},"modelUsage":{"claude-sonnet-4-5":{"contextWindow":500000},"claude-haiku-9":{"contextWindow":123}}}`,
	}
	var evs []agent.Event
	for _, l := range lines {
		evs = append(evs, tr.Line([]byte(l))...)
	}
	// text, mid-turn usage, final usage, turn_done
	if got := kinds(evs); len(got) != 4 || got[1] != agent.KindUsage || got[2] != agent.KindUsage {
		t.Fatalf("kinds %v", got)
	}
	if u := evs[1].Usage; u.ContextTokens != 65 || u.ContextWindow != 200000 || u.CostUSD != 0 || u.OutputTokens != 0 {
		t.Errorf("mid-turn %+v", u)
	}
	if u := evs[2].Usage; u.ContextTokens != 65 || u.ContextWindow != 500000 || u.OutputTokens != 9 || u.CostUSD != 0.01 {
		t.Errorf("final %+v", u)
	}
}

func TestSessionStartedCarriesRunningModel(t *testing.T) {
	evs := replay(t, "testdata/session1.jsonl")
	for _, ev := range evs {
		if ev.Kind == agent.KindSessionStarted {
			if ev.Text != "claude-opus-5-5" {
				t.Fatalf("running model = %q, want claude-opus-5-5", ev.Text)
			}
			return
		}
	}
	t.Fatal("no session_started event")
}

func TestWriteSettingsRedirectsPrivateFolders(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	project := t.TempDir()
	for _, attribution := range []bool{false, true} {
		path, err := writeSettings(project, attribution)
		if err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(path)
		os.Remove(path)
		if err != nil {
			t.Fatal(err)
		}
		var got map[string]any
		if err := json.Unmarshal(data, &got); err != nil {
			t.Fatal(err)
		}
		memory := brand.MemoryDir(gitutil.RepoRoot(project))
		if got["autoMemoryDirectory"] != memory {
			t.Errorf("attribution=%v: autoMemoryDirectory = %v, want %q", attribution, got["autoMemoryDirectory"], memory)
		}
		if !strings.HasPrefix(memory, filepath.Join(home, ".deck", "memory")) {
			t.Errorf("memory dir %q is not under ~/.deck/memory", memory)
		}
		if got["plansDirectory"] != brand.PlansDir() {
			t.Errorf("attribution=%v: plansDirectory = %v, want %q", attribution, got["plansDirectory"], brand.PlansDir())
		}
		if _, err := os.Stat(memory); err != nil {
			t.Errorf("memory dir not created: %v", err)
		}
	}
}
