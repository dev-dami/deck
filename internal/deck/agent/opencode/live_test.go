package opencode

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dev-dami/deck/internal/deck/agent"
	"github.com/dev-dami/deck/internal/deck/approve"
)

// TestLiveSession runs one real tiny session through the installed opencode
// and resumes it. It spends a little of whatever the model costs, so it only
// runs with DECK_LIVE=1; DECK_OPENCODE_MODEL picks the model (a free one by
// default). It never reads any credential: opencode uses its own setup.
func TestLiveSession(t *testing.T) {
	if os.Getenv("DECK_LIVE") != "1" {
		t.Skip("set DECK_LIVE=1 to run a live opencode session")
	}
	model := os.Getenv("DECK_OPENCODE_MODEL")
	if model == "" {
		model = "opencode/big-pickle"
	}
	dir := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	collect := func(r agent.Run) []agent.Event {
		var evs []agent.Event
		for {
			select {
			case e, ok := <-r.Events():
				if !ok {
					return evs
				}
				evs = append(evs, e)
				if e.Kind == agent.KindTurnDone || e.Kind == agent.KindExited {
					return evs
				}
			case req := <-r.Approvals():
				t.Logf("approval %s %s: %s (diff %v)", req.Kind, req.ToolName, req.Summary, req.Diff != nil)
				req.Respond(agent.Decision{Allow: true})
			case <-ctx.Done():
				t.Fatal("timed out")
			}
		}
	}

	r, err := New("").Start(ctx, agent.Spec{ID: "opencode", Dir: dir, Model: model, Instructions: "You are working inside Deck."})
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Send("Create a file named hello.txt containing the word hi using your write tool, then run the shell command: echo ok. Then reply with just: done"); err != nil {
		t.Fatal(err)
	}
	evs := collect(r)
	sid := r.SessionID()
	r.Close()
	var final string
	kinds := map[agent.Kind]int{}
	for _, e := range evs {
		kinds[e.Kind]++
		if e.Kind == agent.KindAssistantText && !e.Delta {
			final = e.Text
		}
		if e.Tool != nil {
			t.Logf("%s %s %q -> %s %q", e.Kind, e.Tool.Name, e.Tool.Title, e.Tool.Status, e.Tool.Output)
		}
		if e.Kind == agent.KindError {
			t.Logf("error: %s", e.Text)
		}
		if e.Kind == agent.KindUsage {
			t.Logf("usage %+v", *e.Usage)
		}
	}
	t.Logf("kinds %v final %q session %s", kinds, final, sid)
	if b, err := os.ReadFile(filepath.Join(dir, "hello.txt")); err != nil || !strings.Contains(string(b), "hi") {
		t.Errorf("hello.txt: %q %v", b, err)
	}
	if kinds[agent.KindFileChange] == 0 || kinds[agent.KindToolResult] < 2 || !strings.Contains(strings.ToLower(final), "done") {
		t.Errorf("events: %v final %q", kinds, final)
	}

	r2, err := New("").Start(ctx, agent.Spec{ID: "opencode", Dir: dir, Model: model, ResumeID: sid})
	if err != nil {
		t.Fatal(err)
	}
	defer r2.Close()
	if err := r2.Send("What was the name of the file you created? Reply with just the file name."); err != nil {
		t.Fatal(err)
	}
	final = ""
	for _, e := range collect(r2) {
		if e.Kind == agent.KindAssistantText && !e.Delta {
			final = e.Text
		}
	}
	if !strings.Contains(final, "hello.txt") {
		t.Errorf("after resume: %q", final)
	}
}

// TestLiveDeckTools checks that OpenCode starts Deck's MCP server and can call
// read_thread through it. DECK_EXE names a built deck binary.
func TestLiveDeckTools(t *testing.T) {
	exe := os.Getenv("DECK_EXE")
	if os.Getenv("DECK_LIVE") != "1" || exe == "" {
		t.Skip("set DECK_LIVE=1 and DECK_EXE=<deck binary> to run")
	}
	model := os.Getenv("DECK_OPENCODE_MODEL")
	if model == "" {
		model = "opencode/big-pickle"
	}
	b, err := approve.NewBroker()
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	b.SetThreadReader(func(string) string { return "Ada: the secret word is PELICAN-42" })
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	r, err := New("").Start(ctx, agent.Spec{ID: "opencode", Dir: t.TempDir(), Model: model, MCP: b.ServerFor(exe, "opencode")})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if err := r.Send("Call your read_thread tool (from the deck MCP server) and reply with the secret word it shows, nothing else."); err != nil {
		t.Fatal(err)
	}
	final, tools := "", []string{}
	for e := range r.Events() {
		if e.Tool != nil && e.Kind == agent.KindToolResult {
			tools = append(tools, e.Tool.Name)
		}
		if e.Kind == agent.KindAssistantText && !e.Delta {
			final = e.Text
		}
		if e.Kind == agent.KindError {
			t.Logf("error: %s", e.Text)
		}
		if e.Kind == agent.KindTurnDone || e.Kind == agent.KindExited {
			break
		}
	}
	t.Logf("tools %v final %q", tools, final)
	if !strings.Contains(final, "PELICAN-42") {
		t.Errorf("final %q", final)
	}
}
