package session

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/dev-dami/deck/internal/deck/changes"
	"github.com/dev-dami/deck/internal/deck/store"
)

// Deck-managed subagents. A member can ask Deck to start a helper agent for a
// task: the helper joins the thread as an ephemeral child of the asker, gets
// the brief, optionally works on a git worktree of its own, and when its task
// is finished (or failed, or cancelled) Deck stops it and tells the asker the
// result. The same member machinery (adapters, briefing, steering) runs it;
// only the lifecycle is Deck's.

// deckSpeaker is the speaker of briefs Deck itself sends (the scheduler, a
// spawn from the UI): not a member, shown as "deck".
const deckSpeaker = "deck"

// SpawnSpec describes a subagent to start.
type SpawnSpec struct {
	Kind   string // agent kind; "" is the task's, else the default agent
	Model  string // "" is the task's, else the configured model for the kind
	Effort string // "" is the task's, else the configured effort for the kind
	Title  string // the task's title; "" takes it from the brief
	Brief  string // what the subagent is asked to do
	// TaskID binds the subagent to a task already on the board; "" creates one.
	TaskID string
	// Isolate gives the subagent its own git worktree and branch.
	Isolate  bool
	Contract store.Contract
}

// SpawnResult says what Spawn started.
type SpawnResult struct {
	AgentID string
	TaskID  string
	Branch  string // the worktree branch when isolated
}

// Spawn starts a subagent for a task of thread threadID. parentID is the
// member asking, or "" when the user or Deck's scheduler asks. Members may
// spawn only in threads with orchestration on, within the limits of tree.go:
// MaxDepth levels, MaxChildren live subagents per member and MaxSubagents per
// thread.
func (m *Manager) Spawn(threadID, parentID string, sp SpawnSpec) (SpawnResult, error) {
	if m.isClosed() {
		return SpawnResult{}, errClosed
	}
	t := m.thread(threadID)
	if t == nil {
		return SpawnResult{}, fmt.Errorf("session: no thread %q", threadID)
	}
	brief := strings.TrimSpace(sp.Brief)
	if brief == "" {
		return SpawnResult{}, errors.New("session: a subagent needs a brief")
	}
	if sp.TaskID != "" {
		// The task says which agent it wants when the spawn does not.
		if tk, ok := m.Task(threadID, sp.TaskID); ok {
			sp.Kind, sp.Model, sp.Effort = or(sp.Kind, tk.Kind), or(sp.Model, tk.Model), or(sp.Effort, tk.Effort)
		}
	}
	kind, routed := sp.Kind, ""
	if kind == "" {
		kind, routed = m.routeKind(m.defaultKind())
	}
	if _, ok := m.adapters[kind]; !ok {
		return SpawnResult{}, fmt.Errorf("session: unknown agent kind %q", kind)
	}

	parent, depth, err := m.spawnRoom(t, parentID, 1)
	if err != nil {
		return SpawnResult{}, err
	}
	if parentID != "" && !m.takeWork(t) {
		return SpawnResult{}, fmt.Errorf("session: %w", errPaused)
	}
	if routed != "" {
		m.notice(t, routed)
	}

	// The task: an existing one, or a new one for this brief.
	taskID, created := sp.TaskID, false
	contract := sp.Contract
	if taskID == "" {
		title := sp.Title
		if title == "" {
			title = clipText(oneLine(brief), 60)
		}
		// Claimed: it is created running (nobody's yet), so the scheduler does
		// not start a second subagent for it while this one is spawning.
		tk, err := m.addTask(t, TaskSpec{Title: title, Brief: brief, Creator: parentID, Contract: &contract, Claimed: true,
			Kind: sp.Kind, Model: sp.Model, Effort: sp.Effort})
		if err != nil {
			return SpawnResult{}, err
		}
		taskID, created = tk.ID, true
	} else {
		if err := m.checkSpawnTask(t, taskID); err != nil {
			return SpawnResult{}, err
		}
		// Same claim for an existing task: running from here on, bound to the
		// subagent once it is up.
		if _, err := m.moveTask(t, taskID, store.TaskRunning, parentID, "starting a subagent", nil); err != nil {
			return SpawnResult{}, err
		}
	}
	fail := func(err error) (SpawnResult, error) {
		note := "could not start a subagent: " + err.Error()
		if created {
			_, _ = m.moveTask(t, taskID, store.TaskCancelled, parentID, note, nil)
		} else {
			_, _ = m.moveTask(t, taskID, store.TaskFailed, parentID, note, nil)
		}
		return SpawnResult{}, err
	}

	a, err := m.newMember(t, kind, sp.Model, "")
	if err != nil {
		return fail(err)
	}
	a.mu.Lock()
	a.parent, a.ephemeral = parentID, true
	if sp.Effort != "" {
		a.effort = sp.Effort
	}
	a.mu.Unlock()
	// A subagent's view of the thread starts at its brief: what came before
	// is not its business unless it asks (thread.read), which keeps a deep
	// tree's prompts small.
	t.mu.Lock()
	a.cursor = len(t.log)
	t.mu.Unlock()
	brief += m.placeNote(t, a, parent, depth, taskID)
	var wt *store.Worktree
	if sp.Isolate {
		var dir string
		if wt, dir, err = m.memberWorktree(t, a); err != nil {
			m.dropMember(t, a)
			return fail(err)
		}
		a.mu.Lock()
		a.worktree, a.dir = wt, dir
		a.mu.Unlock()
		if note := m.seedWorktree(t, a, wt, taskID); note != "" {
			brief += "\n\n" + note
		}
	}
	m.persistMeta(t)
	if err := m.startRun(t, a, "", ""); err != nil {
		m.dropWorktree(wt)
		m.dropMember(t, a)
		return fail(err)
	}

	m.bindRun(t, taskID, a, wt, sp, brief, parentID)
	speaker := deckSpeaker
	if parent != nil {
		speaker = parent.id
	}
	m.deliverBrief(t, speaker, a, brief, taskID)
	res := SpawnResult{AgentID: a.id, TaskID: taskID}
	if wt != nil {
		res.Branch = wt.Branch
	}
	return res, nil
}

