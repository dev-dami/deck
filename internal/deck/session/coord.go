package session

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/bmatcuk/doublestar/v4"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/dev-dami/deck/internal/deck/agent"
	"github.com/dev-dami/deck/internal/deck/config"
	"github.com/dev-dami/deck/internal/deck/store"
)

// Edit coordination (the gate): members of a thread that share a work tree
// do not edit a file under each other. A file of the shared tree has an
// owner while someone else is at it: the member holding a live lease on it
// (lease.acquire), else the member that changed it last in the last
// overlapWindow while its turn still runs. A handoff makes the member it
// was handed to the owner. Before member B's edit of a file A owns lands:
//
//  1. Deck asks A, in A's conversation (an "ask" relay: steered into a turn
//     that takes one, else queued, or it wakes an idle A), and shows the ask
//     in the thread as one line.
//  2. B's edit waits for the answer: A's file.answer, or a thread.send from
//     A to B. A's turn ending (when A holds no lease on the file), A's
//     lease ending, A stopping, or the timeout (config edit_gate) release
//     it without one.
//  3. The edit is then refused with what A changed in the file and what A
//     said, and the file is B's: B redoes the edit on top of A's changes,
//     and that one goes through. A that asked B to wait keeps the file.
//
// When A has already finished (its turn is over and it holds no lease), B
// gets A's changes the same way, without asking. B's own files, and files
// nobody else touched lately, are free; so is every file of a member on a
// worktree of its own. Two members asking each other cannot deadlock: an
// ask that would close a loop of waits releases the older ask of the loop.
//
// Where it holds: the edits that ask Deck first (approvals in ask mode:
// Claude's permission prompt, Codex's and OpenCode's approvals) and, for
// Claude in every mode, a PreToolUse hook (approve.EditGateHook). Every
// agent also has file.claim, which asks the same way before it edits, and
// its briefing says to use it; an edit that skipped the gate is still
// reported as an overlap (leases.go).
//
// shortcut: like leases, it lives in memory only.

const (
	// claimWait is the longest one file.claim call waits: MCP clients give
	// up on a tool call after a minute (approve.execTimeout).
	claimWait = 45 * time.Second
	// maxFileEvents is how many changes of one file are kept.
	maxFileEvents = 16
	// maxShownDiff caps the changes an asker is shown, in bytes.
	maxShownDiff = 6000
	// maxAsks is how many settled asks a thread keeps.
	maxAsks = 64
	viaAsk  = "ask" // EntryRelay.Via of the ask to a file's owner
)

// AskState is where an ask to edit a file stands.
type AskState string

const (
	AskPending  AskState = "pending"
	AskGranted  AskState = "granted"  // the owner said go ahead
	AskHeld     AskState = "held"     // the owner asked the asker to wait
	AskReleased AskState = "released" // Deck let it go: the owner was done, stopped or did not answer
)

// FileAsk is one member's request to edit a file another member owns.
type FileAsk struct {
	ID       string
	From, To string // member ids: the asker and the owner
	Path     string // relative to the repository root
	At       time.Time
	State    AskState
	// Answer is the owner's answer, or why Deck released the ask; one line.
	Answer string
	// By is the member that answered, "" when Deck released it.
	By string
}

// FileAskMsg says an ask was made or settled.
type FileAskMsg struct {
	ThreadID string
	Ask      FileAsk
}

// AskLine is the one line the conversation shows for an ask, which the
// desktop's Work Graph reads back (fileWork.ts, parseAskLine):
//
//	@codex asked @ada about `internal/limit.go`
//	@codex asked @ada about `internal/limit.go` → ada: go ahead, I'm done
//	@codex asked @ada about `internal/limit.go` → ada (wait): still on it
//	@codex asked @ada about `internal/limit.go` → deck: ada finished its turn
func AskLine(a FileAsk) string {
	s := "@" + a.From + " asked @" + a.To + " about `" + a.Path + "`"
	if a.State == AskPending {
		return s
	}
	who := a.By
	if who == "" {
		who = "deck"
	}
	if a.State == AskHeld {
		who += " (wait)"
	}
	return s + " → " + who + ": " + clipText(oneLine(a.Answer), 160)
}

// fileEvent is a change of a file, or a handoff of it (diff nil).
type fileEvent struct {
	member string
	at     time.Time
	diff   *agent.FileDiff
}

