// Package session owns Deck's running agents.
//
// A Manager starts agents from adapters, keeps their status and cost, persists
// their events to the store on a single writer goroutine, and fans every run's
// Events and Approvals into ONE channel of Bubble Tea messages (Updates /
// Listen), so the UI never touches a run directly and never blocks on disk.
package session

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/dev-dami/deck/internal/agent"
	"github.com/dev-dami/deck/internal/config"
	"github.com/dev-dami/deck/internal/store"
)

// Status is the coarse state of one agent.
type Status string

// Agent statuses.
const (
	StatusIdle     Status = "idle"
	StatusWorking  Status = "working"
	StatusTool     Status = "tool"
	StatusApproval Status = "approval"
	StatusError    Status = "error"
	StatusExited   Status = "exited"
)

// EventMsg carries one normalized agent event to the UI.
type EventMsg struct{ agent.Event }

// ApprovalMsg carries a permission request to the UI. Its Respond is wrapped by
// the Manager: calling it (once) clears the request from the agent's pending count.
type ApprovalMsg struct{ agent.ApprovalRequest }

// ApprovalGoneMsg tells the UI that a request it holds is no longer answerable
// (the turn ended, the agent was interrupted or exited).
type ApprovalGoneMsg struct {
	AgentID string
	ID      string
}

// AgentInfo is a snapshot of one agent.
type AgentInfo struct {
	ID       string
	Kind     string
	Name     string
	Title    string // first prompt, one line
	Dir      string
	Model    string
	Status   Status
	CostUSD  float64
	Pending  int // approvals waiting for an answer
	ThreadID string
}

// Manager owns every agent.
type Manager struct {
	adapters map[string]agent.Adapter
	st       *store.Store

	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}
	out    chan tea.Msg
	w      *writer
	wg     sync.WaitGroup

	mu     sync.Mutex
	cfg    config.Config
	closed bool
	order  []*agentState
	byID   map[string]*agentState
	counts map[string]int
}

// NewManager returns a Manager that starts agents from adapters (keyed by kind,
// e.g. "claude", "codex") and persists threads to st. st may be nil, in which
// case nothing is persisted.
func NewManager(adapters map[string]agent.Adapter, st *store.Store, cfg config.Config) *Manager {
	ctx, cancel := context.WithCancel(context.Background())
	m := &Manager{
		adapters: adapters,
		st:       st,
		ctx:      ctx,
		cancel:   cancel,
		done:     make(chan struct{}),
		out:      make(chan tea.Msg, 512),
		cfg:      cfg,
		byID:     map[string]*agentState{},
		counts:   map[string]int{},
	}
	m.w = newWriter()
	return m
}

// SetConfig replaces the config used for agents started from now on.
func (m *Manager) SetConfig(cfg config.Config) {
	m.mu.Lock()
	m.cfg = cfg
	m.mu.Unlock()
}

// Kinds lists the kinds that can be started, in a stable order (claude, codex, then the rest).
func (m *Manager) Kinds() []string {
	var ks []string
	for k := range m.adapters {
		ks = append(ks, k)
	}
	rank := func(k string) int {
		switch k {
		case "claude":
			return 0
		case "codex":
			return 1
		}
		return 2
	}
	sort.Slice(ks, func(i, j int) bool {
		if rank(ks[i]) != rank(ks[j]) {
			return rank(ks[i]) < rank(ks[j])
		}
		return ks[i] < ks[j]
	})
	return ks
}

// Updates is the single stream of EventMsg, ApprovalMsg and ApprovalGoneMsg.
func (m *Manager) Updates() <-chan tea.Msg { return m.out }

// Listen returns a command that waits for the next update. Call it again from
// Update after every message it delivers.
func (m *Manager) Listen() tea.Cmd {
	return func() tea.Msg {
		select {
		case msg := <-m.out:
			return msg
		case <-m.done:
			return nil
		}
	}
}

// Threads lists the stored threads, most recent first.
func (m *Manager) Threads() []store.Thread {
	if m.st == nil {
		return nil
	}
	ts, _ := m.st.List()
	return ts
}

