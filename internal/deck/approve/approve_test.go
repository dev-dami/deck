package approve

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/dev-dami/deck/internal/deck/agent"
)

func rawReq(t *testing.T, addr string, req wireRequest) (string, error) {
	t.Helper()
	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	b, _ := json.Marshal(req)
	c.Write(append(b, '\n'))
	c.SetReadDeadline(time.Now().Add(5 * time.Second))
	line, err := bufio.NewReader(c).ReadString('\n')
	return strings.TrimSpace(line), err
}

func recv(t *testing.T, ch <-chan agent.ApprovalRequest) agent.ApprovalRequest {
	t.Helper()
	select {
	case r := <-ch:
		return r
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for approval request")
		return agent.ApprovalRequest{}
	}
}

func TestWrongTokenRejected(t *testing.T) {
	b, err := NewBroker()
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	ch := b.Register("a1")
	resp, err := rawReq(t, b.Addr(), wireRequest{Token: "nope", AgentID: "a1", ToolName: "Bash", Input: json.RawMessage(`{"command":"ls"}`)})
	if err == nil || resp != "" {
		t.Fatalf("expected closed connection, got %q err=%v", resp, err)
	}
	select {
	case <-ch:
		t.Fatal("request must not be surfaced")
	case <-time.After(100 * time.Millisecond):
	}
}

func TestAllowDenyRoundTrip(t *testing.T) {
	b, _ := NewBroker()
	defer b.Close()
	ch := b.Register("a1")

	type res struct {
		s   string
		err error
	}
	out := make(chan res, 1)
	go func() {
		s, err := rawReq(t, b.Addr(), wireRequest{Token: b.Token(), AgentID: "a1", RequestID: "r1", ToolName: "Bash", Input: json.RawMessage(`{"command":"go test ./..."}`)})
		out <- res{s, err}
	}()
	req := recv(t, ch)
	if req.Kind != agent.ApproveCommand || req.Summary != "go test ./..." || req.ToolName != "Bash" || req.ID != "r1" || req.AgentID != "a1" {
		t.Fatalf("bad request %+v", req)
	}
	req.Respond(agent.Decision{Allow: true})
	r := <-out
	var w struct {
		Behavior     string          `json:"behavior"`
		UpdatedInput json.RawMessage `json:"updatedInput"`
	}
	if err := json.Unmarshal([]byte(r.s), &w); err != nil || w.Behavior != "allow" || string(w.UpdatedInput) != `{"command":"go test ./..."}` {
		t.Fatalf("bad allow reply %q %v", r.s, err)
	}

	go func() {
		s, err := rawReq(t, b.Addr(), wireRequest{Token: b.Token(), AgentID: "a1", RequestID: "r2", ToolName: "Edit", Input: json.RawMessage(`{"file_path":"x.go","old_string":"a\n","new_string":"b\n"}`)})
		out <- res{s, err}
	}()
	req = recv(t, ch)
	if req.Kind != agent.ApproveEdit || req.Summary != "x.go" || req.Diff == nil || req.Diff.Added != 1 || req.Diff.Removed != 1 {
		t.Fatalf("bad edit request %+v diff=%+v", req, req.Diff)
	}
	req.Respond(agent.Decision{Allow: false})
	r = <-out
	if r.s != `{"behavior":"deny","message":"Denied by user in Deck"}` {
		t.Fatalf("bad deny reply %q", r.s)
	}

	go func() {
		s, err := rawReq(t, b.Addr(), wireRequest{Token: b.Token(), AgentID: "a1", RequestID: "r3", ToolName: "WebFetch", Input: json.RawMessage(`{"url":"https://example.com"}`)})
		out <- res{s, err}
	}()
	req = recv(t, ch)
	if req.Kind != agent.ApproveOther {
		t.Fatalf("kind %v", req.Kind)
	}
	req.Respond(agent.Decision{Allow: false, Reason: "no network"})
	r = <-out
	if !strings.Contains(r.s, `"message":"no network"`) {
		t.Fatalf("reason lost: %q", r.s)
	}
}

