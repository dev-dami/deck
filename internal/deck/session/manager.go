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
// The user: Post(threadID, text) or PostWith(threadID, text, images). "@name"
// (or "$name", the same mention) at the start of a clause addresses
// that member (ParseAddressing has the exact rules); "@claude" / "@codex" naming
// a kind reaches whichever live member of that kind is free, or adds one when
// there is none; "@all" addresses everyone; "@charles", a profile (a named
// agent, SetProfiles) no member is called yet, adds a member from it named
// after it. Nothing typed adds a second member of a kind or starts a race:
// agents spawn helpers (Spawn), and the clients start races (Race). With no
// mention the message goes to the member who
// spoke last (or the only member; or, in an empty thread, a new member of the
// default kind). Several addressees receive it concurrently.
//
// Agents: through the deck MCP tool execute with op thread.send (Options.Hub
// receives the calls; Message.To is a member id/name, a kind, "claude*" or "all"), or by starting a line of their
// final reply with "@name" (or "$name"). Both are relayed, logged in the thread and reported
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
//	PostWith(threadID, text, images)  the same with image attachments; returns a PostResult
//	Queue(threadID)                   user messages waiting for busy members
//	Steer(itemID)                     deliver a queued message into the running turn now
//	CancelQueued(itemID)              drop a queued message without delivering it
//	Edit(threadID, entryID, text)     the user edits an earlier message of theirs
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
// as well, whatever the adapter does: in yolo every ApprovalRequest but a share
// or a question (agent.ApproveQuestion, which always waits for the person) is
// answered Allow and surfaces only as AutoApprovedMsg (switching into yolo does the same
// for the requests already pending, after ApprovalGoneMsg); in plan, edit and
// command requests are denied ("Deck is in plan mode: propose the change instead
// of making it.") and surface as AutoDeniedMsg; in ask nothing changes. The UI
// learns of changes through ModeChangedMsg and ModelChangedMsg.
//
// # Images, queue, steering, edits
//
// PostWith attaches images (agent.Image: a local path and a media type) to a
// message. They go to every addressee whose run implements agent.ImageSender;
// an addressee without it gets the text plus the line "[deck] The user attached N
// image(s) that this agent can't view: a.png, b.png". The PostResult carries the
// message's EntryID (EventMsg.EntryID; the handle for Edit) and Queued, the items
// that had to wait because their addressee was busy.
//
// Queue(threadID) lists the user's messages waiting for busy members, oldest
// first - one QueuedItem per (message, member); relays between agents are never
// in it. Every change (added, delivered when the turn ends, steered, cancelled,
// edited in place, dropped by Interrupt) is announced with a QueueChangedMsg
// holding the whole queue. Steer(itemID) takes an item out of the queue and
// delivers it now: through agent.Steerer when the member's run implements it,
// otherwise Interrupt then Send. The prompt starts "[deck] The user is redirecting
// you mid-task:" followed by the text, after the usual briefing of what the
// member missed; anything else queued for that member rides along. The steer is
// logged as a user entry with the Steer flag and surfaces as an EventMsg with
// Steer true (and SteerOf = the queued message's EntryID), in history too.
// CancelQueued(itemID) removes an item without delivering it; the member never
// sees that message. Interrupt(agentID) clears the agent's queue.
//
// Edit(threadID, entryID, newText) lets the user edit one of their earlier
// messages (only user messages; EntryID from EventMsg or PostResult). An
// EditedMsg{ThreadID, EntryID, OldText, NewText} is emitted (and replayed by
// History/Resume after the message itself, so a rebuilt view shows the latest text
// with an "edited" tag). Members that already received the original get a prompt
// "[deck] The user edited their earlier message." / "Before: <old>" / "After: <new>" /
// "Treat the edited version as what they meant."; a busy one gets it queued like any
// message (QueuedItem.Edit true). Members that have not received the original yet
// (it is still queued) simply get the new text in its place (QueueChangedMsg).
//
// Context: AgentInfo.ContextTokens / ContextWindow hold how much of its model's
// context window the member's conversation fills, from the latest usage event
// that reported it (0 = unknown). Usage events that carry only context fields do
// not disturb the cost totals.
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
	"cmp"
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/dev-dami/deck/internal/deck/agent"
	"github.com/dev-dami/deck/internal/deck/attribution"
	"github.com/dev-dami/deck/internal/deck/brand"
	"github.com/dev-dami/deck/internal/deck/config"
	"github.com/dev-dami/deck/internal/deck/hub"
	"github.com/dev-dami/deck/internal/deck/profile"
	"github.com/dev-dami/deck/internal/deck/store"
	"github.com/dev-dami/deck/internal/deck/sub"
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
	// StatusRestarting: the CLI is restarting to apply a setting
	// (agent.KindRestarting). Not busy: a prompt sent now waits in the
	// adapter for the new process.
	StatusRestarting Status = "restarting"
	// StatusWaiting: the member's plan ran out and Deck holds its work until
	// the limit resets (waits.go). Not busy, but it starts no turn: messages
	// for it queue.
	StatusWaiting Status = "waiting"
)

// DefaultMaxHops is the number of agent-to-agent relays allowed after each user
// message before the thread pauses.
const DefaultMaxHops = 6