// spawnRoom checks that parentID ("" for the user and the scheduler) may
// start n more subagents in t, and returns the parent and the depth they
// would have.
func (m *Manager) spawnRoom(t *thread, parentID string, n int) (*agentState, int, error) {
	t.mu.Lock()
	var parent *agentState
	switch {
	case parentID != "" && !t.orchestrate:
		t.mu.Unlock()
		return nil, 0, errors.New("session: orchestration is off for this thread; the user turns it on")
	case parentID != "":
		if parent = t.member(parentID); parent == nil {
			t.mu.Unlock()
			return nil, 0, fmt.Errorf("session: no member %q", parentID)
		}
	}
	depth := t.depth(parent) + 1
	live, all := len(t.children(parentID)), t.liveSubagents()
	t.mu.Unlock()
	// The per-member cap is for members; the scheduler (parentID "") is
	// bounded by MaxParallel, and the user by their own judgement. The
	// thread's cap holds for everyone.
	switch maxD := m.maxDepth(); {
	case depth > maxD:
		return nil, 0, fmt.Errorf("session: %w: subagents go %d levels deep and you are at the last; do the work yourself", errChildLimit, maxD)
	case parentID != "" && live+n > m.maxChildren():
		return nil, 0, fmt.Errorf("session: %w: %d of yours are already running; wait for one to finish", errChildLimit, live)
	case all+n > m.maxSubagents():
		return nil, 0, fmt.Errorf("session: %w: %d are running in this thread; wait for one to finish", errChildLimit, all)
	}
	return parent, depth, nil
}

