package session

import (
	"fmt"
	"strings"
	"time"

	"github.com/dev-dami/deck/internal/deck/agent"
	"github.com/dev-dami/deck/internal/deck/store"
)

// Contract-backed handoffs. thread.send can hand a message over as a task
// (new_task, or task for one already on the board) with terms: what is
// expected back and when it counts as done. The task is tied to the relay in
// the log, the recipient's reply (or its task.done) completes it, and the
// result goes back to whoever handed it over.

// handOver puts the task a thread.send names in the recipient's hands: it
// creates the task (new_task) or takes the existing one (task), makes `to` its
// owner and records the contract. It returns the task's id.
func (m *Manager) handOver(t *thread, from, to *agentState, text string, msg agent.Message) (string, error) {
	contract := &store.Contract{Objective: msg.Objective, Expect: msg.Expect, DoneWhen: msg.DoneWhen,
		Boundaries: msg.Boundaries, Check: msg.Check, ExplicitDone: msg.ExplicitDone}
	if msg.NewTask != "" {
		// Claimed: created running with `to` as owner, so the scheduler cannot
		// hand it to someone else while the relay is still on its way.
		tk, err := m.addTask(t, TaskSpec{
			Title: msg.NewTask, Brief: text, Creator: from.id, Owner: to.id, Contract: contract, Claimed: true,
		})
		if err != nil {
			return "", err
		}
		return tk.ID, nil
	}

	t.mu.Lock()
	i := store.FindTask(t.tasks, msg.Task)
	if i < 0 {
		t.mu.Unlock()
		return "", fmt.Errorf("%w %q: task.list shows the board", store.ErrNoTask, msg.Task)
	}
	tk := &t.tasks[i]
	if tk.Status.Final() {
		t.mu.Unlock()
		return "", fmt.Errorf("%s is already %s", tk.ID, tk.Status)
	}
	tk.Owner = to.id
	if tk.Creator == "" {
		tk.Creator = from.id
	}
	if *contract != (store.Contract{}) || tk.Contract == nil {
		if contract.Check == "" {
			contract.Check = tk.CheckOf() // new terms do not drop the task's check
		}
		tk.Contract = contract
	}
	if tk.Brief == "" {
		tk.Brief = clipText(text, maxTaskText)
	}
	tk.Updated = time.Now()
	id := tk.ID
	old := tk.Run
	if old != nil && old.Member != to.id {
		tk.Run = nil // its subagent is relieved below
	} else {
		old = nil
	}
	t.mu.Unlock()
	// Claim it now, before anything is announced: a ready task that is only
	// announced would be the scheduler's to hand out.
	m.startHandedTask(t, id, to, from.id)
	m.persistMeta(t) // the brief and contract, when the status did not change
	if old != nil {
		m.retireMember(t, old.Member)
	}
	return id, nil
}

// startHandedTask marks a handed-over task running with its new owner.
func (m *Manager) startHandedTask(t *thread, taskID string, owner *agentState, by string) {
	if taskID == "" {
		return
	}
	tk, ok := m.Task(t.id, taskID)
	if !ok || tk.Status == store.TaskRunning && tk.Owner == owner.id {
		return
	}
	if tk.Status == store.TaskFailed || tk.Status == store.TaskBlocked {
		if _, err := m.moveTask(t, taskID, store.TaskReady, by, "handed over again", nil); err != nil {
			return
		}
	}
	note := "handed to " + owner.getName()
	_, _ = m.moveTask(t, taskID, store.TaskRunning, by, note, func(tk *store.Task) { tk.Owner = owner.id })
}

// handoverIndex is the log index of the relay that handed task id to member
// to, or -1. t.mu is held.
func handoverIndex(t *thread, id, to string) int {
	hs := t.index.heads
	for k := len(hs) - 1; k >= 0; k-- {
		if h := hs[k]; h.typ == store.EntryRelay && h.taskID == id && h.via != "task" && contains(h.to, to) {
			return h.i
		}
	}
	return -1
}