func TestAlwaysAutoAllows(t *testing.T) {
	b, _ := NewBroker()
	defer b.Close()
	ch := b.Register("a1")
	other := b.Register("a2")

	done := make(chan string, 1)
	go func() {
		s, _ := rawReq(t, b.Addr(), wireRequest{Token: b.Token(), AgentID: "a1", ToolName: "Bash", Input: json.RawMessage(`{"command":"ls"}`)})
		done <- s
	}()
	recv(t, ch).Respond(agent.Decision{Allow: true, Always: true})
	<-done

	// Second Bash for a1 is auto-allowed without surfacing.
	s, err := rawReq(t, b.Addr(), wireRequest{Token: b.Token(), AgentID: "a1", ToolName: "Bash", Input: json.RawMessage(`{"command":"pwd"}`)})
	if err != nil || !strings.Contains(s, `"behavior":"allow"`) || !strings.Contains(s, "pwd") {
		t.Fatalf("expected auto allow, got %q %v", s, err)
	}
	select {
	case r := <-ch:
		t.Fatalf("auto-allowed request surfaced: %+v", r)
	default:
	}

	// A different agent is not affected.
	go func() {
		s, _ := rawReq(t, b.Addr(), wireRequest{Token: b.Token(), AgentID: "a2", ToolName: "Bash", Input: json.RawMessage(`{"command":"ls"}`)})
		done <- s
	}()
	recv(t, other).Respond(agent.Decision{Allow: false})
	if s := <-done; !strings.Contains(s, "deny") {
		t.Fatalf("a2 reply %q", s)
	}
}

func TestMCPRoundTrip(t *testing.T) {
	b, _ := NewBroker()
	defer b.Close()
	ch := b.Register("a1")

	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	served := make(chan error, 1)
	go func() { served <- MCPServe(inR, outW, b.Addr(), b.Token(), "a1", true); outW.Close() }()

	out := bufio.NewReader(outR)
	call := func(line string) map[string]any {
		t.Helper()
		io.WriteString(inW, line+"\n")
		s, err := out.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(s), &m); err != nil {
			t.Fatalf("bad json %q", s)
		}
		return m
	}

	m := call(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"claude","version":"1"}}}`)
	res := m["result"].(map[string]any)
	if res["protocolVersion"] != "2025-06-18" || res["serverInfo"].(map[string]any)["name"] != "deck" {
		t.Fatalf("initialize: %v", m)
	}
	if _, ok := res["capabilities"].(map[string]any)["tools"]; !ok {
		t.Fatal("no tools capability")
	}
	io.WriteString(inW, `{"jsonrpc":"2.0","method":"notifications/initialized"}`+"\n") // no reply expected

	m = call(`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	tools := m["result"].(map[string]any)["tools"].([]any)
	var names []string
	for _, tl := range tools {
		names = append(names, tl.(map[string]any)["name"].(string))
	}
	if strings.Join(names, ",") != "search,execute,approve" {
		t.Fatalf("tools/list: %v", m)
	}
	ex := tools[1].(map[string]any)
	if d := ex["description"].(string); !strings.Contains(d, "thread.send") || !strings.Contains(d, "search") || !strings.Contains(d, "user sees every call") {
		t.Errorf("execute description %q", d)
	}
	props := ex["inputSchema"].(map[string]any)["properties"].(map[string]any)
	if props["op"] == nil || props["args"] == nil {
		t.Errorf("execute schema %v", ex["inputSchema"])
	}
	sp := tools[0].(map[string]any)["inputSchema"].(map[string]any)["properties"].(map[string]any)
	if sp["query"] == nil || sp["limit"] == nil {
		t.Errorf("search schema %v", sp)
	}

	go func() {
		recv(t, ch).Respond(agent.Decision{Allow: true})
	}()
	m = call(`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"approve","arguments":{"tool_name":"Bash","input":{"command":"echo hi"},"tool_use_id":"toolu_1"}}}`)
	content := m["result"].(map[string]any)["content"].([]any)[0].(map[string]any)
	if content["type"] != "text" {
		t.Fatalf("content: %v", content)
	}
	var d struct {
		Behavior     string `json:"behavior"`
		UpdatedInput struct {
			Command string `json:"command"`
		} `json:"updatedInput"`
	}
	if err := json.Unmarshal([]byte(content["text"].(string)), &d); err != nil || d.Behavior != "allow" || d.UpdatedInput.Command != "echo hi" {
		t.Fatalf("decision %v %v", content["text"], err)
	}

	m = call(`{"jsonrpc":"2.0","id":4,"method":"nope"}`)
	if m["error"] == nil {
		t.Fatalf("expected error: %v", m)
	}

	inW.Close()
	if err := <-served; err != nil {
		t.Fatal(err)
	}
}

