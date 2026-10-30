package session

import (
	"encoding/json"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/dev-dami/deck/internal/deck/agent/fake"
	"github.com/dev-dami/deck/internal/deck/store"
)

// schedRig is a thread with a claude lead and codex as the agent the
// scheduler starts for tasks without an owner.
func schedRig(t *testing.T, maxParallel int, claude, codex []fake.Script) (*rig, string) {
	t.Helper()
	cfg := testCfg()
	cfg.DefaultAgent = "codex"
	cfg.MaxParallel = maxParallel
	r := newRigWith(t, t.TempDir(), false, cfg, claude, codex)
	tid := r.thread(t)
	r.add(t, tid, "claude")
	return r, tid
}

func mkTask(t *testing.T, r *rig, tid string, sp TaskSpec) store.Task {
	t.Helper()
	tk, err := r.m.CreateTask(tid, sp)
	if err != nil {
		t.Fatal(err)
	}
	return tk
}

func countStatus(r *rig, tid string, st store.TaskStatus) int {
	n := 0
	for _, tk := range r.m.Tasks(tid) {
		if tk.Status == st {
			n++
		}
	}
	return n
}

func TestSchedulerRunsReadyTasksInDependencyOrder(t *testing.T) {
	r, tid := schedRig(t, 0, nil, []fake.Script{turn(say("built"), finish())})
	if err := r.m.SetLevel(tid, "auto"); err != nil {
		t.Fatal(err)
	}
	mkTask(t, r, tid, TaskSpec{Title: "first"})
	mkTask(t, r, tid, TaskSpec{Title: "second", Deps: []string{"t1"}})
	mkTask(t, r, tid, TaskSpec{Title: "third", Deps: []string{"t1"}})

	waitFor(t, "all three done", func() bool { return countStatus(r, tid, store.TaskDone) == 3 })
	idx := func(id string, st store.TaskStatus) int {
		for i, m := range r.c.all() {
			if tm, ok := m.(TaskChangedMsg); ok && tm.Task.ID == id && tm.Task.Status == st {
				return i
			}
		}
		return -1
	}
	done1 := idx("t1", store.TaskDone)
	for _, id := range []string{"t2", "t3"} {
		if run := idx(id, store.TaskRunning); done1 < 0 || run < done1 {
			t.Errorf("%s started (msg %d) before t1 was done (msg %d)", id, run, done1)
		}
	}
	for _, tk := range r.m.Tasks(tid) {
		if tk.Result != "built" || tk.Attempts != 1 {
			t.Errorf("%s = %+v", tk.ID, tk)
		}
	}
	// Dispatching is not agents talking to each other: no hop was spent.
	if info, _ := r.m.Thread(tid); info.Hops != 0 {
		t.Errorf("hops = %d", info.Hops)
	}
}

func TestSchedulerKeepsToTheConcurrencyLimit(t *testing.T) {
	r, tid := schedRig(t, 1, nil, []fake.Script{turn(blockStep("ap-slow"), say("ok"), finish())})
	if err := r.m.SetLevel(tid, "auto"); err != nil {
		t.Fatal(err)
	}
	for _, title := range []string{"a", "b", "c"} {
		mkTask(t, r, tid, TaskSpec{Title: title})
	}
	waitFor(t, "the first task's approval", func() bool { return len(r.c.approvals()) == 1 })
	time.Sleep(50 * time.Millisecond) // time for a second dispatch, were it going to happen
	if run, ready := countStatus(r, tid, store.TaskRunning), countStatus(r, tid, store.TaskReady); run != 1 || ready != 2 {
		t.Fatalf("running %d, ready %d; want 1 and 2: %+v", run, ready, r.m.Tasks(tid))
	}
	subs := 0
	for _, a := range r.m.Agents() {
		if a.Ephemeral {
			subs++
		}
	}
	if subs != 1 {
		t.Fatalf("%d subagents started", subs)
	}
}

