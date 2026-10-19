package session

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/dev-dami/deck/internal/deck/agent"
	"github.com/dev-dami/deck/internal/deck/agent/fake"
	"github.com/dev-dami/deck/internal/deck/config"
	"github.com/dev-dami/deck/internal/deck/store"
)

func testCfg() config.Config {
	return config.Config{UserName: "tester", TellAgentsName: true, DefaultAgent: "claude", Motion: "off"}
}

// ---- spy adapter: records every prompt and spec an agent receives ----

type spyAdapter struct {
	agent.Adapter
	setters bool // runs also implement agent.ModeSetter, ModelSetter and EffortSetter
	images  bool // runs implement agent.ImageSender
	steer   bool // runs implement agent.Steerer
	mu      sync.Mutex
	specs   []agent.Spec
	runs    map[string]*spyRun
}

type spyRun struct {
	agent.Run
	mu      sync.Mutex
	prompts []string
	imgs    []sentImages // SendWithImages calls
	steers  []sentImages // Steer calls
}

// sentImages is a prompt handed to SendWithImages or Steer.
type sentImages struct {
	prompt string
	images []agent.Image
}

// ContractSafe forwards to the adapter it wraps.
func (s *spyAdapter) ContractSafe() bool {
	cs, ok := s.Adapter.(agent.ContractSafe)
	return ok && cs.ContractSafe()
}

// OpsSafe forwards to the adapter it wraps.
func (s *spyAdapter) OpsSafe() bool {
	o, ok := s.Adapter.(agent.OpsSafe)
	return ok && o.OpsSafe()
}

func newSpy(a agent.Adapter) *spyAdapter { return &spyAdapter{Adapter: a, runs: map[string]*spyRun{}} }

func (s *spyAdapter) Start(ctx context.Context, sp agent.Spec) (agent.Run, error) {
	run, err := s.Adapter.Start(ctx, sp)
	if err != nil {
		return nil, err
	}
	sr := &spyRun{Run: run}
	if sp.FirstPrompt != "" {
		sr.prompts = append(sr.prompts, sp.FirstPrompt)
	}
	s.mu.Lock()
	s.specs = append(s.specs, sp)
	s.runs[sp.ID] = sr
	s.mu.Unlock()
	switch {
	case s.setters:
		return &spyRunSetters{sr}, nil
	case s.images && s.steer:
		return &spyRunBoth{&spyRunImg{sr}, &spyRunSteer{sr}}, nil
	case s.images:
		return &spyRunImg{sr}, nil
	case s.steer:
		return &spyRunSteer{sr}, nil
	}
	return sr, nil
}

// spyRunImg is a spyRun that also implements agent.ImageSender.
type spyRunImg struct{ *spyRun }

func (r *spyRunImg) SendWithImages(p string, images []agent.Image) error {
	r.mu.Lock()
	r.prompts = append(r.prompts, p)
	r.imgs = append(r.imgs, sentImages{p, images})
	r.mu.Unlock()
	return r.Run.(agent.ImageSender).SendWithImages(p, images)
}

// spyRunSteer is a spyRun that also implements agent.Steerer.
type spyRunSteer struct{ *spyRun }

func (r *spyRunSteer) Steer(p string, images []agent.Image) error {
	r.mu.Lock()
	r.steers = append(r.steers, sentImages{p, images})
	r.mu.Unlock()
	return r.Run.(agent.Steerer).Steer(p, images)
}

// spyRunBoth implements both; the two embedded wrappers share one spyRun.
type spyRunBoth struct {
	*spyRunImg
	*spyRunSteer
}

func (r *spyRunBoth) Events() <-chan agent.Event              { return r.spyRunImg.Run.Events() }
func (r *spyRunBoth) Approvals() <-chan agent.ApprovalRequest { return r.spyRunImg.Run.Approvals() }
func (r *spyRunBoth) Send(p string) error                     { return r.spyRunImg.spyRun.Send(p) }
func (r *spyRunBoth) Interrupt() error                        { return r.spyRunImg.Run.Interrupt() }
func (r *spyRunBoth) SessionID() string                       { return r.spyRunImg.Run.SessionID() }
func (r *spyRunBoth) Close() error                            { return r.spyRunImg.Run.Close() }

