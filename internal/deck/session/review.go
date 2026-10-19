package session

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/dev-dami/deck/internal/deck/store"
)

// Reviews (docs/multiplayer-design.md §4): a task can ask for a reviewer of
// another kind than the agent that does it (Task.Review, or the config's
// review for tasks run by subagents). When its owner finishes, the task goes
// to review instead of done, and Deck starts a reviewer subagent with the
// task, the result and where the changes are. The reviewer answers with
// review.approve (the task is done) or review.changes (the task goes back to
// its owner with the notes). After MaxReviewChanges rounds of changes, or
// when the reviewer stops without an answer, the task is blocked for the
// user. Whoever handed the task over hears once, when it is done or blocked.

// checkReview refuses a review that names no installed kind.
func (m *Manager) checkReview(want string) error {
	switch want {
	case "", store.ReviewNone, store.ReviewOther:
		return nil
	}
	if _, ok := m.adapters[want]; !ok {
		return fmt.Errorf("session: review names no agent kind %q (kinds: %s, or other)", want, strings.Join(m.Kinds(), ", "))
	}
	return nil
}

// reviewOf is the review tk asks for: its own, else the config's for a task
// run by a Deck subagent; "" is none.
func (m *Manager) reviewOf(tk store.Task) string {
	want := tk.Review
	if want == "" && tk.Run != nil {
		want = m.config().Review
	}
	if want == store.ReviewNone {
		return ""
	}
	return want
}

// workerKind is the kind of agent that did tk. t.mu is held.
func (t *thread) workerKind(tk store.Task) string {
	if a := t.member(tk.Owner); a != nil {
		return a.kind
	}
	return tk.Kind
}

// reviewKind is the kind that reviews work done by kind worker: want when it
// names another installed kind, else the installed kind other than worker
// with the most plan room, ranked as routeKind ranks them; "" when there is
// none.
func (m *Manager) reviewKind(want, worker string) string {
	if _, ok := m.adapters[want]; ok && want != worker {
		return want
	}
	limit, now := m.routeAbovePct(), time.Now()
	best, room := "", 0.0
	for _, k := range m.Kinds() {
		if k == worker {
			continue
		}
		pct, _, ok := m.fullest(k, now)
		if !ok {
			pct = limit // after the kinds with room, before the full ones
		}
		if best == "" || pct < room {
			best, room = k, pct
		}
	}
	return best
}

// startReview starts a reviewer for task id, which is in review without one.
// When the thread has no room for another subagent, or is paused, the review
// waits: the scheduler tries again (startReviews).
func (m *Manager) startReview(t *thread, id string) {
	t.mu.Lock()
	i := store.FindTask(t.tasks, id)
	if i < 0 || t.tasks[i].Status != store.TaskReview || t.tasks[i].Reviewer != "" || t.reviewing[t.tasks[i].ID] {
		t.mu.Unlock()
		return
	}
	tk := cloneTask(t.tasks[i])
	if t.reviewing == nil {
		t.reviewing = map[string]bool{}
	}
	t.reviewing[tk.ID] = true
	worker, wkind := t.name(tk.Owner), t.workerKind(tk)
	var wt *store.Worktree
	if o := t.member(tk.Owner); o != nil {
		wt = o.getWorktree()
	}
	t.mu.Unlock()
	defer func() {
		t.mu.Lock()
		delete(t.reviewing, tk.ID)
		t.mu.Unlock()
	}()

	kind := m.reviewKind(m.reviewOf(tk), wkind)
	if kind == "" { // moveTask checked this; its owner changed since
		_, _ = m.moveTask(t, tk.ID, store.TaskBlocked, "", "no agent kind other than "+wkind+" can review it", nil)
		return
	}
	where, isolate := "Its changes are in the shared work tree: `git diff` shows them.", false
	switch {
	case wt != nil:
		where = fmt.Sprintf("Its changes are on a worktree of its own: `git -C %s diff %s` shows them.", wt.Path, wt.Base)
	case len(tk.Capsules) > 0:
		where, isolate = "Its changes are applied to your own worktree from its capsules (below).", true
	}
	brief := fmt.Sprintf("Review task %s \"%s\", which %s (%s) did. Check that the work does what the task asks, that it is correct, and that it is tested. Read the changes, and run the tests if that helps, but do not change any files.\n\nThe task: %s\n\nIts result: %s\n\n%s\n\n"+
		"Answer with execute review.approve {id: %q, note} when the work is right, or review.changes {id: %q, notes} with what must change. Your reply alone does not answer. This is review %d; after %d rounds of changes the user decides.",
		tk.ID, tk.Title, worker, wkind, or(tk.Brief, tk.Title), or(tk.Result, "(none)"), where, tk.ID, tk.ID, tk.Reviews+1, store.MaxReviewChanges)
	_, err := m.Spawn(t.id, "", SpawnSpec{Kind: kind, Title: "review " + tk.ID, Brief: brief, ReviewOf: tk.ID, Isolate: isolate})
	if err != nil && !errors.Is(err, errChildLimit) && !errors.Is(err, errPaused) {
		_, _ = m.moveTask(t, tk.ID, store.TaskBlocked, "", "could not start a reviewer: "+err.Error(), nil)
	}
}

