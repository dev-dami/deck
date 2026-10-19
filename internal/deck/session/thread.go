package session

import (
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/dev-dami/deck/internal/deck/agent"
	"github.com/dev-dami/deck/internal/deck/changes"
	"github.com/dev-dami/deck/internal/deck/store"
)

// thread is one shared conversation: its members, its log and the agent-to-agent
// bookkeeping. mu guards everything below it, including the cursor and holdFrom
// fields of its members. Lock order: thread.mu, then agentState.mu. Never emit
// to the UI while holding either.
type thread struct {
	id string
	m  *Manager

	mu    sync.Mutex
	title string
	// titleSrc is who wrote title (store.Thread.TitleSrc); titling is set while
	// a model-written title is being fetched (title.go).
	titleSrc   string
	titling    bool
	titleTries int
	dir        string
	mode       agent.Mode // permission mode; never empty
	members    []*agentState
	log        []store.Entry

	boundary    map[string]int    // speaker -> log index just after its last finished turn
	inert       map[int]bool      // relay/user entries that must not start a turn
	heldIdx     []int             // relays held by the pause, released by Continue
	lastRelay   map[string]string // "from>to" -> normalised text of the last relay
	lastSpeaker string            // member who most recently wrote a reply
	hops        int
	paused      bool
	// work counts the subagents members started and the tasks the scheduler
	// handed out since the user's last message (brakes.go); sends the times of
	// each member's recent thread.send calls.
	work  int
	sends map[string][]time.Time

	// Lifecycle (lifecycle.go): pinned to the top, resolved (settled until a
	// new message wakes it), archived (off the sidebar, still resumable).
	pinned, resolved, archived bool
	// gone is set when the thread leaves the live set (archive, delete, move):
	// its old agents' late events, approvals and store writes are dropped.
	gone atomic.Bool
	// worktree is set while the thread is isolated (lifecycle.go).
	worktree *store.Worktree
	// tasks is the shared task board (tasks.go), guarded by mu.
	tasks []store.Task
	sched schedState // one scheduler pass at a time (sched.go)
	// orchestrate is set while members may spawn subagents and the scheduler
	// runs the board (spawn.go, sched.go).
	orchestrate bool

	// Queue of user messages waiting for busy members (queue.go), guarded by mu.
	queue  []qitem
	skip   map[skipKey]skipKind     // (entry index, member) pairs that must not be delivered again
	images map[string][]agent.Image // entry id -> images as the user attached them
	// qemit serialises QueueChangedMsg snapshots so they reach the UI in order.
	qemit sync.Mutex

	// The git work tree the thread runs in, opened on first use (changes.go);
	// nil outside a repository.
	repoOnce sync.Once
	repo     *changes.Repo

	created bool // the store knows this thread (touched by the writer goroutine only)
	// metaQueued is set while a metadata write for the thread waits in the
	// writer queue: that write snapshots the thread when it runs, so further
	// changes until then need no write of their own.
	metaQueued atomic.Bool
}

// skipKey names one member's copy of a log entry.
type skipKey struct {
	idx   int
	agent string
}

// skipKind says why a member's copy of an entry is skipped.
type skipKind uint8

const (
	skipCancelled skipKind = iota + 1 // the user cancelled it while queued: the member never sees it
	skipSteered                       // it was delivered as a steer instead
)

func newThread(m *Manager, id, dir string, mode agent.Mode) *thread {
	return &thread{
		id: id, m: m, dir: dir, mode: mode,
		boundary:  map[string]int{},
		inert:     map[int]bool{},
		lastRelay: map[string]string{},
		skip:      map[skipKey]skipKind{},
		images:    map[string][]agent.Image{},
	}
}

// appendLocked adds an entry to the log, hands it to the writer and returns its
// index. t.mu is held.
func (m *Manager) appendLocked(t *thread, e store.Entry) int {
	if e.Time.IsZero() {
		e.Time = time.Now()
	}
	if e.ID == "" {
		e.ID = store.NewID()
	}
	if len(e.Raw) > 0 && !agent.KeepsRaw(e.Kind) {
		e.Raw = nil // the log neither needs nor keeps it
	}
	idx := len(t.log)
	t.log = append(t.log, e)
	if e.Type == store.EntryEvent && (e.Kind == agent.KindTurnDone || e.Kind == agent.KindExited) {
		t.boundary[e.AgentID] = idx + 1
	}
	if e.Type == store.EntryEvent && e.Kind == agent.KindAssistantText && !e.Delta {
		t.lastSpeaker = e.AgentID
	}
	if m.st != nil && !t.gone.Load() {
		m.w.pushAppend(t, e)
	}
	return idx
}