// imageCalls lists the SendWithImages calls a member's run received.
func (s *spyAdapter) imageCalls(id string) []sentImages {
	s.mu.Lock()
	r := s.runs[id]
	s.mu.Unlock()
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]sentImages(nil), r.imgs...)
}

// steerCalls lists the Steer calls a member's run received.
func (s *spyAdapter) steerCalls(id string) []sentImages {
	s.mu.Lock()
	r := s.runs[id]
	s.mu.Unlock()
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]sentImages(nil), r.steers...)
}

// spyRunSetters is a spyRun that forwards SetMode and SetModel to the fake run.
type spyRunSetters struct{ *spyRun }

func (r *spyRunSetters) SetMode(m agent.Mode) error { return r.Run.(agent.ModeSetter).SetMode(m) }
func (r *spyRunSetters) SetModel(m string) error    { return r.Run.(agent.ModelSetter).SetModel(m) }
func (r *spyRunSetters) SetEffort(e string) error   { return r.Run.(agent.EffortSetter).SetEffort(e) }

func (r *spyRun) Send(p string) error {
	r.mu.Lock()
	r.prompts = append(r.prompts, p)
	r.mu.Unlock()
	return r.Run.Send(p)
}

func (s *spyAdapter) prompts(id string) []string {
	s.mu.Lock()
	r := s.runs[id]
	s.mu.Unlock()
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.prompts...)
}

func (s *spyAdapter) spec(id string) (agent.Spec, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := len(s.specs) - 1; i >= 0; i-- {
		if s.specs[i].ID == id {
			return s.specs[i], true
		}
	}
	return agent.Spec{}, false
}

func (s *spyAdapter) run(id string) agent.Run {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.runs[id]
}

// ---- gated adapter: holds one start until the test lets it go ----

// gatedAdapter holds the first Start that hold accepts (any Start when hold
// is nil) until release. Its member then stays between the claim of its task
// and its bind for as long as the test needs.
type gatedAdapter struct {
	agent.Adapter
	hold    func(agent.Spec) bool
	held    sync.Once
	entered chan struct{} // closed when the held Start begins
	open    chan struct{}
	opened  sync.Once
}

func newGate(a agent.Adapter, hold func(agent.Spec) bool) *gatedAdapter {
	return &gatedAdapter{Adapter: a, hold: hold, entered: make(chan struct{}), open: make(chan struct{})}
}

func (g *gatedAdapter) Start(ctx context.Context, sp agent.Spec) (agent.Run, error) {
	if g.hold == nil || g.hold(sp) {
		first := false
		g.held.Do(func() { first = true })
		if first {
			close(g.entered)
			<-g.open
		}
	}
	return g.Adapter.Start(ctx, sp)
}

func (g *gatedAdapter) release() { g.opened.Do(func() { close(g.open) }) }

// ---- collector: drains Updates and answers questions about what came out ----

type collector struct {
	mu   sync.Mutex
	msgs []tea.Msg
	stop chan struct{}
}

func collect(t *testing.T, m *Manager) *collector {
	c := &collector{stop: make(chan struct{})}
	go func() {
		for {
			select {
			case msg := <-m.Updates():
				c.mu.Lock()
				c.msgs = append(c.msgs, msg)
				c.mu.Unlock()
			case <-c.stop:
				return
			}
		}
	}()
	t.Cleanup(func() { close(c.stop) })
	return c
}

func (c *collector) all() []tea.Msg {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]tea.Msg(nil), c.msgs...)
}

func (c *collector) events(f func(EventMsg) bool) []EventMsg {
	var out []EventMsg
	for _, m := range c.all() {
		if e, ok := m.(EventMsg); ok && f(e) {
			out = append(out, e)
		}
	}
	return out
}

func (c *collector) turnDones(id string) int {
	return len(c.events(func(e EventMsg) bool { return e.AgentID == id && e.Kind == agent.KindTurnDone }))
}

