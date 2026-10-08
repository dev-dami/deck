// Package session owns Deck's running agents and the shared threads they talk in.
//
// # Model
//
// The unit is the Thread: the user plus any number of agents (members). A
// one-member thread behaves exactly like a plain agent session. Every member
// sees everything: the Manager keeps one log per thread and a cursor per member,
// and before each turn it feeds the member what it missed since its last turn -
// the user's messages, other agents' final replies and relayed messages, but only
// one-line summaries of tool use ("[codex changed 2 files: a.go, b.go]",
// "[claude ran: go test ./...]"). A message addressed to a member that is
// mid-turn is queued and delivered, merged with anything else queued, when its
// turn ends. Approvals always need the user.
//
// # Talking
//
// The user: Post(threadID, text). "@name" at the start of a clause addresses
// that member (ParseAddressing has the exact rules); "@claude" / "@codex" naming
// a kind the thread has no live member of adds a new member of that kind;
// "@all" addresses everyone; with no mention the message goes to the member who
// spoke last (or the only member; or, in an empty thread, a new member of the
// default kind). Several addressees receive it concurrently.
//
// Agents: through the deck MCP tool send_message (Options.Hub receives the
// calls; Message.To is a member id/name or "all"), or by starting a line of their
// final reply with "@name". Both are relayed, logged in the thread and reported
// to the UI as RelayMsg. At most Options.MaxHops (default 6) relays happen after
// each user message; the next one is held, the thread is paused (PausedMsg) and
// stays so until Continue (or a new Post, which drops the held relays from
// delivery but leaves them in the log). A member cannot relay to itself, and an
// identical message relayed twice in a row between the same pair is dropped with
// a NoticeMsg. Failed tool calls do not count as hops.
//
// # API (UI side)
//
//	NewManager(adapters, store, cfg, Options{...})
//	Updates() / Listen()              the single stream of tea messages (msgs.go)
//	NewThread(dir) -> threadID        an empty thread
//	Post(threadID, text)              the user speaks (see above)
//	AddMember(threadID, kind, model)  add an agent without sending anything
//	Continue(threadID)                release a pause
//	Interrupt(agentID)                stop one member's turn
//	Rename(agentID, name)             change a member's display/addressing name
//	Resume(threadID)                  restart a stored thread's members; returns history
//	Threads() / Thread(id)            live threads (members, paused, hops, cost)
//	Agents() / Agent(id)              every live member across threads
//	Saved()                           stored threads, for a resume picker
//	History(threadID)                 a live thread's log as replayable messages
//	SetMode(threadID, mode)           switch the thread's permission mode (see below)
//	Mode(threadID)                    the thread's mode
//	NextMode(mode)                    plan -> ask -> yolo -> plan (shift+tab)
//	SetModel(agentID, model)          switch one member's model mid-session
//	SetDefaultModel(kind, model)      model for members of that kind started later
//	Models(ctx, kind)                 the models the kind's CLI offers (cached)
//	Start / Send                      one-member shortcuts kept for older callers
//	Close()
//
// # Permission modes and models
//
// Every thread has a mode (agent.ModePlan, ModeAsk, ModeYolo), stored with it.
// New threads start in config.DefaultMode (ask when unset); a config that says
// yolo is ignored at startup with a NoticeMsg, and a stored yolo thread resumes in
// ask, because yolo must be a deliberate act of the session (shift+tab). The mode
// is passed to members as Spec.Mode and, when it changes, to live members whose run
// implements agent.ModeSetter; the next prompt to each member also starts with
// "[deck] Permission mode is now <mode>." once. Deck enforces the mode on its own
// as well, whatever the adapter does: in yolo every ApprovalRequest is answered
// Allow and surfaces only as AutoApprovedMsg (switching into yolo does the same
// for the requests already pending, after ApprovalGoneMsg); in plan, edit and
// command requests are denied ("Deck is in plan mode: propose the change instead
// of making it.") and surface as AutoDeniedMsg; in ask nothing changes. The UI
// learns of changes through ModeChangedMsg and ModelChangedMsg.
//
// Models come from the CLI itself: Models lists them through an adapter that
// implements agent.ModelLister; SetModel needs the member's run to implement
// agent.ModelSetter and otherwise fails with "this agent can't switch model
// mid-session; start a new one". New members get SetDefaultModel's model for their
// kind, else config.Models.
//
// The Manager persists each thread (members, cursors, and a log of entries that
// carry the speaker) on a single writer goroutine, so the UI never blocks on disk.
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

// DefaultMaxHops is the number of agent-to-agent relays allowed after each user
// message before the thread pauses.
const DefaultMaxHops = 6

