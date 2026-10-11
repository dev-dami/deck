package approve

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

// mcpClient runs MCPServe against b and returns a call function.
func mcpClient(t *testing.T, b *Broker, agentID string, approveTool bool) func(string) map[string]any {
	t.Helper()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	go func() { MCPServe(inR, outW, b.Addr(), b.Token(), agentID, approveTool); outW.Close() }()
	t.Cleanup(func() { inW.Close() })
	out := bufio.NewReader(outR)
	return func(line string) map[string]any {
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
}

func resultText(t *testing.T, m map[string]any) (string, bool) {
	t.Helper()
	res, ok := m["result"].(map[string]any)
	if !ok {
		t.Fatalf("no result: %v", m)
	}
	isErr, _ := res["isError"].(bool)
	return res["content"].([]any)[0].(map[string]any)["text"].(string), isErr
}

func toolNames(m map[string]any) string {
	var names []string
	for _, tl := range m["result"].(map[string]any)["tools"].([]any) {
		names = append(names, tl.(map[string]any)["name"].(string))
	}
	return strings.Join(names, ",")
}

func TestToolsListTwoToolsWithoutApproveFlag(t *testing.T) {
	b, _ := NewBroker()
	defer b.Close()
	call := mcpClient(t, b, "codex", false)
	if got := toolNames(call(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)); got != "search,execute" {
		t.Fatalf("tools/list without the flag = %q", got)
	}
	// approve is not callable either.
	m := call(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"approve","arguments":{"tool_name":"Bash","input":{}}}}`)
	if m["error"] == nil {
		t.Fatalf("approve must be unknown without the flag: %v", m)
	}

	call = mcpClient(t, b, "claude", true)
	if got := toolNames(call(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)); got != "search,execute,approve" {
		t.Fatalf("tools/list with the flag = %q", got)
	}
}

func TestMCPSearchExecuteRoundTrip(t *testing.T) {
	b, _ := NewBroker()
	defer b.Close()
	b.SetThreadReader(func(id string) string { return "thread for " + id })
	type got struct {
		agent, op, args string
	}
	var mu sync.Mutex
	var calls []got
	b.SetExecutor(func(agentID, op string, args json.RawMessage) (string, bool) {
		mu.Lock()
		calls = append(calls, got{agentID, op, string(args)})
		mu.Unlock()
		switch op {
		case "big":
			return strings.Repeat("x", 30<<10), false
		case "leak":
			return "token is " + b.Token(), false
		case "bad":
			return "bad op", true
		}
		return "ran " + op, false
	})
	call := mcpClient(t, b, "codex", false)

	// search travels as op "search".
	txt, isErr := resultText(t, call(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"search","arguments":{"query":"git log","limit":3}}}`))
	if txt != "ran search" || isErr {
		t.Fatalf("search %q %v", txt, isErr)
	}
	// execute forwards op and args.
	txt, isErr = resultText(t, call(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"execute","arguments":{"op":"git.log","args":{"n":5}}}}`))
	if txt != "ran git.log" || isErr {
		t.Fatalf("execute %q %v", txt, isErr)
	}
	// flattened args land under args.
	resultText(t, call(`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"execute","arguments":{"op":"git.show","ref":"HEAD"}}}`))
	mu.Lock()
	if len(calls) != 3 || calls[0] != (got{"codex", "search", `{"query":"git log","limit":3}`}) ||
		calls[1] != (got{"codex", "git.log", `{"n":5}`}) || calls[2] != (got{"codex", "git.show", `{"ref":"HEAD"}`}) {
		t.Fatalf("executor calls %+v", calls)
	}
	mu.Unlock()

	// executor errors, the cap and the token scrub.
	if txt, isErr = resultText(t, call(`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"execute","arguments":{"op":"bad"}}}`)); txt != "bad op" || !isErr {
		t.Fatalf("error op %q %v", txt, isErr)
	}
	txt, _ = resultText(t, call(`{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"execute","arguments":{"op":"big"}}}`))
	if len(txt) > maxExecOutput+200 || !strings.Contains(txt, "truncated at 20 KB") {
		t.Fatalf("big output not capped: %d bytes", len(txt))
	}
	txt, _ = resultText(t, call(`{"jsonrpc":"2.0","id":6,"method":"tools/call","params":{"name":"execute","arguments":{"op":"leak"}}}`))
	if strings.Contains(txt, b.Token()) || !strings.Contains(txt, "[redacted]") {
		t.Fatalf("token not scrubbed: %q", txt)
	}
	// missing op: an error pointing at search, nothing forwarded.
	txt, isErr = resultText(t, call(`{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"execute","arguments":{}}}`))
	if !isErr || !strings.Contains(txt, "search") {
		t.Fatalf("missing op %q %v", txt, isErr)
	}

	// thread.send goes to Messages() as send_message did.
	go func() {
		m := recvMsg(t, b.Messages())
		if m.From != "codex" || m.To != "claude" || m.Text != "hi" {
			t.Errorf("bad message %+v", m)
		}
		m.Respond("delivered to claude", nil)
	}()
	txt, isErr = resultText(t, call(`{"jsonrpc":"2.0","id":8,"method":"tools/call","params":{"name":"execute","arguments":{"op":"thread.send","args":{"to":"claude","message":"hi"}}}}`))
	if txt != "delivered to claude" || isErr {
		t.Fatalf("thread.send %q %v", txt, isErr)
	}
	go func() { recvMsg(t, b.Messages()).Respond("", errors.New("claude is not in this thread")) }()
	txt, isErr = resultText(t, call(`{"jsonrpc":"2.0","id":9,"method":"tools/call","params":{"name":"execute","arguments":{"op":"thread.send","to":"claude","message":"hi"}}}`))
	if txt != "claude is not in this thread" || !isErr {
		t.Fatalf("thread.send error %q %v", txt, isErr)
	}
	txt, isErr = resultText(t, call(`{"jsonrpc":"2.0","id":10,"method":"tools/call","params":{"name":"execute","arguments":{"op":"thread.send","args":{"to":"","message":"hi"}}}}`))
	if !isErr || !strings.Contains(txt, "to") {
		t.Fatalf("empty target should be an error, got %q", txt)
	}
	txt, isErr = resultText(t, call(`{"jsonrpc":"2.0","id":11,"method":"tools/call","params":{"name":"execute","arguments":{"op":"thread.read"}}}`))
	if txt != "thread for codex" || isErr {
		t.Fatalf("thread.read %q %v", txt, isErr)
	}
	mu.Lock()
	defer mu.Unlock()
	for _, c := range calls {
		if strings.HasPrefix(c.op, "thread.") {
			t.Fatalf("%s must not reach the executor", c.op)
		}
	}
}

func TestExecWithoutExecutorAndTimeout(t *testing.T) {
	b, _ := NewBroker()
	defer b.Close()
	s, _ := rawReq(t, b.Addr(), wireRequest{Token: b.Token(), Kind: "exec", AgentID: "a", Op: "git.status"})
	var r wireReply
	if json.Unmarshal([]byte(s), &r) != nil || !r.IsError {
		t.Fatalf("no executor reply %q", s)
	}

	old := execTimeout
	execTimeout = 100 * time.Millisecond
	defer func() { execTimeout = old }()
	release := make(chan struct{})
	defer close(release)
	b.SetExecutor(func(string, string, json.RawMessage) (string, bool) { <-release; return "late", false })
	s, _ = rawReq(t, b.Addr(), wireRequest{Token: b.Token(), Kind: "exec", AgentID: "a", Op: "gh.prs"})
	if json.Unmarshal([]byte(s), &r) != nil || !r.IsError || !strings.Contains(r.Text, "too long") {
		t.Fatalf("timeout reply %q", s)
	}
}
