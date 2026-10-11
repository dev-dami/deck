package codex

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dev-dami/deck/internal/deck/agent"
)

const (
	stubMCPFlag  = "--deck-stub-mcp"
	testTokenEnv = "DECK_TEST_TOKEN"
	testToken    = "tok-0123456789-not-a-real-token"
)

// stubMCP is a tiny stdio MCP server (the test binary re-executed with
// stubMCPFlag). It exposes search and execute and records every tool
// call, plus whether the forwarded token env var reached it, to outPath.
func stubMCP(outPath string) {
	out, _ := os.OpenFile(outPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	record := func(s string) { out.WriteString(s + "\n") }
	if os.Getenv(testTokenEnv) == testToken {
		record("token=forwarded")
	} else {
		record("token=missing")
	}
	var wmu sync.Mutex
	w := bufio.NewWriter(os.Stdout)
	write := func(v any) {
		wmu.Lock()
		defer wmu.Unlock()
		b, _ := json.Marshal(v)
		w.Write(b)
		w.WriteByte('\n')
		w.Flush()
	}
	obj := func(props map[string]any, req ...string) map[string]any {
		return map[string]any{"type": "object", "properties": props, "required": req}
	}
	str := map[string]any{"type": "string"}
	tools := []map[string]any{
		{"name": "search", "description": "Search Deck.", "inputSchema": obj(map[string]any{"query": str})},
		{"name": "execute", "description": "Run one Deck op.",
			"inputSchema": obj(map[string]any{"op": str, "args": map[string]any{"type": "object"}}, "op")},
	}
	in := bufio.NewReaderSize(os.Stdin, 1<<20)
	for {
		line, err := in.ReadBytes('\n')
		var m struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if json.Unmarshal(line, &m) == nil && len(m.ID) > 0 {
			switch m.Method {
			case "initialize":
				var p struct {
					ProtocolVersion string `json:"protocolVersion"`
				}
				_ = json.Unmarshal(m.Params, &p)
				write(map[string]any{"jsonrpc": "2.0", "id": m.ID, "result": map[string]any{
					"protocolVersion": p.ProtocolVersion,
					"capabilities":    map[string]any{"tools": map[string]any{}},
					"serverInfo":      map[string]any{"name": "deck-stub", "version": "0"},
				}})
			case "tools/list":
				write(map[string]any{"jsonrpc": "2.0", "id": m.ID, "result": map[string]any{"tools": tools}})
			case "tools/call":
				var p struct {
					Name      string          `json:"name"`
					Arguments json.RawMessage `json:"arguments"`
				}
				_ = json.Unmarshal(m.Params, &p)
				record("call " + p.Name + " " + string(p.Arguments))
				text := "delivered"
				if p.Name == "search" {
					text = "(no ops)"
				}
				write(map[string]any{"jsonrpc": "2.0", "id": m.ID, "result": map[string]any{
					"content": []map[string]any{{"type": "text", "text": text}},
				}})
			default:
				write(map[string]any{"jsonrpc": "2.0", "id": m.ID, "result": map[string]any{}})
			}
		}
		if err != nil {
			return
		}
	}
}

func testMCP() *agent.MCPServer {
	return &agent.MCPServer{
		Name:    "deck",
		Command: `C:\Program Files\Deck\deck.exe`,
		Args:    []string{"mcp", "--agent", "codex"},
		Env:     map[string]string{testTokenEnv: testToken, "DECK_AGENT_ID": "codex"},
	}
}

func TestMCPThreadConfig(t *testing.T) {
	if mcpThreadConfig(nil) != nil || mcpThreadConfig(&agent.MCPServer{Name: "deck"}) != nil {
		t.Fatal("nil / commandless server must give no config")
	}
	cfg := mcpThreadConfig(testMCP())
	if cfg["mcp_servers.deck.command"] != `C:\Program Files\Deck\deck.exe` {
		t.Errorf("command = %v", cfg["mcp_servers.deck.command"])
	}
	if got := cfg["mcp_servers.deck.args"].([]string); strings.Join(got, " ") != "mcp --agent codex" {
		t.Errorf("args = %v", got)
	}
	if got := cfg["mcp_servers.deck.env_vars"].([]string); strings.Join(got, ",") != "DECK_AGENT_ID,DECK_TEST_TOKEN" {
		t.Errorf("env_vars = %v (want sorted names only)", got)
	}
	if cfg["mcp_servers.deck.default_tools_approval_mode"] != "approve" {
		t.Errorf("approval mode = %v", cfg["mcp_servers.deck.default_tools_approval_mode"])
	}
	if _, ok := cfg["mcp_servers.deck.env"]; ok {
		t.Error("env table must not be set: it would carry the token on the wire")
	}
	b, _ := json.Marshal(cfg)
	if strings.Contains(string(b), testToken) {
		t.Fatalf("token leaked into thread config: %s", b)
	}
	env := mcpProcessEnv(testMCP())
	if len(env) != 2 || env[1] != testTokenEnv+"="+testToken {
		t.Errorf("process env = %v", env)
	}
}

func TestThreadParamsCarryInstructionsAndConfig(t *testing.T) {
	p := threadStartParams{DeveloperInstructions: "You share a thread with claude.", Config: mcpThreadConfig(testMCP())}
	b, _ := json.Marshal(p)
	for _, want := range []string{`"developerInstructions":"You share a thread with claude."`, `"config":{`, `"mcp_servers.deck.command"`} {
		if !strings.Contains(string(b), want) {
			t.Errorf("thread/start params missing %s:\n%s", want, b)
		}
	}
	b, _ = json.Marshal(threadStartParams{Cwd: "x"})
	if strings.Contains(string(b), "developerInstructions") || strings.Contains(string(b), "config") {
		t.Errorf("empty instructions/config must be omitted: %s", b)
	}
}

func TestAdapterPassesMCPAndInstructions(t *testing.T) {
	for _, resume := range []bool{false, true} {
		fe := newFakeEnv(t, "testdata/session1.jsonl")
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		spec := agent.Spec{ID: "cx", Dir: t.TempDir(), Env: fe.env, MCP: testMCP(), Instructions: "Be a good teammate."}
		method := "thread/start="
		if resume {
			spec.ResumeID = "old-thread"
			method = "thread/resume="
		}
		run, err := New(os.Args[0]).Start(ctx, spec)
		if err != nil {
			cancel()
			t.Fatal(err)
		}
		out := fe.read(t)
		run.Close()
		cancel()

		if !strings.Contains(out, method) {
			t.Fatalf("no %s:\n%s", method, out)
		}
		for _, want := range []string{
			`"developerInstructions":"Be a good teammate."`,
			`"mcp_servers.deck.command":"C:\\Program Files\\Deck\\deck.exe"`,
			`"mcp_servers.deck.env_vars":["DECK_AGENT_ID","DECK_TEST_TOKEN"]`,
			`"mcp_servers.deck.default_tools_approval_mode":"approve"`,
		} {
			if !strings.Contains(out, want) {
				t.Errorf("resume=%v: server did not see %s:\n%s", resume, want, out)
			}
		}
		// Token only in the process environment: not in argv, not on the wire.
		if !strings.Contains(out, "tokenenv="+testToken) {
			t.Errorf("token not in app-server process env:\n%s", out)
		}
		if !strings.Contains(out, "args=app-server\n") {
			t.Errorf("app-server argv must be plain `app-server`:\n%s", out)
		}
		for _, line := range strings.Split(out, "\n") {
			if strings.HasPrefix(line, "args=") || strings.HasPrefix(line, "thread/") {
				if strings.Contains(line, testToken) {
					t.Errorf("token leaked into %q", line)
				}
			}
		}
	}
}

func TestAdapterNoMCPLeavesParamsUntouched(t *testing.T) {
	fe := newFakeEnv(t, "testdata/session1.jsonl")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	run, err := New(os.Args[0]).Start(ctx, agent.Spec{ID: "cx", Dir: t.TempDir(), Env: fe.env})
	if err != nil {
		t.Fatal(err)
	}
	defer run.Close()
	out := fe.read(t)
	if strings.Contains(out, "mcp_servers") || strings.Contains(out, "developerInstructions") {
		t.Errorf("unexpected mcp/instructions without a spec:\n%s", out)
	}
}

func TestIsDeckToolApproval(t *testing.T) {
	mk := func(server, msg, meta string) json.RawMessage {
		return json.RawMessage(`{"serverName":` + jsonString(server) + `,"threadId":"t","message":` + jsonString(msg) +
			`,"mode":"form","requestedSchema":{"type":"object","properties":{}},"_meta":` + meta + `}`)
	}
	cases := []struct {
		name string
		p    json.RawMessage
		want bool
	}{
		{"search by meta", mk("deck", "Allow?", `{"codex_approval_kind":"mcp_tool_call","tool_name":"search"}`), true},
		{"execute by title", mk("deck", "Allow?", `{"codex_approval_kind":"mcp_tool_call","tool_title":"execute"}`), true},
		{"execute by message", mk("deck", `Allow the deck MCP server to run tool "execute"?`, `null`), true},
		{"approve tool", mk("deck", "Allow?", `{"codex_approval_kind":"mcp_tool_call","tool_name":"approve"}`), false},
		{"old tool name", mk("deck", "Allow?", `{"codex_approval_kind":"mcp_tool_call","tool_name":"send_message"}`), false},
		{"other server same tool", mk("evil", "Allow?", `{"codex_approval_kind":"mcp_tool_call","tool_name":"execute"}`), false},
		{"meta tool beats message", mk("deck", "run execute", `{"tool_name":"approve"}`), false},
		{"unidentifiable tool", mk("deck", "Allow the tool?", `{}`), false},
		{"two tools in message", mk("deck", "search and execute", `null`), false},
		{"other approval kind", mk("deck", "x", `{"codex_approval_kind":"plugin_install","tool_name":"execute"}`), false},
		{"garbage", json.RawMessage(`[1]`), false},
	}
	for _, c := range cases {
		if got := isDeckToolApproval(c.p); got != c.want {
			t.Errorf("%s: got %v want %v", c.name, got, c.want)
		}
	}
}

func TestAdapterElicitationAutoAcceptsOnlyDeckTools(t *testing.T) {
	el := func(id int, server, tool string) string {
		return recv(`{"id":` + itoa(id) + `,"method":"mcpServer/elicitation/request","params":{"serverName":"` + server +
			`","threadId":"thr-syn","turnId":"turn-syn","message":"Allow tool call?","mode":"form",` +
			`"requestedSchema":{"type":"object","properties":{}},"_meta":{"codex_approval_kind":"mcp_tool_call","tool_name":"` + tool + `"}}}`)
	}
	script := writeScript(t,
		send(`{"id":1,"method":"initialize","params":{}}`),
		recv(`{"id":1,"result":{"userAgent":"x"}}`),
		send(`{"id":2,"method":"thread/start","params":{}}`),
		recv(`{"id":2,"result":{"thread":{"id":"thr-syn"}}}`),
		send(`{"id":3,"method":"turn/start","params":{}}`),
		recv(`{"id":3,"result":{"turn":{"id":"turn-syn","status":"inProgress"}}}`),
		recv(`{"method":"turn/started","params":{"threadId":"thr-syn","turn":{"id":"turn-syn","status":"inProgress"}}}`),
		el(21, "deck", "execute"),
		el(22, "deck", "search"),
		el(23, "deck", "approve"),
		el(24, "github", "execute"),
		recv(`{"method":"turn/completed","params":{"threadId":"thr-syn","turn":{"id":"turn-syn","status":"completed"}}}`),
	)
	fe := newFakeEnv(t, script)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	run, err := New(os.Args[0]).Start(ctx, agent.Spec{ID: "cx", Dir: t.TempDir(), Env: fe.env, FirstPrompt: "go"})
	if err != nil {
		t.Fatal(err)
	}
	defer run.Close()
	evs, mu := drain(run)
	waitFor(t, "turn_done", func() bool { return hasKind(evs, mu, agent.KindTurnDone) })
	out := fe.read(t)
	for _, want := range []string{
		`"id":21,"result":{"action":"accept"}`,
		`"id":22,"result":{"action":"accept"}`,
		`"id":23,"result":{"action":"decline"}`,
		`"id":24,"result":{"action":"decline"}`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("fake did not see %s:\n%s", want, out)
		}
	}
	select {
	case req := <-run.Approvals():
		t.Errorf("deck tool surfaced a user approval: %+v", req)
	default:
	}
}