// Options configures a Manager. The zero value is valid: no messaging tools, no
// MCP, six hops.
type Options struct {
	// Hub receives the agents' send_message calls and serves read_thread. Nil
	// disables the tools; @mention relays still work.
	Hub agent.Hub
	// MCPFor returns the MCP server to register with the agent's CLI (approve
	// tool plus the messaging tools). Nil, or a nil result, means none.
	MCPFor func(agentID string) *agent.MCPServer
	// MaxHops caps agent-to-agent relays after each user message. 0 = 6.
	MaxHops int
}

// AgentInfo is a snapshot of one agent (a thread member).
type AgentInfo struct {
	ID       string
	Kind     string
	Name     string
	Title    string // the thread's title: its first message, one line
	Dir      string
	Model    string
	Status   Status
	CostUSD  float64
	Pending  int // approvals waiting for an answer
	ThreadID string
}

// ThreadInfo is a snapshot of one live thread.
type ThreadInfo struct {
	ID      string
	Title   string
	Dir     string
	Members []AgentInfo // in join order
	// Paused is true after the hop limit was hit and until Continue.
	Paused  bool
	Hops    int // agent-to-agent relays since the last user message
	MaxHops int
	Held    int // relays waiting for Continue
	// Pending counts approvals waiting across the members.
	Pending     int
	CostUSD     float64
	LastSpeaker string     // member id the next unaddressed message goes to ("" = none yet)
	Mode        agent.Mode // the thread's permission mode
}

// Manager owns every thread and agent.
type Manager struct {
	adapters map[string]agent.Adapter
	st       *store.Store
	opts     Options
	maxHops  int

	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}
	out    chan tea.Msg
	w      *writer
	wg     sync.WaitGroup

	modelMu    sync.Mutex        // guards defModels and modelCache
	defModels  map[string]string // kind -> model for members started later (SetDefaultModel)
	modelCache map[string][]agent.ModelInfo

	mu      sync.Mutex
	cfg     config.Config
	closed  bool
	order   []*agentState
	byID    map[string]*agentState
	counts  map[string]int
	threads map[string]*thread
	torder  []*thread
}

// NewManager returns a Manager that starts agents from adapters (keyed by kind,
// e.g. "claude", "codex") and persists threads to st. st may be nil, in which
// case nothing is persisted. opts is optional.
func NewManager(adapters map[string]agent.Adapter, st *store.Store, cfg config.Config, opts ...Options) *Manager {
	var o Options
	if len(opts) > 0 {
		o = opts[0]
	}
	ctx, cancel := context.WithCancel(context.Background())
	m := &Manager{
		adapters: adapters,
		st:       st,
		opts:     o,
		maxHops:  o.MaxHops,
		ctx:      ctx,
		cancel:   cancel,
		done:     make(chan struct{}),
		out:      make(chan tea.Msg, 512),
		cfg:      cfg,
		byID:     map[string]*agentState{},
		counts:   map[string]int{},
		threads:  map[string]*thread{},
	}
	if m.maxHops <= 0 {
		m.maxHops = DefaultMaxHops
	}
	if _, ignored := startMode(cfg); ignored {
		m.out <- yoloIgnoredNotice() // the channel is buffered; nobody is listening yet
	}
	m.w = newWriter()
	if o.Hub != nil {
		o.Hub.SetThreadReader(m.readThread)
		m.wg.Add(1)
		go m.hubLoop(o.Hub.Messages())
	}
	return m
}

// SetConfig replaces the config used for agents started from now on.
func (m *Manager) SetConfig(cfg config.Config) {
	m.mu.Lock()
	old := m.cfg
	m.cfg = cfg
	m.mu.Unlock()
	if _, ignored := startMode(cfg); ignored {
		if _, was := startMode(old); !was {
			m.emit(yoloIgnoredNotice())
		}
	}
}