// Agents returns a snapshot of every agent in start order.
func (m *Manager) Agents() []AgentInfo {
	m.mu.Lock()
	order := append([]*agentState(nil), m.order...)
	m.mu.Unlock()
	out := make([]AgentInfo, 0, len(order))
	for _, a := range order {
		out = append(out, a.info())
	}
	return out
}

// Agent returns one agent's snapshot.
func (m *Manager) Agent(id string) (AgentInfo, bool) {
	a := m.get(id)
	if a == nil {
		return AgentInfo{}, false
	}
	return a.info(), true
}

func (m *Manager) get(id string) *agentState {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.byID[id]
}

// Start launches a new agent of the given kind and returns its id ("claude",
// "claude2", "codex", ...). An empty dir means the current directory; an empty
// model means the configured model for that kind.
func (m *Manager) Start(kind, prompt, dir, model string) (string, error) {
	ad, ok := m.adapters[kind]
	if !ok {
		return "", fmt.Errorf("session: unknown agent kind %q", kind)
	}
	if dir == "" {
		wd, err := os.Getwd()
		if err != nil {
			return "", err
		}
		dir = wd
	}
	id, cfg, err := m.allocate(kind)
	if err != nil {
		return "", err
	}
	if model == "" {
		model = modelFor(cfg, kind)
	}
	spec := m.spec(cfg, kind, id, dir, model, prompt, "")
	run, err := ad.Start(m.ctx, spec)
	if err != nil {
		return "", fmt.Errorf("session: start %s: %w", kind, err)
	}
	th := store.Thread{AgentKind: kind, Name: id, Title: oneLine(prompt), Dir: dir, Model: model}
	if m.st != nil {
		created, err := m.st.Create(th)
		if err != nil {
			_ = run.Close()
			return "", fmt.Errorf("session: create thread: %w", err)
		}
		th = created
	}
	a := &agentState{
		id: id, kind: kind, name: id, title: th.Title, dir: dir, model: model,
		thread: th, run: run, base: StatusIdle, started: time.Now(),
	}
	if strings.TrimSpace(prompt) != "" {
		a.base = StatusWorking
	}
	m.register(a)
	return id, nil
}

// Resume restarts a stored thread. It returns the new agent's id and the
// thread's stored events (with AgentID rewritten) for display. If the thread is
// already running its id is returned with no history.
func (m *Manager) Resume(threadID string) (string, []agent.Event, error) {
	if m.st == nil {
		return "", nil, errors.New("session: no store")
	}
	m.mu.Lock()
	for _, a := range m.order {
		if a.thread.ID == threadID && a.status() != StatusExited {
			m.mu.Unlock()
			return a.id, nil, nil
		}
	}
	m.mu.Unlock()

	events, th, err := m.st.Load(threadID)
	if err != nil {
		return "", nil, err
	}
	ad, ok := m.adapters[th.AgentKind]
	if !ok {
		return "", nil, fmt.Errorf("session: unknown agent kind %q", th.AgentKind)
	}
	id, cfg, err := m.allocate(th.AgentKind)
	if err != nil {
		return "", nil, err
	}
	dir := th.Dir
	if dir == "" {
		dir, _ = os.Getwd()
	}
	model := th.Model
	if model == "" {
		model = modelFor(cfg, th.AgentKind)
	}
	run, err := ad.Start(m.ctx, m.spec(cfg, th.AgentKind, id, dir, model, "", th.SessionID))
	if err != nil {
		return "", nil, fmt.Errorf("session: resume %s: %w", th.AgentKind, err)
	}
	name := th.Name
	m.mu.Lock()
	for _, o := range m.order {
		if o.id == name || o.getName() == name {
			name = id
			break
		}
	}
	m.mu.Unlock()
	if name == "" {
		name = id
	}
	a := &agentState{
		id: id, kind: th.AgentKind, name: name, title: th.Title, dir: dir, model: model,
		thread: th, run: run, base: StatusIdle, started: time.Now(), costTotal: th.CostUSD,
		sessionID: th.SessionID,
	}
	m.register(a)
	for i := range events {
		events[i].AgentID = id
	}
	return id, events, nil
}

