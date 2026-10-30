package session

import (
	"fmt"
	"slices"
	"strings"
	"unicode"

	"github.com/dev-dami/deck/internal/deck/agent"
	"github.com/dev-dami/deck/internal/deck/store"
)

// Stall detection, next to the turn budget (Magentic-One's progress ledger
// and max_stalls; OpenHands' stuck detector). MaxTurns stops runaway work
// only at a fixed count; this stops it early when nothing changes. Each turn
// the agents start themselves is judged when it ends, by what it left in the
// log:
//
//   - Strong progress: a file changed (a diff, or an edit tool that
//     succeeded), or the member changed the task board (made a task, moved
//     one). It clears the ledger.
//   - Weak progress: a command ran (tests, a build), whatever it returned. It
//     keeps the turn from counting as idle, but does not break a loop: two
//     agents can run the same failing tests at each other for ever.
//
// Then, with N the level's MaxStalls (3 by default), in this order:
//
//   - Repeats: N near-identical agent messages (word pairs, Jaccard at least
//     stallSimilar against the last stallRecent messages) since a file or
//     the board last changed. An exact repeat is already dropped (relayTask).
//   - Ping-pong: the last 2N agent-started turns alternate between the same
//     two members while no file or task changed.
//   - Idle: N agent-started turns in a row with no progress signal at all.
//
// At a stall the thread pauses, as at the turn budget, and the pause says why;
// the user's next message or Continue goes on. At a level with Replan (full,
// factory mode) the lead is first asked, once, to re-plan instead; a second
// stall before the user speaks pauses. Turns the user (or a teammate)
// started never count, and the user's message clears the ledger as it resets
// the turn budget.

const (
	stallSimilar = 0.9
	stallRecent  = 6
	// stallChars is how much of a message the comparison reads.
	stallChars = 4000
	// viaStall is the Via of Deck's request to the lead to re-plan.
	viaStall = "stall"
)

// stallState is a thread's progress ledger. t.mu guards it.
type stallState struct {
	// begun holds each member's turns in progress, oldest first: where in
	// the log each began and whether the agents started it (deliverEx). A
	// member's next turn can begin before the last one is judged.
	begun map[string][]stallTurn
	// idle are the members of the agent-started turns in a row that showed
	// no progress, and repeats the members that sent a near-identical
	// message since a file or the board last changed.
	idle, repeats []string
	// seq are the agent-started turns judged since a file or the board last
	// changed (the last 2N), in the order they began: two agents' turns can
	// end in either order, but one began because of the other.
	seq []stallTurn
	// recent are the last agent messages, as word pairs.
	recent []shingles
	// replanned is set once the lead was asked to re-plan since the user
	// last spoke.
	replanned bool
}

type stallTurn struct {
	start int
	agent bool
	id    string // in seq: whose turn
}

// begin records that member id's turn begins at log index start; agent says
// the agents started it. t.mu is held.
func (s *stallState) begin(id string, start int, agent bool) {
	if s.begun == nil {
		s.begun = map[string][]stallTurn{}
	}
	s.begun[id] = append(s.begun[id], stallTurn{start: start, agent: agent})
}

// unbegin drops the turn begin recorded last for id: it never started.
// t.mu is held.
func (s *stallState) unbegin(id string) {
	if b := s.begun[id]; len(b) > 0 {
		s.begun[id] = b[:len(b)-1]
	}
}

// clear forgets everything but the turns in progress: the user spoke.
// t.mu is held.
func (s *stallState) clear() {
	*s = stallState{begun: s.begun}
}

// turnSignals is what one turn left in the log.
type turnSignals struct {
	strong, weak bool
	msgs         []shingles
}