type fileAsk struct {
	FileAsk
	key   string
	entry int // log index of the ask relay to the owner, -1 for none
	done  chan struct{}
}

// coordState is a thread's edit coordination; the thread's mu guards it.
type coordState struct {
	// off overrides the config for this thread (SetEditGate): nil follows it.
	off   *bool
	files map[string][]fileEvent // by pathKey, oldest first
	asks  []*fileAsk             // pending ones, and the last settled
	seq   int
}

func (c *coordState) add(key string, e fileEvent) {
	if c.files == nil {
		c.files = map[string][]fileEvent{}
	}
	evs := append(c.files[key], e)
	if len(evs) > maxFileEvents {
		evs = evs[len(evs)-maxFileEvents:]
	}
	c.files[key] = evs
}

// pending is the ask from member from about key that waits, or nil.
func (c *coordState) pending(from, key string) *fileAsk {
	for _, a := range c.asks {
		if a.State == AskPending && a.From == from && a.key == key {
			return a
		}
	}
	return nil
}

func (c *coordState) newAsk(from, to, path, key string, now time.Time) *fileAsk {
	c.seq++
	a := &fileAsk{FileAsk: FileAsk{ID: "ask" + strconv.Itoa(c.seq), From: from, To: to, Path: path, At: now, State: AskPending},
		key: key, entry: -1, done: make(chan struct{})}
	c.asks = append(c.asks, a)
	// Drop the oldest settled asks past maxAsks; pending ones stay.
	for over := len(c.asks) - maxAsks; over > 0; over-- {
		for i, x := range c.asks {
			if x.State != AskPending {
				c.asks = append(c.asks[:i], c.asks[i+1:]...)
				break
			}
		}
	}
	return a
}

// changesSince renders what owner changed in key after since, newest last,
// capped; "" when nothing with a diff.
func (c *coordState) changesSince(key, owner string, since time.Time) string {
	var b strings.Builder
	for _, e := range c.files[key] {
		if e.member != owner || e.diff == nil || !e.at.After(since) || len(e.diff.Hunks) == 0 {
			continue
		}
		writeHunks(&b, e.diff)
	}
	s := b.String()
	if len(s) > maxShownDiff {
		cut := maxShownDiff
		for cut > 0 && (s[cut]&0xC0) == 0x80 {
			cut--
		}
		s = s[:cut] + fmt.Sprintf("\n… (%d more bytes; read the file for the rest)\n", len(s)-cut)
	}
	return s
}

func writeHunks(b *strings.Builder, d *agent.FileDiff) {
	for _, h := range d.Hunks {
		fmt.Fprintf(b, "@@ -%d,%d +%d,%d @@\n", h.OldStart, h.OldLines, h.NewStart, h.NewLines)
		for _, l := range h.Lines {
			b.WriteByte(byte(l.Op))
			b.WriteString(l.Text)
			if !strings.HasSuffix(l.Text, "\n") {
				b.WriteByte('\n')
			}
		}
	}
}

// lastOf is the time of member's last change or handoff of key, or zero.
func (c *coordState) lastOf(key, member string) time.Time {
	evs := c.files[key]
	for i := len(evs) - 1; i >= 0; i-- {
		if evs[i].member == member {
			return evs[i].at
		}
	}
	return time.Time{}
}

// owner is who stands in b's way at a file: the member, whether it holds a
// lease on it, and whether its turn runs.
type owner struct {
	a      *agentState
	leased bool
	busy   bool
}

// ownerOf is the owner of the file rel (key) for b, or ok false when b may
// edit it freely. t.mu is held.
func (t *thread) ownerOf(b *agentState, rel, key string, now time.Time) (o owner, ok bool) {
	mine := t.coord.lastOf(key, b.id)
	for _, l := range t.liveLeases(now) {
		if l.member == b.id || !l.at.After(mine) {
			continue
		}
		for _, g := range l.paths {
			if hit, _ := doublestar.Match(g, rel); hit || g == rel {
				a := t.member(l.member)
				return owner{a: a, leased: true, busy: a.busy()}, true
			}
		}
	}
	evs := t.coord.files[key]
	if len(evs) == 0 {
		return owner{}, false
	}
	e := evs[len(evs)-1]
	if e.member == b.id || now.Sub(e.at) > overlapWindow {
		return owner{}, false
	}
	a := t.member(e.member)
	if a == nil || a.status() == StatusExited {
		return owner{}, false
	}
	return owner{a: a, busy: a.busy()}, true
}

