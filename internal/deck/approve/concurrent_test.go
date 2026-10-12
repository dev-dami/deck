package approve

import (
	"bufio"
	"encoding/json"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/dev-dami/deck/internal/deck/agent"
)

// A slow execute does not hold up an approval sent after it.
func TestMCPCallsRunConcurrently(t *testing.T) {
	b, _ := NewBroker()
	defer b.Close()
	ch := b.Register("a1")
	release := make(chan struct{})
	b.SetExecutor(func(string, string, json.RawMessage) (string, bool) {
		<-release
		return "slow done", false
	})

	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	served := make(chan error, 1)
	go func() { served <- MCPServe(inR, outW, b.Addr(), b.Token(), "a1", true); outW.Close() }()
	out := bufio.NewReader(outR)
	read := func() map[string]any {
		t.Helper()
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

	io.WriteString(inW, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"execute","arguments":{"op":"git.status"}}}`+"\n")
	io.WriteString(inW, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"approve","arguments":{"tool_name":"Bash","input":{"command":"ls"}}}}`+"\n")
	recv(t, ch).Respond(agent.Decision{Allow: true})
	m := read()
	if m["id"].(float64) != 2 {
		t.Fatalf("the approval waited behind the execute: %v", m)
	}
	close(release)
	m = read()
	if text, _ := resultText(t, m); m["id"].(float64) != 1 || text != "slow done" {
		t.Fatalf("execute reply %v", m)
	}
	inW.Close()
	if err := <-served; err != nil {
		t.Fatal(err)
	}
}

// A cancelled call stops waiting for Deck, and the broker lets go of it.
func TestMCPCancelledApprovalLetsGo(t *testing.T) {
	b, _ := NewBroker()
	defer b.Close()
	ch := b.Register("a1")
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	go func() { MCPServe(inR, outW, b.Addr(), b.Token(), "a1", true); outW.Close() }()
	defer inW.Close()
	out := bufio.NewReader(outR)

	io.WriteString(inW, `{"jsonrpc":"2.0","id":"x7","method":"tools/call","params":{"name":"approve","arguments":{"tool_name":"Bash","input":{"command":"ls"}}}}`+"\n")
	recv(t, ch) // surfaced; nobody answers
	io.WriteString(inW, `{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":"x7","reason":"interrupted"}}`+"\n")
	done := make(chan string, 1)
	go func() { s, _ := out.ReadString('\n'); done <- s }()
	select {
	case s := <-done:
		if !strings.Contains(s, `"x7"`) || !strings.Contains(s, "deny") {
			t.Fatalf("cancelled reply %q", s)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a cancelled approval kept waiting")
	}
}

func TestBrokerStopsWaitingForAGoneCaller(t *testing.T) {
	old := approvalTimeout
	approvalTimeout = 200 * time.Millisecond
	defer func() { approvalTimeout = old }()
	b, _ := NewBroker()
	defer b.Close()
	ch := b.Register("a1")

	// Nobody answers: the request is denied after approvalTimeout.
	go func() { recv(t, ch) }()
	s, err := rawReq(t, b.Addr(), wireRequest{Token: b.Token(), AgentID: "a1", ToolName: "Bash", Input: json.RawMessage(`{"command":"ls"}`)})
	if err != nil || !strings.Contains(s, "deny") {
		t.Fatalf("unanswered approval = %q %v", s, err)
	}

	// The caller hangs up: a late answer neither blocks nor panics.
	approvalTimeout = time.Hour
	c, err := net.Dial("tcp", b.Addr())
	if err != nil {
		t.Fatal(err)
	}
	req, _ := json.Marshal(wireRequest{Token: b.Token(), AgentID: "a1", ToolName: "Bash", Input: json.RawMessage(`{"command":"pwd"}`)})
	c.Write(append(req, '\n'))
	r := recv(t, ch)
	c.Close()
	time.Sleep(50 * time.Millisecond)
	r.Respond(agent.Decision{Allow: true})
}