// persist queues f on the single writer, creating the thread in the store first
// if it does not exist there yet.
func (m *Manager) persist(t *thread, f func()) {
	if m.st == nil {
		return
	}
	if t.gone.Load() {
		return
	}
	m.w.push(func() {
		m.ensureStored(t)
		f()
	})
}

// ensureStored creates the thread in the store if it is not there yet. It runs
// on the writer goroutine.
func (m *Manager) ensureStored(t *thread) {
	if !t.created {
		t.created = true
		if _, err := m.st.Create(m.snapshot(t)); err != nil {
			_ = m.st.UpdateMeta(m.snapshot(t))
		}
	}
}

// writeEntries is the writer's job for a run of log entries queued by
// appendLocked: one append to the thread's log for all of them.
func (m *Manager) writeEntries(t *thread, es []store.Entry) {
	m.ensureStored(t)
	_ = m.st.AppendEntries(t.id, es)
}

// persistMeta queues a write of the thread's metadata as it is when the write
// runs. While one is queued, another call adds nothing: the queued write will
// see its change.
func (m *Manager) persistMeta(t *thread) {
	if m.st == nil || t.gone.Load() {
		return
	}
	if !t.metaQueued.CompareAndSwap(false, true) {
		return
	}
	m.persist(t, func() {
		t.metaQueued.Store(false) // before the snapshot, so a later change queues again
		_ = m.st.UpdateMeta(m.snapshot(t))
	})
}

// snapshot is the thread's stored metadata.
func (m *Manager) snapshot(t *thread) store.Thread {
	t.mu.Lock()
	defer t.mu.Unlock()
	th := store.Thread{ID: t.id, Title: t.title, TitleSrc: t.titleSrc, Dir: t.dir, Mode: string(t.mode),
		Pinned: t.pinned, Resolved: t.resolved, Archived: t.archived, Worktree: t.worktree,
		Tasks: cloneTasks(t.tasks), Orchestrate: t.orchestrate}
	for _, a := range t.members {
		th.Members = append(th.Members, a.memberMeta())
	}
	return th
}

// refs lists the members for addressing. t.mu is held or the caller owns t.
func (t *thread) refs() []MemberRef {
	out := make([]MemberRef, 0, len(t.members))
	for _, a := range t.members {
		out = append(out, MemberRef{
			ID: a.id, Name: a.getName(), Kind: a.kind, Exited: a.status() == StatusExited,
			Busy: a.busy(), Waiting: t.waiting(a), Last: a.id == t.lastSpeaker,
		})
	}
	return out
}

// ofKind counts the live members of kind. t.mu is held.
func (t *thread) ofKind(kind string) int {
	n := 0
	for _, a := range t.members {
		if a.kind == kind && a.status() != StatusExited {
			n++
		}
	}
	return n
}

// firstOf is the name of the first member of kind to join, or "". t.mu is held.
func (t *thread) firstOf(kind string) string {
	for _, a := range t.members {
		if a.kind == kind {
			return a.getName()
		}
	}
	return ""
}

// waiting counts the queued messages waiting for a. t.mu is held.
func (t *thread) waiting(a *agentState) int {
	n := 0
	for _, q := range t.queue {
		if q.AgentID == a.id {
			n++
		}
	}
	return n
}

func (t *thread) member(id string) *agentState {
	for _, a := range t.members {
		if a.id == id {
			return a
		}
	}
	return nil
}

// live lists the members that have not exited.
func (t *thread) live() []*agentState {
	var out []*agentState
	for _, a := range t.members {
		if a.status() != StatusExited {
			out = append(out, a)
		}
	}
	return out
}

// defaultTarget is who an unaddressed message goes to: the member who spoke
// last, else the only live member, else the first live one.
func (t *thread) defaultTarget() *agentState {
	if a := t.member(t.lastSpeaker); a != nil && a.status() != StatusExited {
		return a
	}
	live := t.live()
	if len(live) == 0 {
		return nil
	}
	return live[0]
}

// freeName names a new member of kind: the kind itself for the first of its
// kind in the thread, else the first of kind2, kind3, ... no member is called
// (after unshadow has renamed a first one still called by the kind's name).
// t.mu is held or the caller owns t.
func (t *thread) freeName(kind string) string {
	if t.firstOf(kind) == "" && !t.taken(kind) {
		return kind
	}
	t.unshadow(kind, 1)
	for n := 2; ; n++ {
		if name := fmt.Sprintf("%s%d", kind, n); !t.taken(name) {
			return name
		}
	}
}

