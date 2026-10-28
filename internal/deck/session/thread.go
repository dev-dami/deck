package session

import (
	"fmt"
	"maps"
	"slices"
	"sort"
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
	// log holds the newest entries of the thread's log, from index base on;
	// the older ones are in the store only (window.go). The log only grows:
	// an entry, once appended, never changes (History).
	log  []store.Entry
	base int
	disk logDisk
	// index finds log entries by id, the edits of user messages and the
	// messages addressed to members without scanning the log; appendLocked
	// keeps it.
	index logIndex

	boundary map[string]int // speaker -> log index just after its last finished turn
	// browserOK are the sites (origins) the person let this thread's agents
	// open in the shared browser (browser.go).
	browserOK   map[string]bool
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
	// leases are the members' claims on files of the shared tree; edits the
	// last change to each of its files (by pathKey), and warned when an
	// overlap at a file and member was last reported (leases.go).
	leases []lease
	edits  map[string]edit
	warned map[string]time.Time
	// coord is who changed which file when, and the asks to edit a file
	// another member owns (coord.go).
	coord coordState

	// Lifecycle (lifecycle.go): pinned to the top, resolved (settled until a
	// new message wakes it), archived (off the sidebar, still resumable).
	pinned, resolved, archived bool
	// gone is set when the thread leaves the live set (archive, delete, move):
	// its old agents' late events, approvals and store writes are dropped.
	gone atomic.Bool
	// worktree is set while the thread is isolated (lifecycle.go).
	worktree *store.Worktree
	// trees are the thread's own worktrees and capsules its named capsules
	// (work.go, capsulework.go), guarded by mu.
	trees    []*store.Worktree
	capsules []store.CapsuleRec
	// tasks is the shared task board (tasks.go), guarded by mu.
	tasks []store.Task
	sched schedState // one scheduler pass at a time (sched.go)
	// raced holds the races already reported or kept (race.go).
	raced map[string]bool
	// spawns holds the tasks a subagent is starting for, each with the claim
	// of the Spawn that starts it (spawn.go).
	spawns map[string]string
	// reviewing holds the tasks whose reviewer is starting; each channel
	// closes when that start ends (review.go).
	reviewing map[string]chan struct{}
	// reviewWaits holds the tasks in review whose wait for a reviewer the
	// thread has a notice about (review.go).
	reviewWaits map[string]bool
	// level is the thread's orchestration level (config.Levels): how deep
	// and wide members spawn, whether they race, and whether the scheduler
	// runs the board (spawn.go, sched.go).
	level string
	// contract is the teammate whose code contract this thread runs: its
	// mode is never yolo (contract.go).
	contract string
	// ops is the teammate's ops contract the thread runs, or nil: its
	// agents have no tools but sh.run and contract.result (opscontract.go).
	ops *opsState
	// shares are the teammates the thread is shared with, by name, and
	// their roles; grew is closed when the log grows (share.go).
	shares map[string]string
	grew   chan struct{}

	// scheduled are the user's messages waiting for their time, and
	// atTimers their timers by id (waits.go).
	scheduled []store.Scheduled
	atTimers  map[string]*time.Timer

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
	// outbox holds the log entries appended since the writer last took them,
	// the first at log index outAt; one write for them is queued while it is
	// not empty (appendLocked).
	outbox []store.Entry
	outAt  int
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
	t := &thread{
		id: id, m: m, dir: dir, mode: mode,
		boundary:  map[string]int{},
		inert:     map[int]bool{},
		lastRelay: map[string]string{},
		skip:      map[skipKey]skipKind{},
		images:    map[string][]agent.Image{},
		index:     newIndex(),
	}
	if m.st != nil {
		// A new thread's log in the store starts empty (Resume replaces this).
		t.disk = logDisk{synced: true, marks: []mark{{}}}
	}
	return t
}

// logIndex indexes a log: at maps each entry's id to its index, edited each
// edited user message's id to its latest text (an edit always comes after
// the message it edits, and after the edits before it), snaps each user
// message's id to its checkpoint (EntrySnap). heads are the log's messages
// (user messages, relays, edits), oldest first.
type logIndex struct {
	at     map[string]int
	edited map[string]string
	snaps  map[string]string
	heads  []head
	// firstText is the text of the first user message that has some, subSnap
	// the newest SUB checkpoint (subops.go), revert the newest revert not
	// undone ("" for none).
	firstText string
	subSnap   string
	revert    string
}