func (m *Manager) config() config.Config {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.cfg
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

// Updates is the single stream of messages: EventMsg, ApprovalMsg,
// ApprovalGoneMsg, RelayMsg, PausedMsg, ContinuedMsg, MemberAddedMsg, NoticeMsg,
// ModeChangedMsg, ModelChangedMsg, AutoApprovedMsg, AutoDeniedMsg.
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

// Saved lists the stored threads, most recent first (for a resume picker).
func (m *Manager) Saved() []store.Thread {
	if m.st == nil {
		return nil
	}
	ts, _ := m.st.List()
	return ts
}

// Threads lists the live threads in creation order.
func (m *Manager) Threads() []ThreadInfo {
	m.mu.Lock()
	ts := append([]*thread(nil), m.torder...)
	m.mu.Unlock()
	out := make([]ThreadInfo, 0, len(ts))
	for _, t := range ts {
		out = append(out, m.threadInfo(t))
	}
	return out
}

// Thread returns one live thread's snapshot.
func (m *Manager) Thread(id string) (ThreadInfo, bool) {
	t := m.thread(id)
	if t == nil {
		return ThreadInfo{}, false
	}
	return m.threadInfo(t), true
}

func (m *Manager) threadInfo(t *thread) ThreadInfo {
	t.mu.Lock()
	members := append([]*agentState(nil), t.members...)
	info := ThreadInfo{
		ID: t.id, Title: t.title, Dir: t.dir, Paused: t.paused, Hops: t.hops,
		MaxHops: m.maxHops, Held: len(t.heldIdx), LastSpeaker: t.lastSpeaker, Mode: t.mode,
	}
	t.mu.Unlock()
	for _, a := range members {
		ai := a.info()
		ai.Title = info.Title
		info.Members = append(info.Members, ai)
		info.Pending += ai.Pending
		info.CostUSD += ai.CostUSD
	}
	return info
}

// Agents returns a snapshot of every live member, across threads, in start order.
func (m *Manager) Agents() []AgentInfo {
	m.mu.Lock()
	order := append([]*agentState(nil), m.order...)
	m.mu.Unlock()
	out := make([]AgentInfo, 0, len(order))
	for _, a := range order {
		out = append(out, a.fullInfo())
	}
	return out
}

// Agent returns one agent's snapshot.
func (m *Manager) Agent(id string) (AgentInfo, bool) {
	a := m.get(id)
	if a == nil {
		return AgentInfo{}, false
	}
	return a.fullInfo(), true
}

func (m *Manager) get(id string) *agentState {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.byID[id]
}

func (m *Manager) thread(id string) *thread {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.threads[id]
}

// History returns a live thread's log as the messages the live stream carried,
// for a view that needs to be rebuilt.
func (m *Manager) History(threadID string) []tea.Msg {
	t := m.thread(threadID)
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return historyOf(t.id, t.log)
}

func historyOf(threadID string, entries []store.Entry) []tea.Msg {
	var out []tea.Msg
	for _, e := range entries {
		if msg := msgFor(threadID, e); msg != nil {
			out = append(out, msg)
		}
	}
	return out
}

// ---- creating threads and members ----

var errClosed = errors.New("session: closed")

func (m *Manager) isClosed() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.closed
}

// NewThread creates an empty thread whose agents will work in dir (empty = the
// current directory) and returns its id. Nothing is stored until it has content.
func (m *Manager) NewThread(dir string) (string, error) {
	if dir == "" {
		wd, err := os.Getwd()
		if err != nil {
			return "", err
		}
		dir = wd
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return "", errClosed
	}
	mode, _ := startMode(m.cfg)
	t := newThread(m, store.NewID(), dir, mode)
	m.threads[t.id] = t
	m.torder = append(m.torder, t)
	return t.id, nil
}

// AddMember starts an agent of the given kind in the thread without sending it
// anything and returns its id ("claude", "claude2", "codex", ...). An empty model
// means the configured model for that kind. Its first prompt, when someone
// addresses it, includes the whole thread so far.
func (m *Manager) AddMember(threadID, kind, model string) (string, error) {
	t := m.thread(threadID)
	if t == nil {
		return "", fmt.Errorf("session: no thread %q", threadID)
	}
	a, err := m.newMember(t, kind, model, "")
	if err != nil {
		return "", err
	}
	if err := m.startRun(t, a, "", ""); err != nil {
		m.dropMember(t, a)
		return "", err
	}
	return a.id, nil
}

// newMember registers a member (without starting its process) and announces it.
func (m *Manager) newMember(t *thread, kind, model, preferredID string) (*agentState, error) {
	if _, ok := m.adapters[kind]; !ok {
		return nil, fmt.Errorf("session: unknown agent kind %q", kind)
	}
	id, cfg, err := m.allocate(kind, preferredID)
	if err != nil {
		return nil, err
	}
	if model == "" {
		model = m.defaultModel(cfg, kind)
	}
	a := &agentState{id: id, kind: kind, name: id, model: model, thread: t, base: StatusIdle, started: time.Now()}
	m.mu.Lock()
	m.order = append(m.order, a)
	m.byID[id] = a
	m.mu.Unlock()

	e := store.Entry{Type: store.EntryMember, Speaker: id, Event: eventText(kind)}
	t.mu.Lock()
	a.knownMode = t.mode
	t.members = append(t.members, a)
	m.appendLocked(t, e)
	t.mu.Unlock()
	m.persistMeta(t)
	m.emit(msgFor(t.id, e))
	return a, nil
}

