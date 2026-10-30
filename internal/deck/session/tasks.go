package session

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/dev-dami/deck/internal/deck/store"
)

// The thread's shared task board (store/task.go has the types). Every change
// goes through moveTask / addTask: they validate, record the change in the
// log, save the thread, tell the UI and then let the scheduler look at the
// board again.

// maxTaskText caps a task's brief, result and error so the board stays small
// in the thread's metadata.
const maxTaskText = 4000

// TaskChangedMsg says a task was created or changed. From is "" for a new task.
type TaskChangedMsg struct {
	ThreadID string
	Task     store.Task
	From     store.TaskStatus
	// By is the member who made the change; "" for the user or Deck.
	By string
}

// TaskSpec describes a task to create.
type TaskSpec struct {
	Title       string
	Brief       string
	Creator     string // member id; "" is the user
	Owner       string // member id; "" is nobody yet
	Parent      string
	Deps        []string
	OnFail      string
	MaxAttempts int
	Isolate     bool
	Contract    *store.Contract
	// Kind, Model and Effort pick the agent its subagent runs (store.Task).
	Kind, Model, Effort string
	// Budget is what it may spend (brakes.go); nil is the config's.
	Budget *store.Budget
	// Race and Judge make it a race (race.go).
	Race  bool
	Judge string
	// Review is who reviews it before it is done (review.go).
	Review string
	// Remote is the teammate that does it under a code contract
	// (contract.go).
	Remote string
	// Claimed creates the task already running (its first attempt under way),
	// so the scheduler never hands out a task its creator is about to work on
	// or hand over itself.
	Claimed bool
	// spawn is the claim of the Spawn that creates the task for its subagent
	// (spawn.go).
	spawn string
}

// TaskPatch is a change to a task's description; nil fields stay as they are.
type TaskPatch struct {
	Title *string
	Brief *string
	Owner *string
	Deps  *[]string
	// Budget replaces the task's budget; only its creator (or the user) may.
	Budget *store.Budget
}

func cloneTask(t store.Task) store.Task {
	t.Deps = append([]string(nil), t.Deps...)
	t.Capsules = append([]string(nil), t.Capsules...)
	if t.Contract != nil {
		c := *t.Contract
		t.Contract = &c
	}
	if t.Run != nil {
		r := *t.Run
		t.Run = &r
	}
	if t.Diff != nil {
		d := *t.Diff
		d.Files = append([]string(nil), d.Files...)
		t.Diff = &d
	}
	return t
}

func cloneTasks(ts []store.Task) []store.Task {
	if len(ts) == 0 {
		return nil
	}
	out := make([]store.Task, len(ts))
	for i, t := range ts {
		out[i] = cloneTask(t)
	}
	return out
}

func clipText(s string, n int) string {
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}

// Tasks returns a copy of the thread's task board, in creation order.
func (m *Manager) Tasks(threadID string) []store.Task {
	t := m.thread(threadID)
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return cloneTasks(t.tasks)
}