// Options configures a Manager. The zero value is valid: no messaging tools, no
// MCP, six hops.
type Options struct {
	// Hub receives the agents' thread.send calls, serves thread.read and runs
	// the other search/execute ops through the Manager. Nil disables the
	// tools; $mention relays still work.
	Hub agent.Hub
	// MCPFor returns the MCP server to register with the agent's CLI (search
	// and execute, plus approve for Claude). Nil, or a nil result, means none.
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
	Effort   string // "" = the model's default
	Status   Status
	CostUSD  float64
	Pending  int // approvals waiting for an answer
	ThreadID string
	// Parent is the member that started this one when it is a Deck-managed
	// subagent (Ephemeral); "" otherwise.
	Parent    string
	Ephemeral bool
	// Branch is the git branch of the member's own worktree, when it has one.
	Branch string
	// ContextTokens of ContextWindow tokens are in use (0 = not reported yet).
	ContextTokens int
	ContextWindow int
	// ContextSource is where they came from (agent.ContextCLI, ...; "" when
	// the adapter does not say). agent.ApproxContext tells an estimate.
	ContextSource string
	// ContextBaseline of ContextTokens is what the conversation started
	// with (system prompt, tools, instruction files) and DeckTokens what
	// Deck's own text takes; both are estimates, 0 when not known.
	ContextBaseline int
	DeckTokens      int
	// RunningModel is the model slug the CLI reports it is running (e.g.
	// "claude-opus-5-5" when Model is "" or an alias); "" until it says.
	RunningModel string
	// Profile is the named agent it was started from ("" for none), Role
	// that profile's one-line role and Color its CSS colour ("" for the
	// kind's hue).
	Profile string
	Role    string
	Color   string
	// Subs are the subagents its CLI started with its own tool, oldest
	// first (subagents.go).
	Subs []store.SubAgent
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
	// Pinned threads sort first; Resolved ones are settled until a new message.
	Pinned, Resolved bool
	// Orchestration is the thread's orchestration level (config.Levels).
	Orchestration string
	// Branch is the worktree branch of an isolated thread, "" otherwise.
	Branch string
	// Active is when anything last happened in the thread.
	Active time.Time
}

// Manager owns every thread and agent.
type Manager struct {
	adapters map[string]agent.Adapter
	// limits is the last plan limits each kind's CLI reported (brakes.go);
	// guarded by mu.
	limits   map[string]agent.Limits
	st       *store.Store
	subOnce  sync.Once
	sub      *sub.Store // SUB objects, for checkpoints (subStore)
	subFetch SubFetcher // sub.capsule.fetch from peers; nil without them
	opts     Options
	maxHops  int
	// window is how much of each thread's log stays in memory (window.go);
	// 0 keeps all of it.
	window int64

	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}
	out    chan tea.Msg
	w      *writer
	wg     sync.WaitGroup

	// relayQ holds each thread's relay jobs not yet run (onThread); a thread
	// has a key exactly while its worker runs. Guarded by relayMu.
	relayMu sync.Mutex
	relayQ  map[*thread][]func()

	modelMu    sync.Mutex        // guards defModels and modelCache
	defModels  map[string]string // kind -> model for members started later (SetDefaultModel)
	defEfforts map[string]string // kind -> effort for members started later (SetDefaultEffort)
	modelCache map[string][]agent.ModelInfo
	// policyNoted: the kinds whose context policy notice was shown
	// (notePolicy); guarded by mu.
	policyNoted map[string]bool

	mu      sync.Mutex
	cfg     config.Config
	closed  bool
	order   []*agentState
	byID    map[string]*agentState
	counts  map[string]int
	threads map[string]*thread
	torder  []*thread
	// activity reports a thread's running commands (deck.jobs); see SetActivity.
	activity func(threadID string) string
	// team is the peer link for the peers.* ops; see SetTeam.
	team hub.Team
	// profiles reads the named agents for a thread's folder; see
	// SetProfiles.
	profiles func(dir string) []profile.Profile
	// contractor sends code contracts to teammates, and taskEnd hears of
	// every task that ends (contract.go).
	contractor Contractor
	taskEnd    func(threadID string, tk store.Task)
	// sandbox makes the sandbox of each ops contract (opscontract.go).
	sandbox NewSandbox
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
		window:   defaultLogWindow,
		ctx:      ctx,
		cancel:   cancel,
		done:     make(chan struct{}),
		out:      make(chan tea.Msg, 512),
		cfg:      cfg,
		byID:     map[string]*agentState{},
		counts:   map[string]int{},
		threads:  map[string]*thread{},
		relayQ:   map[*thread][]func(){},
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
		o.Hub.SetExecutor(m.execOp)
		// A hub that also serves the CLIs' pre-edit hooks (Claude's).
		if g, ok := o.Hub.(interface {
			SetEditGate(func(agentID string, paths []string) string)
		}); ok {
			g.SetEditGate(m.gateHook)
		}
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
		for i, o := range config.Kinds {
			if o == k {
				return i
			}
		}
		return len(config.Kinds)
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
// ModeChangedMsg, ModelChangedMsg, AutoApprovedMsg, AutoDeniedMsg, QueueChangedMsg,
// EditedMsg.
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

