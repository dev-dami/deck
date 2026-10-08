package tui

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/dev-dami/deck/internal/agent"
	"github.com/dev-dami/deck/internal/agent/fake"
	"github.com/dev-dami/deck/internal/config"
	"github.com/dev-dami/deck/internal/session"
	"github.com/dev-dami/deck/internal/store"
)

// stubAdapter hands the test direct control of what each run emits.
type stubAdapter struct {
	kind string
	mu   sync.Mutex
	runs []*stubRun

	// models is what Models lists; modelsErr makes it fail instead.
	models    []agent.ModelInfo
	modelsErr error
	// plain hides SetMode/SetModel from the manager (a CLI that cannot switch
	// anything mid-session).
	plain bool
}

type stubRun struct {
	ev        chan agent.Event
	ap        chan agent.ApprovalRequest
	mu        sync.Mutex
	sent      []string
	interrupt int
	spec      agent.Spec
	once      sync.Once
	modes     []agent.Mode
	setModels []string
}

func (a *stubAdapter) Name() string { return a.kind }

// Models implements agent.ModelLister.
func (a *stubAdapter) Models(context.Context) ([]agent.ModelInfo, error) {
	if a.modelsErr != nil {
		return nil, a.modelsErr
	}
	return append([]agent.ModelInfo(nil), a.models...), nil
}

// plainRun is a run that offers nothing beyond agent.Run.
type plainRun struct{ agent.Run }

func (a *stubAdapter) Start(_ context.Context, spec agent.Spec) (agent.Run, error) {
	r := &stubRun{ev: make(chan agent.Event, 256), ap: make(chan agent.ApprovalRequest, 16), spec: spec}
	if spec.FirstPrompt != "" {
		r.ev <- agent.Event{AgentID: spec.ID, Kind: agent.KindUserText, Text: spec.FirstPrompt}
	}
	a.mu.Lock()
	a.runs = append(a.runs, r)
	plain := a.plain
	a.mu.Unlock()
	if plain {
		return plainRun{r}, nil
	}
	return r, nil
}

// SetMode implements agent.ModeSetter.
func (r *stubRun) SetMode(m agent.Mode) error {
	r.mu.Lock()
	r.modes = append(r.modes, m)
	r.mu.Unlock()
	return nil
}

// SetModel implements agent.ModelSetter.
func (r *stubRun) SetModel(model string) error {
	r.mu.Lock()
	r.setModels = append(r.setModels, model)
	r.mu.Unlock()
	return nil
}

func (r *stubRun) modeCalls() []agent.Mode {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]agent.Mode(nil), r.modes...)
}

func (r *stubRun) modelCalls() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.setModels...)
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
	hub    *fake.Hub
	saved  []config.Config
	quit   bool
}

func newRig(t *testing.T, w, h int) *rig { return newRigHops(t, w, h, 0) }

// newRigHops is newRig with a different agent-to-agent hop limit (0 = the default).
func newRigHops(t *testing.T, w, h, maxHops int) *rig {
	t.Helper()
	return newRigWith(t, w, h, maxHops, nil)
}

// newRigWith is newRigHops with a say over the config.
func newRigWith(t *testing.T, w, h, maxHops int, tune func(*config.Config)) *rig {
	t.Helper()
	st, err := store.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	r := &rig{t: t, claude: &stubAdapter{kind: "claude", models: fake.ClaudeModels()},
		codex: &stubAdapter{kind: "codex", models: fake.CodexModels()}, hub: fake.NewHub()}
	cfg := testConfig()
	if tune != nil {
		tune(&cfg)
	}
	r.mgr = session.NewManager(map[string]agent.Adapter{"claude": r.claude, "codex": r.codex}, st, cfg,
		session.Options{Hub: r.hub, MCPFor: fake.MCPFor, MaxHops: maxHops})
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

// start sends a first message through the composer and waits for the thread to
// have a member.
func (r *rig) start(text string) {
	r.t.Helper()
	r.typeText(text)
	r.key("enter")
	r.pump("thread started", func() bool { return len(r.m.curThread().Members) > 0 })
	r.drain()
}

// say types a message into the composer and sends it.
func (r *rig) say(text string) {
	r.t.Helper()
	r.typeText(text)
	r.key("enter")
}

// tv is the transcript of the selected thread.
func (r *rig) tv() *threadView { return r.m.views[r.m.sel] }

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

// duo starts a thread with claude and codex in it and waits for both.
func (r *rig) duo() {
	r.t.Helper()
	r.say("@claude plan it, @codex review it")
	r.pump("two members", func() bool { return len(r.m.curThread().Members) == 2 })
	r.drain()
}

// done ends an agent's turn and waits until the manager sees it idle.
func (r *rig) done(run *stubRun, id string) {
	r.t.Helper()
	r.event(run, agent.Event{Kind: agent.KindTurnDone})
	r.pump(id+" idle", func() bool { return status(r, id) == session.StatusIdle })
}

// relay has agent from send_message to `to` through the hub and waits for the
// relay line to reach the transcript.
func (r *rig) relay(from, to, text string) {
	r.t.Helper()
	go func() { _, _ = r.hub.Send(nil, from, to, text) }()
	r.pump("relay", func() bool {
		for _, it := range r.tv().items {
			if it.kind == kRelay && it.text == strings.TrimSpace(text) {
				return true
			}
		}
		return false
	})
}

// text of the view's lines joined, colours stripped.
func (r *rig) lines() []string { return strings.Split(r.view(), "\n") }

// find returns the first screen line containing s, or "".
func (r *rig) find(s string) string {
	for _, l := range r.lines() {
		if strings.Contains(l, s) {
			return l
		}
	}
	return ""
}