// dropMember forgets a member that never started.
func (m *Manager) dropMember(t *thread, a *agentState) {
	m.mu.Lock()
	for i, o := range m.order {
		if o == a {
			m.order = append(m.order[:i], m.order[i+1:]...)
			break
		}
	}
	delete(m.byID, a.id)
	m.mu.Unlock()
	t.mu.Lock()
	for i, o := range t.members {
		if o == a {
			t.members = append(t.members[:i], t.members[i+1:]...)
			break
		}
	}
	t.mu.Unlock()
	m.persistMeta(t)
}

// startRun launches the member's CLI. prompt, when non-empty, is its first turn.
func (m *Manager) startRun(t *thread, a *agentState, prompt, resumeID string) error {
	if m.isClosed() {
		return errClosed
	}
	ad := m.adapters[a.kind]
	if ad == nil {
		return fmt.Errorf("session: unknown agent kind %q", a.kind)
	}
	spec := m.spec(t, a, prompt, resumeID)
	run, err := ad.Start(m.ctx, spec)
	if err != nil {
		return fmt.Errorf("session: start %s: %w", a.kind, err)
	}
	a.mu.Lock()
	a.run = run
	a.mu.Unlock()
	m.wg.Add(1)
	go m.pump(a)
	return nil
}

func (m *Manager) spec(t *thread, a *agentState, prompt, resume string) agent.Spec {
	cfg := m.config()
	s := agent.Spec{
		ID: a.id, Dir: t.dir, Model: a.model, ResumeID: resume, FirstPrompt: prompt,
		UserName: cfg.UserName, TellName: cfg.TellAgentsName, Mode: t.getMode(),
	}
	switch a.kind {
	case "claude":
		s.Binary = cfg.ClaudeBinary
	case "codex":
		s.Binary = cfg.CodexBinary
	}
	if m.opts.MCPFor != nil {
		s.MCP = m.opts.MCPFor(a.id)
	}
	s.Instructions = m.instructions(t, a, s.MCP != nil && m.opts.Hub != nil)
	return s
}

// allocate reserves an agent id for kind: preferred when it is free (resuming),
// otherwise "claude", "claude2", ... unique across the deck.
func (m *Manager) allocate(kind, preferred string) (string, config.Config, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return "", m.cfg, errClosed
	}
	if preferred != "" && m.byID[preferred] == nil {
		return preferred, m.cfg, nil
	}
	for {
		m.counts[kind]++
		n := m.counts[kind]
		id := kind
		if n > 1 {
			id = fmt.Sprintf("%s%d", kind, n)
		}
		if m.byID[id] == nil && !m.reservedLocked(id) {
			return id, m.cfg, nil
		}
	}
}

