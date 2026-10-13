package session

import (
	"encoding/json"
	"testing"

	"github.com/dev-dami/deck/internal/deck/agent"
	"github.com/dev-dami/deck/internal/deck/agent/fake"
	"github.com/dev-dami/deck/internal/deck/store"
)

// handTo is a thread.send that hands a message over as a new task.
func handTo(to, text, title string, c store.Contract) fake.Step {
	return fake.Step{Message: &agent.Message{
		To: to, Text: text, NewTask: title, Expect: c.Expect, DoneWhen: c.DoneWhen, ExplicitDone: c.ExplicitDone,
	}}
}

func taskOf(t *testing.T, r *rig, tid, id string) store.Task {
	t.Helper()
	tk, ok := r.m.Task(tid, id)
	if !ok {
		t.Fatalf("no task %s in %v", id, r.m.Tasks(tid))
	}
	return tk
}

func TestHandoffCompletesWithTheRecipientsReplyAndReturnsTheResult(t *testing.T) {
	r := newRig(t, []fake.Script{
		turn(handTo("codex", "review parser.go", "review parser", store.Contract{Expect: "a bug list"}), say("handed it over"), finish()),
		turn(say("thanks"), finish()),
	}, []fake.Script{
		turn(say("2 bugs: a and b"), finish()),
	})
	tid := r.thread(t)
	r.add(t, tid, "claude")
	r.add(t, tid, "codex")
	if err := r.m.Post(tid, "$claude delegate the review"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "claude's second turn", func() bool { return r.c.turnDones("claude") == 2 })

	tk := taskOf(t, r, tid, "t1")
	if tk.Status != store.TaskDone || tk.Result != "2 bugs: a and b" || tk.Owner != "codex" || tk.Creator != "claude" {
		t.Fatalf("task = %+v", tk)
	}
	if tk.Contract == nil || tk.Contract.Expect != "a bug list" {
		t.Fatalf("contract = %+v", tk.Contract)
	}
	has(t, r.codex.prompts("codex")[0], `task t1 "review parser"`, "Expected back: a bug list")
	has(t, r.claude.prompts("claude")[1], `task t1 "review parser" done: 2 bugs: a and b`)
	if res := r.c.toolResults("claude", "message"); len(res) != 1 || res[0] != "delivered to codex" {
		t.Fatalf("thread.send result = %q", res)
	}
	var viaTask int
	for _, rl := range r.c.relays() {
		if rl.Via == "task" && rl.From == "codex" && rl.To[0] == "claude" {
			viaTask++
		}
	}
	if viaTask != 1 {
		t.Fatalf("result relays = %d, want 1", viaTask)
	}
}

func TestHandoffWithExplicitDoneBlocksWhenNothingIsReported(t *testing.T) {
	r := newRig(t, []fake.Script{
		turn(handTo("codex", "check the cache", "check cache", store.Contract{ExplicitDone: true}), say("asked"), finish()),
		turn(say("noted"), finish()),
	}, []fake.Script{
		turn(say("looked at it"), finish()),
	})
	tid := r.thread(t)
	r.add(t, tid, "claude")
	r.add(t, tid, "codex")
	if err := r.m.Post(tid, "$claude go"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "claude's second turn", func() bool { return r.c.turnDones("claude") == 2 })
	tk := taskOf(t, r, tid, "t1")
	if tk.Status != store.TaskBlocked || tk.Error != "ended its turn without reporting task.done" {
		t.Fatalf("task = %+v", tk)
	}
	has(t, r.claude.prompts("claude")[1], "blocked: ended its turn without reporting task.done")
	has(t, r.codex.prompts("codex")[0], "Report it with execute task.done")
}

func TestHandoffFinishedWithTaskDoneUsesTheReportedResult(t *testing.T) {
	done, _ := json.Marshal(map[string]string{"id": "t1", "result": "cache is fine"})
	r := newRig(t, []fake.Script{
		turn(handTo("codex", "check the cache", "check cache", store.Contract{ExplicitDone: true}), say("asked"), finish()),
		turn(say("noted"), finish()),
	}, []fake.Script{
		turn(fake.Step{Exec: &fake.Exec{Op: "task.done", Args: done}}, say("reported"), finish()),
	})
	tid := r.thread(t)
	r.add(t, tid, "claude")
	r.add(t, tid, "codex")
	if err := r.m.Post(tid, "$claude go"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "claude's second turn", func() bool { return r.c.turnDones("claude") == 2 })
	if tk := taskOf(t, r, tid, "t1"); tk.Status != store.TaskDone || tk.Result != "cache is fine" {
		t.Fatalf("task = %+v", tk)
	}
	has(t, r.claude.prompts("claude")[1], "done: cache is fine")
}

// A handover that waits in a busy member's queue is not finished by the turn
// the member was in the middle of.
func TestHandoffToBusyMemberIsNotSettledByItsCurrentTurn(t *testing.T) {
	block := fake.Step{
		Event:    agent.Event{Kind: agent.KindToolCall, Tool: &agent.Tool{ID: "t", Name: "Bash", Title: "make", Status: agent.ToolRunning}},
		Approval: &agent.ApprovalRequest{ID: "ap-busy", Kind: agent.ApproveCommand, ToolName: "Bash", Summary: "make"},
	}
	r := newRig(t, []fake.Script{
		turn(handTo("codex", "do the second thing", "second thing", store.Contract{}), say("queued it"), finish()),
		turn(say("ok"), finish()),
	}, []fake.Script{
		turn(block, say("finished the first thing"), finish()),
		turn(say("did the second thing"), finish()),
	})
	tid := r.thread(t)
	r.add(t, tid, "claude")
	r.add(t, tid, "codex")
	if err := r.m.Post(tid, "$codex first"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "codex approval", func() bool { return len(r.c.approvals()) == 1 })
	if err := r.m.Post(tid, "$claude hand over"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "claude's first turn", func() bool { return r.c.turnDones("claude") == 1 })
	if tk := taskOf(t, r, tid, "t1"); tk.Status != store.TaskRunning || tk.Owner != "codex" {
		t.Fatalf("handed over but not yet seen: %+v", tk)
	}
	r.c.approvals()[0].Respond(agent.Decision{Allow: true})
	waitFor(t, "codex's second turn", func() bool { return r.c.turnDones("codex") == 2 })
	waitFor(t, "task done", func() bool { return taskOf(t, r, tid, "t1").Status == store.TaskDone })
	if got := taskOf(t, r, tid, "t1").Result; got != "did the second thing" {
		t.Fatalf("result = %q: the first turn must not have settled it", got)
	}
}

func TestHandoffRefusesAllAndUnknownTasks(t *testing.T) {
	r := newRig(t, []fake.Script{
		turn(handTo("all", "hi", "x", store.Contract{}), fake.Step{Message: &agent.Message{To: "codex", Text: "hi", Task: "t9"}}, say("tried"), finish()),
	}, nil)
	tid := r.thread(t)
	r.add(t, tid, "claude")
	r.add(t, tid, "codex")
	r.add(t, tid, "codex") // two others, so "all" is several agents
	if err := r.m.Post(tid, "$claude go"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "claude's turn", func() bool { return r.c.turnDones("claude") == 1 })
	res := r.c.toolResults("claude", "message")
	if len(res) != 2 {
		t.Fatalf("results = %q", res)
	}
	has(t, res[0], "single name")
	has(t, res[1], "no such task")
	if n := len(r.m.Tasks(tid)); n != 0 {
		t.Fatalf("a refused handover left %d tasks", n)
	}
}
