package session

import (
	"fmt"
	"strings"
	"sync"

	"github.com/dev-dami/deck/internal/deck/store"
)

// Report-backs in batches. A member that hands work out (tasks to its
// subagents or to other members, asks to its sub-threads) hears how each
// piece ended as a relay, logged and shown the moment it comes in. But it is
// woken once, when the last of what it handed out has ended, so it reads the
// results together instead of re-planning after each one (Anthropic, "How we
// built our multi-agent research system", 2025). A failure or a block wakes
// it at once, with the rest still running named, so it can step in.
//
// A report that waits is held: an inert relay, which starts no turn but is
// in the member's next briefing, so a turn that starts for another reason
// still reads it. Nothing is released while the member is in a turn of its
// own either: it may still be handing work out, and the end of its turn looks
// again (afterTurn). Releasing makes the held relays live and delivers them
// as one prompt; with several, a line ahead of them lists what came back.

// batchMu serialises the decisions to hold or release reports, with what
// they read of other threads: two sub-threads that report at once, each on
// its own relay worker, must not both see the other still working and both
// hold. It is taken before any thread's mu, and never held while delivering.
var batchMu sync.Mutex

// heldReport is a report relay that has not woken its member yet: its log
// index and how the batch line names it (`t2 "lint" done`).
type heldReport struct {
	idx  int
	line string
}

// handedOutLocked lists the tasks cr still waits on: those it handed to
// someone else (or a member below it that has stopped did) that are running
// or in review. A task that is ready but not started, or blocked, is not
// being worked on, so it does not hold a batch back. t.mu is held.
func (t *thread) handedOutLocked(cr *agentState) []string {
	var out []string
	for _, tk := range t.tasks {
		if tk.Creator == "" || tk.Owner == cr.id || tk.Status != store.TaskRunning && tk.Status != store.TaskReview {
			continue
		}
		if t.ancestor(tk.Creator) == cr {
			out = append(out, tk.ID)
		}
	}
	return out
}

// busySubThreads counts the sub-threads of thread parentID that member
// agentID opened and that are still working on an ask of it: one waits, the
// sub-thread is not paused, and its lead has not read it yet or has a turn
// that has begun and not been settled (stallState.begun). That covers the
// moment between the end of the lead's turn and its report, when the lead is
// idle but the answer is on its way. No thread's mu may be held: it locks
// each sub-thread.
func (m *Manager) busySubThreads(parentID, agentID string) int {
	if agentID == "" {
		return 0
	}
	m.mu.Lock()
	ts := append([]*thread(nil), m.torder...)
	m.mu.Unlock()
	n := 0
	for _, t := range ts {
		if t.gone.Load() {
			continue
		}
		t.mu.Lock()
		if t.parent == parentID && t.parentAgent == agentID && !t.paused {
			if lead := t.leadLocked(); lead != nil && t.workingOnAsk(lead) {
				n++
			}
		}
		t.mu.Unlock()
	}
	return n
}

// workingOnAsk reports whether lead, sub-thread t's lead, has an ask waiting
// that it has not read yet, or is in (or just ending) a turn while one waits.
// t.mu is held.
func (t *thread) workingOnAsk(lead *agentState) bool {
	if len(t.asks) == 0 {
		return false
	}
	if len(t.stall.begun[lead.id]) > 0 {
		return true
	}
	for _, id := range t.asks {
		if i, ok := t.index.at[id]; ok && i >= lead.cursor {
			return true
		}
	}
	return false
}

// holdReportLocked decides about report r to cr, just logged: it is held
// while cr is in a turn or still waits on other work (subs is how many of
// cr's sub-threads are busy, counted before t.mu was taken), unless urgent
// (a failure or a block). It reports whether cr is to be woken now, in which
// case every report held for cr is released with it. t.mu is held.
func (t *thread) holdReportLocked(cr *agentState, r heldReport, urgent bool, why string, subs int) bool {
	if t.held == nil {
		t.held = map[string][]heldReport{}
	}
	t.held[cr.id] = append(t.held[cr.id], r)
	running := t.handedOutLocked(cr)
	if !urgent && (cr.busy() || subs > 0 || len(running) > 0) {
		t.inert[r.idx] = true
		return false
	}
	if !urgent {
		why = ""
	}
	t.releaseLocked(cr, why, running, subs)
	return true
}