func (m *Manager) reservedLocked(id string) bool {
	for _, a := range m.order {
		if strings.EqualFold(a.getName(), id) {
			return true
		}
	}
	return false
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

func (m *Manager) defaultKind() string {
	cfg := m.config()
	if _, ok := m.adapters[cfg.DefaultAgent]; ok {
		return cfg.DefaultAgent
	}
	if ks := m.Kinds(); len(ks) > 0 {
		return ks[0]
	}
	return ""
}

// ---- the user speaks ----

// Post sends the user's message into a thread. See the package comment for how
// it is addressed. A message that is only mentions (for example "@codex") just
// adds the missing members. Errors from starting or sending to an agent are
// returned, but the message stays in the thread.
func (m *Manager) Post(threadID, text string) error {
	t := m.thread(threadID)
	if t == nil {
		return fmt.Errorf("session: no thread %q", threadID)
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return errors.New("session: empty message")
	}

	t.mu.Lock()
	refs := t.refs()
	t.mu.Unlock()
	ad := ParseAddressing(text, refs, m.Kinds())
	if len(ad.Unknown) > 0 {
		var names []string
		for _, r := range refs {
			names = append(names, r.Name)
		}
		return fmt.Errorf("no agent @%s in this thread (members: %s; kinds: %s)",
			ad.Unknown[0], strings.Join(names, ", "), strings.Join(m.Kinds(), ", "))
	}

	var targets []*agentState
	newKinds := ad.NewKinds
	t.mu.Lock()
	switch {
	case ad.All:
		targets = t.live()
		if len(targets) == 0 && len(newKinds) == 0 {
			t.mu.Unlock()
			return errors.New("nobody is running in this thread")
		}
	case len(ad.Members) > 0:
		for _, id := range ad.Members {
			a := t.member(id)
			if a != nil && a.status() == StatusExited {
				t.mu.Unlock()
				return fmt.Errorf("%s has exited", a.getName())
			}
			targets = append(targets, a)
		}
	}
	if !ad.Mentioned() {
		if a := t.defaultTarget(); a != nil {
			targets = []*agentState{a}
		} else if len(t.members) == 0 {
			newKinds = []string{m.defaultKind()}
		} else {
			t.mu.Unlock()
			return errors.New("every agent in this thread has exited; @claude or @codex adds a new one")
		}
	}
	t.mu.Unlock()
	if len(newKinds) == 1 && newKinds[0] == "" {
		return errors.New("session: no agent kinds available")
	}

	body := ad.Body
	if !ad.Mentioned() {
		body = text
	}
	for _, k := range newKinds {
		a, err := m.newMember(t, k, "", "")
		if err != nil {
			return err
		}
		if body == "" {
			if err := m.startRun(t, a, "", ""); err != nil {
				m.dropMember(t, a)
				return err
			}
			continue
		}
		targets = append(targets, a)
	}
	if body == "" {
		return nil
	}
	return m.postTo(t, body, targets)
}

// postTo logs a user message addressed to targets and delivers it. A user
// message ends any pause and resets the hop count.
func (m *Manager) postTo(t *thread, text string, targets []*agentState) error {
	cfg := m.config()
	speaker := cfg.UserName
	if speaker == "" {
		speaker = "user"
	}
	e := store.Entry{Type: store.EntryUser, Speaker: speaker, To: ids(targets), Event: agent.Event{Kind: agent.KindUserText, Text: text}}

	var msgs []tea.Msg
	t.mu.Lock()
	wasPaused := t.paused
	t.paused, t.hops, t.heldIdx = false, 0, nil
	t.lastRelay = map[string]string{}
	if t.title == "" {
		t.title = oneLine(text)
	}
	m.appendLocked(t, e)
	msgs = append(msgs, msgFor(t.id, e))
	if wasPaused {
		ce := store.Entry{Type: store.EntryContinue}
		m.appendLocked(t, ce)
		msgs = append(msgs, msgFor(t.id, ce))
	}
	t.mu.Unlock()
	m.persistMeta(t)
	m.emitAll(msgs...)

	var first error
	for _, a := range targets {
		if _, err := m.deliver(t, a); err != nil && first == nil {
			first = err
		}
	}
	return first
}

// Start creates a one-member thread in dir with an agent of the given kind and
// sends it prompt (if any). It returns the agent's id; Agent(id).ThreadID is the
// thread. Kept for callers that predate shared threads.
func (m *Manager) Start(kind, prompt, dir, model string) (string, error) {
	if _, ok := m.adapters[kind]; !ok {
		return "", fmt.Errorf("session: unknown agent kind %q", kind)
	}
	tid, err := m.NewThread(dir)
	if err != nil {
		return "", err
	}
	t := m.thread(tid)
	a, err := m.newMember(t, kind, model, "")
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(prompt) == "" {
		if err := m.startRun(t, a, "", ""); err != nil {
			m.dropMember(t, a)
			return "", err
		}
		return a.id, nil
	}
	if err := m.postTo(t, strings.TrimSpace(prompt), []*agentState{a}); err != nil {
		return a.id, err
	}
	return a.id, nil
}

// Send delivers the user's message to one agent, in its own thread, whatever
// the text says about addressing. Kept for callers that predate shared threads.
func (m *Manager) Send(id, prompt string) error {
	a := m.get(id)
	if a == nil {
		return fmt.Errorf("session: no agent %q", id)
	}
	if a.status() == StatusExited {
		return fmt.Errorf("session: %s has exited", id)
	}
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return errors.New("session: empty message")
	}
	return m.postTo(a.thread, prompt, []*agentState{a})
}

// ---- delivery ----

type delivery int

const (
	dNone    delivery = iota // nothing was pending
	dStarted                 // a turn was started
	dQueued                  // the member is busy; it will get the message when its turn ends
	dExited                  // the member has exited
)

// deliver starts a turn for a if messages addressed to it are waiting and it is
// idle. Its prompt is a briefing of what it missed plus those messages.
func (m *Manager) deliver(t *thread, a *agentState) (delivery, error) {
	if m.isClosed() {
		return dNone, errClosed
	}
	t.mu.Lock()
	if !t.hasTrigger(a) {
		t.mu.Unlock()
		return dNone, nil
	}
	prev, ok, exited := a.tryBegin()
	if exited {
		t.mu.Unlock()
		return dExited, nil
	}
	if !ok {
		t.mu.Unlock()
		return dQueued, nil
	}
	prompt, cursor, hold := m.buildPrompt(t, a)
	oldCursor, oldHold, oldKnown := a.cursor, a.holdFrom, a.knownMode
	a.cursor, a.holdFrom = cursor, hold
	if a.knownMode != t.mode {
		prompt = modeLine(t.mode) + "\n\n" + prompt
		a.knownMode = t.mode
	}
	t.mu.Unlock()

	var err error
	if run := a.getRun(); run != nil {
		err = run.Send(prompt)
	} else {
		err = m.startRun(t, a, prompt, "")
	}
	if err != nil {
		t.mu.Lock()
		a.cursor, a.holdFrom, a.knownMode = oldCursor, oldHold, oldKnown
		t.mu.Unlock()
		a.restore(prev)
		return dNone, err
	}
	m.persistMeta(t)
	return dStarted, nil
}

