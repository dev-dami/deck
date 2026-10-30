package session

import (
	"strings"

	"github.com/dev-dami/deck/internal/deck/agent"
	"github.com/dev-dami/deck/internal/deck/store"
)

// Terminal states for a turn (issue #103). Every agent turn ends as one of
// agent.TurnDone, TurnHandoff, TurnBlocked or TurnContinue, read from what
// happened in it, and the turn_done event carries it (agent.Event.End): the
// thread's log keeps it, and the clients show a state rather than prose.
//
//   - blocked: the turn ends on a question for the user. A question it put
//     to a peer was redirected to the user (handcontract.go), the lead's
//     final reply ends asking something, or the agent blocked a task.
//   - handoff: the turn handed work over under a contract: a thread.send
//     that hands a task over, agent.spawn, a sub-thread's brief or post, a
//     teammate's contract, a race, a review's changes, or work it finished
//     that went to a reviewer.
//   - done: the turn finished a task: task.done (with the evidence its check
//     asks for, evidence.go), review.approve, or a reply that settles a task
//     handed to it (settleHanded).
//   - continue: none of those.
//
// When several apply, the first in that order wins: what needs the user
// matters most, then where the work went.

// endFact is one thing a turn did that decides how it ended.
type endFact uint8

const (
	factDone endFact = iota + 1
	factHandoff
	factBlocked
)

// turnFacts is what a member's running turn did so far.
type turnFacts struct {
	done, handoff, blocked bool
}

// noteTurn records fact for a's running turn. The ops that make one run
// while the turn waits for their answer, so the turn's end comes after.
func (m *Manager) noteTurn(a *agentState, fact endFact) {
	t := a.thread
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.flow.ends == nil {
		t.flow.ends = map[string]turnFacts{}
	}
	f := t.flow.ends[a.id]
	switch fact {
	case factDone:
		f.done = true
	case factHandoff:
		f.handoff = true
	case factBlocked:
		f.blocked = true
	}
	t.flow.ends[a.id] = f
}

// noteOp records what a task-board op that a ran without error did for its
// turn (taskBoard.TaskOp).
func (m *Manager) noteOp(a *agentState, op string, args map[string]any, err error) {
	if err != nil {
		return
	}
	str := func(k string) string { s, _ := args[k].(string); return strings.TrimSpace(s) }
	switch op {
	case "task.done":
		tk, ok := m.Task(a.thread.id, str("id"))
		switch {
		case !ok:
		case tk.Status == store.TaskDone:
			m.noteTurn(a, factDone)
		case tk.Status == store.TaskReview && tk.Reviewer != checkReviewer:
			m.noteTurn(a, factHandoff) // a reviewer has it now
		}
	case "review.approve":
		m.noteTurn(a, factDone)
	case "agent.spawn", "thread.open", "thread.post", "task.contract", "task.race", "review.changes":
		m.noteTurn(a, factHandoff)
	case "task.update":
		if strings.EqualFold(str("status"), string(store.TaskBlocked)) {
			m.noteTurn(a, factBlocked)
		}
	}
}

// turnEndOf says how a's turn, which just ended as end, ended, and starts
// the record of its next one. It runs on a's pump when the turn_done
// arrives: every event of the turn has been read, so it also weighs the
// evidence for the tasks the turn reported done (end.claims, which
// afterTurn settles) and for the one its reply finishes, which it reads the
// way settleHanded will.
func (m *Manager) turnEndOf(t *thread, a *agentState, end *turnEnd) agent.TurnEnd {
	t.mu.Lock()
	f := t.flow.ends[a.id]
	delete(t.flow.ends, a.id)
	settles := ""
	if !end.interrupted && strings.TrimSpace(end.text) != "" && t.ops == nil {
		if jobs := t.handedJobsLocked(a, end.answered); len(jobs) == 1 && !jobs[0].explicit {
			settles = jobs[0].id
		}
	}
	claims, settled := t.weighClaimsLocked(a, settles)
	end.claims, end.settles = claims, settles
	asks := !end.interrupted && t.leadLocked() == a && endsAsking(end.text)
	t.mu.Unlock()
	for _, c := range claims {
		f.done = f.done || c.ok
	}
	switch {
	case f.blocked || asks:
		return agent.TurnBlocked
	case f.handoff:
		return agent.TurnHandoff
	case f.done || settled:
		return agent.TurnDone
	}
	return agent.TurnContinue
}

// endsAsking reports whether a reply ends on a question: its last line,
// markdown emphasis aside, ends with "?".
func endsAsking(reply string) bool {
	last := strings.TrimSpace(reply)
	if i := strings.LastIndexByte(last, '\n'); i >= 0 {
		last = last[i+1:]
	}
	return strings.HasSuffix(strings.TrimRight(last, " *_`)\"'"), "?")
}