// placeNote tells a new subagent where it sits: who started it, for which
// task, that it sees only what is sent to it, and whether it may start
// subagents of its own.
func (m *Manager) placeNote(t *thread, a, parent *agentState, depth int, taskID string) string {
	who := "Deck"
	if parent != nil {
		who = parent.getName()
	}
	t.mu.Lock()
	title, orchestrate := t.title, t.orchestrate
	t.mu.Unlock()
	var b strings.Builder
	fmt.Fprintf(&b, "\n\n[deck] You are %s, a subagent %s started for task %s", a.getName(), who, taskID)
	if title != "" {
		fmt.Fprintf(&b, " in the thread %q", clipText(oneLine(title), 80))
	}
	b.WriteString(". You see only what is sent to you; op thread.read shows the whole thread.")
	switch {
	case !orchestrate:
	case depth < m.maxDepth():
		b.WriteString(" You may start subagents of your own (agent.spawn); your task then waits for their results.")
	default:
		b.WriteString(" You cannot start subagents: do the work yourself.")
	}
	return b.String()
}

func or(s, alt string) string {
	if s != "" {
		return s
	}
	return alt
}

// checkSpawnTask refuses a task that cannot take a new owner. It is not
// racing a live owner: a running task with a live owner is already being worked.
func (m *Manager) checkSpawnTask(t *thread, id string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	i := store.FindTask(t.tasks, id)
	if i < 0 {
		return fmt.Errorf("%w %q", store.ErrNoTask, id)
	}
	tk := t.tasks[i]
	if tk.Status.Final() {
		return fmt.Errorf("session: %s is already %s", tk.ID, tk.Status)
	}
	if o := t.member(tk.Owner); tk.Status == store.TaskRunning && o != nil && o.status() != StatusExited {
		return fmt.Errorf("session: %s is already being worked on by %s", tk.ID, o.getName())
	}
	return nil
}

// bindRun records on the task that subagent a works on it: owner, run, and
// contract (the spawn's, when the task has none yet).
func (m *Manager) bindRun(t *thread, taskID string, a *agentState, wt *store.Worktree, sp SpawnSpec, brief, by string) {
	var old *store.RunRef
	t.mu.Lock()
	if i := store.FindTask(t.tasks, taskID); i >= 0 {
		tk := &t.tasks[i]
		if tk.Run != nil && tk.Run.Member != a.id {
			old = tk.Run // relieved below
		}
		tk.Owner = a.id
		ref := &store.RunRef{Member: a.id}
		if wt != nil {
			ref.Branch, ref.Path = wt.Branch, wt.Path
		}
		tk.Run = ref
		if tk.Contract == nil || *tk.Contract == (store.Contract{}) {
			c := sp.Contract
			tk.Contract = &c
		}
		if tk.Creator == "" {
			tk.Creator = by
		}
		if tk.Brief == "" {
			tk.Brief = clipText(brief, maxTaskText)
		}
		// A retry starts the same kind of agent again.
		if tk.Kind == "" {
			tk.Kind, tk.Model, tk.Effort = a.kind, sp.Model, sp.Effort
		}
	}
	t.mu.Unlock()
	m.startHandedTask(t, taskID, a, by)
	if old != nil {
		m.retireMember(t, old.Member)
	}
}

// deliverBrief logs the brief as a message from speaker to the subagent and
// delivers it. It does not count as a hop: spawning is bounded by maxChildren
// per member and by MaxParallel for the scheduler.
func (m *Manager) deliverBrief(t *thread, speaker string, to *agentState, text, taskID string) {
	e := store.Entry{Type: store.EntryRelay, Speaker: speaker, To: []string{to.id}, Via: "spawn", TaskID: taskID, Event: eventText(text)}
	t.mu.Lock()
	m.appendLocked(t, e)
	t.mu.Unlock()
	m.emit(msgFor(t.id, e))
	_, _ = m.deliver(t, to)
}