// unshadow renames a member still called by its kind's name to kind1 (or the
// next free kindN) once the thread has, counting extra members about to join,
// two or more of that kind: the kind's name then reaches whichever is free
// (see choose), and each one keeps a name of its own. t.mu is held or the
// caller owns t.
func (t *thread) unshadow(kind string, extra int) {
	n := extra
	for _, a := range t.members {
		if a.kind == kind {
			n++
		}
	}
	if n < 2 {
		return
	}
	for _, a := range t.members {
		if a.kind != kind || !strings.EqualFold(a.getName(), kind) {
			continue
		}
		for i := 1; ; i++ {
			if name := fmt.Sprintf("%s%d", kind, i); !t.taken(name) {
				a.setName(name)
				break
			}
		}
	}
}

// taken reports whether a member of the thread is called name. t.mu is held.
func (t *thread) taken(name string) bool {
	for _, a := range t.members {
		if strings.EqualFold(a.getName(), name) {
			return true
		}
	}
	return false
}

// find resolves a name as a recipient among members other than self. A kind
// reaches one member of that kind other than self (see choose), or one that
// has exited when none is running, so the caller can say so; otherwise a name
// or id matches (case-insensitive; names win, as an id can look like another
// member's name).
func (t *thread) find(self *agentState, tok string) *agentState {
	tok = TrimSigil(tok)
	if kind, ok := kindOf(tok, t.m.Kinds()); ok {
		if a := t.pick(self, kind); a != nil {
			return a
		}
	}
	for _, a := range t.members {
		if strings.EqualFold(a.getName(), tok) {
			return a
		}
	}
	for _, a := range t.members {
		if strings.EqualFold(a.id, tok) {
			return a
		}
	}
	return nil
}

// pick is choose over the members other than self: a live member of kind, or
// else one that exited, or nil. t.mu is held.
func (t *thread) pick(self *agentState, kind string) *agentState {
	var refs []MemberRef
	var gone *agentState
	for _, r := range t.refs() {
		if self != nil && r.ID == self.id {
			continue
		}
		refs = append(refs, r)
		if r.Exited && gone == nil && strings.EqualFold(r.Kind, kind) {
			gone = t.member(r.ID)
		}
	}
	if r, ok := choose(refs, kind); ok {
		return t.member(r.ID)
	}
	return gone
}

// findAll resolves "claude*" to every live member of that kind other than
// self; ok is false when tok is no kind with that suffix. t.mu is held.
func (t *thread) findAll(self *agentState, tok string) (out []*agentState, ok bool) {
	base, suf := splitSuffix(TrimSigil(tok))
	if suf != suffixAll {
		return nil, false
	}
	kind, isKind := kindOf(base, t.m.Kinds())
	if !isKind {
		return nil, false
	}
	for _, a := range t.members {
		if a != self && a.kind == kind && a.status() != StatusExited {
			out = append(out, a)
		}
	}
	return out, true
}

// triggers reports whether e is the kind of entry that is delivered to the
// members it is addressed to: a user message, a relay, or an edit.
func triggers(e store.Entry) bool {
	return e.Type == store.EntryUser || e.Type == store.EntryRelay || e.Type == store.EntryEdit
}

// hasTrigger reports whether a has messages addressed to it that it has not
// been given yet and that are allowed to start a turn.
func (t *thread) hasTrigger(a *agentState) bool {
	for i := a.cursor; i < len(t.log); i++ {
		e := t.log[i]
		if triggers(e) && !t.inert[i] && t.skip[skipKey{i, a.id}] == 0 && contains(e.To, a.id) {
			return true
		}
	}
	return false
}

// dropPending makes every undelivered message addressed to a inert: it stays in
// the log (and in the next briefing) but no longer starts a turn. t.mu is held.
func (t *thread) dropPending(a *agentState) {
	for i := a.cursor; i < len(t.log); i++ {
		e := t.log[i]
		if triggers(e) && contains(e.To, a.id) {
			t.inert[i] = true
		}
	}
}

func ids(as []*agentState) []string {
	out := make([]string, len(as))
	for i, a := range as {
		out[i] = a.id
	}
	return out
}

func normText(s string) string { return strings.Join(strings.Fields(strings.ToLower(s)), " ") }

// emitAll hands messages to the UI in order.
func (m *Manager) emitAll(msgs ...tea.Msg) {
	for _, msg := range msgs {
		if msg != nil {
			m.emit(msg)
		}
	}
}