func TestBuildEditDiffCounts(t *testing.T) {
	d := BuildEditDiff("f.go", "a\nb\nc\nd\ne\n", "a\nb\nX\nY\nd\ne\n")
	if d.Added != 2 || d.Removed != 1 || d.New {
		t.Fatalf("counts %+v", d)
	}
	if len(d.Hunks) != 1 {
		t.Fatalf("hunks %+v", d.Hunks)
	}
	h := d.Hunks[0]
	if h.OldStart != 1 || h.OldLines != 5 || h.NewStart != 1 || h.NewLines != 6 {
		t.Fatalf("hunk header %+v", h)
	}

	n := BuildEditDiff("new.txt", "", "hi\nthere\n")
	if !n.New || n.Added != 2 || n.Removed != 0 || len(n.Hunks) != 1 || n.Hunks[0].OldLines != 0 {
		t.Fatalf("new file %+v", n)
	}

	same := BuildEditDiff("s", "a\n", "a\n")
	if same.Added != 0 || same.Removed != 0 || len(same.Hunks) != 0 {
		t.Fatalf("same %+v", same)
	}
}

func TestBuildEditDiffSeparateHunks(t *testing.T) {
	var old, nw []string
	for i := 0; i < 30; i++ {
		old = append(old, "line")
		nw = append(nw, "line")
	}
	old[2], nw[2] = "old1", "new1"
	old[25], nw[25] = "old2", "new2"
	d := BuildEditDiff("f", strings.Join(old, "\n"), strings.Join(nw, "\n"))
	if len(d.Hunks) != 2 || d.Added != 2 || d.Removed != 2 {
		t.Fatalf("%+v", d)
	}
	if d.Hunks[1].OldStart != 23 {
		t.Fatalf("second hunk start %d", d.Hunks[1].OldStart)
	}
}

func recvMsg(t *testing.T, ch <-chan agent.Message) agent.Message {
	t.Helper()
	select {
	case m := <-ch:
		return m
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for message")
		return agent.Message{}
	}
}

func TestMessageRoundTrip(t *testing.T) {
	b, _ := NewBroker()
	defer b.Close()
	var hub agent.Hub = b

	out := make(chan string, 1)
	go func() {
		s, _ := rawReq(t, b.Addr(), wireRequest{Token: b.Token(), Kind: "exec", AgentID: "claude", RequestID: "m1", Op: "thread.send", Args: json.RawMessage(`{"to":"codex","message":"hi there"}`)})
		out <- s
	}()
	m := recvMsg(t, hub.Messages())
	if m.From != "claude" || m.To != "codex" || m.Text != "hi there" || m.ID != "m1" {
		t.Fatalf("bad message %+v", m)
	}
	m.Respond("delivered to codex", nil)
	m.Respond("ignored second call", nil)
	if s := <-out; s != `{"text":"delivered to codex"}` {
		t.Fatalf("reply %q", s)
	}

	go func() {
		s, _ := rawReq(t, b.Addr(), wireRequest{Token: b.Token(), Kind: "exec", AgentID: "claude", Op: "thread.send", Args: json.RawMessage(`{"to":"nobody","message":"x"}`)})
		out <- s
	}()
	m = recvMsg(t, hub.Messages())
	m.Respond("", errors.New("no such agent: nobody"))
	if s := <-out; s != `{"text":"no such agent: nobody","is_error":true}` {
		t.Fatalf("error reply %q", s)
	}
}

