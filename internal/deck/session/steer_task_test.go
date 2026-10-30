package session

import (
	"strings"
	"testing"

	"github.com/dev-dami/deck/internal/deck/agent"
	"github.com/dev-dami/deck/internal/deck/agent/fake"
	"github.com/dev-dami/deck/internal/deck/store"
)

// ownedTask puts a running task on the board owned by member owner.
func ownedTask(t *testing.T, r *rig, tid, creator, owner string) {
	t.Helper()
	if _, err := r.m.CreateTask(tid, TaskSpec{Title: "port the cache", Creator: creator, Owner: owner}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.m.SetTaskStatus(tid, "t1", store.TaskRunning, creator, ""); err != nil {
		t.Fatal(err)
	}
}

func TestSteerTaskReachesAnIdleOwnerWithTheTaskContext(t *testing.T) {
	r := newRig(t, nil, []fake.Script{turn(say("on it"), finish())})
	tid := r.thread(t)
	r.add(t, tid, "claude")
	codex := r.add(t, tid, "codex")
	ownedTask(t, r, tid, "claude", codex)

	if err := r.m.SteerTask(tid, "t1", "", "use the v2 api"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "codex's turn", func() bool { return r.c.turnDones("codex") == 1 })
	p := r.codex.prompts("codex")
	if len(p) != 1 {
		t.Fatalf("prompts = %q", p)
	}
	has(t, p[0], "is redirecting you mid-task on task t1 \"port the cache\"", "use the v2 api")
}

func TestSteerTaskInterruptsABusyOwnerThroughItsSteerer(t *testing.T) {
	block := fake.Step{
		Event:    agent.Event{Kind: agent.KindToolCall, Tool: &agent.Tool{ID: "t", Name: "Bash", Title: "make", Status: agent.ToolRunning}},
		Approval: &agent.ApprovalRequest{ID: "ap-busy", Kind: agent.ApproveCommand, ToolName: "Bash", Summary: "make"},
	}
	r := newRig(t, nil, []fake.Script{turn(block, say("done"), finish()), turn(say("redirected"), finish())})
	r.codex.steer = true
	tid := r.thread(t)
	r.add(t, tid, "claude")
	codex := r.add(t, tid, "codex")
	ownedTask(t, r, tid, "claude", codex)
	if err := r.m.postAt(tid, "$codex start"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "codex approval", func() bool { return len(r.c.approvals()) == 1 })

	// Claude created the task, so Claude may steer it.
	if err := r.m.SteerTask(tid, "t1", "claude", "stop, use sqlite"); err != nil {
		t.Fatal(err)
	}
	calls := r.codex.steerCalls("codex")
	if len(calls) != 1 {
		t.Fatalf("steer calls = %d", len(calls))
	}
	has(t, calls[0].prompt, "claude is redirecting you mid-task on task t1", "stop, use sqlite")
}

func TestSteerTaskIsRefusedWhenItMakesNoSense(t *testing.T) {
	r := newRig(t, nil, nil)
	tid := r.thread(t)
	claude := r.add(t, tid, "claude")
	codex := r.add(t, tid, "codex")
	ownedTask(t, r, tid, claude, codex)

	for _, c := range []struct{ by, id, text, want string }{
		{"", "t9", "x", "no such task"},
		{"", "t1", "  ", "empty message"},
		{codex, "t1", "x", "your own task"},
	} {
		if err := r.m.SteerTask(tid, c.id, c.by, c.text); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("SteerTask(%q, %q, %q) = %v, want %q", c.by, c.id, c.text, err, c.want)
		}
	}
	if _, err := r.m.CreateTask(tid, TaskSpec{Title: "unowned"}); err != nil {
		t.Fatal(err)
	}
	if err := r.m.SteerTask(tid, "t2", "", "x"); err == nil || !strings.Contains(err.Error(), "no owner") {
		t.Errorf("unowned: %v", err)
	}
	// Someone who neither owns nor created the task cannot steer it through the op.
	third := r.add(t, tid, "codex")
	if out := execErr(t, r, third, "task.steer", `{"id":"t1","message":"x"}`); !strings.Contains(out, "can steer t1") {
		t.Errorf("op: %s", out)
	}
	if _, err := r.m.CompleteTask(tid, "t1", codex, "done"); err != nil {
		t.Fatal(err)
	}
	if err := r.m.SteerTask(tid, "t1", "", "x"); err == nil || !strings.Contains(err.Error(), "is done") {
		t.Errorf("finished: %v", err)
	}
}
