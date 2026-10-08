package claude

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dev-dami/deck/internal/agent"
	"github.com/dev-dami/deck/internal/approve"
)

// TestMain lets the test binary double as `deck mcp-approve` for the live tests.
func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == "mcp-approve" {
		var addr, id string
		for i := 2; i+1 < len(os.Args); i += 2 {
			switch os.Args[i] {
			case "--addr":
				addr = os.Args[i+1]
			case "--agent":
				id = os.Args[i+1]
			}
		}
		if err := approve.MCPServe(os.Stdin, os.Stdout, addr, approve.TokenFromEnv(), id); err != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func liveRun(t *testing.T, mode string, extra []string, out string, onApproval func(agent.ApprovalRequest)) []agent.Event {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	broker, err := approve.NewBroker()
	if err != nil {
		t.Fatal(err)
	}
	defer broker.Close()

	a := New("", self, broker).(*adapter)
	a.extraArgs = extra
	var raw bytes.Buffer
	a.tap = func(l []byte) { raw.Write(l) }

	dir := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	r, err := a.Start(ctx, agent.Spec{ID: "claude1", Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()

	if onApproval != nil {
		go func() {
			for req := range r.Approvals() {
				onApproval(req)
			}
		}()
	}
	if err := r.Send("Create hello.txt containing hi, then reply DONE."); err != nil {
		t.Fatal(err)
	}
	var evs []agent.Event
	timeout := time.After(4 * time.Minute)
loop:
	for {
		select {
		case ev, ok := <-r.Events():
			if !ok {
				break loop
			}
			evs = append(evs, ev)
			if ev.Kind == agent.KindTurnDone || ev.Kind == agent.KindExited {
				break loop
			}
		case <-timeout:
			t.Fatal("timeout")
		}
	}
	if out != "" {
		if err := os.WriteFile(out, raw.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if b, err := os.ReadFile(filepath.Join(dir, "hello.txt")); err != nil || len(b) == 0 {
		t.Logf("hello.txt missing or empty (err=%v)", err)
	}
	return evs
}

// TestLiveRecord records testdata/session1.jsonl. It spends a little real Claude
// usage, so it only runs with DECK_LIVE=1.
func TestLiveRecord(t *testing.T) {
	if os.Getenv("DECK_LIVE") != "1" {
		t.Skip("set DECK_LIVE=1 to record a real session")
	}
	evs := liveRun(t, "record", []string{"--permission-mode", "acceptEdits"}, filepath.Join("testdata", "session1.jsonl"), nil)
	for _, e := range evs {
		t.Logf("%s %q", e.Kind, e.Text)
	}
}

// TestLiveApproval runs the same prompt without acceptEdits so the write must go
// through the approval broker and MCP helper. DECK_LIVE=approve.
func TestLiveApproval(t *testing.T) {
	if os.Getenv("DECK_LIVE") != "approve" {
		t.Skip("set DECK_LIVE=approve to exercise the approval path against real Claude")
	}
	approved := 0
	evs := liveRun(t, "approve", nil, "", func(req agent.ApprovalRequest) {
		t.Logf("approval: kind=%s tool=%s summary=%q diff=%v", req.Kind, req.ToolName, req.Summary, req.Diff != nil)
		approved++
		req.Respond(agent.Decision{Allow: true})
	})
	for _, e := range evs {
		t.Logf("%s %q", e.Kind, e.Text)
	}
	if approved == 0 {
		t.Fatal("no approval request surfaced")
	}
}

// TestLiveMessaging checks against real Claude that the deck messaging tools are
// visible and callable without a permission prompt. DECK_LIVE=message.
func TestLiveMessaging(t *testing.T) {
	if os.Getenv("DECK_LIVE") != "message" {
		t.Skip("set DECK_LIVE=message to exercise send_message against real Claude")
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	broker, err := approve.NewBroker()
	if err != nil {
		t.Fatal(err)
	}
	defer broker.Close()

	got := make(chan agent.Message, 4)
	go func() {
		for m := range broker.Messages() {
			got <- m
			m.Respond("delivered to "+m.To, nil)
		}
	}()

	a := New("", self, broker)
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	r, err := a.Start(ctx, agent.Spec{
		ID: "claude1", Dir: t.TempDir(),
		MCP:          broker.ServerFor(self, "claude1"),
		Instructions: "You share a Deck thread with an agent named codex. Use the deck send_message tool to talk to it.",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	go func() {
		for req := range r.Approvals() {
			t.Logf("approval surfaced: tool=%s", req.ToolName)
			req.Respond(agent.Decision{Allow: true})
		}
	}()
	if err := r.Send("Use the deck send_message tool to send 'hi' to codex, then reply DONE."); err != nil {
		t.Fatal(err)
	}
	timeout := time.After(4 * time.Minute)
loop:
	for {
		select {
		case ev, ok := <-r.Events():
			if !ok {
				break loop
			}
			t.Logf("%s %s %q", ev.Kind, func() string {
				if ev.Tool != nil {
					return ev.Tool.Name + " | " + ev.Tool.Title
				}
				return ""
			}(), ev.Text)
			if ev.Kind == agent.KindTurnDone || ev.Kind == agent.KindExited {
				break loop
			}
		case <-timeout:
			t.Fatal("timeout")
		}
	}
	select {
	case m := <-got:
		t.Logf("hub received: from=%s to=%s text=%q", m.From, m.To, m.Text)
		if m.To != "codex" {
			t.Errorf("unexpected target %q", m.To)
		}
	default:
		t.Fatal("send_message never reached the hub")
	}
}