// Send delivers a prompt to a running agent.
func (m *Manager) Send(id, prompt string) error {
	a := m.get(id)
	if a == nil {
		return fmt.Errorf("session: no agent %q", id)
	}
	prev, err := a.beginTurn(prompt)
	if err != nil {
		return err
	}
	if err := a.run.Send(prompt); err != nil {
		a.restore(prev)
		return err
	}
	if m.st != nil {
		m.w.push(func() { _ = m.st.UpdateMeta(a.metaSnapshot()) })
	}
	return nil
}

// Interrupt stops the agent's current turn. Pending approvals are denied first
// so the underlying CLI is never left waiting.
func (m *Manager) Interrupt(id string) error {
	a := m.get(id)
	if a == nil {
		return fmt.Errorf("session: no agent %q", id)
	}
	for _, p := range a.takePending() {
		p.respond(agent.Decision{Allow: false, Reason: "interrupted"})
		m.emit(ApprovalGoneMsg{AgentID: id, ID: p.id})
	}
	return a.run.Interrupt()
}

// Rename changes an agent's display name. Names must be unique, non-empty and
// free of whitespace and '@' (they are used for addressing).
func (m *Manager) Rename(id, name string) error {
	name = strings.TrimSpace(name)
	if name == "" || strings.ContainsAny(name, " \t\r\n@/") {
		return fmt.Errorf("session: invalid name %q", name)
	}
	m.mu.Lock()
	a := m.byID[id]
	if a == nil {
		m.mu.Unlock()
		return fmt.Errorf("session: no agent %q", id)
	}
	for _, o := range m.order {
		if o != a && (o.getName() == name || o.id == name) {
			m.mu.Unlock()
			return fmt.Errorf("session: name %q is taken", name)
		}
	}
	m.mu.Unlock()
	a.mu.Lock()
	a.name = name
	a.thread.Name = name
	a.mu.Unlock()
	if m.st != nil {
		m.w.push(func() { _ = m.st.UpdateMeta(a.metaSnapshot()) })
	}
	return nil
}

// Flush blocks until every queued store write has been performed.
func (m *Manager) Flush() { m.w.flush() }

// Close stops every agent, drains the writer and releases Listen.
func (m *Manager) Close() {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	m.closed = true
	order := append([]*agentState(nil), m.order...)
	m.mu.Unlock()

	close(m.done)
	m.cancel()
	var cw sync.WaitGroup
	for _, a := range order {
		cw.Add(1)
		go func(a *agentState) {
			defer cw.Done()
			_ = a.run.Close()
		}(a)
	}
	wait(&cw, 3*time.Second)
	wait(&m.wg, 2*time.Second)
	m.w.stop()
}

func wait(wg *sync.WaitGroup, d time.Duration) {
	c := make(chan struct{})
	go func() { wg.Wait(); close(c) }()
	select {
	case <-c:
	case <-time.After(d):
	}
}

func (m *Manager) allocate(kind string) (string, config.Config, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return "", m.cfg, errors.New("session: closed")
	}
	m.counts[kind]++
	n := m.counts[kind]
	id := kind
	if n > 1 {
		id = fmt.Sprintf("%s%d", kind, n)
	}
	return id, m.cfg, nil
}

func (m *Manager) spec(cfg config.Config, kind, id, dir, model, prompt, resume string) agent.Spec {
	s := agent.Spec{
		ID: id, Dir: dir, Model: model, ResumeID: resume, FirstPrompt: prompt,
		UserName: cfg.UserName, TellName: cfg.TellAgentsName,
	}
	switch kind {
	case "claude":
		s.Binary = cfg.ClaudeBinary
	case "codex":
		s.Binary = cfg.CodexBinary
	}
	return s
}

func modelFor(cfg config.Config, kind string) string {
	switch kind {
	case "claude":
		return cfg.Models.Claude
	case "codex":
		return cfg.Models.Codex
	}
	return ""
}