// turnSignalsLocked reads what member a did in the log from start to stop,
// and its final reply. t.mu is held.
func (t *thread) turnSignalsLocked(a *agentState, start, stop int, reply string) turnSignals {
	var sig turnSignals
	addMsg := func(text string) {
		if text = strings.TrimSpace(text); text != "" && !isAck(text) {
			sig.msgs = append(sig.msgs, shinglesOf(text))
		}
	}
	// A part of the log that cannot be read back is left out.
	_ = t.view().each(start, stop, func(_ int, e store.Entry) bool {
		switch e.Type {
		case store.EntryEvent:
			if e.AgentID != a.id || e.Delta {
				return true
			}
			switch {
			case e.Kind == agent.KindFileChange && e.Diff != nil:
				sig.strong = true
			case e.Kind == agent.KindToolResult && e.Tool != nil && isEditTool(e.Tool.Name) && e.Tool.Status != agent.ToolError:
				sig.strong = true
			case e.Kind == agent.KindToolResult && e.Tool != nil && isCommandTool(e.Tool.Name):
				sig.weak = true
			}
		case store.EntryTask:
			if e.Speaker == a.id {
				sig.strong = true
			}
		case store.EntryRelay:
			if e.Speaker == a.id && e.Via == "tool" {
				addMsg(e.Text)
			}
		}
		return true
	})
	addMsg(reply)
	return sig
}

// stall kinds, in the order they are checked.
const (
	stallNone = iota
	stallRepeats
	stallPingPong
	stallIdle
)

// judge counts agent-started turn tr of member id against the ledger and
// returns the kind of stall it completes, with the members in it. n is the
// level's MaxStalls. t.mu is held.
func (s *stallState) judge(id string, tr stallTurn, sig turnSignals, n int) (kind int, who []string) {
	if sig.strong {
		s.idle, s.repeats, s.seq = nil, nil, nil
		return stallNone, nil
	}
	if sig.weak {
		s.idle = nil
	} else {
		s.idle = append(s.idle, id)
	}
	if s.repeated(sig.msgs) {
		s.repeats = append(s.repeats, id)
	}
	tr.id = id
	k := len(s.seq)
	for k > 0 && s.seq[k-1].start > tr.start {
		k--
	}
	s.seq = slices.Insert(s.seq, k, tr)
	if len(s.seq) > 2*n {
		s.seq = s.seq[len(s.seq)-2*n:]
	}
	order := make([]string, len(s.seq))
	for i, x := range s.seq {
		order[i] = x.id
	}
	switch {
	case len(s.repeats) >= n:
		return stallRepeats, s.repeats
	case alternates(order, n):
		return stallPingPong, order[len(order)-2:]
	case len(s.idle) >= n:
		return stallIdle, s.idle
	}
	return stallNone, nil
}

// repeated reports whether one of msgs is nearly one of the recent agent
// messages. Messages of the same turn are not compared with each other: a
// reply that repeats the message the turn sent is one message.
func (s *stallState) repeated(msgs []shingles) bool {
	for _, m := range msgs {
		for _, r := range s.recent {
			if similarity(m, r) >= stallSimilar {
				return true
			}
		}
	}
	return false
}

// remember keeps msgs among the recent agent messages.
func (s *stallState) remember(msgs []shingles) {
	s.recent = append(s.recent, msgs...)
	if len(s.recent) > stallRecent {
		s.recent = s.recent[len(s.recent)-stallRecent:]
	}
}

// alternates reports whether the last 2n members of seq alternate between the
// same two.
func alternates(seq []string, n int) bool {
	if n <= 0 || len(seq) < 2*n {
		return false
	}
	s := seq[len(seq)-2*n:]
	if s[0] == s[1] {
		return false
	}
	for i, id := range s {
		if id != s[i%2] {
			return false
		}
	}
	return true
}

// shingles are the pairs of consecutive words of a message (its one word,
// for a message of one).
type shingles map[string]struct{}

func shinglesOf(text string) shingles {
	if len(text) > stallChars {
		text = text[:stallChars]
	}
	words := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
	out := shingles{}
	if len(words) == 1 {
		out[words[0]] = struct{}{}
	}
	for i := 0; i+1 < len(words); i++ {
		out[words[i]+" "+words[i+1]] = struct{}{}
	}
	return out
}

