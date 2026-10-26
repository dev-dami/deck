package session

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/dev-dami/deck/internal/deck/agent"
	"github.com/dev-dami/deck/internal/deck/attribution"
	"github.com/dev-dami/deck/internal/deck/store"
)

// hubLoop answers the thread.send calls of every agent, each on its thread's
// relay worker (onThread).
func (m *Manager) hubLoop(msgs <-chan agent.Message) {
	defer m.wg.Done()
	for {
		select {
		case msg, ok := <-msgs:
			if !ok {
				return
			}
			from := m.get(msg.From)
			if from == nil {
				m.handleMessage(msg) // answered with an error at once
				continue
			}
			m.onThread(from.thread, func() { m.handleMessage(msg) })
		case <-m.done:
			return
		}
	}
}

// onThread queues job on t's relay worker. A thread's thread.send calls and
// the ends of its members' turns run there one at a time, in the order they
// came, so a delivery that blocks holds up only its own thread: never every
// other thread's thread.send, nor the pump of the member whose turn ended.
func (m *Manager) onThread(t *thread, job func()) {
	m.relayMu.Lock()
	q, running := m.relayQ[t]
	m.relayQ[t] = append(q, job)
	m.relayMu.Unlock()
	if !running {
		m.wg.Add(1)
		go m.relayWork(t)
	}
}

// relayWork runs t's queued relay jobs until there are none; t has a key in
// relayQ exactly while it runs.
func (m *Manager) relayWork(t *thread) {
	defer m.wg.Done()
	for {
		m.relayMu.Lock()
		q := m.relayQ[t]
		if len(q) == 0 || m.isDone() {
			delete(m.relayQ, t)
			m.relayMu.Unlock()
			return
		}
		job := q[0]
		q[0] = nil
		m.relayQ[t] = q[1:]
		m.relayMu.Unlock()
		job()
	}
}

// isDone reports whether the manager is shutting down.
func (m *Manager) isDone() bool {
	select {
	case <-m.done:
		return true
	default:
		return false
	}
}

// handleMessage validates and routes one thread.send call. Failed calls (unknown
// sender or recipient, an exited agent, a message to oneself) are answered with
// an error and never count as hops.
func (m *Manager) handleMessage(msg agent.Message) {
	respond := msg.Respond
	if respond == nil {
		respond = func(string, error) {}
	}
	from := m.get(msg.From)
	if from == nil {
		respond("", fmt.Errorf("unknown sender %q: not an agent in a Deck thread", msg.From))
		return
	}
	t := from.thread
	if t.opsOf() != nil {
		respond("", errOpsOnly)
		return
	}
	if from.status() == StatusExited {
		respond("", fmt.Errorf("%s is not running", from.getName()))
		return
	}
	text := strings.TrimSpace(msg.Text)
	to := TrimSigil(msg.To)

	t.mu.Lock()
	valid := validNames(t, from)
	var targets []*agentState
	var err error
	switch {
	case to == "":
		err = fmt.Errorf("missing recipient: set `to` to one of: %s", valid)
	case text == "":
		err = errors.New("empty message")
	case strings.EqualFold(to, "all"):
		for _, a := range t.members {
			if a != from && a.status() != StatusExited {
				targets = append(targets, a)
			}
		}
		if len(targets) == 0 {
			err = errors.New("there is nobody else running in this thread")
		}
	default:
		if all, ok := t.findAll(from, to); ok {
			targets = all
			if len(targets) == 0 {
				err = fmt.Errorf("no other %s is running in this thread: valid recipients are %s", strings.TrimSuffix(to, "*"), valid)
			}
			break
		}
		a := t.find(from, to)
		switch {
		case a == nil:
			err = fmt.Errorf("unknown recipient %q: valid recipients are %s", to, valid)
		case a == from:
			err = errors.New("you cannot send a message to yourself")
		case a.status() == StatusExited:
			err = fmt.Errorf("%s is not running", a.getName())
		default:
			targets = []*agentState{a}
		}
	}
	if err == nil {
		targets, err = t.guardSend(from, targets, time.Now())
	}
	t.mu.Unlock()
	if err != nil {
		respond("", err)
		return
	}
	capsule := strings.TrimSpace(msg.Capsule)
	if capsule != "" {
		if err := m.knownCapsule(capsule); err != nil {
			respond("", err)
			return
		}
	}
	if msg.Task != "" || msg.NewTask != "" {
		if len(targets) != 1 {
			respond("", errors.New("a task is handed to one agent: set `to` to a single name, not \"all\""))
			return
		}
		id, err := m.handOver(t, from, targets[0], text, msg)
		if err != nil {
			respond("", err)
			return
		}
		if capsule != "" {
			note, err := m.handCapsule(t, targets, id, capsule)
			if err != nil {
				respond("", err)
				return
			}
			text += "\n\n" + note
		}
		respond(m.relayTask(t, from, targets, text, "tool", id), nil)
		return
	}
	if capsule != "" {
		note, err := m.handCapsule(t, targets, "", capsule)
		if err != nil {
			respond("", err)
			return
		}
		text += "\n\n" + note
	}
	respond(m.relay(t, from, targets, text, "tool"), nil)
}

