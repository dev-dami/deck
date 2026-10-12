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
	titleSrc string
	titling  bool
	dir      string
	mode     agent.Mode // permission mode; never empty
	members  []*agentState
	log      []store.Entry

	boundary    map[string]int    // speaker -> log index just after its last finished turn
	inert       map[int]bool      // relay/user entries that must not start a turn
	heldIdx     []int             // relays held by the pause, released by Continue
	lastRelay   map[string]string // "from>to" -> normalised text of the last relay
	lastSpeaker string            // member who most recently wrote a reply
	hops        int
	paused      bool

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
		out = append(out, MemberRef{ID: a.id, Name: a.getName(), Kind: a.kind, Exited: a.status() == StatusExited})
	}
	return out
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

// freeName is the first of kind, kind2, kind3, ... that no member of the thread
// is called. t.mu is held or the caller owns t.
func (t *thread) freeName(kind string) string {
	for n := 1; ; n++ {
		name := kind
		if n > 1 {
			name = fmt.Sprintf("%s%d", kind, n)
		}
		taken := false
		for _, a := range t.members {
			if strings.EqualFold(a.getName(), name) {
				taken = true
				break
			}
		}
		if !taken {
			return name
		}
	}
}

// find resolves a name as a recipient among members other than self: a name or
// id (case-insensitive; names win, as an id can look like another member's
// name), or a kind with exactly one member in the thread.
func (t *thread) find(self *agentState, tok string) *agentState {
	tok = TrimSigil(tok)
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
	var hit *agentState
	for _, a := range t.members {
		if a != self && strings.EqualFold(a.kind, tok) {
			if hit != nil {
				return nil
			}
			hit = a
		}
	}
	return hit
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
