package session

import (
	"strings"
	"testing"

	"github.com/dev-dami/deck/internal/deck/agent/fake"
	"github.com/dev-dami/deck/internal/deck/store"
)

func memberStatus(r *rig, id string) (AgentInfo, bool) { return r.m.Agent(id) }

func TestSpawnRunsASubagentAndReturnsItsResult(t *testing.T) {
	r := newRig(t, []fake.Script{turn(say("thanks, merging"), finish())}, []fake.Script{turn(say("audit: all clean"), finish())})
	tid := r.thread(t)
	r.add(t, tid, "claude")

	if _, err := r.m.Spawn(tid, "claude", SpawnSpec{Kind: "codex", Brief: "audit the logs"}); err == nil || !strings.Contains(err.Error(), "orchestration is off") {
		t.Fatalf("spawn with orchestration off: %v", err)
	}
	if err := r.m.SetOrchestrate(tid, true); err != nil {
		t.Fatal(err)
	}
	res, err := r.m.Spawn(tid, "claude", SpawnSpec{Kind: "codex", Brief: "audit the logs", Title: "audit logs"})
	if err != nil {
		t.Fatal(err)
	}
	if res.AgentID != "codex" || res.TaskID != "t1" {
		t.Fatalf("result = %+v", res)
	}
	waitFor(t, "the task done", func() bool { return taskOf(t, r, tid, "t1").Status == store.TaskDone })
	tk := taskOf(t, r, tid, "t1")
	if tk.Result != "audit: all clean" || tk.Owner != "codex" || tk.Creator != "claude" || tk.Run == nil || tk.Run.Member != "codex" {
		t.Fatalf("task = %+v", tk)
	}
	waitFor(t, "the subagent retired", func() bool { a, _ := memberStatus(r, "codex"); return a.Status == StatusExited })
	a, _ := memberStatus(r, "codex")
	if !a.Ephemeral || a.Parent != "claude" {
		t.Fatalf("subagent = %+v", a)
	}
	has(t, r.codex.prompts("codex")[0], `task t1 "audit logs"`, "audit the logs")
	waitFor(t, "claude told", func() bool { return r.c.turnDones("claude") == 1 })
	has(t, r.claude.prompts("claude")[0], `task t1 "audit logs" done: audit: all clean`)
}

func TestSpawnLimits(t *testing.T) {
	r := newRig(t, nil, []fake.Script{turn(blockStep("ap-child"), say("done"), finish())})
	tid := r.thread(t)
	r.add(t, tid, "claude")
	if err := r.m.SetOrchestrate(tid, true); err != nil {
		t.Fatal(err)
	}
	if _, err := r.m.Spawn(tid, "claude", SpawnSpec{Kind: "nope", Brief: "x"}); err == nil || !strings.Contains(err.Error(), "unknown agent kind") {
		t.Errorf("unknown kind: %v", err)
	}
	if _, err := r.m.Spawn(tid, "claude", SpawnSpec{Kind: "codex", Brief: "  "}); err == nil || !strings.Contains(err.Error(), "needs a brief") {
		t.Errorf("empty brief: %v", err)
	}
	res, err := r.m.Spawn(tid, "claude", SpawnSpec{Kind: "codex", Brief: "build it"})
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the child's approval", func() bool { return len(r.c.approvals()) == 1 })
	if _, err := r.m.Spawn(tid, res.AgentID, SpawnSpec{Kind: "codex", Brief: "nested"}); err == nil || !strings.Contains(err.Error(), "cannot start subagents") {
		t.Errorf("a subagent spawning: %v", err)
	}
	if _, err := r.m.Spawn(tid, "claude", SpawnSpec{Kind: "codex", Brief: "again", TaskID: res.TaskID}); err == nil || !strings.Contains(err.Error(), "already being worked on") {
		t.Errorf("a task someone is working on: %v", err)
	}
	if _, err := r.m.Spawn(tid, "ghost", SpawnSpec{Kind: "codex", Brief: "x"}); err == nil {
		t.Error("an unknown parent")
	}
}

func TestCancelTaskStopsItsSubagentAndSubtasks(t *testing.T) {
	r := newRig(t, nil, []fake.Script{turn(blockStep("ap-child"), say("done"), finish())})
	tid := r.thread(t)
	r.add(t, tid, "claude")
	if err := r.m.SetOrchestrate(tid, true); err != nil {
		t.Fatal(err)
	}
	res, err := r.m.Spawn(tid, "claude", SpawnSpec{Kind: "codex", Brief: "build it"})
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the child's approval", func() bool { return len(r.c.approvals()) == 1 })
	sub, err := r.m.CreateTask(tid, TaskSpec{Title: "part of it", Parent: res.TaskID})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := r.m.CancelTask(tid, res.TaskID, ""); err != nil {
		t.Fatal(err)
	}
	if tk := taskOf(t, r, tid, res.TaskID); tk.Status != store.TaskCancelled || tk.Run != nil {
		t.Fatalf("task = %+v", tk)
	}
	if tk := taskOf(t, r, tid, sub.ID); tk.Status != store.TaskCancelled {
		t.Fatalf("a subtask follows its parent: %+v", tk)
	}
	waitFor(t, "the subagent stopped", func() bool { a, _ := memberStatus(r, res.AgentID); return a.Status == StatusExited })
}

func TestSubagentsDoNotSurviveAResumeButTheirTasksAreFailed(t *testing.T) {
	r := newRig(t, nil, []fake.Script{turn(blockStep("ap-child"), say("done"), finish())})
	tid := r.thread(t)
	r.add(t, tid, "claude")
	if err := r.m.SetOrchestrate(tid, true); err != nil {
		t.Fatal(err)
	}
	res, err := r.m.Spawn(tid, "claude", SpawnSpec{Kind: "codex", Brief: "build it"})
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the child's approval", func() bool { return len(r.c.approvals()) == 1 })
	r.m.Flush()
	if th := stored(t, r, tid); !th.Orchestrate || len(th.Members) != 2 || !th.Members[1].Ephemeral || th.Members[1].Parent != "claude" {
		t.Fatalf("stored = %+v", th)
	}
	if err := r.m.Archive(tid); err != nil {
		t.Fatal(err)
	}
	info, _, err := r.m.Resume(tid)
	if err != nil {
		t.Fatal(err)
	}
	if !info.Orchestrate {
		t.Fatal("orchestration is stored with the thread")
	}
	child, ok := memberStatus(r, res.AgentID)
	if !ok || child.Status != StatusExited || !child.Ephemeral {
		t.Fatalf("child = %+v", child)
	}
	tk := taskOf(t, r, tid, res.TaskID)
	if tk.Status != store.TaskFailed || !strings.Contains(tk.Error, "lost when Deck stopped") {
		t.Fatalf("task = %+v", tk)
	}
}
