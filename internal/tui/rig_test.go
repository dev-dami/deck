package tui

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/dev-dami/deck/internal/agent"
	"github.com/dev-dami/deck/internal/config"
	"github.com/dev-dami/deck/internal/session"
	"github.com/dev-dami/deck/internal/store"
)

// stubAdapter hands the test direct control of what each run emits.
type stubAdapter struct {
	kind string
	mu   sync.Mutex
	runs []*stubRun
}

type stubRun struct {
	ev        chan agent.Event
	ap        chan agent.ApprovalRequest
	mu        sync.Mutex
	sent      []string
	interrupt int
	spec      agent.Spec
	once      sync.Once
}

func (a *stubAdapter) Name() string { return a.kind }

func (a *stubAdapter) Start(_ context.Context, spec agent.Spec) (agent.Run, error) {
	r := &stubRun{ev: make(chan agent.Event, 256), ap: make(chan agent.ApprovalRequest, 16), spec: spec}
	if spec.FirstPrompt != "" {
		r.ev <- agent.Event{AgentID: spec.ID, Kind: agent.KindUserText, Text: spec.FirstPrompt}
	}
	a.mu.Lock()
	a.runs = append(a.runs, r)
	a.mu.Unlock()
	return r, nil
}

func (a *stubAdapter) last() *stubRun {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.runs[len(a.runs)-1]
}

func (r *stubRun) Events() <-chan agent.Event              { return r.ev }
func (r *stubRun) Approvals() <-chan agent.ApprovalRequest { return r.ap }
func (r *stubRun) SessionID() string                       { return "" }
func (r *stubRun) Close() error {
	r.once.Do(func() { close(r.ev); close(r.ap) })
	return nil
}
func (r *stubRun) Interrupt() error {
	r.mu.Lock()
	r.interrupt++
	r.mu.Unlock()
	r.ev <- agent.Event{Kind: agent.KindTurnDone, Text: "interrupted"}
	return nil
}
func (r *stubRun) Send(p string) error {
	r.mu.Lock()
	r.sent = append(r.sent, p)
	r.mu.Unlock()
	r.ev <- agent.Event{Kind: agent.KindUserText, Text: p}
	return nil
}

func (r *stubRun) sentList() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.sent...)
}

// rig drives a model by hand: keys in, messages pumped from the manager.
type rig struct {
	t      *testing.T
	m      *model
	mgr    *session.Manager
	claude *stubAdapter
	codex  *stubAdapter
	saved  []config.Config
	quit   bool
}

func newRig(t *testing.T, w, h int) *rig {
	t.Helper()
	st, err := store.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	r := &rig{t: t, claude: &stubAdapter{kind: "claude"}, codex: &stubAdapter{kind: "codex"}}
	cfg := testConfig()
	r.mgr = session.NewManager(map[string]agent.Adapter{"claude": r.claude, "codex": r.codex}, st, cfg)
	t.Cleanup(r.mgr.Close)
	r.m = newModel(r.mgr, cfg, Options{
		Dir:        repoDir(),
		SaveConfig: func(c config.Config) error { r.saved = append(r.saved, c); return nil },
	})
	r.m.noListen = true
	r.feed(tea.WindowSizeMsg{Width: w, Height: h})
	return r
}

func (r *rig) feed(msg tea.Msg) {
	switch v := msg.(type) {
	case nil:
	case tea.BatchMsg:
		for _, c := range v {
			r.exec(c)
		}
	case tea.QuitMsg:
		r.quit = true
	default:
		_, cmd := r.m.Update(msg)
		r.exec(cmd)
	}
}

// exec runs a command, feeding back whatever it returns promptly.
func (r *rig) exec(cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()
	select {
	case msg := <-done:
		r.feed(msg)
	case <-time.After(120 * time.Millisecond):
	}
}

func (r *rig) key(names ...string) {
	for _, n := range names {
		var km tea.KeyMsg
		switch n {
		case "enter":
			km = tea.KeyMsg{Type: tea.KeyEnter}
		case "alt+enter":
			km = tea.KeyMsg{Type: tea.KeyEnter, Alt: true}
		case "tab":
			km = tea.KeyMsg{Type: tea.KeyTab}
		case "esc":
			km = tea.KeyMsg{Type: tea.KeyEsc}
		case "up":
			km = tea.KeyMsg{Type: tea.KeyUp}
		case "down":
			km = tea.KeyMsg{Type: tea.KeyDown}
		case "ctrl+c":
			km = tea.KeyMsg{Type: tea.KeyCtrlC}
		case "backspace":
			km = tea.KeyMsg{Type: tea.KeyBackspace}
		case " ":
			km = tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}}
		default:
			km = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(n)}
		}
		r.feed(km)
	}
}

func (r *rig) typeText(s string) {
	for _, c := range s {
		if c == ' ' {
			r.key(" ")
		} else {
			r.key(string(c))
		}
	}
}

// pump feeds manager updates until cond holds.
func (r *rig) pump(what string, cond func() bool) {
	r.t.Helper()
	deadline := time.Now().Add(4 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			r.t.Fatalf("timed out waiting for %s\n%s", what, ansiStrip(r.m.View()))
		}
		select {
		case msg := <-r.mgr.Updates():
			r.feed(msg)
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func (r *rig) view() string { return ansiStrip(r.m.View()) }

func (r *rig) has(s string) bool { return strings.Contains(r.view(), s) }

// start launches an agent through the composer and waits for it to be selected.
func (r *rig) start(text string) {
	r.t.Helper()
	r.typeText(text)
	r.key("enter")
	r.pump("agent started", func() bool { return len(r.mgr.Agents()) > 0 && r.m.sel != "" })
}

// approval pushes an approval request from the latest run of a stub.
func (r *rig) approval(run *stubRun, id, summary string, kind agent.ApprovalKind) chan agent.Decision {
	got := make(chan agent.Decision, 1)
	run.ap <- agent.ApprovalRequest{
		ID: id, Kind: kind, ToolName: "Bash", Summary: summary, Cwd: repoDir(),
		Respond: func(d agent.Decision) { got <- d },
	}
	return got
}

func (r *rig) event(run *stubRun, ev agent.Event) { run.ev <- ev }