// Task returns one task of the thread.
func (m *Manager) Task(threadID, id string) (store.Task, bool) {
	t := m.thread(threadID)
	if t == nil {
		return store.Task{}, false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if i := store.FindTask(t.tasks, id); i >= 0 {
		return cloneTask(t.tasks[i]), true
	}
	return store.Task{}, false
}

// taskChange is one change made under t.mu, to be announced after it.
type taskChange struct {
	task store.Task
	from store.TaskStatus
	by   string
	msg  tea.Msg
}

// logTaskLocked records a change in the log and returns what to announce.
// t.mu is held.
func (m *Manager) logTaskLocked(t *thread, task store.Task, from store.TaskStatus, by, note string) taskChange {
	ev := &store.TaskEvent{ID: task.ID, From: from, To: task.Status, Owner: task.Owner, Note: clipText(note, 300)}
	if from == "" {
		ev.Title = task.Title
	}
	e := store.Entry{Type: store.EntryTask, ID: store.NewID(), Speaker: by, Task: ev}
	m.appendLocked(t, e)
	return taskChange{task: cloneTask(task), from: from, by: by}
}

// announce tells the UI about changes, saves the thread and runs the
// scheduler. t.mu must not be held.
func (m *Manager) announce(t *thread, changes []taskChange) {
	if len(changes) == 0 {
		return
	}
	m.persistMeta(t)
	for _, c := range changes {
		m.emit(TaskChangedMsg{ThreadID: t.id, Task: c.task, From: c.from, By: c.by})
	}
	m.kick(t)
}

// depSatisfied reports whether dependency d lets what waits on it go: it is
// done, or it failed for good and its OnFail says to continue.
func depSatisfied(d store.Task) bool {
	return d.Status == store.TaskDone || d.Status == store.TaskFailed && d.FailPolicy() == store.OnFailContinue && exhausted(d)
}

// refreshReadyLocked keeps the board's derived states honest: a blocked task
// whose blocker is no longer failed or cancelled goes back to pending, a
// pending task whose dependencies are all satisfied becomes ready, and a ready
// one that lost a dependency goes back to pending. Dependencies that no longer
// exist count as satisfied. t.mu is held.
func (m *Manager) refreshReadyLocked(t *thread) []taskChange {
	var out []taskChange
	move := func(tk *store.Task, to store.TaskStatus) {
		from := tk.Status
		tk.Status = to
		tk.Updated = time.Now()
		out = append(out, m.logTaskLocked(t, *tk, from, "", ""))
	}
	for i := range t.tasks {
		tk := &t.tasks[i]
		if tk.Status == store.TaskBlocked && tk.BlockedBy != "" {
			if j := store.FindTask(t.tasks, tk.BlockedBy); j < 0 || t.tasks[j].Status != store.TaskFailed && t.tasks[j].Status != store.TaskCancelled {
				tk.BlockedBy, tk.Error = "", ""
				move(tk, store.TaskPending)
			}
		}
		if tk.Status != store.TaskPending && tk.Status != store.TaskReady {
			continue
		}
		ok := true
		for _, d := range tk.Deps {
			if j := store.FindTask(t.tasks, d); j >= 0 && !depSatisfied(t.tasks[j]) {
				ok = false
				break
			}
		}
		switch {
		case ok && tk.Status == store.TaskPending:
			move(tk, store.TaskReady)
		case !ok && tk.Status == store.TaskReady:
			move(tk, store.TaskPending)
		}
	}
	return out
}

// CreateTask adds a task to the thread's board.
func (m *Manager) CreateTask(threadID string, sp TaskSpec) (store.Task, error) {
	t := m.thread(threadID)
	if t == nil {
		return store.Task{}, fmt.Errorf("session: no thread %q", threadID)
	}
	return m.addTask(t, sp)
}

func (m *Manager) addTask(t *thread, sp TaskSpec) (store.Task, error) {
	title := oneLine(sp.Title)
	if title == "" {
		return store.Task{}, store.ErrTaskEmptyTitle
	}
	t.mu.Lock()
	id := store.NextTaskID(t.tasks)
	if sp.Kind != "" {
		if _, ok := m.adapters[sp.Kind]; !ok {
			t.mu.Unlock()
			return store.Task{}, fmt.Errorf("session: unknown agent kind %q", sp.Kind)
		}
	}
	if err := m.checkReview(sp.Review); err != nil {
		t.mu.Unlock()
		return store.Task{}, err
	}
	if sp.Parent != "" {
		if store.FindTask(t.tasks, sp.Parent) < 0 {
			t.mu.Unlock()
			return store.Task{}, fmt.Errorf("%w %q", store.ErrTaskUnknownPart, sp.Parent)
		}
	}
	deps := canonicalIDs(t.tasks, sp.Deps)
	if err := store.ValidateDeps(t.tasks, id, deps); err != nil {
		t.mu.Unlock()
		return store.Task{}, err
	}
	now := time.Now()
	task := store.Task{
		ID: id, Title: clipText(title, 120), Brief: clipText(sp.Brief, maxTaskText),
		Creator: sp.Creator, Owner: sp.Owner, Parent: canonicalID(t.tasks, sp.Parent), Deps: deps,
		Status: store.TaskPending, OnFail: sp.OnFail, MaxAttempts: sp.MaxAttempts, Contract: sp.Contract, Isolate: sp.Isolate,
		Kind: sp.Kind, Model: sp.Model, Effort: sp.Effort, Budget: sp.Budget, Race: sp.Race, Judge: sp.Judge, Remote: sp.Remote,
		Review: sp.Review, Created: now, Updated: now,
	}
	if sp.Claimed {
		task.Status, task.Attempts, task.Started = store.TaskRunning, 1, now
		if sp.spawn != "" {
			t.claimLocked(id, sp.spawn)
		}
	}
	t.tasks = append(t.tasks, task)
	changes := []taskChange{m.logTaskLocked(t, task, "", sp.Creator, "")}
	changes = append(changes, m.refreshReadyLocked(t)...)
	out := cloneTask(t.tasks[len(t.tasks)-1])
	t.mu.Unlock()
	m.announce(t, changes)
	return out, nil
}

// canonicalID is the board's spelling of id (so "T1" and "t1" are one task);
// id itself when there is no such task.
func canonicalID(tasks []store.Task, id string) string {
	if i := store.FindTask(tasks, id); i >= 0 {
		return tasks[i].ID
	}
	return strings.TrimSpace(id)
}

func canonicalIDs(tasks []store.Task, ids []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, id := range ids {
		if strings.TrimSpace(id) == "" {
			continue
		}
		c := canonicalID(tasks, id)
		if !seen[c] {
			seen[c] = true
			out = append(out, c)
		}
	}
	return out
}

// UpdateTask changes a task's title, brief, owner or dependencies.
func (m *Manager) UpdateTask(threadID, id, by string, p TaskPatch) (store.Task, error) {
	t := m.thread(threadID)
	if t == nil {
		return store.Task{}, fmt.Errorf("session: no thread %q", threadID)
	}
	t.mu.Lock()
	i := store.FindTask(t.tasks, id)
	if i < 0 {
		t.mu.Unlock()
		return store.Task{}, store.ErrNoTask
	}
	tk := &t.tasks[i]
	if tk.Status.Final() {
		t.mu.Unlock()
		return store.Task{}, fmt.Errorf("%s is %s", tk.ID, tk.Status)
	}
	if p.Deps != nil {
		deps := canonicalIDs(t.tasks, *p.Deps)
		if err := store.ValidateDeps(t.tasks, tk.ID, deps); err != nil {
			t.mu.Unlock()
			return store.Task{}, err
		}
		tk.Deps = deps
	}
	if p.Title != nil {
		title := oneLine(*p.Title)
		if title == "" {
			t.mu.Unlock()
			return store.Task{}, store.ErrTaskEmptyTitle
		}
		tk.Title = clipText(title, 120)
	}
	if p.Brief != nil {
		tk.Brief = clipText(*p.Brief, maxTaskText)
	}
	if p.Owner != nil {
		tk.Owner = *p.Owner
	}
	if p.Budget != nil {
		if by != "" && by != tk.Creator {
			t.mu.Unlock()
			return store.Task{}, fmt.Errorf("only whoever created %s can change its budget", tk.ID)
		}
		b := *p.Budget
		tk.Budget = &b
	}
	tk.Updated = time.Now()
	changes := []taskChange{m.logTaskLocked(t, *tk, tk.Status, by, "updated")}
	changes = append(changes, m.refreshReadyLocked(t)...)
	out := cloneTask(t.tasks[i])
	t.mu.Unlock()
	m.announce(t, changes)
	return out, nil
}

// moveTask moves a task to status to, applying mutate (which may be nil) under
// the thread's lock. note is the result, error or reason that goes with it.
func (m *Manager) moveTask(t *thread, id string, to store.TaskStatus, by, note string, mutate func(*store.Task)) (store.Task, error) {
	return m.moveTaskIf(t, id, to, by, note, nil, mutate)
}

// moveTaskIf is moveTask when check (which may be nil) accepts the task. check
// runs under the same lock as the move, so nothing changes the task between
// the two.
func (m *Manager) moveTaskIf(t *thread, id string, to store.TaskStatus, by, note string, check func(store.Task) error, mutate func(*store.Task)) (store.Task, error) {
	if !to.Valid() {
		return store.Task{}, fmt.Errorf("%q is not a task status", to)
	}
	t.mu.Lock()
	i := store.FindTask(t.tasks, id)
	if i < 0 {
		t.mu.Unlock()
		return store.Task{}, store.ErrNoTask
	}
	tk := &t.tasks[i]
	if check != nil {
		if err := check(*tk); err != nil {
			t.mu.Unlock()
			return store.Task{}, err
		}
	}
	from := tk.Status
	if !store.CanTransition(from, to) {
		t.mu.Unlock()
		return store.Task{}, fmt.Errorf("%w: %s is %s, not %s", store.ErrTaskTransition, tk.ID, from, to)
	}
	// Work that ends goes to review first when the task asks for one.
	unreviewed := ""
	if to == store.TaskDone && from == store.TaskRunning {
		switch want, worker := m.reviewOf(*tk, m.levelLocked(t)), t.workerKind(*tk); {
		case want == "":
		case m.reviewKind(want, worker) != "":
			to = store.TaskReview
		default:
			unreviewed = fmt.Sprintf("%s was not reviewed: no agent kind other than %s is installed", tk.ID, worker)
			note = strings.TrimSpace(note + "\n[" + unreviewed + "]")
		}
	}
	reviewer := ""
	if to != store.TaskReview {
		reviewer, tk.Reviewer = tk.Reviewer, ""
		delete(t.reviewWaits, tk.ID) // a later review that waits gets a new notice
	}
	if to != store.TaskRunning {
		delete(t.spawns, tk.ID) // a subagent still starting for it is not bound (bindRun)
	}
	var run *store.RunRef
	if tk.Run != nil {
		r := *tk.Run
		run = &r
	}
	var kids []string
	if to == store.TaskCancelled {
		for _, o := range t.tasks {
			if strings.EqualFold(o.Parent, tk.ID) && !o.Status.Final() {
				kids = append(kids, o.ID)
			}
		}
	}
	if mutate != nil {
		mutate(tk)
	}
	tk.Status = to
	tk.Updated = time.Now()
	if to == store.TaskRunning && from != store.TaskRunning && from != store.TaskReview { // a review's changes are the same attempt
		tk.Attempts++
		tk.Started, tk.Spent = time.Now(), store.Spend{} // each attempt has the whole budget
	}
	switch to {
	case store.TaskDone, store.TaskReview:
		tk.Result, tk.Error = clipText(note, maxTaskText), ""
	case store.TaskFailed, store.TaskBlocked:
		tk.Error = clipText(note, maxTaskText)
	case store.TaskCancelled:
		tk.Run = nil
	}
	// What the scheduler noted about a failure or a block is for that state
	// only: left behind, it would send a task round again (BlockedBy) or
	// skip its backoff (RetryAt).
	if to != store.TaskBlocked {
		tk.BlockedBy = ""
	}
	if to != store.TaskFailed {
		tk.RetryAt = time.Time{}
	}
	if to.Final() {
		t.settleBriefsLocked(tk.ID)
	}
	changes := []taskChange{m.logTaskLocked(t, *tk, from, by, note)}
	changes = append(changes, m.refreshReadyLocked(t)...)
	out := cloneTask(t.tasks[i])
	t.mu.Unlock()
	m.announce(t, changes)
	if unreviewed != "" {
		m.notice(t, unreviewed)
	}
	if to == store.TaskDone || to == store.TaskFailed || to == store.TaskBlocked {
		m.notifyCreator(t, out, by)
	}
	// A reviewer that did not make this move itself is done with the task.
	if reviewer != "" && reviewer != by {
		m.retireMember(t, reviewer)
	}
	if to == store.TaskReview && from != store.TaskReview {
		m.startReview(t, out.ID)
	}
	// A subagent whose task ended is stopped at once if it is idle; a busy one
	// (it reported through task.done mid-turn) is stopped when its turn ends.
	if run != nil && (to == store.TaskCancelled || (to == store.TaskDone || to == store.TaskFailed) && m.memberIdle(t, run.Member)) {
		m.retireMember(t, run.Member)
	}
	for _, id := range kids {
		_, _ = m.moveTask(t, id, store.TaskCancelled, by, "its parent task was cancelled", nil)
	}
	m.raceStep(t, out)
	if from != to {
		m.taskEnded(t, out)
	}
	return out, nil
}

// SetTaskStatus moves a task to any status its current one allows.
func (m *Manager) SetTaskStatus(threadID, id string, to store.TaskStatus, by, note string) (store.Task, error) {
	t := m.thread(threadID)
	if t == nil {
		return store.Task{}, fmt.Errorf("session: no thread %q", threadID)
	}
	return m.moveTask(t, id, to, by, note, nil)
}

// CompleteTask marks a task done with its result.
func (m *Manager) CompleteTask(threadID, id, by, result string) (store.Task, error) {
	return m.SetTaskStatus(threadID, id, store.TaskDone, by, result)
}

// FailTask marks a task failed, with why.
func (m *Manager) FailTask(threadID, id, by, reason string) (store.Task, error) {
	return m.SetTaskStatus(threadID, id, store.TaskFailed, by, reason)
}

// CancelTask abandons a task.
func (m *Manager) CancelTask(threadID, id, by string) (store.Task, error) {
	return m.SetTaskStatus(threadID, id, store.TaskCancelled, by, "cancelled")
}

// errTaskNotYours is what a member gets for finishing a task someone else owns.
var errTaskNotYours = errors.New("that task belongs to someone else")

// ---- the task ops agents call (hub.TaskBoard) ----

// taskBoard is the thread's board as one agent's task ops see it.
type taskBoard struct {
	m *Manager
	a *agentState
}

// TaskOp runs one of the task.* ops for the calling agent.
func (b taskBoard) TaskOp(op string, a map[string]any) (string, error) {
	m, t, me := b.m, b.a.thread, b.a.id
	str := func(k string) string { s, _ := a[k].(string); return strings.TrimSpace(s) }
	num := func(k string) int { n, _ := a[k].(int); return n }
	switch op {
	case "task.create":
		sp := TaskSpec{
			Title: str("title"), Brief: str("brief"), Creator: me, Parent: str("parent"),
			Deps: splitIDs(str("deps")), OnFail: normOnFail(str("on_fail")), MaxAttempts: num("max_attempts"),
			Isolate: a["isolate"] == true, Kind: strings.ToLower(str("kind")), Model: str("model"), Effort: str("effort"),
			Budget: budgetArgs(a), Review: strings.ToLower(str("review")),
		}
		if o := str("owner"); o != "" {
			id, err := b.resolveOwner(o)
			if err != nil {
				return "", err
			}
			sp.Owner = id
		}
		tk, err := m.addTask(t, sp)
		if err != nil {
			return "", err
		}
		return "created " + b.line(tk), nil
	case "task.list":
		return b.list(str("status")), nil
	case "task.update":
		id := str("id")
		var p TaskPatch
		if _, ok := a["title"]; ok {
			s := str("title")
			p.Title = &s
		}
		if _, ok := a["brief"]; ok {
			s := str("brief")
			p.Brief = &s
		}
		if _, ok := a["deps"]; ok {
			d := splitIDs(str("deps"))
			p.Deps = &d
		}
		if o, ok := a["owner"]; ok {
			s, _ := o.(string)
			owner := ""
			if strings.TrimSpace(s) != "" {
				id, err := b.resolveOwner(s)
				if err != nil {
					return "", err
				}
				owner = id
			}
			p.Owner = &owner
		}
		p.Budget = budgetArgs(a)
		var tk store.Task
		var err error
		if p != (TaskPatch{}) {
			tk, err = m.UpdateTask(t.id, id, me, p)
			if err != nil {
				return "", err
			}
		}
		if st := str("status"); st != "" {
			to := store.TaskStatus(strings.ToLower(st))
			switch to {
			case store.TaskPending, store.TaskReady, store.TaskRunning, store.TaskBlocked:
			default:
				return "", fmt.Errorf("agents set a task to pending, ready, running or blocked; use task.done, task.fail or task.cancel for the rest")
			}
			tk, err = m.moveTask(t, id, to, me, str("note"), func(tk *store.Task) {
				if to == store.TaskRunning && tk.Owner == "" {
					tk.Owner = me // starting a task claims it
				}
			})
			if err != nil {
				return "", err
			}
		}
		if tk.ID == "" {
			return "", errors.New("nothing to change: give title, brief, deps, owner or status")
		}
		return "updated " + b.line(tk), nil
	case "thread.roster":
		return m.roster(b.a), nil
	case "thread.open":
		id, err := m.openSubThread(t, b.a, str("title"), str("brief"), str("kind"), str("model"), str("effort"))
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("opened sub-thread %s; its agent's reply to your brief comes back to you, and the user can talk to it there", id), nil
	case "thread.post":
		if err := m.postToSubThread(t, b.a, str("thread"), str("message")); err != nil {
			return "", err
		}
		return "sent; its agent's reply comes back to you", nil
	case "thread.children":
		return m.childrenText(t, b.a), nil
	case "lease.acquire":
		return m.acquireLease(b.a, str("paths"), num("minutes"), a["exclusive"] == true, str("reason"))
	case "lease.release":
		return m.releaseLeases(b.a, str("paths")), nil
	case "lease.list":
		return m.listLeases(b.a), nil
	case "file.claim":
		return m.claimFile(b.a, str("paths"))
	case "file.answer":
		return m.answerAsks(b.a, str("path"), str("text"), a["wait"] == true)
	case "memory.add":
		return m.addNote(b.a, str("text"), str("paths"), str("lines"))
	case "memory.read":
		return m.readNotes(b.a, str("path"))
	case "memory.list":
		return m.listNotes(b.a)
	case "memory.drop":
		return m.dropNote(b.a, str("id"))
	case "team.post":
		return m.teamPost(b.a, str("to"), str("text"))
	case "task.steer":
		if err := m.SteerTask(t.id, str("id"), me, str("message")); err != nil {
			return "", err
		}
		return "steered " + str("id"), nil
	case "agent.spawn":
		isolate, tree := spawnTree(a["isolate"] == true, a["worktree"])
		res, err := m.Spawn(t.id, me, SpawnSpec{
			Kind: strings.ToLower(str("kind")), Model: str("model"), Effort: str("effort"), Title: str("title"), Brief: str("brief"), TaskID: str("task"),
			Isolate: isolate, Tree: tree, Review: strings.ToLower(str("review")),
			Contract: store.Contract{Expect: str("expect"), DoneWhen: str("done_when"), ExplicitDone: a["explicit_done"] == true},
		})
		if err != nil {
			return "", err
		}
		out := fmt.Sprintf("started %s on task %s; its result comes back to you when it finishes", res.AgentID, res.TaskID)
		if res.Branch != "" {
			out += " (it works on branch " + res.Branch + ")"
		}
		return out, nil
	case "task.race":
		res, err := m.Race(t.id, me, RaceSpec{Title: str("title"), Brief: str("brief"), Entrants: parseEntries(str("agents")), Judge: strings.ToLower(str("judge")), Review: strings.ToLower(str("review"))})
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("started race %s with entries %s; when all have ended, a summary comes back to you, and the user keeps one", res.TaskID, strings.Join(res.Entries, ", ")), nil
	case "task.contract":
		tk, queued, err := m.Contract(t.id, me, ContractSpec{Peer: str("peer"), Title: str("title"), Brief: str("brief")})
		if err != nil {
			return "", err
		}
		how := "its person confirms it first"
		if queued {
			how = tk.Remote + " cannot be reached now: the request goes when its link is back, then its person confirms it"
		}
		return fmt.Sprintf("asked %s to do %s %q; %s. When it ends, its result comes back to you, with a capsule of its changes.", tk.Remote, tk.ID, tk.Title, how), nil
	case "review.approve":
		return m.answerReview(b.a, str("id"), true, str("note"))
	case "review.changes":
		return m.answerReview(b.a, str("id"), false, str("notes"))
	case "task.done", "task.fail", "task.cancel":
		id := str("id")
		if err := b.mayFinish(id); err != nil {
			return "", err
		}
		var tk store.Task
		var err error
		switch op {
		case "task.done":
			result, capsule := str("result"), str("capsule")
			var add func(*store.Task)
			if capsule != "" {
				if err := m.knownCapsule(capsule); err != nil {
					return "", err
				}
				add = func(tk *store.Task) { tk.Capsules = appendCapsule(tk.Capsules, capsule) }
				result = strings.TrimSpace(result + "\ncapsule: " + capsule)
			}
			// Work an agent did without starting the task first still goes to
			// the review it asked for: the review stage takes running work.
			if cur, ok := m.Task(t.id, id); ok && (cur.Status == store.TaskReady || cur.Status == store.TaskPending) && cur.Review != "" && cur.Review != store.ReviewNone {
				if _, err := m.moveTask(t, cur.ID, store.TaskRunning, me, "", nil); err != nil {
					return "", err
				}
			}
			tk, err = m.moveTask(t, id, store.TaskDone, me, result, add)
		case "task.fail":
			tk, err = m.moveTask(t, id, store.TaskFailed, me, str("reason"), nil)
		default:
			tk, err = m.moveTask(t, id, store.TaskCancelled, me, str("reason"), nil)
		}
		if err != nil {
			return "", err
		}
		if cur, ok := m.Task(t.id, tk.ID); ok && tk.Status == store.TaskReview {
			tk = cur // with its reviewer: startReview returns once it is bound
		}
		return b.line(tk), nil
	}
	return "", fmt.Errorf("unknown task op %q", op)
}

// budgetArgs reads budget_tokens, budget_usd and budget_minutes; nil when
// none is given.
func budgetArgs(a map[string]any) *store.Budget {
	var b store.Budget
	given := false
	if n, ok := a["budget_tokens"].(int); ok {
		b.Tokens, given = max(0, n), true
	}
	if n, ok := a["budget_minutes"].(int); ok {
		b.Minutes, given = max(0, n), true
	}
	switch v := a["budget_usd"].(type) {
	case string: // the hub has no float type: "1.50"
		if f, err := strconv.ParseFloat(strings.TrimPrefix(strings.TrimSpace(v), "$"), 64); err == nil {
			b.CostUSD, given = max(0, f), true
		}
	case float64:
		b.CostUSD, given = max(0, v), true
	}
	if !given {
		return nil
	}
	return &b
}

// line renders tk as taskLine does, taking the thread's lock.
func (b taskBoard) line(tk store.Task) string {
	t := b.a.thread
	t.mu.Lock()
	defer t.mu.Unlock()
	return taskLine(t, tk)
}

// mayFinish checks that the caller may report task id: its owner, its creator,
// or anyone when the task has no owner.
func (b taskBoard) mayFinish(id string) error {
	t := b.a.thread
	t.mu.Lock()
	defer t.mu.Unlock()
	i := store.FindTask(t.tasks, id)
	if i < 0 {
		return store.ErrNoTask
	}
	tk := t.tasks[i]
	if tk.Owner != "" && tk.Owner != b.a.id && tk.Creator != b.a.id {
		return fmt.Errorf("%w (%s owns %s)", errTaskNotYours, t.name(tk.Owner), tk.ID)
	}
	return nil
}

// resolveOwner turns an agent name into a member id.
func (b taskBoard) resolveOwner(name string) (string, error) {
	t := b.a.thread
	t.mu.Lock()
	defer t.mu.Unlock()
	if name == "me" || strings.EqualFold(name, "self") {
		return b.a.id, nil
	}
	if o := t.find(nil, name); o != nil {
		return o.id, nil
	}
	return "", fmt.Errorf("no agent called %q in this thread", name)
}

func (b taskBoard) list(status string) string {
	t := b.a.thread
	t.mu.Lock()
	defer t.mu.Unlock()
	var lines []string
	for _, tk := range t.tasks {
		if status != "" && !strings.EqualFold(string(tk.Status), status) {
			continue
		}
		lines = append(lines, taskLine(t, tk))
	}
	if len(lines) == 0 {
		return "no tasks"
	}
	return strings.Join(lines, "\n")
}

// taskLine is a task on one line: "t3 [running] codex ← t1,t2: title".
// t.mu is held, or the caller owns t.
func taskLine(t *thread, tk store.Task) string {
	var b strings.Builder
	b.WriteString(tk.ID + " [" + string(tk.Status) + "]")
	if tk.Owner != "" {
		b.WriteString(" " + t.name(tk.Owner))
	}
	if tk.Reviewer != "" {
		b.WriteString(", reviewed by " + t.name(tk.Reviewer))
	}
	if len(tk.Deps) > 0 {
		b.WriteString(" after " + strings.Join(tk.Deps, ","))
	}
	b.WriteString(": " + tk.Title)
	switch {
	case tk.Status == store.TaskDone && tk.Result != "":
		b.WriteString(" → " + clipText(oneLine(tk.Result), 200))
	case tk.Error != "" && (tk.Status == store.TaskFailed || tk.Status == store.TaskBlocked):
		b.WriteString(" ✗ " + clipText(oneLine(tk.Error), 200))
	}
	if n := len(tk.Capsules); n > 0 {
		b.WriteString(fmt.Sprintf(" [capsule %s", tk.Capsules[n-1]))
		if n > 1 {
			b.WriteString(fmt.Sprintf(", %d in all", n))
		}
		b.WriteString("]")
	}
	return b.String()
}

// splitIDs reads a list of task ids written "t1, t2" or "t1 t2".
func splitIDs(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' || r == ';' })
}

func normOnFail(s string) string {
	switch strings.ToLower(s) {
	case store.OnFailCancel, store.OnFailContinue, store.OnFailBlock:
		return strings.ToLower(s)
	}
	return ""
}

// RetryTask puts a failed, blocked or waiting task back to ready with a fresh
// set of attempts. A subagent that worked on it is not reused: the scheduler
// starts a new one.
func (m *Manager) RetryTask(threadID, id, by string) (store.Task, error) {
	t := m.thread(threadID)
	if t == nil {
		return store.Task{}, fmt.Errorf("session: no thread %q", threadID)
	}
	if tk, ok := m.Task(threadID, id); ok && tk.Remote != "" {
		return store.Task{}, fmt.Errorf("%s was a contract with %s: ask again with task.contract", tk.ID, tk.Remote)
	}
	var old *store.RunRef
	tk, err := m.moveTask(t, id, store.TaskReady, by, "retry", func(tk *store.Task) {
		tk.Attempts, tk.Reviews, tk.RetryAt, tk.Error, tk.BlockedBy = 0, 0, time.Time{}, "", ""
		old = tk.Run
		if tk.Run != nil {
			tk.Owner, tk.Run = "", nil
		}
	})
	if err == nil && old != nil {
		m.retireMember(t, old.Member) // a blocked task's subagent was kept alive for a steer
	}
	return tk, err
}
