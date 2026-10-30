package session

import (
	"errors"
	"fmt"
	"strings"

	"github.com/dev-dami/deck/internal/deck/agent"
	"github.com/dev-dami/deck/internal/deck/config"
	"github.com/dev-dami/deck/internal/deck/store"
)

// Structured hand-offs (issue #103). Free peer chatter is how agent crews
// loop, so a thread.send starts the recipient's turn only when it is work
// handed over under a contract: {to, objective, deliverable, done_when,
// boundaries, check}, which becomes a task the recipient owns (handoff.go),
// or the task/new_task forms that name one. A message without a contract is
// information for a peer: it is logged, and the peer reads it the next time
// it is briefed, but it starts no turn and spends no hop, like an
// acknowledgement (ack.go). Three kinds of plain message still start a turn:
//
//   - one along the line of command: to the sender's own subagents (their
//     instructions) or to the member the sender works for (its report);
//   - an answer: to a peer whose own message (a thread.send, not a report)
//     was the last word between the two and started the sender's turn, or
//     to one waiting for the sender's answer about a file (coord.go);
//   - at orchestration full, a report to the lead.
//
// A question to a peer goes to the user instead: Deck shows it in the thread
// and tells the sender to end its turn on it (turnend.go marks that turn
// blocked). At orchestration full only the lead routes work: other members
// may message the lead and their own subagents or parent, never each other.

// normHandoff checks the terms of a structured hand-off and fills in what
// they imply: the deliverable is the contract's Expect, an objective with no
// task named creates one titled after it, and a hand-off with no message
// says its objective. text is the message's trimmed text.
func normHandoff(msg *agent.Message, text *string) error {
	if msg.Deliverable != "" {
		msg.Expect = msg.Deliverable
	}
	named := msg.Task != "" || msg.NewTask != ""
	if msg.Objective == "" {
		switch {
		case msg.Deliverable != "":
			return errors.New("a hand-off names its objective: what the work is for")
		case !named && (msg.Boundaries != "" || msg.Check != ""):
			return errors.New("boundaries and check belong to a hand-off: give objective and deliverable too")
		}
		return nil
	}
	if msg.Expect == "" {
		return errors.New("a hand-off names its deliverable: what comes back to you")
	}
	if !named {
		msg.NewTask = firstLine(msg.Objective)
	}
	if *text == "" {
		*text = msg.Objective
	}
	return nil
}

// firstLine is the first non-empty line of s.
func firstLine(s string) string {
	for l := range strings.SplitSeq(s, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			return l
		}
	}
	return ""
}

// factoryLocked refuses, at orchestration full, a thread.send from a member
// other than the lead to anyone but the lead and its own parent or
// subagents: workers report to the orchestrator, who picks who works next.
// t.mu is held.
func (t *thread) factoryLocked(from *agentState, targets []*agentState) error {
	if t.level != config.LevelFull {
		return nil
	}
	lead := t.leadLocked()
	if lead == nil || from == lead {
		return nil
	}
	for _, a := range targets {
		if a == lead || t.lineageLocked(from, a) {
			continue
		}
		return fmt.Errorf("orchestration is full, so only the lead routes work: you cannot message %s. Report to %s, the lead (task.done, or thread.send to it), and it picks who works next", a.getName(), lead.getName())
	}
	return nil
}

// lineageLocked reports whether one of a and b started the other, directly
// or through subagents of subagents. t.mu is held.
func (t *thread) lineageLocked(a, b *agentState) bool {
	above := func(x, y *agentState) bool { // x is above y
		p := t.member(y.parentID())
		for i := 0; p != nil && i < 64; i++ {
			if p == x {
				return true
			}
			p = t.member(p.parentID())
		}
		return false
	}
	return above(a, b) || above(b, a)
}

// answersLocked reports whether a message from from to to answers to: the
// last thread.send between the two was to's, to from, and it started a turn
// of from's; or to waits for from's answer about a file. t.mu is held.
func (t *thread) answersLocked(from, to *agentState) bool {
	for _, ask := range t.coord.asks {
		if ask.State == AskPending && ask.To == from.id && ask.From == to.id {
			return true
		}
	}
	hs := t.index.heads
	for k := len(hs) - 1; k >= 0; k-- {
		h := hs[k]
		if h.typ != store.EntryRelay || h.via != "tool" {
			continue
		}
		switch {
		case h.from == to.id && contains(h.to, from.id):
			return !t.inert[h.i]
		case h.from == from.id && contains(h.to, to.id):
			return false
		}
	}
	return false
}

// sortSendLocked splits the recipients of a plain message (one that hands no
// task over) into those it starts a turn for and the peers it only informs.
// t.mu is held.
func (t *thread) sortSendLocked(from *agentState, targets []*agentState) (wake, info []*agentState) {
	var lead *agentState
	if t.level == config.LevelFull {
		lead = t.leadLocked()
	}
	for _, a := range targets {
		if a == lead || t.lineageLocked(from, a) || t.answersLocked(from, a) {
			wake = append(wake, a)
		} else {
			info = append(info, a)
		}
	}
	return wake, info
}

// isQuestion reports whether text asks something: a question mark that ends
// a sentence (not one inside a URL's query).
func isQuestion(text string) bool {
	for i := strings.IndexByte(text, '?'); i >= 0; {
		if i+1 == len(text) || strings.ContainsRune(" \t\n\r\"')*_`", rune(text[i+1])) {
			return true
		}
		j := strings.IndexByte(text[i+1:], '?')
		if j < 0 {
			break
		}
		i += 1 + j
	}
	return false
}

// redirectQuestion takes a question from to peers to the user instead: a
// notice in the thread shows it, the sender's turn ends blocked on it
// (turnend.go), and the sender is told what to do. It returns that answer.
func (m *Manager) redirectQuestion(t *thread, from *agentState, peers []*agentState, text string) string {
	names := nameList(peers)
	m.notice(t, fmt.Sprintf("%s has a question (it asked %s; agents' questions go to you): %s", from.getName(), names, clip(text, maxEntryChars)))
	m.noteTurn(from, factBlocked)
	return fmt.Sprintf("not sent to %s: agents do not ask each other questions, the user answers them. Deck showed the user your question; end your turn with it in your reply, and carry on when they answer. To have %s do something, hand it over with a contract: thread.send {to, objective, deliverable, done_when}.", names, names)
}

// relayPlain delivers a plain message: to wake as a relay that starts their
// turns, to info as information for their next briefing. It returns the
// sentence the thread.send call answers with.
func (m *Manager) relayPlain(t *thread, from *agentState, wake, info []*agentState, text string) string {
	var res []string
	if len(wake) > 0 {
		res = append(res, m.relay(t, from, wake, text, "tool"))
	}
	if len(info) > 0 {
		e := store.Entry{Type: store.EntryRelay, Speaker: from.id, To: ids(info), Via: "tool", Event: eventText(text)}
		t.mu.Lock()
		idx := m.appendLocked(t, e)
		t.inert[idx] = true
		t.mu.Unlock()
		m.emitAll(msgFor(t.id, e))
		names := nameList(info)
		res = append(res, fmt.Sprintf("noted for %s: a message without a contract starts no turn; they see it when next briefed. To have one act on it, hand it over: thread.send {to, objective, deliverable, done_when}", names))
	}
	return strings.Join(res, "; ")
}

// nameList is the members' names, comma-separated.
func nameList(as []*agentState) string {
	names := make([]string, len(as))
	for i, a := range as {
		names[i] = a.getName()
	}
	return strings.Join(names, ", ")
}