// gateOn reports whether t coordinates edits.
func (m *Manager) gateOn(t *thread) bool {
	t.mu.Lock()
	off := t.coord.off
	t.mu.Unlock()
	if off != nil {
		return !*off
	}
	return !m.config().EditGate.Off
}

func (m *Manager) gateWait() time.Duration {
	if s := m.config().EditGate.TimeoutSec; s > 0 {
		return time.Duration(s) * time.Second
	}
	return config.DefaultEditGateSec * time.Second
}

// SetEditGate turns edit coordination on or off for one thread, whatever the
// config says.
func (m *Manager) SetEditGate(threadID string, on bool) error {
	t := m.thread(threadID)
	if t == nil {
		return fmt.Errorf("session: no thread %q", threadID)
	}
	off := !on
	t.mu.Lock()
	t.coord.off = &off
	t.mu.Unlock()
	if !on {
		m.releaseAsks(t, func(*fileAsk) bool { return true }, "Deck's edit coordination was turned off")
	}
	return nil
}

// gateResult is how b's edit of a file passed the gate.
type gateResult struct {
	// deny is why the edit may not land as it is; "" to go ahead.
	deny string
	// waiting is set when the wait ran out before the owner answered (only
	// for file.claim, whose wait is shorter than the ask's).
	waiting bool
	owner   string
}

// gate is the gate b's edit of path p passes (see the top of the file): it
// asks the file's owner when there is one and waits up to max for the ask to
// settle.
func (m *Manager) gate(b *agentState, p string, max time.Duration) gateResult {
	t := b.thread
	if b.getWorktree() != nil || strings.TrimSpace(p) == "" || !m.gateOn(t) {
		return gateResult{}
	}
	root := m.subRoot(b)
	now := time.Now()
	var msgs []tea.Msg
	t.mu.Lock()
	rel, ok := relTo(root, t.dir, p)
	if !ok {
		t.mu.Unlock()
		return gateResult{}
	}
	key := pathKey(rel)
	ask := t.coord.pending(b.id, key)
	var tell *agentState
	if ask == nil {
		o, owned := t.ownerOf(b, rel, key, now)
		if !owned {
			t.mu.Unlock()
			return gateResult{}
		}
		since := t.coord.lastOf(key, b.id)
		ask = t.coord.newAsk(b.id, o.a.id, rel, key, now)
		if !o.busy && !o.leased {
			// The owner is done with it: the file is b's, with its changes.
			changes := t.coord.changesSince(key, o.a.id, since)
			msgs = append(msgs, m.settleLocked(t, ask, AskReleased, o.a.getName()+" had finished with it", "", now))
			t.mu.Unlock()
			m.emitAll(msgs...)
			if changes == "" {
				return gateResult{}
			}
			return gateResult{owner: o.a.getName(), deny: handoffText(rel, o.a.getName(), changes, ask.FileAsk)}
		}
		msgs = append(msgs, m.breakLoops(t, ask, now)...)
		e := store.Entry{Type: store.EntryRelay, Speaker: b.id, To: []string{o.a.id}, Via: viaAsk, Event: eventText(askText(rel, o, m.gateWait()))}
		ask.entry = m.appendLocked(t, e)
		msgs = append(msgs, msgFor(t.id, e), FileAskMsg{ThreadID: t.id, Ask: ask.FileAsk})
		tell = o.a
		wait := m.gateWait()
		time.AfterFunc(wait, func() {
			m.settle(t, ask, AskReleased, "no answer in "+span(wait), "")
		})
	}
	t.mu.Unlock()
	m.emitAll(msgs...)
	if tell != nil {
		m.tellOwner(t, tell)
	}

	timer := time.NewTimer(max)
	defer timer.Stop()
	select {
	case <-ask.done:
	case <-timer.C:
	case <-m.done:
		return gateResult{}
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	res := gateResult{owner: t.name(ask.To)}
	switch ask.State {
	case AskPending:
		res.waiting = true
	case AskHeld:
		res.deny = fmt.Sprintf("Deck held this edit of %s: %s asks you to wait: %q\nLeave %s alone for now; file.claim {path} asks again when you need it.",
			rel, res.owner, ask.Answer, rel)
	default:
		changes := t.coord.changesSince(key, ask.To, t.coord.prevOf(key, ask))
		res.deny = handoffText(rel, res.owner, changes, ask.FileAsk)
	}
	return res
}

// prevOf is when the asker last had key before ask: what it saw.
func (c *coordState) prevOf(key string, ask *fileAsk) time.Time {
	evs := c.files[key]
	for i := len(evs) - 1; i >= 0; i-- {
		if evs[i].member == ask.From && evs[i].at.Before(ask.At) {
			return evs[i].at
		}
	}
	return time.Time{}
}

// askText is what the owner of a file is told when another member asks.
func askText(rel string, o owner, wait time.Duration) string {
	why := "you changed in this turn"
	if o.leased {
		why = "you hold a lease on"
	}
	return fmt.Sprintf("I'm about to edit %s, which %s. Deck holds my edit until you answer: file.answer {path: %q, text: \"go ahead\" or what I should know} (wait: true if I must not touch it yet), or a thread.send to me. Without an answer it goes ahead in %s.",
		rel, why, rel, span(wait))
}

// span is d as a person says it: "2m", "90s".
func span(d time.Duration) string {
	if d >= time.Minute && d%time.Minute == 0 {
		return strconv.Itoa(int(d/time.Minute)) + "m"
	}
	return strconv.Itoa(int(d.Round(time.Second)/time.Second)) + "s"
}

// handoffText is what the asker is told when the file is handed to it.
func handoffText(rel, owner, changes string, a FileAsk) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Deck held this edit of %s: %s was changing it.", rel, owner)
	if changes != "" {
		fmt.Fprintf(&b, "\n%s's changes to %s:\n%s", owner, rel, strings.TrimRight(changes, "\n"))
	}
	switch {
	case a.By != "":
		fmt.Fprintf(&b, "\n%s answered: %q", owner, a.Answer)
	case a.Answer != "":
		fmt.Fprintf(&b, "\n(%s)", a.Answer)
	}
	fmt.Fprintf(&b, "\n%s is yours now: read it again and redo your edit on top of %s's changes.", rel, owner)
	return b.String()
}