// Interrupt stops the agent's current turn. Pending approvals are denied first
// so the underlying CLI is never left waiting, and messages queued for the
// agent no longer start a turn (they stay in the thread and in its next briefing).
func (m *Manager) Interrupt(id string) error {
	a := m.get(id)
	if a == nil {
		return fmt.Errorf("session: no agent %q", id)
	}
	t := a.thread
	t.mu.Lock()
	t.dropPending(a)
	t.mu.Unlock()
	a.markInterrupted()
	for _, p := range a.takePending() {
		p.respond(agent.Decision{Allow: false, Reason: "interrupted"})
		m.emit(ApprovalGoneMsg{ThreadID: t.id, AgentID: id, ID: p.id})
	}
	if run := a.getRun(); run != nil {
		return run.Interrupt()
	}
	return nil
}

// Rename changes an agent's display name. Names must be unique, non-empty and
// free of whitespace and '@' (they are used for addressing), and cannot be "all",
// "user", "deck" or another kind's name.
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
	reserved := strings.EqualFold(name, "all") || strings.EqualFold(name, "user") || strings.EqualFold(name, "deck")
	for k := range m.adapters {
		if strings.EqualFold(name, k) && k != a.kind {
			reserved = true
		}
	}
	if reserved {
		m.mu.Unlock()
		return fmt.Errorf("session: name %q is reserved", name)
	}
	for _, o := range m.order {
		if o != a && (strings.EqualFold(o.getName(), name) || strings.EqualFold(o.id, name)) {
			m.mu.Unlock()
			return fmt.Errorf("session: name %q is taken", name)
		}
	}
	m.mu.Unlock()
	a.mu.Lock()
	a.name = name
	a.mu.Unlock()
	m.persistMeta(a.thread)
	return nil
}

// ---- resume ----