// settleHanded runs when member a's turn ends. A task handed to a under a
// contract, whose handover a had been briefed on when the turn began, is
// finished by the turn: its last reply becomes the result. When the contract
// wants an explicit task.done, when the reply is empty, or when one reply
// would have to cover several tasks, the task is blocked instead, so it never
// hangs unnoticed. An ops contract's task fails instead: only
// contract.result ends it, and no person waits to unblock it. While a has
// live subagents of its own nothing is settled: the task waits for their
// results, which start a's next turn.
func (m *Manager) settleHanded(t *thread, a *agentState, reply string, answered int) {
	t.mu.Lock()
	jobs := t.handedJobsLocked(a, answered)
	ops := t.ops != nil
	t.mu.Unlock()

	for _, j := range jobs {
		switch {
		case ops && m.isClosed():
			// Deck's stop cut the turn short: the task stays running, and the
			// next start fails it and its contract (failOrphans,
			// peer.Service.FailRunningOps).
		case ops:
			_, _ = m.moveTask(t, j.id, store.TaskFailed, a.id, "its agent ended its turn without a result", nil)
		case len(jobs) > 1:
			_, _ = m.moveTask(t, j.id, store.TaskBlocked, a.id, "one reply covered several tasks; report each with task.done", nil)
		case j.explicit:
			_, _ = m.moveTask(t, j.id, store.TaskBlocked, a.id, "ended its turn without reporting task.done", nil)
		case reply == "":
			_, _ = m.moveTask(t, j.id, store.TaskBlocked, a.id, "ended its turn without a result", nil)
		default:
			_, _ = m.moveTask(t, j.id, store.TaskDone, a.id, reply, nil)
		}
	}
}

// handedJob is a task the end of a turn settles (settleHanded).
type handedJob struct {
	id       string
	explicit bool
}

// handedJobsLocked are the tasks the turn of a that just ended settles: those
// handed to a under a contract, running, whose handover a had been briefed
// on. None while a waits on subagents of its own. turnend.go reads them too,
// to say how the turn ended before settleHanded runs. t.mu is held.
func (t *thread) handedJobsLocked(a *agentState, answered int) []handedJob {
	if t.waitsOnChildren(a, answered) {
		return nil
	}
	var jobs []handedJob
	for _, tk := range t.tasks {
		if tk.Owner != a.id || tk.Status != store.TaskRunning || tk.Contract == nil {
			continue
		}
		// a has not been told about it yet (still ahead of its cursor, or held
		// by a pause): this turn was about something else.
		if idx := handoverIndex(t, tk.ID, a.id); idx < 0 || idx >= answered || t.inert[idx] {
			continue
		}
		jobs = append(jobs, handedJob{tk.ID, tk.Contract.ExplicitDone})
	}
	return jobs
}

// settleBriefsLocked makes the hand-overs of task id that their recipients
// have not been given yet inert, once the task is done or cancelled: a brief
// for finished work must not start another turn (an agent that planned its
// own work on the board and finished it in the same turn would be asked to
// do it again). They stay in the log and in the next briefing. Reports to
// the task's creator (via "task") are left alone. t.mu is held.
func (t *thread) settleBriefsLocked(id string) {
	for _, h := range t.index.heads {
		if h.typ != store.EntryRelay || h.via == "task" || !strings.EqualFold(h.taskID, id) || t.inert[h.i] {
			continue
		}
		pending := false
		for _, to := range h.to {
			if a := t.member(to); a != nil && h.i >= a.cursor {
				pending = true
			}
		}
		if pending {
			t.inert[h.i] = true
		}
	}
}