// validNames lists who a member can message: names, then "claude" and
// "claude*" for a kind with several live members, then "all". t.mu is held.
func validNames(t *thread, from *agentState) string {
	var names, pools []string
	count := map[string]int{}
	for _, a := range t.members {
		if a != from && a.status() != StatusExited {
			names = append(names, a.getName())
			if count[a.kind]++; count[a.kind] == 2 {
				pools = append(pools, a.kind+" (any free one)", a.kind+"* (every one)")
			}
		}
	}
	names = append(names, pools...)
	if len(names) > 0 {
		names = append(names, "all")
	}
	if len(names) == 0 {
		return "(nobody else is running in this thread)"
	}
	return strings.Join(names, ", ")
}

// relay delivers text from one member to others, counting one hop. Past the hop
// limit the message is logged but held until Continue. It returns the sentence
// a thread.send call answers with.
func (m *Manager) relay(t *thread, from *agentState, targets []*agentState, text, via string) string {
	return m.relayTask(t, from, targets, text, via, "")
}

// relayTask is relay for a message that hands over task taskID ("" for a plain
// message); handOver has already put the task in the recipient's hands.
func (m *Manager) relayTask(t *thread, from *agentState, targets []*agentState, text, via, taskID string) string {
	norm := normText(text)
	if taskID != "" {
		norm += "#" + taskID // the same words for another task are not a repeat
	}
	var pre, post []tea.Msg

	t.mu.Lock()
	var live []*agentState
	var dups []string
	for _, tg := range targets {
		if t.lastRelay[from.id+">"+tg.id] == norm {
			dups = append(dups, tg.getName())
			continue
		}
		live = append(live, tg)
	}
	if len(live) == 0 {
		note := fmt.Sprintf("dropped a repeated message from %s to %s", from.getName(), strings.Join(dups, ", "))
		e := store.Entry{Type: store.EntryNotice, Event: eventText(note)}
		m.appendLocked(t, e)
		t.mu.Unlock()
		m.emitAll(msgFor(t.id, e))
		return "dropped: you already sent exactly this message to " + strings.Join(dups, ", ")
	}
	for _, tg := range live {
		t.lastRelay[from.id+">"+tg.id] = norm
	}
	e := store.Entry{Type: store.EntryRelay, Speaker: from.id, To: ids(live), Via: via, TaskID: taskID, Event: eventText(text)}

	if t.hops >= m.maxHops {
		e.Held = true
		idx := m.appendLocked(t, e)
		t.inert[idx] = true
		t.heldIdx = append(t.heldIdx, idx)
		pre = append(pre, msgFor(t.id, e))
		if !t.paused {
			t.paused = true
			pe := store.Entry{Type: store.EntryPause, Event: eventText(strconv.Itoa(t.hops))}
			m.appendLocked(t, pe)
			post = append(post, msgFor(t.id, pe))
		}
		hops := t.hops
		t.mu.Unlock()
		m.emitAll(append(pre, post...)...)
		return fmt.Sprintf("held: Deck paused this thread after %d agent-to-agent messages; the user will continue it", hops)
	}
	t.hops++
	m.appendLocked(t, e)
	t.mu.Unlock()
	m.emitAll(msgFor(t.id, e))

	var res []string
	for _, tg := range live {
		d, err := m.deliver(t, tg)
		name := tg.getName()
		switch {
		case err != nil:
			res = append(res, "failed to deliver to "+name+": "+err.Error())
		case d == dQueued:
			res = append(res, "queued for "+name+" (busy)")
		case d == dExited:
			res = append(res, name+" is not running")
		default:
			res = append(res, "delivered to "+name)
		}
	}
	if len(dups) > 0 {
		res = append(res, "skipped "+strings.Join(dups, ", ")+" (identical message already sent)")
	}
	return strings.Join(res, "; ")
}

// turnEnd is how a member's turn ended, taken on its pump when the turn_done
// arrives: by the time afterTurn runs, the member's next turn may have begun.
type turnEnd struct {
	text        string // the final reply
	interrupted bool
	// answered is the member's cursor when the turn ended: what the turn was
	// briefed on (settleHanded, waitsOnChildren).
	answered int
}

// afterTurn runs on the thread's relay worker when a member's turn ends: it
// relays the $mention lines of the final reply, then starts the member's next
// turn if messages queued up.
func (m *Manager) afterTurn(a *agentState, end turnEnd) {
	text, interrupted := end.text, end.interrupted
	t := a.thread
	if !interrupted {
		m.settleHanded(t, a, text, end.answered)
		m.retireSpent(t, a, end.answered)
	}
	if text != "" && !interrupted {
		m.maybeAutoTitle(t, text)
		for _, mn := range parseMentions(text) {
			t.mu.Lock()
			targets := mentionTargets(t, a, mn.Names)
			t.mu.Unlock()
			if len(targets) > 0 {
				m.relay(t, a, targets, mn.Text, "mention")
			}
		}
	}
	_, _ = m.deliver(t, a)
}