// Resume restarts a stored thread: every member is started again with its CLI
// session id, in the thread's directory, under its old id when that is free.
// It returns the thread and its stored history as the messages the live stream
// would have carried (with member ids rewritten if they had to change). If the
// thread is already running it returns its info and no history.
func (m *Manager) Resume(threadID string) (ThreadInfo, []tea.Msg, error) {
	if m.st == nil {
		return ThreadInfo{}, nil, errors.New("session: no store")
	}
	if old := m.thread(threadID); old != nil {
		if len(old.live()) > 0 {
			return m.threadInfo(old), nil, nil
		}
		m.forget(old)
	}
	entries, th, err := m.st.LoadEntries(threadID)
	if err != nil {
		return ThreadInfo{}, nil, err
	}
	if len(th.Members) == 0 {
		return ThreadInfo{}, nil, errors.New("session: thread has no agents")
	}
	for _, mem := range th.Members {
		if _, ok := m.adapters[mem.Kind]; !ok {
			return ThreadInfo{}, nil, fmt.Errorf("session: unknown agent kind %q", mem.Kind)
		}
	}
	dir := th.Dir
	if dir == "" {
		dir, _ = os.Getwd()
	}
	cfg := m.config()
	for i := range entries {
		canonical(&entries[i], cfg.UserName)
	}

	mode := agent.Mode(th.Mode)
	switch {
	case mode == agent.ModeYolo:
		// yolo is a per-session decision: a stored thread never wakes up in it.
		mode = agent.ModeAsk
		m.emit(NoticeMsg{ThreadID: th.ID, Text: "this thread was in yolo mode; resumed in ask mode. Press shift+tab to switch"})
	case !validMode(mode):
		mode = agent.ModeAsk
	}
	t := newThread(m, th.ID, dir, mode)
	t.created = true
	t.title = th.Title
	remap := map[string]string{}
	var started []*agentState
	fail := func(err error) (ThreadInfo, []tea.Msg, error) {
		for _, a := range started {
			if run := a.getRun(); run != nil {
				_ = run.Close()
			}
		}
		m.forget(t)
		return ThreadInfo{}, nil, err
	}

	var members []*agentState
	for _, mem := range th.Members {
		id, _, err := m.allocate(mem.Kind, mem.ID)
		if err != nil {
			return fail(err)
		}
		if id != mem.ID {
			remap[mem.ID] = id
		}
		name := mem.Name
		if name == "" {
			name = id
		}
		model := mem.Model
		if model == "" {
			model = m.defaultModel(cfg, mem.Kind)
		}
		cursor := mem.Cursor
		if th.Version < store.CurrentVersion && len(th.Members) == 1 {
			cursor = len(entries) // a single-agent thread: that agent has seen it all
		}
		if cursor > len(entries) {
			cursor = len(entries)
		}
		a := &agentState{
			id: id, kind: mem.Kind, name: name, model: model, thread: t, base: StatusIdle,
			started: time.Now(), costTotal: mem.CostUSD, sessionID: mem.SessionID, cursor: cursor,
			knownMode: mode,
		}
		m.mu.Lock()
		taken := false
		for _, o := range m.order {
			if strings.EqualFold(o.getName(), name) {
				taken = true
			}
		}
		if taken {
			a.name = id
		}
		m.order = append(m.order, a)
		m.byID[id] = a
		m.mu.Unlock()
		members = append(members, a)
	}
	t.members = members

	for i := range entries {
		e := &entries[i]
		e.AgentID = mapID(remap, e.AgentID)
		if e.Type != store.EntryUser {
			e.Speaker = mapID(remap, e.Speaker)
		}
		for j := range e.To {
			e.To[j] = mapID(remap, e.To[j])
		}
		if e.Type == store.EntryEvent && e.Kind == agent.KindAssistantText && !e.Delta && e.AgentID != "" {
			t.lastSpeaker = e.AgentID
		}
	}
	t.log = entries
	for _, a := range members {
		t.boundary[a.id] = len(entries) // a turn cut short by a crash counts as finished
		if t.lastSpeaker == "" {
			t.lastSpeaker = a.id
		}
	}
	if t.member(t.lastSpeaker) == nil {
		t.lastSpeaker = members[0].id
	}

	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return fail(errClosed)
	}
	m.threads[t.id] = t
	m.torder = append(m.torder, t)
	m.mu.Unlock()

	for i, a := range members {
		if err := m.startRun(t, a, "", th.Members[i].SessionID); err != nil {
			return fail(fmt.Errorf("session: resume %s: %w", a.kind, err))
		}
		started = append(started, a)
	}
	return m.threadInfo(t), historyOf(t.id, entries), nil
}

func mapID(remap map[string]string, id string) string {
	if n, ok := remap[id]; ok {
		return n
	}
	return id
}

// canonical rewrites a legacy log line (the adapter's user_text echo of a prompt)
// as a user entry addressed to the agent that received it.
func canonical(e *store.Entry, user string) {
	if e.Type == store.EntryEvent && e.Kind == agent.KindUserText {
		e.Type = store.EntryUser
		e.Speaker = user
		if e.AgentID != "" && len(e.To) == 0 {
			e.To = []string{e.AgentID}
		}
		e.AgentID = ""
	}
}

// forget removes a thread whose members have all exited so it can be resumed again.
func (m *Manager) forget(t *thread) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.threads, t.id)
	for i, o := range m.torder {
		if o == t {
			m.torder = append(m.torder[:i], m.torder[i+1:]...)
			break
		}
	}
	keep := m.order[:0]
	for _, a := range m.order {
		if a.thread == t {
			delete(m.byID, a.id)
			continue
		}
		keep = append(keep, a)
	}
	m.order = keep
}

// ---- lifecycle ----

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
		run := a.getRun()
		if run == nil {
			continue
		}
		cw.Add(1)
		go func(run agent.Run) {
			defer cw.Done()
			_ = run.Close()
		}(run)
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

// emit hands a message to the UI, giving up once the manager is closed.
func (m *Manager) emit(msg tea.Msg) {
	if msg == nil {
		return
	}
	select {
	case m.out <- msg:
	case <-m.done:
	}
}

// pump fans one run's events and approvals into the shared channel.
func (m *Manager) pump(a *agentState) {
	defer m.wg.Done()
	run := a.getRun()
	evs, aps := run.Events(), run.Approvals()
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
		m.onEvent(a, agent.Event{AgentID: a.id, Time: time.Now(), Kind: agent.KindExited})
	}
}