// breakLoops releases the asks that, with ask, would make members wait on
// each other in a loop: the ask of the loop whose owner is ask's asker. Its
// asker goes ahead; ask's owner can then answer. t.mu is held.
func (m *Manager) breakLoops(t *thread, ask *fileAsk, now time.Time) []tea.Msg {
	var msgs []tea.Msg
	seen := map[string]bool{}
	var walk func(member string) bool
	// walk follows the waits from member and releases the ask into the asker.
	walk = func(member string) bool {
		if seen[member] {
			return false
		}
		seen[member] = true
		for _, a := range t.coord.asks {
			if a.State != AskPending || a.From != member || a == ask {
				continue
			}
			if a.To == ask.From {
				msgs = append(msgs, m.settleLocked(t, a, AskReleased, t.name(ask.From)+" is waiting on "+t.name(ask.To)+" too", "", now))
				return true
			}
			if walk(a.To) {
				return true
			}
		}
		return false
	}
	for walk(ask.To) {
		clear(seen)
	}
	return msgs
}

// tellOwner brings the ask to the owner: into its turn when its CLI takes a
// message mid-turn, else when its turn ends; an idle owner starts a turn.
func (m *Manager) tellOwner(t *thread, o *agentState) {
	m.onThread(t, func() {
		if o.busy() {
			if st, ok := o.getRun().(agent.Steerer); ok && m.nativeSteer(o) {
				if err := m.steerNow(t, o, st); err == nil {
					return
				}
			}
		}
		_, _ = m.deliver(t, o)
	})
}

func (m *Manager) nativeSteer(a *agentState) bool {
	ad := m.adapters[a.kind]
	return ad != nil && agent.CapsOf(ad).NativeSteer
}

// settle settles ask, if it still waits, and says so.
func (m *Manager) settle(t *thread, ask *fileAsk, state AskState, answer, by string) {
	t.mu.Lock()
	if ask.State != AskPending {
		t.mu.Unlock()
		return
	}
	msg := m.settleLocked(t, ask, state, answer, by, time.Now())
	t.mu.Unlock()
	m.emit(msg)
}

