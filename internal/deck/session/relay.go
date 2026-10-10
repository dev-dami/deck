package session

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/dev-dami/deck/internal/deck/agent"
	"github.com/dev-dami/deck/internal/deck/store"
)

// hubLoop answers the send_message tool calls of every agent.
func (m *Manager) hubLoop(msgs <-chan agent.Message) {
	defer m.wg.Done()
	for {
		select {
		case msg, ok := <-msgs:
			if !ok {
				return
			}
			m.handleMessage(msg)
		case <-m.done:
			return
		}
	}
}

// handleMessage validates and routes one send_message call. Failed calls (unknown
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
	t.mu.Unlock()
	if err != nil {
		respond("", err)
		return
	}
	respond(m.relay(t, from, targets, text, "tool"), nil)
}

// validNames lists who a member can message. t.mu is held.
func validNames(t *thread, from *agentState) string {
	var names []string
	for _, a := range t.members {
		if a != from && a.status() != StatusExited {
			names = append(names, a.getName())
		}
	}
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
// the send_message tool answers with.
func (m *Manager) relay(t *thread, from *agentState, targets []*agentState, text, via string) string {
	norm := normText(text)
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
	e := store.Entry{Type: store.EntryRelay, Speaker: from.id, To: ids(live), Via: via, Event: eventText(text)}

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

// afterTurn runs when a member's turn ends: it relays the $mention lines of the
// final reply, then starts the member's next turn if messages queued up.
func (m *Manager) afterTurn(a *agentState) {
	text, interrupted := a.takeFinal()
	t := a.thread
	if text != "" && !interrupted {
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
	t.hops = 0
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