func itoa(n int) string { b, _ := json.Marshal(n); return string(b) }

func TestTranslateDeckMessageToolCall(t *testing.T) {
	tr := newTranslator("c1", t.TempDir())
	args := `{"op":"thread.send","args":{"to":"claude","message":"please review the parser change in internal/parse and tell me what you think about the edge cases\nthanks"}}`
	item := func(status, extra string) string {
		return `{"threadId":"t","turnId":"u","item":{"type":"mcpToolCall","id":"m1","server":"deck","tool":"execute","status":"` +
			status + `","arguments":` + args + extra + `}}`
	}
	started := notif(t, tr, noteItemStarted, item("inProgress", ""))
	if len(started) != 1 || started[0].Kind != agent.KindToolCall {
		t.Fatalf("started = %+v", started)
	}
	tool := started[0].Tool
	wantTitle := "to claude: please review the parser change in internal/parse and tell m"
	if tool.Name != "message" || tool.Title != wantTitle || tool.Status != agent.ToolRunning || tool.ID != "m1" {
		t.Fatalf("started tool = %+v", tool)
	}
	if len([]rune(strings.TrimPrefix(tool.Title, "to claude: "))) != 60 {
		t.Errorf("message not cut to 60 runes: %q", tool.Title)
	}
	done := notif(t, tr, noteItemCompleted, item("completed", `,"result":{"content":[{"type":"text","text":"delivered to claude"}]}`))
	if len(done) != 1 || done[0].Kind != agent.KindToolResult {
		t.Fatalf("done = %+v", done)
	}
	res := done[0].Tool
	if res.Name != "message" || res.Title != wantTitle || res.Output != "delivered to claude" || res.Status != agent.ToolOK || string(res.Input) != args {
		t.Fatalf("result tool = %+v", res)
	}
}

