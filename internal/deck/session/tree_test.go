package session

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/dev-dami/deck/internal/deck/agent"
	"github.com/dev-dami/deck/internal/deck/agent/fake"
	"github.com/dev-dami/deck/internal/deck/config"
	"github.com/dev-dami/deck/internal/deck/store"
)

// treeRig is a rig with orchestration on in one thread holding the user's
// claude, and the given limits.
func treeRig(t *testing.T, cfg config.Config, claude, codex []fake.Script) (*rig, string) {
	t.Helper()
	r := newRigWith(t, t.TempDir(), false, cfg, claude, codex)
	tid := r.thread(t)
	r.add(t, tid, "claude")
	if err := r.m.SetOrchestrate(tid, true); err != nil {
		t.Fatal(err)
	}
	return r, tid
}

// approvalOf answers the pending approval of member id.
func approvalOf(t *testing.T, r *rig, id string) ApprovalMsg {
	t.Helper()
	var out ApprovalMsg
	waitFor(t, id+"'s approval", func() bool {
		for _, a := range r.c.approvals() {
			if a.AgentID == id {
				out = a
				return true
			}
		}
		return false
	})
	return out
}

func TestSubagentsNestWithinLimits(t *testing.T) {
	cfg := testCfg()
	cfg.MaxDepth, cfg.MaxSubagents = 2, 3
	r, tid := treeRig(t, cfg, nil, []fake.Script{turn(blockStep("ap"), say("done"), finish())})

	a, err := r.m.Spawn(tid, "claude", SpawnSpec{Kind: "codex", Brief: "level one"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := r.m.Spawn(tid, a.AgentID, SpawnSpec{Kind: "codex", Brief: "level two"})
	if err != nil {
		t.Fatalf("a subagent spawning within max_depth: %v", err)
	}
	if _, err := r.m.Spawn(tid, b.AgentID, SpawnSpec{Kind: "codex", Brief: "level three"}); err == nil || !strings.Contains(err.Error(), "2 levels deep") {
		t.Fatalf("past max_depth: %v", err)
	}
	if _, err := r.m.Spawn(tid, "claude", SpawnSpec{Kind: "codex", Brief: "another"}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.m.Spawn(tid, "claude", SpawnSpec{Kind: "codex", Brief: "one too many"}); err == nil || !strings.Contains(err.Error(), "3 are running in this thread") {
		t.Fatalf("past max_subagents: %v", err)
	}

	// The second level knows where it is, and does not see the thread's past.
	waitFor(t, "b's brief", func() bool { return len(r.codex.prompts(b.AgentID)) == 1 })
	p := r.codex.prompts(b.AgentID)[0]
	has(t, p, "level two", "a subagent "+r.name(t, a.AgentID)+" started for task "+b.TaskID, "You cannot start subagents")
	lacks(t, p, "level one")
}

func TestStoppingAMemberStopsItsSubtree(t *testing.T) {
	r, tid := treeRig(t, testCfg(), nil, []fake.Script{turn(blockStep("ap"), say("done"), finish())})
	a, err := r.m.Spawn(tid, "claude", SpawnSpec{Kind: "codex", Brief: "lead"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := r.m.Spawn(tid, a.AgentID, SpawnSpec{Kind: "codex", Brief: "help"})
	if err != nil {
		t.Fatal(err)
	}
	approvalOf(t, r, b.AgentID)

	// Cancelling a's task retires a, and with it b.
	if _, err := r.m.CancelTask(tid, a.TaskID, ""); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "b stopped", func() bool { s, _ := memberStatus(r, b.AgentID); return s.Status == StatusExited })
	if tk := taskOf(t, r, tid, b.TaskID); tk.Status != store.TaskCancelled {
		t.Fatalf("b's task = %+v", tk)
	}

	// A member the user brought in takes its subagents with it when it exits.
	c, err := r.m.Spawn(tid, "claude", SpawnSpec{Kind: "codex", Brief: "more"})
	if err != nil {
		t.Fatal(err)
	}
	approvalOf(t, r, c.AgentID)
	_ = r.claude.run("claude").Close()
	waitFor(t, "c stopped", func() bool { s, _ := memberStatus(r, c.AgentID); return s.Status == StatusExited })
	if tk := taskOf(t, r, tid, c.TaskID); tk.Status != store.TaskCancelled {
		t.Fatalf("c's task = %+v", tk)
	}
}

func TestKeepOrphansAndResultsGoUp(t *testing.T) {
	cfg := testCfg()
	cfg.KeepOrphans = true
	r, tid := treeRig(t, cfg,
		[]fake.Script{turn(blockStep("ap-claude"), say("b finished"), finish())},
		[]fake.Script{turn(blockStep("ap-codex"), say("lead done"), finish())})
	a, err := r.m.Spawn(tid, "claude", SpawnSpec{Kind: "codex", Brief: "lead"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := r.m.Spawn(tid, a.AgentID, SpawnSpec{Kind: "claude", Brief: "help"})
	if err != nil {
		t.Fatal(err)
	}
	approvalOf(t, r, b.AgentID)
	if _, err := r.m.CancelTask(tid, a.TaskID, ""); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "a stopped", func() bool { s, _ := memberStatus(r, a.AgentID); return s.Status == StatusExited })
	if s, _ := memberStatus(r, b.AgentID); s.Status == StatusExited {
		t.Fatal("keep_orphans: b was stopped with its parent")
	}

	// b finishes; a is gone, so its result goes to a's parent, the user's claude.
	approvalOf(t, r, b.AgentID).Respond(agent.Decision{Allow: true})
	waitFor(t, "b's task done", func() bool { return taskOf(t, r, tid, b.TaskID).Status == store.TaskDone })
	waitFor(t, "claude told", func() bool { return len(r.claude.prompts("claude")) == 1 })
	has(t, r.claude.prompts("claude")[0], "b finished", "it was for "+r.name(t, a.AgentID)+", who has stopped")
}

func TestTaskWaitsForItsOwnersSubagents(t *testing.T) {
	spawn, _ := json.Marshal(map[string]any{"brief": "do the part", "kind": "claude"})
	r, tid := treeRig(t, testCfg(),
		[]fake.Script{turn(say("part done"), finish())},
		[]fake.Script{
			turn(fake.Step{Exec: &fake.Exec{Op: "agent.spawn", Args: spawn}}, say("waiting on my helper"), finish()),
			turn(say("all done"), finish()),
		})
	a, err := r.m.Spawn(tid, "", SpawnSpec{Kind: "codex", Brief: "the whole job"})
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "a's task done", func() bool { return taskOf(t, r, tid, a.TaskID).Status == store.TaskDone })
	if tk := taskOf(t, r, tid, a.TaskID); tk.Result != "all done" {
		t.Fatalf("a's task settled on %q, before its helper reported", tk.Result)
	}
	p := r.codex.prompts(a.AgentID)
	if len(p) != 2 {
		t.Fatalf("a's prompts = %q", p)
	}
	has(t, p[1], "done: part done")
}

func TestTaskKindPicksTheSubagent(t *testing.T) {
	r, tid := treeRig(t, testCfg(), nil, []fake.Script{turn(blockStep("ap"), say("done"), finish())})
	if _, err := r.m.CreateTask(tid, TaskSpec{Title: "x", Kind: "nope"}); err == nil {
		t.Fatal("a task for an unknown kind")
	}
	tk, err := r.m.CreateTask(tid, TaskSpec{Title: "review it", Kind: "codex", Model: "gpt-x", Effort: "high"})
	if err != nil {
		t.Fatal(err)
	}
	var owner string
	waitFor(t, "dispatched", func() bool { owner = taskOf(t, r, tid, tk.ID).Owner; return owner != "" })
	s, _ := memberStatus(r, owner)
	if s.Kind != "codex" || s.Model != "gpt-x" || s.Effort != "high" || !s.Ephemeral {
		t.Fatalf("subagent = %+v", s)
	}
}

// name is member id's name in the thread.
func (r *rig) name(t *testing.T, id string) string {
	t.Helper()
	s, ok := r.m.Agent(id)
	if !ok {
		t.Fatalf("no member %s", id)
	}
	return s.Name
}