func (m *Manager) register(a *agentState) {
	a.m = m
	m.mu.Lock()
	m.order = append(m.order, a)
	m.byID[a.id] = a
	m.mu.Unlock()
	m.wg.Add(1)
	go m.pump(a)
}

// emit hands a message to the UI, giving up once the manager is closed.
func (m *Manager) emit(msg tea.Msg) {
	select {
	case m.out <- msg:
	case <-m.done:
	}
}

// pump fans one run's events and approvals into the shared channel.
func (m *Manager) pump(a *agentState) {
	defer m.wg.Done()
	evs, aps := a.run.Events(), a.run.Approvals()
	for evs != nil || aps != nil {
		select {
		case ev, ok := <-evs:
			if !ok {
				evs = nil
				continue
			}
			m.onEvent(a, ev)
		case req, ok := <-aps:
			if !ok {
				aps = nil
				continue
			}
			m.onApproval(a, req)
		}
	}
	if a.markExited() {
		m.emit(EventMsg{agent.Event{AgentID: a.id, Time: time.Now(), Kind: agent.KindExited}})
	}
}

func (m *Manager) onEvent(a *agentState, ev agent.Event) {
	if ev.AgentID == "" {
		ev.AgentID = a.id
	}
	if ev.Time.IsZero() {
		ev.Time = time.Now()
	}
	res := a.apply(ev)
	if m.st != nil {
		if !ev.Delta {
			th := a.thread.ID
			e := ev
			m.w.push(func() { _ = m.st.Append(th, e) })
		}
		if res.metaDirty {
			m.w.push(func() { _ = m.st.UpdateMeta(a.metaSnapshot()) })
		}
	}
	for _, id := range res.gone {
		m.emit(ApprovalGoneMsg{AgentID: a.id, ID: id})
	}
	m.emit(EventMsg{ev})
}

func (m *Manager) onApproval(a *agentState, req agent.ApprovalRequest) {
	req.AgentID = a.id
	orig := req.Respond
	var once sync.Once
	id := req.ID
	req.Respond = func(d agent.Decision) {
		once.Do(func() {
			a.removePending(id)
			if orig != nil {
				orig(d)
			}
		})
	}
	a.addPending(id, req.Respond)
	m.emit(ApprovalMsg{req})
}

// ---- per-agent state ----

type pendingApproval struct {
	id      string
	respond func(agent.Decision)
}

type agentState struct {
	m *Manager

	id, kind string
	started  time.Time
	run      agent.Run

	mu        sync.Mutex
	name      string
	title     string
	dir       string
	model     string
	thread    store.Thread
	base      Status
	pending   []pendingApproval
	costTotal float64 // folded turns
	costTurn  float64 // current turn, cumulative as reported
	sessionID string
}

func (a *agentState) getName() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.name
}

func (a *agentState) status() Status {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.statusLocked()
}

func (a *agentState) statusLocked() Status {
	if a.base == StatusExited {
		return StatusExited
	}
	if len(a.pending) > 0 {
		return StatusApproval
	}
	return a.base
}

func (a *agentState) info() AgentInfo {
	a.mu.Lock()
	defer a.mu.Unlock()
	return AgentInfo{
		ID: a.id, Kind: a.kind, Name: a.name, Title: a.title, Dir: a.dir, Model: a.model,
		Status: a.statusLocked(), CostUSD: a.costTotal + a.costTurn, Pending: len(a.pending),
		ThreadID: a.thread.ID,
	}
}

func (a *agentState) metaSnapshot() store.Thread {
	a.mu.Lock()
	defer a.mu.Unlock()
	t := a.thread
	t.Name, t.Title, t.Dir, t.Model = a.name, a.title, a.dir, a.model
	t.SessionID = a.sessionID
	t.CostUSD = a.costTotal + a.costTurn
	return t
}

// beginTurn marks the agent working before the prompt is sent, so a fast
// turn_done can never be overwritten by a late "working".
func (a *agentState) beginTurn(prompt string) (prev Status, err error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.base == StatusExited {
		return a.base, fmt.Errorf("session: %s has exited", a.id)
	}
	prev = a.base
	a.costTotal += a.costTurn
	a.costTurn = 0
	a.base = StatusWorking
	if a.title == "" {
		a.title = oneLine(prompt)
		a.thread.Title = a.title
	}
	return prev, nil
}