// head is what the scans for messages waiting for a member need of one
// message (see triggers), and what a checkpoint needs of a user message.
type head struct {
	i      int
	typ    string
	id     string
	to     []string
	via    string
	taskID string
	steer  bool
	// text and time of a user message that is not a steer: the checkpoint
	// it is (changes.go).
	text string
	time time.Time
}

func newIndex() logIndex {
	return logIndex{at: map[string]int{}, edited: map[string]string{}, snaps: map[string]string{}}
}

// add indexes entry e at log index i, which comes after every entry indexed
// so far.
func (x *logIndex) add(i int, e store.Entry) {
	if e.ID != "" {
		x.at[e.ID] = i
	}
	switch e.Type {
	case store.EntryEdit:
		x.edited[e.Ref] = e.Text
	case store.EntrySnap:
		x.snaps[e.Ref] = e.Snap
		if isSubSnap(e.Snap) {
			x.subSnap = e.Snap
		}
	case store.EntryRevert:
		x.revert = e.ID
	case store.EntryUnrevert:
		if e.Ref == x.revert {
			x.revert = ""
		}
	case store.EntryUser:
		if x.firstText == "" && strings.TrimSpace(e.Text) != "" {
			x.firstText = e.Text
		}
	}
	if !triggers(e) {
		return
	}
	h := head{i: i, typ: e.Type, id: e.ID, to: e.To, via: e.Via, taskID: e.TaskID, steer: e.Steer}
	if e.Type == store.EntryUser && !e.Steer {
		h.text, h.time = e.Text, e.Time
	}
	x.heads = append(x.heads, h)
}

// since is the heads of the messages from log index from on.
func (x *logIndex) since(from int) []head {
	return x.heads[sort.Search(len(x.heads), func(k int) bool { return x.heads[k].i >= from }):]
}

// head is the head of the message at log index i.
func (x *logIndex) head(i int) (head, bool) {
	if hs := x.since(i); len(hs) > 0 && hs[0].i == i {
		return hs[0], true
	}
	return head{}, false
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
	idx := t.size()
	t.log = append(t.log, e)
	t.index.add(idx, e)
	if t.grew != nil {
		close(t.grew) // wakes the teammates that wait for it (WaitShared)
		t.grew = nil
	}
	if e.Type == store.EntryEvent && (e.Kind == agent.KindTurnDone || e.Kind == agent.KindExited) {
		t.boundary[e.AgentID] = idx + 1
	}
	if e.Type == store.EntryEvent && e.Kind == agent.KindAssistantText && !e.Delta {
		t.lastSpeaker = e.AgentID
	}
	if m.st != nil && !t.gone.Load() {
		if len(t.outbox) == 0 {
			t.outAt = idx
			m.w.push(func() { m.writeEntries(t) })
		}
		t.outbox = append(t.outbox, e)
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

// writeEntries is the writer's job for the log entries in t's outbox: one
// append to the thread's log for all of them, however many arrived while the
// job waited. Once they are in the store, the oldest entries may leave
// memory (window.go).
func (m *Manager) writeEntries(t *thread) {
	t.mu.Lock()
	es, at := t.outbox, t.outAt
	t.outbox = nil
	t.mu.Unlock()
	m.ensureStored(t)
	start, end, err := m.st.AppendEntriesAt(t.id, es)
	t.mu.Lock()
	t.disk.wroteLocked(at, es, start, end, err)
	t.trimLocked(false)
	t.mu.Unlock()
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
		Tasks: cloneTasks(t.tasks), Orchestration: t.level, Orchestrate: m.levelLocked(t).Schedule, Contract: t.contract, Ops: t.ops != nil,
		Scheduled: slices.Clone(t.scheduled), Trees: slices.Clone(t.trees), Capsules: cloneCapsules(t.capsules)}
	for _, peer := range slices.Sorted(maps.Keys(t.shares)) {
		th.Shares = append(th.Shares, store.Share{Peer: peer, Role: t.shares[peer]})
	}
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
	for _, h := range t.index.since(a.cursor) {
		if !t.inert[h.i] && t.skip[skipKey{h.i, a.id}] == 0 && contains(h.to, a.id) {
			return true
		}
	}
	return false
}

// dropPending makes every undelivered message addressed to a inert: it stays in
// the log (and in the next briefing) but no longer starts a turn. t.mu is held.
func (t *thread) dropPending(a *agentState) {
	for _, h := range t.index.since(a.cursor) {
		if contains(h.to, a.id) {
			t.inert[h.i] = true
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