// releaseHeldLocked releases cr's held reports when nothing it handed out is
// still being worked on and it is not in a turn, and reports whether it did:
// the caller then delivers to cr. subs is as in holdReportLocked. t.mu is
// held.
func (t *thread) releaseHeldLocked(cr *agentState, subs int) bool {
	if cr == nil || len(t.held[cr.id]) == 0 || subs > 0 || cr.busy() || len(t.handedOutLocked(cr)) > 0 {
		return false
	}
	t.releaseLocked(cr, "", nil, 0)
	return true
}

// releaseLocked makes cr's held reports live. With more than one still
// unread, or with work still running (an urgent release), cr's next prompt
// starts with a line that lists them. t.mu is held.
func (t *thread) releaseLocked(cr *agentState, why string, running []string, subs int) {
	var lines []string
	for _, r := range t.held[cr.id] {
		delete(t.inert, r.idx)
		if r.idx >= cr.cursor { // one a turn has read already is not news
			lines = append(lines, r.line)
		}
	}
	delete(t.held, cr.id)
	var head string
	switch {
	case why != "" && (len(running) > 0 || subs > 0):
		head = fmt.Sprintf("[deck] %s, so you hear now; still running: %s.", why, stillRunning(running, subs))
		if len(lines) > 1 {
			head += fmt.Sprintf(" The %d reports so far follow: %s.", len(lines), strings.Join(lines, ", "))
		}
	case len(lines) > 1:
		head = fmt.Sprintf("[deck] Everything you handed out has ended; the %d reports follow: %s.", len(lines), strings.Join(lines, ", "))
	default:
		return
	}
	cr.mu.Lock()
	cr.nudge = strings.TrimSpace(cr.nudge + "\n" + head)
	cr.mu.Unlock()
}

// stillRunning names the work a release leaves running.
func stillRunning(tasks []string, subs int) string {
	out := append([]string(nil), tasks...)
	if subs > 0 {
		out = append(out, plural(subs, "sub-thread"))
	}
	return strings.Join(out, ", ")
}

// reportLine is how a batch line names the report of task tk.
func reportLine(tk store.Task) string {
	return fmt.Sprintf("%s %q %s", tk.ID, clip(tk.Title, 60), tk.Status)
}

// releaseHeld makes a's held reports live when its turn has ended and
// nothing it handed out is still being worked on; the end of the turn
// (afterTurn) then delivers them. It runs before the turn is settled, so a
// task that waits on those results (waitsOnChildren) sees them.
func (m *Manager) releaseHeld(t *thread, a *agentState) {
	batchMu.Lock()
	defer batchMu.Unlock()
	subs := m.busySubThreads(t.id, a.id)
	t.mu.Lock()
	t.releaseHeldLocked(a, subs)
	t.mu.Unlock()
}

// flushReports wakes the member that task tk's report goes to when tk's
// move left nothing it handed out in flight: a cancelled task, or one its
// creator settled itself, sends no report of its own to release the others.
func (m *Manager) flushReports(t *thread, tk store.Task) {
	if tk.Creator == "" || tk.Status == store.TaskRunning || tk.Status == store.TaskReview {
		return
	}
	t.mu.Lock()
	cr := t.ancestor(tk.Creator)
	pending := cr != nil && len(t.held[cr.id]) > 0
	t.mu.Unlock()
	if !pending {
		return
	}
	m.flushMember(t, cr)
}

// flushMember wakes cr for its held reports when nothing it handed out is in
// flight and it is not in a turn.
func (m *Manager) flushMember(t *thread, cr *agentState) {
	batchMu.Lock()
	subs := m.busySubThreads(t.id, cr.id)
	t.mu.Lock()
	ok := t.releaseHeldLocked(cr, subs)
	t.mu.Unlock()
	batchMu.Unlock()
	if ok {
		_, _ = m.deliver(t, cr)
	}
}

// flushParent runs when a member of sub-thread t ends a turn: when it is t's
// lead, the parent agent may have been waiting on it alone, without a report
// coming (an interrupted turn, or one about the person's own message).
func (m *Manager) flushParent(t *thread, a *agentState) {
	t.mu.Lock()
	parentID, agentID := t.parent, t.parentAgent
	lead := t.leadLocked() == a
	t.mu.Unlock()
	if parentID == "" || agentID == "" || !lead {
		return
	}
	p := m.thread(parentID)
	if p == nil {
		return
	}
	p.mu.Lock()
	to := p.member(agentID)
	pending := to != nil && len(p.held[to.id]) > 0
	p.mu.Unlock()
	if pending {
		m.flushMember(p, to)
	}
}