// mentionTargets resolves the names of an $mention line to live members other
// than the sender; names that match nobody are ignored. t.mu is held.
func mentionTargets(t *thread, from *agentState, names []string) []*agentState {
	var out []*agentState
	add := func(a *agentState) {
		if a == nil || a == from || a.status() == StatusExited {
			return
		}
		for _, o := range out {
			if o == a {
				return
			}
		}
		out = append(out, a)
	}
	for _, n := range names {
		if strings.EqualFold(n, "all") {
			for _, a := range t.members {
				add(a)
			}
			continue
		}
		if all, ok := t.findAll(from, n); ok {
			for _, a := range all {
				add(a)
			}
			continue
		}
		add(t.find(from, n))
	}
	return out
}

// Continue ends a pause: the thread's hop count restarts at zero and the relays
// held while it was paused are delivered. It does nothing for a thread that is
// not paused.
func (m *Manager) Continue(threadID string) error {
	t := m.thread(threadID)
	if t == nil {
		return fmt.Errorf("session: no thread %q", threadID)
	}
	t.mu.Lock()
	if !t.paused {
		t.mu.Unlock()
		return nil
	}
	t.paused = false
	t.hops, t.work = 0, 0
	var targets []*agentState
	for _, idx := range t.heldIdx {
		delete(t.inert, idx)
		for _, id := range t.log[idx].To {
			if a := t.member(id); a != nil {
				targets = append(targets, a)
			}
		}
	}
	t.heldIdx = nil
	e := store.Entry{Type: store.EntryContinue}
	m.appendLocked(t, e)
	t.mu.Unlock()
	m.emitAll(msgFor(t.id, e))

	m.kick(t) // the scheduler stopped at the pause
	seen := map[*agentState]bool{}
	var first error
	for _, a := range targets {
		if seen[a] {
			continue
		}
		seen[a] = true
		if _, err := m.deliver(t, a); err != nil && first == nil {
			first = err
		}
	}
	return first
}

// attrib serialises the attribution checks of each folder.
var attrib = struct {
	sync.Mutex
	dirs map[string]*attribDir
}{dirs: map[string]*attribDir{}}

// attribDir is one folder's checks: run is held while one runs, waiting
// holds each member's check that has not started yet, and users counts the
// checks running or waiting (the folder's entry goes when it drops to zero).
type attribDir struct {
	run     sync.Mutex
	waiting map[*agentState]*attribJob
	users   int
}

type attribJob struct{ since time.Time }

// checkAttribution is the watcher behind the commit hooks: after a turn it
// rewrites the turn's unpushed commits that still credit another AI (one made
// with --no-verify skips the hooks), so Deck's attribution holds whatever the
// model did. It runs off the event loop.
func (m *Manager) checkAttribution(a *agentState) {
	if !m.config().DeckAttribution {
		return
	}
	a.mu.Lock()
	since := a.turnAt
	a.mu.Unlock()
	t := a.thread
	t.mu.Lock()
	dir := t.dir
	t.mu.Unlock()

	// One check at a time per folder (members sharing a work tree must not
	// rewrite its history at once), and at most one waiting per member: a
	// turn that ends while the member's last check still waits joins it.
	key := pathKey(filepath.Clean(dir))
	attrib.Lock()
	d := attrib.dirs[key]
	if d == nil {
		d = &attribDir{waiting: map[*agentState]*attribJob{}}
		attrib.dirs[key] = d
	}
	if j := d.waiting[a]; j != nil {
		if since.Before(j.since) {
			j.since = since
		}
		attrib.Unlock()
		return
	}
	j := &attribJob{since: since}
	d.waiting[a] = j
	d.users++
	attrib.Unlock()

	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		d.run.Lock()
		attrib.Lock()
		delete(d.waiting, a)
		since := j.since
		attrib.Unlock()
		defer func() {
			d.run.Unlock()
			attrib.Lock()
			if d.users--; d.users == 0 {
				delete(attrib.dirs, key)
			}
			attrib.Unlock()
		}()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		n, err := attribution.Fix(ctx, dir, since)
		switch {
		case errors.Is(err, attribution.ErrPushed):
			m.emit(NoticeMsg{ThreadID: t.id, Text: a.getName() + " pushed a commit crediting another AI; it is on the remote, so Deck left it"})
		case err != nil:
			m.emit(NoticeMsg{ThreadID: t.id, Text: "commit attribution check failed: " + err.Error()})
		case n > 0:
			m.emit(NoticeMsg{ThreadID: t.id, Text: fmt.Sprintf("replaced another AI's attribution with Deck's on %s by %s", plural(n, "commit"), a.getName())})
		}
	}()
}