// memberWorktree makes a worktree and branch for a subagent from the
// repository's last commit, beside the thread's own worktrees. It returns the
// worktree and the directory in it matching the thread's directory.
func (m *Manager) memberWorktree(t *thread, a *agentState) (*store.Worktree, string, error) {
	if m.st == nil {
		return nil, "", errors.New("session: no store")
	}
	t.mu.Lock()
	dir := t.dir
	t.mu.Unlock()
	ctx, cancel := context.WithTimeout(m.ctx, gitTimeout)
	defer cancel()
	r, err := changes.Open(ctx, dir)
	if err != nil {
		return nil, "", errors.New("session: an isolated subagent needs a git repository")
	}
	id := t.id
	if len(id) > 6 {
		id = id[:6]
	}
	name := id + "-" + a.id
	path := filepath.Join(filepath.Dir(m.st.Dir()), "worktrees", filepath.Base(r.Root)+"-"+name)
	branch := "deck/" + name
	base, err := r.AddWorktree(ctx, path, branch)
	if err != nil {
		return nil, "", err
	}
	target := path
	if rel, err := filepath.Rel(r.Root, dir); err == nil && rel != "." && !strings.HasPrefix(rel, "..") {
		target = filepath.Join(path, rel)
	}
	return &store.Worktree{Path: path, Branch: branch, Main: dir, Base: base}, target, nil
}

// retireMember stops a finished subagent: its process is interrupted and
// closed and it shows as exited. Its worktree stays until ApplyMember or
// DiscardMember. Anything but a subagent is left alone.
func (m *Manager) retireMember(t *thread, id string) {
	t.mu.Lock()
	a := t.member(id)
	t.mu.Unlock()
	if a == nil || !a.isEphemeral() {
		return
	}
	a.mu.Lock()
	run, done := a.run, a.retired || a.base == StatusExited
	a.retired = true
	a.mu.Unlock()
	if done {
		return
	}
	_ = m.Interrupt(a.id) // denies what it was waiting on
	defer m.cascade(t, a)
	if run != nil {
		// Closed off this goroutine: it may be the one draining the run's
		// events. The exit itself is logged by the pump when the run's channels
		// close, like every other exit, so the others' briefings see it leave.
		m.wg.Add(1)
		go func() {
			defer m.wg.Done()
			_ = run.Close()
		}()
	}
}

// memberIdle reports whether member id has no turn in progress (or is gone).
func (m *Manager) memberIdle(t *thread, id string) bool {
	t.mu.Lock()
	a := t.member(id)
	t.mu.Unlock()
	return a == nil || !a.busy()
}

// retireSpent stops subagent a when its turn ended and nothing it owns is
// still running or blocked (a blocked task keeps it for the user to steer).
func (m *Manager) retireSpent(t *thread, a *agentState) {
	if !a.isEphemeral() {
		return
	}
	t.mu.Lock()
	if t.waitsOnChildren(a) {
		t.mu.Unlock()
		return
	}
	for _, tk := range t.tasks {
		if tk.Owner == a.id && (tk.Status == store.TaskRunning || tk.Status == store.TaskBlocked) {
			t.mu.Unlock()
			return
		}
	}
	t.mu.Unlock()
	m.retireMember(t, a.id)
}

// failOrphans fails the running tasks whose subagent did not survive a
// restart of Deck: its process is gone, so nothing will ever report them.
func (m *Manager) failOrphans(t *thread) {
	var ids []string
	t.mu.Lock()
	for _, tk := range t.tasks {
		if tk.Status != store.TaskRunning {
			continue
		}
		if o := t.member(tk.Owner); o != nil && o.isEphemeral() {
			ids = append(ids, tk.ID)
		}
	}
	t.mu.Unlock()
	for _, id := range ids {
		_, _ = m.moveTask(t, id, store.TaskFailed, "", "its subagent was lost when Deck stopped", nil)
	}
}