func TestTranslateDeckToolErrorsAndReadThread(t *testing.T) {
	tr := newTranslator("c1", t.TempDir())
	mk := func(id, tool, status, args, extra string) string {
		return `{"threadId":"t","turnId":"u","item":{"type":"mcpToolCall","id":"` + id + `","server":"deck","tool":"` + tool +
			`","status":"` + status + `","arguments":` + args + extra + `}}`
	}
	// thread.read
	s := notif(t, tr, noteItemStarted, mk("r1", "execute", "inProgress", `{"op":"thread.read"}`, ""))
	if len(s) != 1 || s[0].Tool.Name != "read" || s[0].Tool.Title != "shared thread" {
		t.Fatalf("read started = %+v", s)
	}
	d := notif(t, tr, noteItemCompleted, mk("r1", "execute", "completed", `{"op":"thread.read"}`, `,"result":{"content":[{"type":"text","text":"[you] hi"},{"type":"text","text":"[claude] yo"}]}`))
	if d[0].Tool.Name != "read" || d[0].Tool.Title != "shared thread" || d[0].Tool.Output != "[you] hi\n[claude] yo" || d[0].Tool.Status != agent.ToolOK {
		t.Fatalf("read done = %+v", d[0].Tool)
	}
	// failed thread.send (flattened args) with no preceding item/started: still translated.
	d = notif(t, tr, noteItemCompleted, mk("s1", "execute", "failed", `{"op":"thread.send","to":"all","message":"hi"}`, `,"error":{"message":"hub closed"}`))
	if len(d) != 1 || d[0].Tool.Name != "message" || d[0].Tool.Title != "to all: hi" || d[0].Tool.Status != agent.ToolError || d[0].Tool.Output != "hub closed" {
		t.Fatalf("failed done = %+v", d[0].Tool)
	}
	// search and other ops render under "deck".
	s = notif(t, tr, noteItemStarted, mk("q1", "search", "inProgress", `{"query":"git log"}`, ""))
	if len(s) != 1 || s[0].Tool.Name != "deck" || s[0].Tool.Title != "search git log" {
		t.Fatalf("search started = %+v", s[0].Tool)
	}
	s = notif(t, tr, noteItemStarted, mk("g1", "execute", "inProgress", `{"op":"git.status"}`, ""))
	if len(s) != 1 || s[0].Tool.Name != "deck" || s[0].Tool.Title != "git.status" {
		t.Fatalf("op started = %+v", s[0].Tool)
	}
}