// similarity is the Jaccard index of a and b: 1 for the same word pairs, 0
// for none in common.
func similarity(a, b shingles) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	common := 0
	for k := range a {
		if _, ok := b[k]; ok {
			common++
		}
	}
	return float64(common) / float64(len(a)+len(b)-common)
}

// stallWhy says what a stall of kind was, naming its members. t.mu is held.
func (t *thread) stallWhy(kind int, who []string, n int) string {
	var names []string
	seen := map[string]bool{}
	for _, id := range who {
		if !seen[id] {
			seen[id] = true
			names = append(names, t.name(id))
		}
	}
	all := joinAnd(names)
	switch kind {
	case stallRepeats:
		if len(names) == 1 {
			return fmt.Sprintf("%s sent nearly the same message %d times with no file or task changing", all, n)
		}
		return fmt.Sprintf("%s repeated the same exchange %d times with no file or task changing", all, n)
	case stallPingPong:
		return fmt.Sprintf("%s passed the work back and forth %d times with no file or task changing", all, n)
	default:
		return fmt.Sprintf("%s took %d turns in a row without changing a file or a task or running a command", all, n)
	}
}

// joinAnd joins names as "a", "a and b", "a, b and c".
func joinAnd(names []string) string {
	if len(names) < 2 {
		return strings.Join(names, "")
	}
	return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
}

// checkStall runs when member a's turn ends (afterTurn): it judges the turn
// when the agents started it, and at a stall pauses the thread or asks the
// lead to re-plan.
func (m *Manager) checkStall(t *thread, a *agentState, end turnEnd) {
	t.mu.Lock()
	turns := t.stall.begun[a.id]
	if len(turns) == 0 {
		t.mu.Unlock()
		return
	}
	tr, stop := turns[0], t.size()
	if len(turns) > 1 {
		stop = turns[1].start // the next turn has begun: what follows is its
	}
	if t.stall.begun[a.id] = turns[1:]; len(turns) == 1 {
		delete(t.stall.begun, a.id)
	}
	n := m.levelLocked(t).MaxStalls
	if end.interrupted || t.ops != nil || t.gone.Load() || n <= 0 {
		t.mu.Unlock()
		return
	}
	sig := t.turnSignalsLocked(a, tr.start, stop, end.text)
	kind := stallNone
	var who []string
	if tr.agent {
		kind, who = t.stall.judge(a.id, tr, sig, n)
	}
	t.stall.remember(sig.msgs)
	if kind == stallNone {
		t.mu.Unlock()
		return
	}
	why := t.stallWhy(kind, who, n)
	t.stall.idle, t.stall.repeats, t.stall.seq = nil, nil, nil
	if t.paused { // something else paused it already
		t.mu.Unlock()
		return
	}
	lead := t.leadLocked()
	if m.levelLocked(t).Replan && !t.stall.replanned && lead != nil {
		t.stall.replanned = true
		ne := store.Entry{Type: store.EntryNotice, Event: eventText(fmt.Sprintf("stalled: %s; Deck asked %s to re-plan", why, lead.getName()))}
		m.appendLocked(t, ne)
		re := store.Entry{Type: store.EntryRelay, Speaker: deckSpeaker, To: []string{lead.id}, Via: viaStall, Event: eventText(replanText(why))}
		m.appendLocked(t, re)
		t.mu.Unlock()
		m.emitAll(msgFor(t.id, ne), msgFor(t.id, re))
		_, _ = m.deliver(t, lead)
		return
	}
	t.paused = true
	pe := store.Entry{Type: store.EntryPause, Event: eventText("the agents stalled: " + why)}
	m.appendLocked(t, pe)
	t.mu.Unlock()
	m.emitAll(msgFor(t.id, pe))
}

// replanText is what the lead is asked when the agents stall at a level
// with Replan.
func replanText(why string) string {
	return "[deck] The work has stalled: " + why + ". Step back and re-plan before anyone goes on: " +
		"what is done, what is in the way, and what each agent should do next; stop work that is going nowhere. " +
		"If you need the user, say so and stop. Another stall before the user writes pauses the thread."
}