func TestSchedulerRetriesAFailedTaskWithTheFailureInItsBrief(t *testing.T) {
	old := retryBackoff
	retryBackoff = func(int) time.Duration { return 5 * time.Millisecond }
	t.Cleanup(func() { retryBackoff = old })

	fail := json.RawMessage(`{"id":"t1","reason":"flaky network"}`)
	r, tid := schedRig(t, 0, []fake.Script{
		turn(fake.Step{Exec: &fake.Exec{Op: "task.fail", Args: fail}}, say("it failed"), finish()),
		turn(say("fixed it"), finish()),
	}, nil)
	if err := r.m.SetLevel(tid, "auto"); err != nil {
		t.Fatal(err)
	}
	mkTask(t, r, tid, TaskSpec{Title: "fetch the data", Owner: "claude"})

	waitFor(t, "the retry to finish", func() bool { return taskOf(t, r, tid, "t1").Status == store.TaskDone })
	tk := taskOf(t, r, tid, "t1")
	if tk.Attempts != 2 || tk.Result != "fixed it" {
		t.Fatalf("task = %+v", tk)
	}
	p := r.claude.prompts("claude")
	if len(p) != 2 {
		t.Fatalf("prompts = %q", p)
	}
	has(t, p[1], "Attempt 1 of 2 failed: flaky network")
}

func TestSchedulerGivesUpAfterTheLastAttempt(t *testing.T) {
	old := retryBackoff
	retryBackoff = func(int) time.Duration { return time.Millisecond }
	t.Cleanup(func() { retryBackoff = old })

	fail := json.RawMessage(`{"id":"t1","reason":"nope"}`)
	r, tid := schedRig(t, 0, []fake.Script{
		turn(fake.Step{Exec: &fake.Exec{Op: "task.fail", Args: fail}}, say("failed"), finish()),
		turn(fake.Step{Exec: &fake.Exec{Op: "task.fail", Args: fail}}, say("failed again"), finish()),
	}, nil)
	if err := r.m.SetLevel(tid, "auto"); err != nil {
		t.Fatal(err)
	}
	mkTask(t, r, tid, TaskSpec{Title: "doomed", Owner: "claude"})
	mkTask(t, r, tid, TaskSpec{Title: "waits on it", Deps: []string{"t1"}})

	waitFor(t, "t2 blocked by the failure", func() bool { return taskOf(t, r, tid, "t2").Status == store.TaskBlocked })
	t1, t2 := taskOf(t, r, tid, "t1"), taskOf(t, r, tid, "t2")
	if t1.Status != store.TaskFailed || t1.Attempts != 2 {
		t.Fatalf("t1 = %+v", t1)
	}
	if t2.BlockedBy != "t1" {
		t.Fatalf("t2 = %+v", t2)
	}
	time.Sleep(30 * time.Millisecond) // no third attempt follows
	if got := taskOf(t, r, tid, "t1"); got.Attempts != 2 || got.Status != store.TaskFailed {
		t.Fatalf("t1 after a pause = %+v", got)
	}
}

// failedFor puts two tasks on a board without a scheduler: t1 (failed for
// good, with the given policy) and t2 waiting on it.
func failedFor(t *testing.T, policy string) (*rig, string, *thread) {
	t.Helper()
	r := newRig(t, nil, nil)
	tid := r.thread(t)
	r.add(t, tid, "claude")
	mkTask(t, r, tid, TaskSpec{Title: "one", MaxAttempts: 1, OnFail: policy})
	mkTask(t, r, tid, TaskSpec{Title: "two", Deps: []string{"t1"}})
	for _, to := range []store.TaskStatus{store.TaskRunning, store.TaskFailed} {
		if _, err := r.m.SetTaskStatus(tid, "t1", to, "", "broke"); err != nil {
			t.Fatal(err)
		}
	}
	return r, tid, r.m.thread(tid)
}