// memberTree opens a subagent's worktree and the tree it came from.
func (m *Manager) memberTree(ctx context.Context, threadID, memberID string) (*thread, *agentState, *store.Worktree, *changes.Repo, *changes.Repo, error) {
	t := m.thread(threadID)
	if t == nil {
		return nil, nil, nil, nil, nil, fmt.Errorf("session: no thread %q", threadID)
	}
	t.mu.Lock()
	a := t.member(memberID)
	t.mu.Unlock()
	if a == nil {
		return nil, nil, nil, nil, nil, fmt.Errorf("session: no member %q", memberID)
	}
	wt := a.getWorktree()
	if wt == nil {
		return nil, nil, nil, nil, nil, fmt.Errorf("session: %s has no worktree of its own", a.getName())
	}
	main, err := changes.Open(ctx, wt.Main)
	if err != nil {
		return nil, nil, nil, nil, nil, err
	}
	tree, err := changes.Open(ctx, wt.Path)
	if err != nil {
		return nil, nil, nil, nil, nil, err
	}
	return t, a, wt, main, tree, nil
}

// ApplyMember writes what an isolated subagent changed into the tree it was
// started from, as uncommitted edits, whole or not at all, and returns the
// files.
func (m *Manager) ApplyMember(threadID, memberID string) ([]string, error) {
	ctx, cancel := context.WithTimeout(m.ctx, gitTimeout)
	defer cancel()
	t, a, wt, main, tree, err := m.memberTree(ctx, threadID, memberID)
	if err != nil {
		return nil, err
	}
	patch, files, now, err := main.Pending(ctx, tree, wt.Base)
	if err != nil || len(files) == 0 {
		return nil, err
	}
	if err := main.Apply(ctx, patch); err != nil {
		return nil, fmt.Errorf("the changes do not apply cleanly to your tree (it changed the same lines): %w", err)
	}
	a.mu.Lock()
	a.worktree = &store.Worktree{Path: wt.Path, Branch: wt.Branch, Main: wt.Main, Base: now, SubBase: wt.SubBase, SubParent: wt.SubParent}
	a.mu.Unlock()
	m.persistMeta(t)
	m.emit(NoticeMsg{ThreadID: threadID, Text: fmt.Sprintf("applied %d files from %s to %s", len(files), a.getName(), wt.Main)})
	return files, nil
}

// DiscardMember removes a subagent's worktree and branch, whatever it changed.
func (m *Manager) DiscardMember(threadID, memberID string) error {
	t := m.thread(threadID)
	if t == nil {
		return fmt.Errorf("session: no thread %q", threadID)
	}
	t.mu.Lock()
	a := t.member(memberID)
	t.mu.Unlock()
	if a == nil {
		return fmt.Errorf("session: no member %q", memberID)
	}
	wt := a.getWorktree()
	if wt == nil {
		return fmt.Errorf("session: %s has no worktree of its own", a.getName())
	}
	m.retireMember(t, memberID)
	m.dropWorktree(wt)
	a.mu.Lock()
	a.worktree = nil
	a.mu.Unlock()
	m.persistMeta(t)
	return nil
}

// SetOrchestrate turns orchestration on or off for a thread: members may spawn
// subagents in it, and the scheduler runs its ready tasks.
func (m *Manager) SetOrchestrate(threadID string, on bool) error {
	err := m.change(threadID, func(t *thread) { t.orchestrate = on })
	if err == nil && on {
		if t := m.thread(threadID); t != nil {
			m.kick(t)
		}
	}
	return err
}

// ---- member accessors ----

func (a *agentState) isEphemeral() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.ephemeral
}

func (a *agentState) parentID() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.parent
}

func (a *agentState) getWorktree() *store.Worktree {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.worktree
}

// workDir is where the member's process runs: its own worktree directory, or
// the thread's.
func (a *agentState) workDir(threadDir string) string {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.dir != "" {
		return a.dir
	}
	return threadDir
}