// Busy reports whether any member of a live thread is in a turn (working, in a
// tool, or waiting for an approval). It is Thread without the snapshot, for
// callers that poll it.
func (m *Manager) Busy(threadID string) bool {
	t := m.thread(threadID)
	if t == nil {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, a := range t.members {
		if a.busy() {
			return true
		}
	}
	return false
}

func (m *Manager) threadInfo(t *thread) ThreadInfo {
	t.mu.Lock()
	members := append([]*agentState(nil), t.members...)
	info := ThreadInfo{
		ID: t.id, Title: t.title, Dir: t.dir, Paused: t.paused, Hops: t.hops,
		MaxHops: m.maxHops, Held: len(t.heldIdx), LastSpeaker: t.lastSpeaker, Mode: t.mode,
		Pinned: t.pinned, Resolved: t.resolved, Orchestration: t.level,
	}
	if t.worktree != nil {
		info.Branch = t.worktree.Branch
	}
	if n := len(t.log); n > 0 { // the newest entries are always in memory
		info.Active = t.log[n-1].Time
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
	// The log is append-only: the entries up to its length now never change,
	// so they are rendered outside the lock, which event ingest needs.
	t.mu.Lock()
	h := t.history()
	t.mu.Unlock()
	return h.Msgs()
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
	t.level = m.cfg.StartLevel()
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
	return m.addMember(t, kind, model, preferredID, profile.Profile{})
}

// addMember is newMember for a member that may come from a profile (prof's
// Name is set): it is named after the profile and runs the profile's model
// and effort when it has them.
func (m *Manager) addMember(t *thread, kind, model, preferredID string, prof profile.Profile) (*agentState, error) {
	if _, ok := m.adapters[kind]; !ok {
		if prof.Name != "" {
			return nil, fmt.Errorf("session: %s runs %s, which is not installed", prof.Name, kind)
		}
		return nil, fmt.Errorf("session: unknown agent kind %q", kind)
	}
	id, cfg, err := m.allocate(kind, preferredID)
	if err != nil {
		return nil, err
	}
	if model == "" {
		model = cmp.Or(prof.Model, m.defaultModel(cfg, kind))
	}
	effort := cmp.Or(prof.Effort, m.defaultEffort(cfg, kind))
	a := &agentState{id: id, kind: kind, model: model, effort: effort, thread: t, base: StatusIdle, started: time.Now(), prof: prof}
	m.mu.Lock()
	m.order = append(m.order, a)
	m.byID[id] = a
	m.mu.Unlock()

	e := store.Entry{Type: store.EntryMember, Speaker: id, Event: eventText(kind)}
	t.mu.Lock()
	// The id is unique across the deck (the hub routes by it); the name only
	// has to be unique in this thread, so each session starts at "claude".
	if prof.Name != "" {
		a.name = t.freeProfileName(prof.Name)
	} else {
		a.name = t.freeName(kind)
	}
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
// A new member joins the thread before its CLI starts, so a message to it can
// start the CLI from deliver at the same time. The second caller finds the run
// already there: it does not start a second CLI, it only sends its prompt.
func (m *Manager) startRun(t *thread, a *agentState, prompt, resumeID string) error {
	if m.isClosed() {
		return errClosed
	}
	a.startMu.Lock()
	defer a.startMu.Unlock()
	if run := a.getRun(); run != nil {
		if prompt == "" {
			return nil
		}
		return run.Send(prompt)
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
	m.notePolicy(t, a, spec.Context)
	a.beginContext(spec, agent.CapsOf(ad))
	a.mu.Lock()
	a.run = run
	a.mu.Unlock()
	m.wg.Add(1)
	go m.pump(a)
	return nil
}

// startAll starts the CLIs of several members at once, so they wait for the
// slowest handshake rather than for the sum of them (Codex and OpenCode block
// in Start until theirs is done). resume, when set, holds the CLI session each
// member resumes. The members keep their places in the thread; errs[i] is
// as[i]'s.
func (m *Manager) startAll(t *thread, as []*agentState, resume []string) (errs []error) {
	errs = make([]error, len(as))
	var wg sync.WaitGroup
	for i, a := range as {
		session := ""
		if resume != nil {
			session = resume[i]
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[i] = m.startRun(t, a, "", session)
		}()
	}
	wg.Wait()
	return errs
}

func (m *Manager) spec(t *thread, a *agentState, prompt, resume string) agent.Spec {
	cfg := m.config()
	s := agent.Spec{
		ID: a.id, Dir: a.workDir(t.dir), Model: a.model, Effort: a.effort, ResumeID: resume, FirstPrompt: prompt,
		UserName: cfg.UserName, TellName: cfg.TellAgentsName, Mode: t.getMode(),
		Attribution: cfg.DeckAttribution, Context: contextPolicy(cfg, a.kind),
	}
	if cfg.DeckAttribution {
		// The guard: the agent's git runs Deck's hooks, which put Deck's
		// trailer in place of any other AI attribution, whatever the model wrote.
		if env, err := attribution.Env(cfg.DeckAuthor); err == nil {
			s.Env = env
		} else {
			m.emit(NoticeMsg{ThreadID: t.id, Text: "commit attribution guard is off: " + err.Error()})
		}
	}
	if b := cfg.Binary(a.kind); b != a.kind {
		s.Binary = b
	}
	if m.opts.MCPFor != nil {
		s.MCP = m.opts.MCPFor(a.id)
	}
	// Best effort: the agent can still make the folder, only without the
	// .gitignore that keeps its plans out of git.
	_ = brand.EnsurePlansDir(t.dir)
	s.Instructions = m.instructions(t, a, s.MCP != nil && m.opts.Hub != nil)
	a.mu.Lock()
	s.Briefed = resume != "" && resume == a.sessionID && a.briefed
	a.mu.Unlock()
	if t.opsOf() != nil {
		s.NoTools, s.Instructions = true, opsInstructions
	}
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

func modelFor(cfg config.Config, kind string) string { return cfg.Models.For(kind) }

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
// it is addressed. A message that is only mentions (for example "$codex") just
// adds the missing members. Errors from starting or sending to an agent are
// returned, but the message stays in the thread.
func (m *Manager) Post(threadID, text string) error {
	_, err := m.PostWith(threadID, text, nil)
	return err
}

// PostWith is Post with images attached (see the package comment). An image-only
// message (empty text) is sent as "See the attached image(s)."
func (m *Manager) PostWith(threadID, text string, images []agent.Image) (PostResult, error) {
	t := m.thread(threadID)
	if t == nil {
		return PostResult{}, fmt.Errorf("session: no thread %q", threadID)
	}
	text = strings.TrimSpace(text)
	if text == "" && len(images) > 0 {
		text = "See the attached image(s)."
	}
	if text == "" {
		return PostResult{}, errors.New("session: empty message")
	}

	t.mu.Lock()
	refs := t.refs()
	t.mu.Unlock()
	profiles := m.profilesFor(t, text)
	ad := ParseAddressing(text, refs, m.Kinds(), profile.Names(profiles)...)
	if len(ad.Unknown) > 0 {
		var names []string
		for _, r := range refs {
			names = append(names, r.Name)
		}
		return PostResult{}, fmt.Errorf("no agent @%s in this thread (members: %s; kinds: %s)",
			ad.Unknown[0], strings.Join(names, ", "), strings.Join(m.Kinds(), ", "))
	}

	m.wake(t)

	var targets []*agentState
	newKinds := ad.NewKinds
	t.mu.Lock()
	switch {
	case ad.All:
		targets = t.live()
		if len(targets) == 0 && len(newKinds) == 0 && len(ad.NewProfiles) == 0 {
			t.mu.Unlock()
			return PostResult{}, errors.New("nobody is running in this thread")
		}
	case len(ad.Members) > 0:
		for _, id := range ad.Members {
			a := t.member(id)
			if a != nil && a.status() == StatusExited {
				t.mu.Unlock()
				return PostResult{}, fmt.Errorf("%s has exited", a.getName())
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
			return PostResult{}, errors.New("every agent in this thread has exited; @claude or @codex adds a new one")
		}
	}
	t.mu.Unlock()
	if len(newKinds) == 1 && newKinds[0] == "" {
		return PostResult{}, errors.New("session: no agent kinds available")
	}

	body := ad.Body
	if !ad.Mentioned() {
		body = text
	}
	adds := make([]profile.Profile, 0, len(newKinds)+len(ad.NewProfiles))
	for _, k := range newKinds {
		adds = append(adds, profile.Profile{Kind: k})
	}
	for _, name := range ad.NewProfiles {
		p, _ := profile.Find(profiles, name)
		adds = append(adds, p)
	}
	var idle []*agentState // added with nothing to say: started together below
	var addErr error
	for _, p := range adds {
		a, err := m.addMember(t, p.Kind, "", "", p)
		if err != nil {
			addErr = err
			break
		}
		if body == "" {
			idle = append(idle, a)
			continue
		}
		targets = append(targets, a)
	}
	var startErr error
	for i, err := range m.startAll(t, idle, nil) {
		if err != nil {
			m.dropMember(t, idle[i])
			startErr = cmp.Or(startErr, err)
		}
	}
	if err := cmp.Or(startErr, addErr); err != nil {
		return PostResult{}, err
	}
	if body == "" {
		return PostResult{}, nil
	}
	return m.postTo(t, body, targets, images)
}

// postTo logs a user message addressed to targets and delivers it. A user
// message ends any pause and resets the hop count.
func (m *Manager) postTo(t *thread, text string, targets []*agentState, images []agent.Image) (PostResult, error) {
	cfg := m.config()
	speaker := cfg.UserName
	if speaker == "" {
		speaker = "user"
	}
	return m.postFrom(t, speaker, text, targets, images, true)
}

// postFrom is postTo for speaker. A message from a teammate (local false)
// does not end a pause or reset the brakes: only the user does.
func (m *Manager) postFrom(t *thread, speaker, text string, targets []*agentState, images []agent.Image, local bool) (PostResult, error) {
	e := store.Entry{Type: store.EntryUser, ID: store.NewID(), Speaker: speaker, To: ids(targets), Event: agent.Event{Kind: agent.KindUserText, Text: text}}
	if len(images) > 0 {
		e.Images = imagePaths(images)
	}

	var msgs []tea.Msg
	t.mu.Lock()
	wasPaused := t.paused && local
	if local {
		t.paused, t.hops, t.heldIdx, t.work = false, 0, nil, 0
		t.lastRelay = map[string]string{}
	}
	if t.title == "" {
		t.title = placeholderTitle(text)
	}
	if len(images) > 0 {
		t.images[e.ID] = append([]agent.Image(nil), images...)
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
	m.checkpoint(t, e.ID)
	if wasPaused {
		m.kick(t) // the scheduler and the waiting reviews stopped at the pause
	}

	var first error
	for _, a := range targets {
		if _, err := m.deliver(t, a); err != nil && first == nil {
			first = err
		}
	}
	return PostResult{EntryID: e.ID, Queued: t.queuedFor(e.ID)}, first
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
	if _, err := m.postTo(t, strings.TrimSpace(prompt), []*agentState{a}, nil); err != nil {
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
	_, err := m.postTo(a.thread, prompt, []*agentState{a}, nil)
	return err
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
// idle. Its prompt is a briefing of what it missed plus those messages. When a
// is busy, the user's waiting messages are entered in the thread's queue.
func (m *Manager) deliver(t *thread, a *agentState) (delivery, error) {
	return m.deliverEx(t, a, true)
}

// deliverEx is deliver; with needTrigger false it starts the turn even though no
// entry would trigger one (a steer is delivered explicitly).
func (m *Manager) deliverEx(t *thread, a *agentState, needTrigger bool) (delivery, error) {
	if m.isClosed() {
		return dNone, errClosed
	}
	t.mu.Lock()
	if needTrigger && !t.hasTrigger(a) {
		t.mu.Unlock()
		return dNone, nil
	}
	prev, ok, exited := a.tryBegin()
	if exited {
		t.mu.Unlock()
		return dExited, nil
	}
	if !ok {
		changed := needTrigger && t.syncQueueLocked(a)
		t.mu.Unlock()
		if changed {
			m.emitQueue(t)
		}
		return dQueued, nil
	}
	prompt, images, cursor, hold := m.buildPrompt(t, a)
	user := a.promptUser
	oldCursor, oldHold, oldKnown := a.cursor, a.holdFrom, a.knownMode
	a.cursor, a.holdFrom = cursor, hold
	if a.knownMode != t.mode {
		prompt = modeLine(t.mode) + "\n\n" + prompt
		a.knownMode = t.mode
	}
	a.mu.Lock()
	if a.nudge != "" {
		prompt = strings.TrimSpace(a.nudge + "\n\n" + prompt)
		a.nudge = ""
	}
	a.mu.Unlock()
	// A contract's agents work for a teammate: the team chat is not theirs.
	quiet := t.ops != nil || t.contract != ""
	t.mu.Unlock()

	var newsSeq uint64
	if !quiet {
		var news string
		if news, newsSeq = m.teamNews(a); news != "" {
			prompt = news + "\n\n" + prompt
		}
	}
	a.notePrompt(prompt, user)
	err := m.sendPrompt(t, a, prompt, images)
	a.donePrompt(err == nil)
	if err != nil {
		t.mu.Lock()
		a.cursor, a.holdFrom, a.knownMode = oldCursor, oldHold, oldKnown
		t.mu.Unlock()
		a.restore(prev)
		return dNone, err
	}
	if newsSeq > 0 {
		a.mu.Lock()
		a.teamSeq = newsSeq
		a.mu.Unlock()
	}
	m.persistMeta(t)
	m.queueDelivered(t, a, cursor)
	return dStarted, nil
}

// sendPrompt hands a prompt (and images) to a member's run, starting the run if
// it has not been started. A run that cannot take images gets a line saying so.
func (m *Manager) sendPrompt(t *thread, a *agentState, prompt string, images []agent.Image) error {
	run := a.getRun()
	if len(images) == 0 {
		if run != nil {
			return run.Send(prompt)
		}
		return m.startRun(t, a, prompt, "")
	}
	if run == nil {
		if err := m.startRun(t, a, "", ""); err != nil {
			return err
		}
		run = a.getRun()
	}
	if is, ok := run.(agent.ImageSender); ok {
		return is.SendWithImages(prompt, images)
	}
	return run.Send(prompt + "\n\n" + noImageLine(images))
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
	changed := t.dropQueueLocked(a, -1)
	t.mu.Unlock()
	if changed {
		m.emitQueue(t)
	}
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
// free of whitespace, '@' and '$' (agents), '#' (context), '/' (paths) and
// '*' and '+' (kind suffixes), and cannot be "all", "user", "deck", another
// kind's name, or its own kind's name while its thread has another member of
// that kind (profile.CheckName and profile.Reserved; profiles meet the same
// rules).
func (m *Manager) Rename(id, name string) error {
	name = strings.TrimSpace(name)
	if profile.CheckName(name) != nil {
		return fmt.Errorf("session: invalid name %q", name)
	}
	m.mu.Lock()
	a := m.byID[id]
	if a == nil {
		m.mu.Unlock()
		return fmt.Errorf("session: no agent %q", id)
	}
	reserved := profile.IsReserved(name)
	for k := range m.adapters {
		if strings.EqualFold(name, k) && k != a.kind {
			reserved = true
		}
	}
	// Its own kind's name is free only while it is the one member of that
	// kind in its thread: with two, "@claude" means whichever is free.
	if strings.EqualFold(name, a.kind) {
		for _, o := range m.order {
			if o != a && o.thread == a.thread && o.kind == a.kind {
				reserved = true
			}
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
// thread is already running it returns its info and no history. The log is
// read a part at a time and only its newest part stays in memory
// (window.go); the history is rendered as it is read.
func (m *Manager) Resume(threadID string) (ThreadInfo, History, error) {
	if m.st == nil {
		return ThreadInfo{}, History{}, errors.New("session: no store")
	}
	if old := m.thread(threadID); old != nil {
		if len(old.live()) > 0 {
			return m.threadInfo(old), History{}, nil
		}
		m.forget(old)
	}
	th, err := m.st.Meta(threadID)
	if err != nil {
		return ThreadInfo{}, History{}, err
	}
	if len(th.Members) == 0 {
		return ThreadInfo{}, History{}, errors.New("session: thread has no agents")
	}
	for _, mem := range th.Members {
		if _, ok := m.adapters[mem.Kind]; !ok {
			return ThreadInfo{}, History{}, fmt.Errorf("session: unknown agent kind %q", mem.Kind)
		}
	}
	dir := th.Dir
	if dir == "" {
		dir, _ = os.Getwd()
	}
	cfg := m.config()

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
	t.title, t.titleSrc = th.Title, th.TitleSrc
	t.pinned, t.resolved = th.Pinned, th.Resolved // resuming un-archives
	t.worktree = th.Worktree
	t.trees, t.capsules = th.Trees, cloneCapsules(th.Capsules)
	t.tasks = cloneTasks(th.Tasks)
	t.level = storedLevel(th, cfg)
	t.contract = th.Contract
	if th.Ops {
		// Its contract ended when Deck stopped, but the thread stays locked
		// down: its agents get no tools, approvals, thread.send and
		// thread.read are refused, and sh.run and contract.result answer that
		// the contract is over. The thread resumes, so its person can still
		// read it; a refusal would hide its history.
		t.ops = &opsState{}
	}
	for _, sh := range th.Shares {
		if validShareRole(sh.Role) {
			if t.shares == nil {
				t.shares = map[string]string{}
			}
			t.shares[sh.Peer] = sh.Role
		}
	}
	remap := map[string]string{}
	var members []*agentState
	fail := func(err error) (ThreadInfo, History, error) {
		for _, a := range members {
			if run := a.getRun(); run != nil {
				_ = run.Close()
			}
		}
		m.forget(t)
		return ThreadInfo{}, History{}, err
	}

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
		model, effort := mem.Model, mem.Effort
		if model == "" {
			model = m.defaultModel(cfg, mem.Kind)
		}
		if effort == "" {
			effort = m.defaultEffort(cfg, mem.Kind)
		}
		a := &agentState{
			id: id, kind: mem.Kind, name: name, model: model, effort: effort, thread: t, base: StatusIdle,
			started: time.Now(), costTotal: mem.CostUSD, sessionID: mem.SessionID, cursor: mem.Cursor,
			knownMode: mode, ctxTokens: mem.ContextTokens, ctxWindow: mem.ContextWindow, ctxSource: mem.ContextSource, briefed: mem.Briefed,
			ctxBaseline: mem.ContextBaseline, deckTokens: mem.DeckTokens,
			parent: mem.Parent, ephemeral: mem.Ephemeral, dir: mem.Dir, worktree: mem.Worktree,
			prof: m.storedProfile(t, mem), subs: slices.Clone(mem.Subs),
		}
		// Its subagents did not outlive the CLI that ran them.
		a.stopSubsLocked(time.Now())
		if mem.Ephemeral {
			a.base = StatusExited // a subagent's run does not outlive Deck; its worktree and task stay
		}
		// Names are per thread: only another member of this thread can clash.
		for _, o := range members {
			if strings.EqualFold(o.getName(), name) {
				a.name = id
			}
		}
		m.mu.Lock()
		m.order = append(m.order, a)
		m.byID[id] = a
		m.mu.Unlock()
		members = append(members, a)
	}
	t.members = members
	for _, a := range members {
		a.parent = mapID(remap, a.parent)
	}
	// Threads saved before kind names reached a pool may hold "claude" and
	// "claude2": free "claude" for the pool.
	for _, k := range m.Kinds() {
		t.unshadow(k, 0)
	}
	for i := range t.tasks {
		tk := &t.tasks[i]
		tk.Owner, tk.Creator = mapID(remap, tk.Owner), mapID(remap, tk.Creator)
		if tk.Run != nil {
			tk.Run.Member = mapID(remap, tk.Run.Member)
		}
	}

	if err := m.loadLog(t, remap, cfg.UserName); err != nil {
		return fail(err)
	}
	n := t.size()
	for _, a := range members {
		if th.Version < store.CurrentVersion && len(th.Members) == 1 {
			a.cursor = n // a single-agent thread: that agent has seen it all
		}
		a.cursor = min(a.cursor, n)
		t.boundary[a.id] = n // a turn cut short by a crash counts as finished
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

	var starting []*agentState
	var sessions []string
	for i, a := range members {
		if !a.ephemeral {
			starting = append(starting, a)
			sessions = append(sessions, th.Members[i].SessionID)
		}
	}
	for i, err := range m.startAll(t, starting, sessions) {
		if err != nil {
			return fail(fmt.Errorf("session: resume %s: %w", starting[i].kind, err))
		}
	}
	m.failOrphans(t)
	for i, a := range members {
		if w := th.Members[i].Wait; w != nil && !a.ephemeral {
			m.restoreWait(t, a, *w)
		}
	}
	t.scheduled = append([]store.Scheduled(nil), th.Scheduled...)
	for _, sc := range th.Scheduled {
		m.armScheduled(t, sc)
	}
	m.kick(t)
	if th.Archived {
		m.persistMeta(t) // resuming brings it back to the sidebar
	}
	if len(th.Trees) > 0 {
		m.wg.Add(1)
		go func() {
			defer m.wg.Done()
			m.pruneTrees(t) // worktrees landed, or removed, while Deck was away
		}()
	}
	t.mu.Lock()
	h := t.history()
	t.mu.Unlock()
	return m.threadInfo(t), h, nil
}

// loadLog reads a resumed thread's log from the store, fixing each entry as
// Resume needs (logFix) and indexing it, and keeps the newest part of it in
// memory. The part that is sure to leave memory again is read without its
// events' payload, which the index does not need. t is not shared yet.
func (m *Manager) loadLog(t *thread, remap map[string]string, user string) error {
	fix := &logFix{user: user, remap: remap, upto: math.MaxInt} // every entry, until the end is known
	t.disk = logDisk{marks: []mark{{}}, fix: fix}
	// The entries that stay take at most the window, from the end.
	var full int64
	if size, err := m.st.LogSize(t.id); err == nil && m.window > 0 {
		full = max(size-m.window, 0)
	}
	n, whole := 0, -1 // whole: the first entry read with its payload
	end, _, err := m.st.ScanEntries(t.id, 0, 0, full, func(e store.Entry, next int64) bool {
		if whole < 0 && t.disk.end >= full {
			whole = n
		}
		fix.apply(n, &e)
		if e.Type == store.EntryEvent && e.Kind == agent.KindAssistantText && !e.Delta && e.AgentID != "" {
			t.lastSpeaker = e.AgentID
		}
		t.log = append(t.log, e)
		t.index.add(n, e)
		t.restoreSkip(e)
		n++
		t.disk.n, t.disk.end = n, next
		t.disk.markLocked(n, next)
		t.trimLocked(true)
		return true
	})
	if err != nil {
		return err
	}
	fix.upto = n
	// Appends go after the last line read; a torn one would swallow the
	// first of them, so the store's log no longer lines up with this one.
	t.disk.synced = end >= 0
	if whole < 0 {
		whole = n
	}
	if t.base < whole {
		// Kept entries read without their payload (logKeep): read them whole.
		v := t.view()
		v.base, v.mem = whole, nil
		k := t.base
		if err := v.each(t.base, whole, func(i int, e store.Entry) bool {
			t.log[i-t.base], k = e, i+1
			return true
		}); err != nil || k != whole {
			return cmp.Or(err, fmt.Errorf("session: log entry %d is not in the store", k))
		}
	}
	return nil
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
	t.stopTimers()
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

// Flush blocks until every queued store write has been performed, including
// the metadata and index writes the store holds back.
func (m *Manager) Flush() {
	m.w.flush()
	if m.st != nil {
		_ = m.st.Flush()
	}
}

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
	if m.st != nil {
		_ = m.st.Flush()
	}
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
	if len(ev.Raw) > 0 && !agent.KeepsRaw(ev.Kind) {
		ev.Raw = nil // repeats the rest of the event; not worth logging or passing on
	}
	if a.thread.gone.Load() {
		return // the thread was archived, deleted or moved
	}
	if ev.Kind == agent.KindSubagent || ev.Sub != "" {
		m.onSubagent(a, ev) // a CLI-native subagent's (subagents.go)
		return
	}
	t := a.thread
	if ev.Kind == agent.KindError {
		// Every adapter's error text is read too: OpenCode and Antigravity
		// say it only in words.
		if ev.LimitHit == nil {
			ev.LimitHit = agent.ParseLimitHit(ev.Text, ev.Time)
		}
		if ev.LimitHit != nil {
			m.noteHit(a, *ev.LimitHit)
		}
	}
	var end turnEnd
	if ev.Kind == agent.KindTurnDone {
		// Taken before apply makes a idle: from then on its next turn can
		// start, and move cursor and the final reply, before afterTurn
		// settles this one.
		t.mu.Lock()
		end.answered = a.cursor
		t.mu.Unlock()
		end.text, end.interrupted = a.takeFinal()
	}
	res := a.apply(ev)
	// Plan limits are about the account, not the conversation, and a
	// restart about the process: they are not part of the thread's log.
	if !ev.Delta && ev.Kind != agent.KindLimits && ev.Kind != agent.KindRestarting && ev.Kind != agent.KindRestarted {
		t.mu.Lock()
		m.appendLocked(t, store.Entry{Event: ev})
		t.mu.Unlock()
	}
	if res.metaDirty {
		m.persistMeta(t)
	}
	if res.waits {
		m.startedWait(t, a)
	}
	for _, s := range res.stopped {
		sa := subagentOf(s)
		m.emit(EventMsg{Event: agent.Event{AgentID: a.id, Time: ev.Time, Kind: agent.KindSubagent, Subagent: &sa}, ThreadID: t.id})
	}
	for _, id := range res.gone {
		m.emit(ApprovalGoneMsg{ThreadID: t.id, AgentID: a.id, ID: id})
	}
	m.emit(EventMsg{Event: ev, ThreadID: t.id})
	switch {
	case ev.Kind == agent.KindLimits && ev.Limits != nil:
		m.setLimits(a.kind, *ev.Limits)
		m.rearmWaits(a.kind)
	case ev.Kind == agent.KindFileChange && ev.Diff != nil:
		m.noteEdit(t, a, ev.Diff)
	case ev.Kind == agent.KindTurnDone || ev.Kind == agent.KindExited:
		m.charge(t, a, res.spent)
	case ev.Kind == agent.KindUsage || ev.Kind == agent.KindToolCall && !ev.Delta:
		// Mid-turn: a turn that runs past a budget is stopped, not finished.
		a.mu.Lock()
		turn := store.Spend{Tokens: a.tokTurn, CostUSD: a.costTurn}
		a.mu.Unlock()
		m.checkBudgets(t, a, turn)
	}
	// What follows the end of a turn or of the run goes to the thread's relay
	// worker, in order with its thread.send calls, so this pump goes on
	// draining a's events while relays are delivered; an exit is handled
	// after the end of the turn before it has been settled.
	if ev.Kind == agent.KindTurnDone {
		// Before afterTurn delivers what queued: an ask settled here no
		// longer starts a turn.
		reply := end.text
		if end.interrupted {
			reply = ""
		}
		m.ownerDone(t, a, reply)
		m.onThread(t, func() { m.afterTurn(a, end) })
		m.checkAttribution(a)
	}
	if ev.Kind == agent.KindExited {
		m.sweepLeases(t)
		m.ownerDone(t, a, "")
		m.onThread(t, func() {
			m.failOwned(t, a)
			m.cascade(t, a)
			m.kick(t) // a subagent's slot is free (MaxSubagents)
		})
	}
}

func (m *Manager) onApproval(a *agentState, req agent.ApprovalRequest) {
	if a.thread.gone.Load() {
		if req.Respond != nil {
			req.Respond(agent.Decision{Allow: false, Reason: "the thread was closed"})
		}
		return
	}
	if a.thread.opsOf() != nil {
		if req.Respond != nil {
			req.Respond(agent.Decision{Allow: false, Reason: errOpsOnly.Error()})
		}
		return
	}
	if req.Kind == agent.ApproveEdit && a.thread.getMode() != agent.ModePlan && m.gated(a, req) {
		// An edit first passes the gate (coord.go), which may wait for the
		// file's owner: off the pump, which goes on with a's events.
		m.wg.Add(1)
		go func() {
			defer m.wg.Done()
			if m.gateApproval(a, req) {
				m.askApproval(a, req)
			}
		}()
		return
	}
	m.askApproval(a, req)
}

// askApproval is the rest of onApproval: the mode answers the request, or
// the person does.
func (m *Manager) askApproval(a *agentState, req agent.ApprovalRequest) {
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
	turnAt  time.Time // when the current or last turn began; guarded by mu

	// cursor and holdFrom are guarded by thread.mu: cursor is how much of the
	// thread log the member has been briefed on; holdFrom maps another member to
	// the first log index of its still-running turn that was held back.
	cursor   int
	holdFrom map[string]int
	// knownMode is the permission mode the member was last told about (its
	// Spec.Mode, or the line at the top of its last prompt); guarded by thread.mu.
	knownMode agent.Mode

	// startMu lets one startRun at a time start the member's CLI.
	startMu sync.Mutex

	mu          sync.Mutex
	run         agent.Run
	name        string
	model       string
	effort      string
	base        Status
	pending     []pendingApproval
	costTotal   float64 // folded turns
	costTurn    float64 // current turn, cumulative as reported
	tokTurn     int     // current turn's input and output tokens, as last reported
	sessionID   string
	briefed     bool   // sessionID has had Deck's instructions (store.Member.Briefed)
	teamSeq     uint64 // the last team chat message it was told of (teamNews)
	lastText    string // the last assistant text of the running turn
	interrupted bool
	ctxTokens   int // context window use, from the latest usage event that reported it
	ctxWindow   int
	ctxSource   string // where ctxTokens and ctxWindow came from (agent.Usage.ContextSource)
	// The context breakdown (context.go): ctxBaseline is what the CLI
	// session filled before its first turn, deckTokens what Deck's own text
	// in it takes, sentTokens what Deck sent it until the baseline was
	// measured, and ctxFresh that the session started with Deck (not
	// resumed) and has no baseline yet. Estimates, in tokens.
	ctxBaseline int
	deckTokens  int
	sentTokens  int
	ctxFresh    bool
	// inflight is the prompt being delivered, as notePrompt counted it
	// (Deck's tokens, all tokens).
	inflight [2]int
	// promptUser is how many characters of the prompt buildPrompt last
	// built are the user's own words; guarded by thread.mu.
	promptUser int
	running    string // model slug the CLI reported at session start
	// hit is the plan limit the running turn ran into, wait the member's
	// wait for it to reset, and nudge what its next prompt starts with
	// (waits.go).
	hit   *agent.LimitHit
	wait  *waitState
	nudge string
	// stirred is closed, and dropped, when the member's status may have
	// changed (stirLocked); nil while nobody waits for that (waitIdle).
	stirred chan struct{}

	// A Deck-managed subagent (spawn.go): the member that asked for it, whether
	// Deck stops it when its task ends, and its own worktree. dir is the folder
	// it runs in when that is not the thread's.
	parent    string
	ephemeral bool
	dir       string
	worktree  *store.Worktree
	retired   bool   // retireMember has stopped it (spawn.go)
	reviews   string // the task it was started to review (review.go)

	// subBase is the SUB snapshot its next sub.patch.capture diffs from, and
	// subParent the capsule that snapshot already includes (subops.go).
	subBase   sub.ID
	subParent sub.ID

	// prof is the profile the member was started from (its Name is ""
	// for none): its role, instructions, voice and paths go into the
	// member's briefing. Set when the member is made.
	prof profile.Profile

	// subs are the subagents its CLI started with its own tool, oldest
	// first (subagents.go); guarded by mu.
	subs []store.SubAgent
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

func (a *agentState) setName(name string) {
	a.mu.Lock()
	a.name = name
	a.mu.Unlock()
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
	dir := a.thread.dir
	if a.dir != "" {
		dir = a.dir
	}
	branch := ""
	if a.worktree != nil {
		branch = a.worktree.Branch
	}
	return AgentInfo{
		ID: a.id, Kind: a.kind, Name: a.name, Dir: dir, Model: a.model, Effort: a.effort,
		Parent: a.parent, Ephemeral: a.ephemeral, Branch: branch,
		Status: a.statusLocked(), CostUSD: a.costTotal + a.costTurn, Pending: len(a.pending),
		ThreadID: a.thread.id, ContextTokens: a.ctxTokens, ContextWindow: a.ctxWindow, ContextSource: a.ctxSource,
		ContextBaseline: a.ctxBaseline, DeckTokens: a.deckTokens,
		RunningModel: a.running, Profile: a.prof.Name, Role: a.prof.Role, Color: a.prof.Color,
		Subs: slices.Clone(a.subs),
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
		ID: a.id, Kind: a.kind, Name: a.name, Model: a.model, Effort: a.effort, SessionID: a.sessionID,
		CostUSD: a.costTotal + a.costTurn, Cursor: a.cursor,
		ContextTokens: a.ctxTokens, ContextWindow: a.ctxWindow, ContextSource: a.ctxSource, Briefed: a.briefed,
		ContextBaseline: a.ctxBaseline, DeckTokens: a.deckTokens,
		Parent: a.parent, Ephemeral: a.ephemeral, Dir: a.dir, Worktree: a.worktree,
		Profile: a.prof.Name, Role: a.prof.Role, Color: a.prof.Color,
		Wait: a.storedWaitLocked(), Subs: slices.Clone(a.subs),
	}
}

// storedWaitLocked is the member's wait as stored; a.mu is held.
func (a *agentState) storedWaitLocked() *store.Wait {
	if a.wait == nil {
		return nil
	}
	w := a.wait.Wait
	return &w
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
	case StatusWorking, StatusTool, StatusApproval, StatusWaiting:
		return a.base, false, false
	}
	prev = a.base
	a.costTotal += a.costTurn
	a.costTurn = 0
	a.base = StatusWorking
	a.lastText = ""
	a.interrupted = false
	a.turnAt = time.Now()
	return prev, true, false
}

func (a *agentState) restore(prev Status) {
	a.mu.Lock()
	if a.base == StatusWorking {
		a.base = prev
	}
	a.stirLocked()
	a.mu.Unlock()
}

// stirLocked wakes the callers of waitIdle: the member's status may have
// changed. a.mu is held.
func (a *agentState) stirLocked() {
	if a.stirred != nil {
		close(a.stirred)
		a.stirred = nil
	}
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
	a.stirLocked()
	a.mu.Unlock()
}

// takePending removes and returns the pending approvals, but those of kind
// keep, which stay.
func (a *agentState) takePending(keep ...agent.ApprovalKind) []pendingApproval {
	a.mu.Lock()
	defer a.mu.Unlock()
	var out, kept []pendingApproval
	for _, p := range a.pending {
		if slices.Contains(keep, p.kind) {
			kept = append(kept, p)
		} else {
			out = append(out, p)
		}
	}
	a.pending = kept
	a.stirLocked()
	return out
}

func (a *agentState) markExited() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.base == StatusExited {
		return false
	}
	a.base = StatusExited
	a.pending = nil
	a.stirLocked()
	return true
}

type applyResult struct {
	gone      []string
	metaDirty bool
	// spent is what a turn that just ended spent (KindTurnDone, KindExited).
	spent store.Spend
	// waits: the turn that ended ran out of plan, and the member now waits.
	waits bool
	// stopped are the subagents that ended with the CLI (KindExited).
	stopped []store.SubAgent
}

// apply folds one event into the agent's status and cost.
func (a *agentState) apply(ev agent.Event) (res applyResult) {
	a.mu.Lock()
	defer a.mu.Unlock()
	defer a.stirLocked()
	if a.base == StatusExited && ev.Kind != agent.KindExited {
		return res
	}
	switch ev.Kind {
	case agent.KindSessionStarted:
		if ev.SessionID != "" && ev.SessionID != a.sessionID {
			a.sessionID = ev.SessionID
			a.briefed = false // a new CLI session has not seen the instructions
			res.metaDirty = true
		}
		if ev.Text != "" {
			a.running = ev.Text
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
		if u := ev.Usage; u != nil {
			// Zero means "not reported by this event": mid-turn usage events carry
			// only the context fields and must not reset the turn's cost.
			if u.CostUSD > 0 {
				a.costTurn = u.CostUSD
			}
			if n := u.InputTokens + u.OutputTokens; n > 0 {
				a.tokTurn = n
			}
			if u.ContextTokens > 0 {
				a.ctxTokens = u.ContextTokens
				a.ctxSource = u.ContextSource
				a.measureBaselineLocked(u)
			}
			if u.ContextWindow > 0 {
				a.ctxWindow = u.ContextWindow
			}
		}
	case agent.KindCompacted:
		// The summary replaced what Deck had sent: count from here.
		a.deckTokens = 0
		res.metaDirty = true
	case agent.KindError:
		a.base = StatusError
	case agent.KindRestarting:
		if a.base == StatusIdle || a.base == StatusError {
			a.base = StatusRestarting
		}
	case agent.KindRestarted:
		if a.base == StatusRestarting {
			a.base = StatusIdle
		}
	case agent.KindTurnDone:
		res.spent = store.Spend{Tokens: a.tokTurn, CostUSD: a.costTurn}
		a.costTotal += a.costTurn
		a.costTurn, a.tokTurn = 0, 0
		a.base = StatusIdle
		if a.hit != nil {
			// The plan ran out: the member waits for the reset (waits.go).
			if a.wait == nil {
				a.wait = &waitState{Wait: store.Wait{Since: ev.Time, Until: a.hit.ResetsAt, Window: a.hit.Window}}
				res.waits = true
			}
			a.base = StatusWaiting
			a.hit = nil
		}
		// The run's first prompt carried the instructions (Spec.Briefed).
		a.briefed = a.sessionID != ""
		res.gone = a.clearPendingLocked()
		res.metaDirty = true
	case agent.KindExited:
		a.hit = nil
		res.stopped = a.stopSubsLocked(ev.Time) // they went with the CLI
		res.spent = store.Spend{Tokens: a.tokTurn, CostUSD: a.costTurn}
		a.costTotal += a.costTurn
		a.costTurn, a.tokTurn = 0, 0
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
//
// push never blocks: it is called with thread locks held, which the queued
// operations take. The queue is bounded instead by what is pushed: a thread
// has at most one log write queued (its new entries wait in its outbox, and
// the write takes them all) and one metadata write (persistMeta); the rest
// are the rare archive, delete, move and flush operations.
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
	w.signal()
}

// queued is the number of operations waiting.
func (w *writer) queued() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.q)
}

func (w *writer) signal() {
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