// handoverNote is what a recipient is told about the task a relay hands over.
// t.mu is held.
func handoverNote(t *thread, e store.Entry) string {
	if e.TaskID == "" || e.Via == "task" {
		return ""
	}
	i := store.FindTask(t.tasks, e.TaskID)
	if i < 0 {
		return ""
	}
	tk := t.tasks[i]
	if tk.Status.Final() {
		return fmt.Sprintf("\n[deck] Task %s \"%s\" is already %s: nothing more to do for it.", tk.ID, tk.Title, tk.Status)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "\n[deck] This hands you task %s \"%s\".", tk.ID, tk.Title)
	if c := tk.Contract; c != nil {
		if c.Objective != "" && c.Objective != tk.Title {
			b.WriteString(" Objective: " + c.Objective + ".")
		}
		if c.Expect != "" {
			b.WriteString(" Expected back: " + c.Expect + ".")
		}
		if c.DoneWhen != "" {
			b.WriteString(" Done when: " + c.DoneWhen + ".")
		}
		if c.Boundaries != "" {
			b.WriteString(" Boundaries: " + c.Boundaries + ".")
		}
		if c.Check != "" {
			fmt.Fprintf(&b, " Check: run `%s` after your last change; it is done only once that passes (without a passing run, task.done sends it back).", c.Check)
		}
		if c.ExplicitDone {
			fmt.Fprintf(&b, " Report it with execute task.done {id: %q, result} when finished; a reply alone does not finish it.", tk.ID)
			return b.String()
		}
	}
	fmt.Fprintf(&b, " When you finish, your final reply is taken as the result (or report it with execute task.done {id: %q, result}).", tk.ID)
	return b.String()
}

// notifyCreator tells the member who handed a task over how it ended: the
// result, the failure or why it is blocked, as a relay entry, logged at once;
// a result wakes them when the last of what they handed out has ended, a
// failure or a block at once (batch.go). It does nothing when the
// task has no owner or creator, or when the creator made the change
// themselves. When the creator has left, its nearest live ancestor is told
// instead (a subagent's parent, and so on); with none, nobody is.
func (m *Manager) notifyCreator(t *thread, tk store.Task, by string) {
	if tk.Creator == "" || tk.Owner == "" || tk.Creator == tk.Owner || by == tk.Creator {
		return
	}
	var what string
	switch tk.Status {
	case store.TaskDone:
		what = "done"
		if tk.Result != "" {
			what += ": " + tk.Result
		}
		if tk.Run != nil && tk.Run.Branch != "" {
			what += fmt.Sprintf(" [its changes are on branch %s]", tk.Run.Branch)
		}
	case store.TaskFailed:
		what = "failed"
		if tk.Error != "" {
			what += ": " + tk.Error
		}
	case store.TaskBlocked:
		what = "blocked"
		if tk.Error != "" {
			what += ": " + tk.Error
		}
	default:
		return
	}
	batchMu.Lock()                             // the batch decision below (batch.go)
	subs := m.busySubThreads(t.id, tk.Creator) // before t.mu: it locks the sub-threads
	t.mu.Lock()
	cr := t.ancestor(tk.Creator)
	if cr == nil || cr.id == tk.Owner || t.raceOf(tk) >= 0 { // a race reports once, for all its entries
		t.mu.Unlock()
		batchMu.Unlock()
		return
	}
	text := fmt.Sprintf("task %s \"%s\" %s", tk.ID, tk.Title, clipText(what, maxEntryChars))
	if cr.id != tk.Creator {
		text += fmt.Sprintf(" [it was for %s, who has stopped]", t.name(tk.Creator))
	}
	e := store.Entry{Type: store.EntryRelay, Speaker: tk.Owner, To: []string{cr.id}, Via: "task", TaskID: tk.ID, Event: eventText(text)}
	idx := m.appendLocked(t, e)
	// Logged now, but cr is woken once for the batch (batch.go).
	line := reportLine(tk)
	wake := t.holdReportLocked(cr, heldReport{idx, line}, tk.Status != store.TaskDone, line, subs)
	t.mu.Unlock()
	batchMu.Unlock()
	m.emit(msgFor(t.id, e))
	if wake {
		_, _ = m.deliver(t, cr)
	}
}
