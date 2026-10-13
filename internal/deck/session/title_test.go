package session

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/dev-dami/deck/internal/deck/agent"
	"github.com/dev-dami/deck/internal/deck/agent/fake"
	"github.com/dev-dami/deck/internal/deck/store"
)

func TestCleanTitle(t *testing.T) {
	for in, want := range map[string]string{
		"Fix the login bug":                         "Fix the login bug",
		"\"Fix the login bug.\"":                    "Fix the login bug",
		"Title: Fix the login bug":                  "Fix the login bug",
		"**Refactor store layout**\n\nMore detail.": "Refactor store layout",
		"":        "",
		"   \n  ": "",
		"one two three four five six seven eight nine ten eleven twelve thirteen": "",
	} {
		if got := cleanTitle(in); got != want {
			t.Errorf("cleanTitle(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPlaceholderTitle(t *testing.T) {
	for in, want := range map[string]string{
		"do the thing":                                "do the thing",
		"please fix the login bug":                    "fix the login bug",
		"Can you explain the relay code?":             "explain the relay code?",
		"$codex review this":                          "review this",
		"```go\nfmt.Println()\n```\nwhy is this slow": "why is this slow",
		"please": "please",
	} {
		if got := placeholderTitle(in); got != want {
			t.Errorf("placeholderTitle(%q) = %q, want %q", in, got, want)
		}
	}
}

// titleAdapter answers the one-shot title run with a canned reply and hands
// every other run to the wrapped adapter.
type titleAdapter struct {
	agent.Adapter
	reply string
	mu    sync.Mutex
	asks  []string
}

func (a *titleAdapter) Start(ctx context.Context, sp agent.Spec) (agent.Run, error) {
	if !strings.HasPrefix(sp.ID, titleRunPrefix) {
		return a.Adapter.Start(ctx, sp)
	}
	return &titleRun{a: a, ev: make(chan agent.Event, 4), ap: make(chan agent.ApprovalRequest)}, nil
}

type titleRun struct {
	a  *titleAdapter
	ev chan agent.Event
	ap chan agent.ApprovalRequest
}

func (r *titleRun) Events() <-chan agent.Event              { return r.ev }
func (r *titleRun) Approvals() <-chan agent.ApprovalRequest { return r.ap }
func (r *titleRun) Interrupt() error                        { return nil }
func (r *titleRun) SessionID() string                       { return "" }
func (r *titleRun) Close() error                            { return nil }
func (r *titleRun) Send(p string) error {
	r.a.mu.Lock()
	r.a.asks = append(r.a.asks, p)
	r.a.mu.Unlock()
	r.ev <- agent.Event{Kind: agent.KindAssistantText, Text: r.a.reply}
	r.ev <- agent.Event{Kind: agent.KindTurnDone}
	return nil
}

func titleManager(t *testing.T, reply string, auto bool) (*Manager, *titleAdapter) {
	t.Helper()
	st, err := store.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	hub := fake.NewHub()
	ta := &titleAdapter{Adapter: fake.NewTurns(0, hub, turn(say("I fixed the null check in login.go"), finish())), reply: reply}
	cfg := testCfg()
	cfg.AutoTitle = auto
	m := NewManager(map[string]agent.Adapter{"claude": ta}, st, cfg, Options{Hub: hub, MCPFor: fake.MCPFor})
	t.Cleanup(m.Close)
	collect(t, m)
	return m, ta
}

func TestAutoTitleReplacesPlaceholder(t *testing.T) {
	m, ta := titleManager(t, "\"Fix login null check.\"", true)
	id, err := m.Start("claude", "please fix the login bug", t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "model title", func() bool {
		info, _ := m.Agent(id)
		return info.Title == "Fix login null check"
	})
	ta.mu.Lock()
	defer ta.mu.Unlock()
	if len(ta.asks) != 1 {
		t.Fatalf("title run asked %d times, want 1", len(ta.asks))
	}
}

func TestAutoTitleOffKeepsPlaceholder(t *testing.T) {
	m, ta := titleManager(t, "Never used", false)
	id, err := m.Start("claude", "please fix the login bug", t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "turn done", func() bool { info, _ := m.Agent(id); return info.Status == StatusIdle })
	info, _ := m.Agent(id)
	if info.Title != "fix the login bug" {
		t.Fatalf("title = %q", info.Title)
	}
	ta.mu.Lock()
	defer ta.mu.Unlock()
	if len(ta.asks) != 0 {
		t.Fatalf("title run started with AutoTitle off")
	}
}

func TestAutoTitleNeverReplacesRename(t *testing.T) {
	m, _ := titleManager(t, "Model title", true)
	tid, err := m.NewThread(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := m.RenameThread(tid, "my name"); err != nil {
		t.Fatal(err)
	}
	m.applyAutoTitle(m.thread(tid), "Model title") // a late model title
	if info, _ := m.Thread(tid); info.Title != "my name" {
		t.Fatalf("title = %q, want the user's", info.Title)
	}
}