// settleLocked settles a pending ask: a handoff makes the asker the file's
// owner, and an ask the owner has not been told of yet no longer starts its
// turn. t.mu is held.
func (m *Manager) settleLocked(t *thread, ask *fileAsk, state AskState, answer, by string, now time.Time) tea.Msg {
	ask.State, ask.Answer, ask.By = state, clipText(answer, 2000), by
	if state != AskHeld {
		t.coord.add(ask.key, fileEvent{member: ask.From, at: now})
	}
	if ask.entry >= 0 {
		t.inert[ask.entry] = true
	}
	close(ask.done)
	return FileAskMsg{ThreadID: t.id, Ask: ask.FileAsk}
}

// releaseAsks settles the pending asks of t that match, with why.
func (m *Manager) releaseAsks(t *thread, match func(*fileAsk) bool, why string) {
	now := time.Now()
	var msgs []tea.Msg
	t.mu.Lock()
	for _, a := range t.coord.asks {
		if a.State == AskPending && match(a) {
			msgs = append(msgs, m.settleLocked(t, a, AskReleased, why, "", now))
		}
	}
	t.mu.Unlock()
	m.emitAll(msgs...)
}

// ownerDone settles the asks to o once it is out of their way: every one
// when it stopped (and o's own asks with them). When its turn is over, an
// ask it was told of is answered by its last reply, and one on a file it
// holds no lease on is released. Asks to a member in a turn wait.
func (m *Manager) ownerDone(t *thread, o *agentState, final string) {
	exited := o.status() == StatusExited
	if !exited && o.busy() {
		return
	}
	now := time.Now()
	final = strings.TrimSpace(final)
	var msgs []tea.Msg
	t.mu.Lock()
	leases := t.liveLeases(now)
	held := func(rel string) bool {
		for _, l := range leases {
			if l.member != o.id {
				continue
			}
			for _, g := range l.paths {
				if hit, _ := doublestar.Match(g, rel); hit || g == rel {
					return true
				}
			}
		}
		return false
	}
	for _, a := range t.coord.asks {
		switch {
		case a.State != AskPending:
		case exited && (a.To == o.id || a.From == o.id):
			msgs = append(msgs, m.settleLocked(t, a, AskReleased, o.getName()+" stopped", "", now))
		case a.To != o.id:
		case final != "" && a.entry >= 0 && o.cursor > a.entry:
			msgs = append(msgs, m.settleLocked(t, a, AskGranted, final, o.id, now))
		case !held(a.Path):
			msgs = append(msgs, m.settleLocked(t, a, AskReleased, o.getName()+" finished its turn", "", now))
		}
	}
	t.mu.Unlock()
	m.emitAll(msgs...)
}

// answerAsks settles the pending asks to o: those about path (every one
// when path is ""), with text; wait keeps the file o's.
func (m *Manager) answerAsks(o *agentState, path, text string, wait bool) (string, error) {
	t := o.thread
	text = strings.TrimSpace(text)
	if text == "" {
		text = "go ahead"
		if wait {
			text = "wait"
		}
	}
	key := ""
	if strings.TrimSpace(path) != "" {
		root := m.subRoot(o)
		t.mu.Lock()
		rel, ok := relTo(root, t.dir, path)
		t.mu.Unlock()
		if !ok {
			return "", fmt.Errorf("%q is not inside the repository", path)
		}
		key = pathKey(rel)
	}
	state := AskGranted
	if wait {
		state = AskHeld
	}
	now := time.Now()
	var msgs []tea.Msg
	var who []string
	t.mu.Lock()
	for _, a := range t.coord.asks {
		if a.State == AskPending && a.To == o.id && (key == "" || a.key == key) {
			msgs = append(msgs, m.settleLocked(t, a, state, text, o.id, now))
			who = append(who, t.name(a.From)+" about "+a.Path)
		}
	}
	t.mu.Unlock()
	if len(who) == 0 {
		return "", fmt.Errorf("nobody is waiting on you for %s", orAll(path))
	}
	m.emitAll(msgs...)
	return "answered " + strings.Join(who, "; "), nil
}

func orAll(path string) string {
	if strings.TrimSpace(path) == "" {
		return "any file"
	}
	return path
}