func (a *agentState) restore(prev Status) {
	a.mu.Lock()
	if a.base == StatusWorking {
		a.base = prev
	}
	a.mu.Unlock()
}

func (a *agentState) addPending(id string, respond func(agent.Decision)) {
	a.mu.Lock()
	a.pending = append(a.pending, pendingApproval{id: id, respond: respond})
	a.mu.Unlock()
}

func (a *agentState) removePending(id string) {
	a.mu.Lock()
	for i, p := range a.pending {
		if p.id == id {
			a.pending = append(a.pending[:i], a.pending[i+1:]...)
			break
		}
	}
	a.mu.Unlock()
}

func (a *agentState) takePending() []pendingApproval {
	a.mu.Lock()
	defer a.mu.Unlock()
	p := a.pending
	a.pending = nil
	return p
}

func (a *agentState) markExited() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.base == StatusExited {
		return false
	}
	a.base = StatusExited
	a.pending = nil
	return true
}

type applyResult struct {
	gone      []string
	metaDirty bool
}

// apply folds one event into the agent's status and cost.
func (a *agentState) apply(ev agent.Event) (res applyResult) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.base == StatusExited && ev.Kind != agent.KindExited {
		return res
	}
	switch ev.Kind {
	case agent.KindSessionStarted:
		if ev.SessionID != "" && ev.SessionID != a.sessionID {
			a.sessionID = ev.SessionID
			a.thread.SessionID = ev.SessionID
			res.metaDirty = true
		}
	case agent.KindUserText, agent.KindAssistantText, agent.KindReasoning, agent.KindToolResult:
		a.base = StatusWorking
	case agent.KindToolCall:
		a.base = StatusTool
	case agent.KindUsage:
		if ev.Usage != nil {
			a.costTurn = ev.Usage.CostUSD
		}
	case agent.KindError:
		a.base = StatusError
	case agent.KindTurnDone:
		a.costTotal += a.costTurn
		a.costTurn = 0
		a.base = StatusIdle
		res.gone = a.clearPendingLocked()
		res.metaDirty = true
	case agent.KindExited:
		a.costTotal += a.costTurn
		a.costTurn = 0
		a.base = StatusExited
		res.gone = a.clearPendingLocked()
		res.metaDirty = true
	}
	return res
}

func (a *agentState) clearPendingLocked() []string {
	var ids []string
	for _, p := range a.pending {
		ids = append(ids, p.id)
	}
	a.pending = nil
	return ids
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

// ---- single store writer ----

// writer runs queued store operations one at a time on its own goroutine, so
// store.Append (which costs milliseconds) never runs on the UI goroutine.
type writer struct {
	mu     sync.Mutex
	q      []func()
	wake   chan struct{}
	closed bool
	done   chan struct{}
}

func newWriter() *writer {
	w := &writer{wake: make(chan struct{}, 1), done: make(chan struct{})}
	go w.loop()
	return w
}

func (w *writer) push(f func()) {
	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		return
	}
	w.q = append(w.q, f)
	w.mu.Unlock()
	select {
	case w.wake <- struct{}{}:
	default:
	}
}

func (w *writer) loop() {
	defer close(w.done)
	for {
		w.mu.Lock()
		q := w.q
		w.q = nil
		closed := w.closed
		w.mu.Unlock()
		for _, f := range q {
			f()
		}
		if len(q) > 0 {
			continue
		}
		if closed {
			return
		}
		<-w.wake
	}
}

func (w *writer) flush() {
	c := make(chan struct{})
	w.push(func() { close(c) })
	select {
	case <-c:
	case <-w.done:
	}
}

func (w *writer) stop() {
	w.mu.Lock()
	w.closed = true
	w.mu.Unlock()
	select {
	case w.wake <- struct{}{}:
	default:
	}
	<-w.done
}