func TestMessageTimeout(t *testing.T) {
	old := messageTimeout
	messageTimeout = 100 * time.Millisecond
	defer func() { messageTimeout = old }()
	b, _ := NewBroker()
	defer b.Close()

	s, err := rawReq(t, b.Addr(), wireRequest{Token: b.Token(), Kind: "exec", AgentID: "claude", Op: "thread.send", Args: json.RawMessage(`{"to":"codex","message":"x"}`)})
	if err != nil || s != `{"text":"Deck did not answer","is_error":true}` {
		t.Fatalf("timeout reply %q %v", s, err)
	}
	// a late Respond must not panic or block
	m := recvMsg(t, b.Messages())
	m.Respond("late", nil)
}

func TestReadThread(t *testing.T) {
	b, _ := NewBroker()
	defer b.Close()

	s, _ := rawReq(t, b.Addr(), wireRequest{Token: b.Token(), Kind: "exec", Op: "thread.read", AgentID: "claude"})
	if s != `{"text":"No shared thread."}` {
		t.Fatalf("no provider reply %q", s)
	}
	var gotID string
	b.SetThreadReader(func(id string) string { gotID = id; return "you: hi\ncodex: yo" })
	s, _ = rawReq(t, b.Addr(), wireRequest{Token: b.Token(), Kind: "exec", Op: "thread.read", AgentID: "claude2"})
	if s != `{"text":"you: hi\ncodex: yo"}` || gotID != "claude2" {
		t.Fatalf("reader reply %q id=%q", s, gotID)
	}
}

func TestWrongTokenRejectedNewKinds(t *testing.T) {
	b, _ := NewBroker()
	defer b.Close()
	b.SetThreadReader(func(string) string { return "secret transcript" })
	called := false
	b.SetExecutor(func(string, string, json.RawMessage) (string, bool) { called = true; return "secret", false })
	for _, op := range []string{"thread.send", "thread.read", "search", "git.status"} {
		s, err := rawReq(t, b.Addr(), wireRequest{Token: "nope", Kind: "exec", Op: op, AgentID: "a", Args: json.RawMessage(`{"to":"codex","message":"x"}`)})
		if err == nil || s != "" {
			t.Fatalf("%s: expected closed connection, got %q err=%v", op, s, err)
		}
	}
	if called {
		t.Fatal("executor ran for a wrong token")
	}
	select {
	case m := <-b.Messages():
		t.Fatalf("message must not be surfaced: %+v", m)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestServerFor(t *testing.T) {
	b, _ := NewBroker()
	defer b.Close()
	srv := b.ServerFor("/bin/deck", "claude3")
	if srv.Name != "deck" || srv.Command != "/bin/deck" {
		t.Fatalf("%+v", srv)
	}
	want := []string{"mcp-approve", "--addr", b.Addr(), "--agent", "claude3"}
	if strings.Join(srv.Args, "|") != strings.Join(want, "|") {
		t.Fatalf("args %v", srv.Args)
	}
	if srv.Env[TokenEnv] != b.Token() || len(srv.Env) != 1 {
		t.Fatalf("env %v", srv.Env)
	}
	if strings.Contains(strings.Join(srv.Args, " "), b.Token()) {
		t.Fatal("token leaked into args")
	}
}