// startReviews starts the reviewers that wait for room.
func (m *Manager) startReviews(t *thread) {
	var ids []string
	t.mu.Lock()
	for _, tk := range t.tasks {
		if tk.Status == store.TaskReview && tk.Reviewer == "" {
			ids = append(ids, tk.ID)
		}
	}
	t.mu.Unlock()
	for _, id := range ids {
		m.startReview(t, id)
	}
}

// bindReviewer records a as the reviewer of task id. It fails when the task
// left review while a started.
func (m *Manager) bindReviewer(t *thread, id string, a *agentState) error {
	t.mu.Lock()
	i := store.FindTask(t.tasks, id)
	if i < 0 || t.tasks[i].Status != store.TaskReview {
		t.mu.Unlock()
		return fmt.Errorf("session: %s is no longer in review", id)
	}
	tk := &t.tasks[i]
	tk.Reviewer, tk.Updated = a.id, time.Now()
	c := m.logTaskLocked(t, *tk, tk.Status, "", "reviewed by "+a.getName())
	t.mu.Unlock()
	m.announce(t, []taskChange{c})
	return nil
}

// answerReview runs review.approve and review.changes for member a, which
// must be the task's reviewer. Approved, the task is done with the review
// added to its result. Sent back, it runs again with its owner, who gets the
// notes; when that owner has stopped, a new subagent of its kind takes over
// its worktree.
func (m *Manager) answerReview(a *agentState, id string, approve bool, text string) (string, error) {
	t := a.thread
	text = strings.TrimSpace(text)
	t.mu.Lock()
	i := store.FindTask(t.tasks, id)
	if i < 0 {
		t.mu.Unlock()
		return "", store.ErrNoTask
	}
	tk := cloneTask(t.tasks[i])
	if tk.Status != store.TaskReview || tk.Reviewer != a.id {
		t.mu.Unlock()
		return "", fmt.Errorf("you are not reviewing %s: only its reviewer answers its review", tk.ID)
	}
	worker, wkind, wname := t.member(tk.Owner), t.workerKind(tk), t.name(tk.Owner)
	t.mu.Unlock()
	me := a.getName()
	counted := func(tk *store.Task) { tk.Reviews++ }

	switch {
	case approve:
		note := tk.Result + "\nreview by " + me + ": approved"
		if text != "" {
			note += ": " + text
		}
		if _, err := m.moveTask(t, tk.ID, store.TaskDone, a.id, note, counted); err != nil {
			return "", err
		}
		return fmt.Sprintf("approved %s: it is done", tk.ID), nil
	case text == "":
		return "", errors.New("say what must change (notes)")
	case tk.Reviews >= store.MaxReviewChanges:
		why := fmt.Sprintf("review did not converge: after %d rounds of changes, %s still asks for changes: %s", tk.Reviews, me, text)
		if _, err := m.moveTask(t, tk.ID, store.TaskBlocked, a.id, why, counted); err != nil {
			return "", err
		}
		return fmt.Sprintf("%s is blocked: after %d rounds of changes the user decides", tk.ID, tk.Reviews), nil
	}

	notes := fmt.Sprintf("The review of task %s asks for changes (round %d of %d):\n%s", tk.ID, tk.Reviews+1, store.MaxReviewChanges, text)
	if worker != nil && worker.alive() {
		// The notes go into the log under the same lock as the move, so the
		// owner's turn cannot settle the task before it has read them.
		e := store.Entry{Type: store.EntryRelay, ID: store.NewID(), Speaker: a.id, To: []string{worker.id}, Via: "review", TaskID: tk.ID, Event: eventText(notes)}
		_, err := m.moveTask(t, tk.ID, store.TaskRunning, a.id, "changes asked by "+me, func(tk *store.Task) {
			tk.Reviews++
			if tk.Contract == nil {
				tk.Contract = &store.Contract{} // its next reply settles it (settleHanded)
			}
			m.appendLocked(t, e)
		})
		if err != nil {
			return "", err
		}
		m.emit(msgFor(t.id, e))
		_, _ = m.deliver(t, worker)
		return fmt.Sprintf("sent your notes to %s; its next result comes back for review", worker.getName()), nil
	}

	// Its owner has stopped: a new subagent of its kind takes over its work.
	run := tk.Run
	switch {
	case run == nil:
		return m.blockReview(t, a, tk.ID, fmt.Sprintf("changes asked, but %s has stopped: %s", wname, text))
	case run.Branch != "" && (worker == nil || worker.getWorktree() == nil):
		return m.blockReview(t, a, tk.ID, "changes asked, but the worktree of its work is gone: "+text)
	}
	parent := ""
	if worker != nil {
		t.mu.Lock()
		if p := t.member(worker.parentID()); p != nil && p.alive() {
			parent = p.id
		}
		t.mu.Unlock()
	}
	if _, err := m.moveTask(t, tk.ID, store.TaskRunning, a.id, "changes asked by "+me, counted); err != nil {
		return "", err
	}
	brief := or(tk.Brief, tk.Title) + fmt.Sprintf("\n\n[deck] An agent did this task and stopped; you carry on with its work. Its result was: %s\n\n%s", or(tk.Result, "(none)"), notes)
	res, err := m.Spawn(t.id, parent, SpawnSpec{TaskID: tk.ID, Kind: wkind, Brief: brief, Adopt: run.Member})
	if err != nil {
		if cur, ok := m.Task(t.id, tk.ID); ok && cur.Status == store.TaskRunning {
			_, _ = m.moveTask(t, tk.ID, store.TaskBlocked, a.id, "changes asked, but a new agent could not start: "+err.Error(), nil)
		}
		return fmt.Sprintf("%s is blocked: its owner stopped and a new one could not start (%v)", tk.ID, err), nil
	}
	return fmt.Sprintf("its owner had stopped: %s takes over with your notes; its next result comes back for review", res.AgentID), nil
}

// blockReview blocks task id from a review that cannot go back to its owner.
func (m *Manager) blockReview(t *thread, a *agentState, id, why string) (string, error) {
	if _, err := m.moveTask(t, id, store.TaskBlocked, a.id, why, func(tk *store.Task) { tk.Reviews++ }); err != nil {
		return "", err
	}
	return id + " is blocked: " + why, nil
}

// reviewerGone runs when member a stops. When a was a reviewer, the review it
// did not answer is blocked, and the worktree it had for it is dropped.
func (m *Manager) reviewerGone(t *thread, a *agentState) {
	a.mu.Lock()
	id, wt := a.reviews, a.worktree
	if id != "" {
		a.worktree = nil
	}
	a.mu.Unlock()
	if id == "" {
		return
	}
	if wt != nil {
		m.dropWorktree(wt)
		m.persistMeta(t)
	}
	if tk, ok := m.Task(t.id, id); ok && tk.Status == store.TaskReview && tk.Reviewer == a.id {
		_, _ = m.moveTask(t, tk.ID, store.TaskBlocked, "", a.getName()+" stopped before it answered the review", nil)
	}
}