// answerBySend settles the pending asks to from by the members it sends a
// message to: the message is the answer.
func (m *Manager) answerBySend(t *thread, from *agentState, to []*agentState, text string) {
	now := time.Now()
	var msgs []tea.Msg
	t.mu.Lock()
	for _, a := range t.coord.asks {
		if a.State != AskPending || a.To != from.id {
			continue
		}
		for _, x := range to {
			if x.id == a.From {
				msgs = append(msgs, m.settleLocked(t, a, AskGranted, text, from.id, now))
				break
			}
		}
	}
	t.mu.Unlock()
	m.emitAll(msgs...)
}

// claimFile is file.claim: b asks for paths before it edits them, for the
// CLIs whose edits do not pass the gate on their own.
func (m *Manager) claimFile(b *agentState, raw string) (string, error) {
	var paths []string
	for _, p := range strings.Split(raw, ",") {
		if p = strings.TrimSpace(p); p != "" {
			paths = append(paths, p)
		}
	}
	if len(paths) == 0 {
		return "", fmt.Errorf("give the paths you are about to edit, comma-separated")
	}
	if !m.gateOn(b.thread) {
		return "edit coordination is off in this thread: go ahead", nil
	}
	var out []string
	for _, p := range paths {
		r := m.gate(b, p, claimWait)
		switch {
		case r.waiting:
			out = append(out, fmt.Sprintf("%s: still waiting for %s; call file.claim again to keep waiting (Deck lets it go after %s)", p, r.owner, span(m.gateWait())))
		case r.deny != "":
			// Through the gate: the reason is the handoff.
			out = append(out, strings.Replace(r.deny, "Deck held this edit of", "Before you edit", 1))
		default:
			out = append(out, p+": free, go ahead")
		}
	}
	return strings.Join(out, "\n\n"), nil
}

// Asks is the thread's asks, oldest first, as the UI shows them.
func (m *Manager) Asks(threadID string) []FileAsk {
	t := m.thread(threadID)
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]FileAsk, 0, len(t.coord.asks))
	for _, a := range t.coord.asks {
		out = append(out, a.FileAsk)
	}
	return out
}

// gated reports whether an edit approval meets an owner at the gate: one of
// its files has one. The rest go on at once, in order.
func (m *Manager) gated(b *agentState, req agent.ApprovalRequest) bool {
	t := b.thread
	if b.getWorktree() != nil || !m.gateOn(t) {
		return false
	}
	root := m.subRoot(b)
	now := time.Now()
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, p := range approvalPaths(req) {
		rel, ok := relTo(root, t.dir, p)
		if !ok {
			continue
		}
		if t.coord.pending(b.id, pathKey(rel)) != nil {
			return true
		}
		if _, owned := t.ownerOf(b, rel, pathKey(rel), now); owned {
			return true
		}
	}
	return false
}

// gateApproval passes an edit approval through the gate before the rest of
// onApproval: each of its files in turn. It reports false when the request
// was answered here.
func (m *Manager) gateApproval(a *agentState, req agent.ApprovalRequest) bool {
	var denies []string
	for _, p := range approvalPaths(req) {
		if r := m.gate(a, p, m.gateWait()+time.Second); r.deny != "" {
			denies = append(denies, r.deny)
		}
	}
	if len(denies) == 0 {
		return true
	}
	if req.Respond != nil {
		req.Respond(agent.Decision{Allow: false, Reason: strings.Join(denies, "\n\n")})
	}
	return false
}

// approvalPaths are the files an edit approval would change: Codex names
// several, comma-separated.
func approvalPaths(req agent.ApprovalRequest) []string {
	var out []string
	add := func(p string) {
		p = strings.TrimSpace(p)
		for _, x := range out {
			if x == p {
				return
			}
		}
		if p != "" {
			out = append(out, p)
		}
	}
	if req.Diff != nil {
		add(req.Diff.Path)
	}
	for _, p := range strings.Split(req.Summary, ", ") {
		if !strings.ContainsAny(p, " \t\n") {
			add(p)
		}
	}
	return out
}

// gateHook answers a CLI's own pre-edit hook (approve.Broker.SetEditGate):
// "" lets the edit go on, anything else refuses it with that reason.
func (m *Manager) gateHook(agentID string, paths []string) string {
	a := m.get(agentID)
	if a == nil {
		return ""
	}
	var denies []string
	for _, p := range paths {
		if r := m.gate(a, p, m.gateWait()+time.Second); r.deny != "" {
			denies = append(denies, r.deny)
		}
	}
	return strings.Join(denies, "\n\n")
}