func TestTranslateOtherMCPServerUnchanged(t *testing.T) {
	tr := newTranslator("c1", t.TempDir())
	params := `{"threadId":"t","turnId":"u","item":{"type":"mcpToolCall","id":"x1","server":"other","tool":"execute","status":"inProgress","arguments":{"op":"thread.send","args":{"to":"a","message":"b"}}}}`
	s := notif(t, tr, noteItemStarted, params)
	if len(s) != 1 || s[0].Tool.Name != "execute" || s[0].Tool.Title != "other/execute" {
		t.Fatalf("started = %+v", s[0].Tool)
	}
	// And an unknown tool on the deck server keeps the generic rendering.
	s = notif(t, tr, noteItemStarted, strings.Replace(strings.Replace(params, `"other"`, `"deck"`, 1), `"execute"`, `"approve"`, 1))
	if s[0].Tool.Name != "approve" || s[0].Tool.Title != "deck/approve" {
		t.Fatalf("deck/approve = %+v", s[0].Tool)
	}
}

// TestLiveDeckMCPServerVisible starts the real codex app-server with the stub
// MCP server registered through thread/start config, then (without any model
// call) asks codex for the server's tool inventory and calls execute
// through mcpServer/tool/call. It never reads credentials and does not touch
// ~/.codex/config.toml. Gated: set DECK_LIVE=1.
func TestLiveDeckMCPServerVisible(t *testing.T) {
	if os.Getenv("DECK_LIVE") == "" {
		t.Skip("set DECK_LIVE=1 to run against the installed codex")
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	outPath := filepath.Join(t.TempDir(), "stub.out")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	mcp := testMCP()
	mcp.Command = exe
	mcp.Args = []string{stubMCPFlag, outPath}

	r, err := New("codex").Start(ctx, agent.Spec{
		ID: "live", Dir: t.TempDir(), MCP: mcp, Instructions: "You share a thread with claude.",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	rr := r.(*run)
	go func() { // the event loop owns Incoming(); keep events flowing
		for range r.Events() {
		}
	}()
	tid := r.SessionID()

	var status struct {
		Data []struct {
			Name          string                     `json:"name"`
			RuntimeStatus string                     `json:"runtimeStatus"`
			Tools         map[string]json.RawMessage `json:"tools"`
			ToolsError    string                     `json:"toolsError"`
		} `json:"data"`
	}
	if err := rr.client.Call(ctx, "mcpServerStatus/list", map[string]any{"threadId": tid, "serverName": "deck"}, &status); err != nil {
		t.Fatalf("mcpServerStatus/list: %v", err)
	}
	if len(status.Data) != 1 {
		t.Fatalf("status = %+v", status)
	}
	s := status.Data[0]
	t.Logf("server %q runtime=%s tools=%d toolsError=%q", s.Name, s.RuntimeStatus, len(s.Tools), s.ToolsError)
	var names []string
	for n := range s.Tools {
		names = append(names, n)
	}
	t.Logf("tools: %v", names)
	if s.Tools["search"] == nil || s.Tools["execute"] == nil {
		t.Fatalf("deck tools not visible to codex: %v (toolsError=%q)", names, s.ToolsError)
	}

	var call struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	if err := rr.client.Call(ctx, "mcpServer/tool/call", map[string]any{
		"threadId": tid, "server": "deck", "tool": "execute",
		"arguments": map[string]any{"op": "thread.send", "args": map[string]any{"to": "claude", "message": "hello from codex"}},
	}, &call); err != nil {
		t.Fatalf("mcpServer/tool/call: %v", err)
	}
	if call.IsError || len(call.Content) == 0 || call.Content[0].Text != "delivered" {
		t.Fatalf("call result = %+v", call)
	}
	b, _ := os.ReadFile(outPath)
	rec := string(b)
	if !strings.Contains(rec, `call execute `) || !strings.Contains(rec, `"op":"thread.send"`) || !strings.Contains(rec, `"message":"hello from codex"`) {
		t.Errorf("stub did not record the call:\n%s", rec)
	}
	if !strings.Contains(rec, "token=forwarded") {
		t.Errorf("token env var was not forwarded to the MCP server via env_vars:\n%s", rec)
	}
}
