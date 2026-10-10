package codex

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/dev-dami/deck/internal/deck/agent"
)

// TestRecordLiveSession records one real tiny session to testdata/session1.jsonl.
// It spends a little of the user's Codex usage, so it only runs with DECK_LIVE=1.
// It never reads any credential file: it only launches the installed codex
// binary, which uses the user's own login.
func TestRecordLiveSession(t *testing.T) {
	live := os.Getenv("DECK_LIVE")
	if live != "1" && live != "2" {
		t.Skip("set DECK_LIVE=1 (or 2 for the patch/approval session) to record a live session")
	}
	name := "session1.jsonl"
	prompt := "Create a file hello.txt containing hi, then run `type hello.txt`, then reply DONE."
	commandDecisionValue := decisionAccept
	if live == "2" {
		name = "session2.jsonl"
		prompt = "Use the apply_patch tool (not the shell) to create a file hello.txt containing hi. " +
			"Then run `curl.exe -sI https://example.com`, which needs network access outside the sandbox, so request approval to run it. " +
			"Finally reply DONE."
		commandDecisionValue = decisionDecline
	}
	bin, err := exec.LookPath("codex")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	cmd := exec.Command(bin, "app-server")
	cmd.Dir = dir
	cmd.Env = os.Environ()
	stdin, _ := cmd.StdinPipe()
	stdout, _ := cmd.StdoutPipe()
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer killTree(cmd)

	var mu sync.Mutex
	var rec []json.RawMessage
	tapf := func(d byte, line []byte) {
		dir := "send"
		if d == '<' {
			dir = "recv"
		}
		b, _ := json.Marshal(map[string]any{"dir": dir, "msg": json.RawMessage(append([]byte(nil), line...))})
		mu.Lock()
		rec = append(rec, b)
		mu.Unlock()
	}
	save := func() {
		mu.Lock()
		defer mu.Unlock()
		var out bytes.Buffer
		for _, r := range rec {
			out.Write(r)
			out.WriteByte('\n')
		}
		_ = os.WriteFile(filepath.Join("testdata", name), out.Bytes(), 0o644)
	}
	defer save()

	c := NewClient(stdout, stdin, tapf)
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()

	if err := c.Call(ctx, methodInitialize, initializeParams{ClientInfo: clientInfo{Name: "deck", Title: "Deck", Version: "0.1.0"}}, nil); err != nil {
		t.Fatalf("initialize: %v (stderr: %s)", err, stderr.String())
	}
	_ = c.Notify(notifyInitialized, nil)
	var tr threadResponse
	if err := c.Call(ctx, methodThreadStart, threadStartParams{Cwd: dir, ApprovalPolicy: DefaultApprovalPolicy, Sandbox: DefaultSandbox}, &tr); err != nil {
		t.Fatalf("thread/start: %v (stderr: %s)", err, stderr.String())
	}
	t.Logf("thread %s", tr.Thread.ID)
	if err := c.Call(ctx, methodTurnStart, turnStartParams{ThreadID: tr.Thread.ID, Input: []userInput{{Type: "text", Text: prompt, TextElements: []any{}}}}, nil); err != nil {
		t.Fatalf("turn/start: %v", err)
	}
	for {
		select {
		case m, ok := <-c.Incoming():
			if !ok {
				t.Fatalf("stream ended early (stderr: %s)", stderr.String())
			}
			if m.IsRequest() {
				switch m.Method {
				case reqCommandApproval:
					_ = c.Respond(m.ID, decisionResponse{Decision: commandDecisionValue})
				case reqFileChangeApproval:
					_ = c.Respond(m.ID, decisionResponse{Decision: decisionAccept})
				default:
					_ = c.RespondError(m.ID, -32601, "unsupported")
				}
				continue
			}
			if m.Method == noteTurnCompleted {
				return
			}
		case <-ctx.Done():
			t.Fatalf("timeout")
		}
	}
}

// TestLiveHandshake starts the real adapter against the installed codex, does
// the initialize/thread/start handshake (no model call, no prompt), and closes.
func TestLiveHandshake(t *testing.T) {
	if os.Getenv("DECK_LIVE") == "" {
		t.Skip("set DECK_LIVE=1 to run against the installed codex")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	run, err := New("codex").Start(ctx, agent.Spec{ID: "live", Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	e := <-run.Events()
	if e.Kind != agent.KindSessionStarted || e.SessionID == "" || run.SessionID() != e.SessionID {
		t.Fatalf("first event = %+v", e)
	}
	run.Close()
	for ev := range run.Events() {
		if ev.Kind == agent.KindExited {
			return
		}
	}
	t.Fatal("no exited event")
}