func TestFailurePolicyBlockUnblocksWhenTheFailureIsFixed(t *testing.T) {
	r, tid, th := failedFor(t, "")
	r.m.propagateFailures(th)
	if tk := taskOf(t, r, tid, "t2"); tk.Status != store.TaskBlocked || tk.BlockedBy != "t1" {
		t.Fatalf("t2 = %+v", tk)
	}
	if _, err := r.m.SetTaskStatus(tid, "t1", store.TaskReady, "", "retry by hand"); err != nil {
		t.Fatal(err)
	}
	if tk := taskOf(t, r, tid, "t2"); tk.Status != store.TaskPending || tk.BlockedBy != "" || tk.Error != "" {
		t.Fatalf("t2 should be waiting again, not blocked: %+v", tk)
	}
}

func TestFailurePolicyCancelCancelsWhatWaits(t *testing.T) {
	r, tid, th := failedFor(t, "cancel")
	r.m.propagateFailures(th)
	if tk := taskOf(t, r, tid, "t2"); tk.Status != store.TaskCancelled {
		t.Fatalf("t2 = %+v", tk)
	}
}

func TestFailurePolicyContinueLetsWhatWaitsGo(t *testing.T) {
	r, tid, th := failedFor(t, "continue")
	r.m.propagateFailures(th)
	if _, err := r.m.UpdateTask(tid, "t2", "", TaskPatch{}); err != nil { // any change re-checks the board
		t.Fatal(err)
	}
	if tk := taskOf(t, r, tid, "t2"); tk.Status != store.TaskReady {
		t.Fatalf("t2 = %+v", tk)
	}
}

func TestSchedulerIsOffUnlessOrchestrating(t *testing.T) {
	r, tid := schedRig(t, 0, nil, []fake.Script{turn(say("built"), finish())})
	mkTask(t, r, tid, TaskSpec{Title: "idle"})
	time.Sleep(50 * time.Millisecond)
	if tk := taskOf(t, r, tid, "t1"); tk.Status != store.TaskReady {
		t.Fatalf("a task on a passive board just becomes ready: %+v", tk)
	}
	if n := r.c.count(func(m tea.Msg) bool { tm, ok := m.(TaskChangedMsg); return ok && tm.Task.Status == store.TaskRunning }); n != 0 {
		t.Fatalf("something ran: %d", n)
	}
}

// An agent that plans its own work on the board and finishes it in the turn
// the user asked for is not asked to do it again: the briefs the scheduler
// queued for those tasks no longer start a turn once the tasks are done.
func TestSchedulerDoesNotReturnSelfOwnedWorkFinishedInTheSameTurn(t *testing.T) {
	op := func(name, args string) fake.Step {
		return fake.Step{Exec: &fake.Exec{Op: name, Args: json.RawMessage(args)}}
	}
	cfg := testCfg()
	cfg.Orchestration = "auto"
	r := newRigWith(t, t.TempDir(), false, cfg, []fake.Script{
		turn(
			op("task.create", `{"title":"read the code","owner":"claude"}`),
			op("task.create", `{"title":"fix the bug","owner":"claude"}`),
			op("task.done", `{"id":"t1","result":"read"}`),
			op("task.done", `{"id":"t2","result":"fixed"}`),
			say("fixed the bug"), finish()),
		turn(say("already did that"), finish()),
	}, nil)
	tid := r.thread(t)
	r.add(t, tid, "claude")
	if err := r.m.Post(tid, "fix the login bug"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the turn to end", func() bool { return r.c.turnDones("claude") >= 1 })
	time.Sleep(100 * time.Millisecond) // time for a second turn, were it going to start
	if p := r.claude.prompts("claude"); len(p) != 1 {
		t.Fatalf("claude got %d prompts, want 1: %q", len(p), p)
	}
	if n := countStatus(r, tid, store.TaskDone); n != 2 {
		t.Fatalf("%d tasks done, want 2: %+v", n, r.m.Tasks(tid))
	}
}