func (m *Manager) onEvent(a *agentState, ev agent.Event) {
	if ev.AgentID == "" {
		ev.AgentID = a.id
	}
	if ev.Time.IsZero() {
		ev.Time = time.Now()
	}
	if ev.Kind == agent.KindUserText {
		return // the adapter's echo of a prompt; Deck's own user entry stands for it
	}
	t := a.thread
	res := a.apply(ev)
	if !ev.Delta {
		t.mu.Lock()
		m.appendLocked(t, store.Entry{Event: ev})
		t.mu.Unlock()
	}
	if res.metaDirty {
		m.persistMeta(t)
	}
	for _, id := range res.gone {
		m.emit(ApprovalGoneMsg{ThreadID: t.id, AgentID: a.id, ID: id})
	}
	m.emit(EventMsg{Event: ev, ThreadID: t.id})
	if ev.Kind == agent.KindTurnDone {
		m.afterTurn(a)
	}
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
	a.addPending(id, req.Kind, summaryOf(req), req.Respond)
	// Added to the pending list first, so a SetMode(yolo) racing with this
	// request answers it either here or when it drains the list.
	if m.enforce(a.thread, a, req) {
		return
	}
	m.emit(ApprovalMsg{ApprovalRequest: req, ThreadID: a.thread.id})
}

// ---- per-agent state ----

type pendingApproval struct {
	id      string
	kind    agent.ApprovalKind
	summary string
	respond func(agent.Decision)
}

type agentState struct {
	thread  *thread
	id      string
	kind    string
	started time.Time

	// cursor and holdFrom are guarded by thread.mu: cursor is how much of the
	// thread log the member has been briefed on; holdFrom maps another member to
	// the first log index of its still-running turn that was held back.
	cursor   int
	holdFrom map[string]int
	// knownMode is the permission mode the member was last told about (its
	// Spec.Mode, or the line at the top of its last prompt); guarded by thread.mu.
	knownMode agent.Mode

	mu          sync.Mutex
	run         agent.Run
	name        string
	model       string
	base        Status
	pending     []pendingApproval
	costTotal   float64 // folded turns
	costTurn    float64 // current turn, cumulative as reported
	sessionID   string
	lastText    string // the last assistant text of the running turn
	interrupted bool
}

func (a *agentState) getRun() agent.Run {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.run
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
		ID: a.id, Kind: a.kind, Name: a.name, Dir: a.thread.dir, Model: a.model,
		Status: a.statusLocked(), CostUSD: a.costTotal + a.costTurn, Pending: len(a.pending),
		ThreadID: a.thread.id,
	}
}

// fullInfo is info plus the thread title.
func (a *agentState) fullInfo() AgentInfo {
	ai := a.info()
	a.thread.mu.Lock()
	ai.Title = a.thread.title
	a.thread.mu.Unlock()
	return ai
}

// memberMeta is the member's stored form. The caller holds thread.mu.
func (a *agentState) memberMeta() store.Member {
	a.mu.Lock()
	defer a.mu.Unlock()
	return store.Member{
		ID: a.id, Kind: a.kind, Name: a.name, Model: a.model, SessionID: a.sessionID,
		CostUSD: a.costTotal + a.costTurn, Cursor: a.cursor,
	}
}

// tryBegin marks the agent working before a prompt is sent, so a fast turn_done
// can never be overwritten by a late "working". ok is false when the agent is
// busy (working, in a tool, waiting for approval); exited when it is gone.
func (a *agentState) tryBegin() (prev Status, ok, exited bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	switch a.statusLocked() {
	case StatusExited:
		return a.base, false, true
	case StatusWorking, StatusTool, StatusApproval:
		return a.base, false, false
	}
	prev = a.base
	a.costTotal += a.costTurn
	a.costTurn = 0
	a.base = StatusWorking
	a.lastText = ""
	a.interrupted = false
	return prev, true, false
}

func (a *agentState) restore(prev Status) {
	a.mu.Lock()
	if a.base == StatusWorking {
		a.base = prev
	}
	a.mu.Unlock()
}

func (a *agentState) markInterrupted() {
	a.mu.Lock()
	a.interrupted = true
	a.mu.Unlock()
}

// takeFinal returns the final assistant text of the turn that just ended and
// whether the turn was interrupted.
func (a *agentState) takeFinal() (string, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	text, intr := a.lastText, a.interrupted
	a.lastText = ""
	return strings.TrimSpace(text), intr
}

func (a *agentState) addPending(id string, kind agent.ApprovalKind, summary string, respond func(agent.Decision)) {
	a.mu.Lock()
	a.pending = append(a.pending, pendingApproval{id: id, kind: kind, summary: summary, respond: respond})
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
			res.metaDirty = true
		}
	case agent.KindAssistantText:
		a.base = StatusWorking
		if !ev.Delta && strings.TrimSpace(ev.Text) != "" {
			a.lastText = ev.Text
		}
	case agent.KindReasoning, agent.KindToolResult:
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

func eventText(s string) agent.Event { return agent.Event{Text: s} }

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