func (c *collector) relays() []RelayMsg {
	var out []RelayMsg
	for _, m := range c.all() {
		if r, ok := m.(RelayMsg); ok {
			out = append(out, r)
		}
	}
	return out
}

func (c *collector) count(f func(tea.Msg) bool) int {
	n := 0
	for _, m := range c.all() {
		if f(m) {
			n++
		}
	}
	return n
}

func (c *collector) approvals() []ApprovalMsg {
	var out []ApprovalMsg
	for _, m := range c.all() {
		if a, ok := m.(ApprovalMsg); ok {
			out = append(out, a)
		}
	}
	return out
}

// toolResults lists the output of a member's tool results with the given tool name.
func (c *collector) toolResults(id, tool string) []string {
	var out []string
	for _, e := range c.events(func(e EventMsg) bool {
		return e.AgentID == id && e.Kind == agent.KindToolResult && e.Tool != nil && e.Tool.Name == tool
	}) {
		out = append(out, e.Tool.Output)
	}
	return out
}

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(3 * time.Millisecond)
	}
}

// ---- rig ----

type rig struct {
	m      *Manager
	st     *store.Store
	dir    string
	hub    *fake.Hub
	c      *collector
	claude *spyAdapter
	codex  *spyAdapter
}

// newRig builds a Manager over per-turn fake claude and codex agents that share a hub.
func newRig(t *testing.T, claude, codex []fake.Script) *rig {
	t.Helper()
	dir := t.TempDir()
	return newRigIn(t, dir, claude, codex)
}

func newRigIn(t *testing.T, dir string, claude, codex []fake.Script) *rig {
	t.Helper()
	return newRigWith(t, dir, false, testCfg(), claude, codex)
}

// newRigWith is newRigIn with the spies' runs optionally implementing the mode and
// model setters, and a given config.
func newRigWith(t *testing.T, dir string, setters bool, cfg config.Config, claude, codex []fake.Script) *rig {
	t.Helper()
	st, err := store.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	hub := fake.NewHub()
	r := &rig{st: st, dir: dir, hub: hub,
		claude: newSpy(fake.NewTurns(0, hub, claude...)),
		codex:  newSpy(fake.NewTurns(0, hub, codex...)),
	}
	r.claude.setters, r.codex.setters = setters, setters
	r.m = NewManager(map[string]agent.Adapter{"claude": r.claude, "codex": r.codex}, st, cfg,
		Options{Hub: hub, MCPFor: fake.MCPFor})
	t.Cleanup(r.m.Close)
	r.c = collect(t, r.m)
	return r
}

func (r *rig) thread(t *testing.T) string {
	t.Helper()
	id, err := r.m.NewThread(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func (r *rig) add(t *testing.T, tid, kind string) string {
	t.Helper()
	id, err := r.m.AddMember(tid, kind, "")
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// ---- script helpers ----

func say(text string) fake.Step {
	return fake.Step{Event: agent.Event{Kind: agent.KindAssistantText, Text: text}}
}

func finish() fake.Step { return fake.Step{Event: agent.Event{Kind: agent.KindTurnDone}} }

func turn(steps ...fake.Step) fake.Script { return fake.Script(steps) }

func started(id string) fake.Step {
	return fake.Step{Event: agent.Event{Kind: agent.KindSessionStarted, SessionID: id}}
}

func sendTo(to, text string) fake.Step {
	return fake.Step{Message: &agent.Message{To: to, Text: text}}
}

func cost(c float64) fake.Step {
	return fake.Step{Event: agent.Event{Kind: agent.KindUsage, Usage: &agent.Usage{CostUSD: c}}}
}

func has(t *testing.T, s string, subs ...string) {
	t.Helper()
	for _, sub := range subs {
		if !strings.Contains(s, sub) {
			t.Errorf("missing %q in:\n%s", sub, s)
		}
	}
}

func lacks(t *testing.T, s string, subs ...string) {
	t.Helper()
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			t.Errorf("unexpected %q in:\n%s", sub, s)
		}
	}
}
