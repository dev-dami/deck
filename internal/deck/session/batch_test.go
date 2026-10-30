package session

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/dev-dami/deck/internal/deck/agent"
	"github.com/dev-dami/deck/internal/deck/agent/fake"
	"github.com/dev-dami/deck/internal/deck/store"
)

func spawnStep(brief string) fake.Step {
	return opStep("agent.spawn", fmt.Sprintf(`{"brief":%q,"kind":"codex"}`, brief))
}

// tasksBy lists the tasks member creator handed out.
func tasksBy(r *rig, tid, creator string) []store.Task {
	var out []store.Task
	for _, tk := range r.m.Tasks(tid) {
		if tk.Creator == creator {
			out = append(out, tk)
		}
	}
	return out
}

// A lead that hands work to three subagents is woken once, when the last of
// them has reported, with every report in that one prompt.
func TestReportsWakeTheCreatorOncePerBatch(t *testing.T) {
	r, tid := treeRig(t, testCfg(), []fake.Script{
		turn(spawnStep("part a"), spawnStep("part b"), spawnStep("part c"), say("three helpers on it"), finish()),
		turn(say("all three are in"), finish()),
		turn(say("unexpected"), finish()),
	}, []fake.Script{
		turn(say("my part is finished"), finish()),
	})
	if err := r.m.Post(tid, "split it three ways"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "three tasks done", func() bool {
		tks := tasksBy(r, tid, "claude")
		n := 0
		for _, tk := range tks {
			if tk.Status == store.TaskDone {
				n++
			}
		}
		return len(tks) == 3 && n == 3
	})
	waitFor(t, "the lead's report turn", func() bool { return r.c.turnDones("claude") == 2 })
	time.Sleep(60 * time.Millisecond) // time for a third turn, were one coming
	ps := r.claude.prompts("claude")
	if len(ps) != 2 {
		t.Fatalf("the lead had %d prompts, want 2 (its own and one for the batch):\n%s", len(ps), strings.Join(ps, "\n---\n"))
	}
	has(t, ps[1], "Everything you handed out has ended; the 3 reports follow")
	for _, tk := range tasksBy(r, tid, "claude") {
		has(t, ps[1], fmt.Sprintf("task %s %q done: my part is finished", tk.ID, tk.Title))
	}
	// Each report was logged and shown as it came, not only at the wake.
	var reports int
	for _, rl := range r.c.relays() {
		if rl.Via == "task" && len(rl.To) == 1 && rl.To[0] == "claude" {
			reports++
		}
	}
	if reports != 3 {
		t.Fatalf("report relays = %d, want 3", reports)
	}
}

// A failure wakes the creator at once, naming what still runs; the rest
// still come back together later.
func TestAFailedTaskWakesTheCreatorAtOnce(t *testing.T) {
	r, tid := treeRig(t, testCfg(), []fake.Script{
		turn(spawnStep("part a"), spawnStep("part b"), say("two helpers on it"), finish()),
		turn(say("one failed"), finish()),
		turn(say("the other is in"), finish()),
	}, []fake.Script{
		turn(blockStep("ap"), say("my part is finished"), finish()),
	})
	if err := r.m.Post(tid, "split it two ways"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the lead's turn", func() bool { return r.c.turnDones("claude") == 1 })
	var kids []string
	waitFor(t, "both helpers waiting on approval", func() bool {
		kids = nil
		for _, a := range r.c.approvals() {
			kids = append(kids, a.AgentID)
		}
		return len(kids) == 2
	})
	tks := tasksBy(r, tid, "claude")
	if len(tks) != 2 {
		t.Fatalf("tasks = %+v", tks)
	}
	if _, err := r.m.FailTask(tid, tks[0].ID, "", "the build broke"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the lead woken by the failure", func() bool { return r.c.turnDones("claude") == 2 })
	p := r.claude.prompts("claude")[1]
	has(t, p, fmt.Sprintf("task %s %q failed: the build broke", tks[0].ID, tks[0].Title),
		fmt.Sprintf("%s %q failed, so you hear now; still running: %s.", tks[0].ID, tks[0].Title, tks[1].ID))

	// The other helper finishes: its report wakes the lead on its own.
	for _, a := range r.c.approvals() {
		if a.AgentID != tks[0].Owner {
			a.Respond(agent.Decision{Allow: true})
		}
	}
	waitFor(t, "the last report", func() bool { return r.c.turnDones("claude") == 3 })
	has(t, r.claude.prompts("claude")[2], fmt.Sprintf("task %s %q done: my part is finished", tks[1].ID, tks[1].Title))
}

// Sub-threads a lead opens report in one batch as well.
func TestSubThreadReportsComeBackTogether(t *testing.T) {
	r := newRig(t, []fake.Script{
		turn(opStep("thread.open", `{"title":"Docs","brief":"draft the docs","kind":"codex"}`),
			opStep("thread.open", `{"title":"Tests","brief":"draft the tests","kind":"codex"}`), say("asked two sub-threads"), finish()),
		turn(say("both are in"), finish()),
		turn(say("unexpected"), finish()),
	}, []fake.Script{
		turn(say("drafted"), finish()),
	})
	tid := r.thread(t)
	if err := r.m.Post(tid, "docs and tests, in sub-threads"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the lead hears back", func() bool { return r.c.turnDones("claude") == 2 })
	time.Sleep(60 * time.Millisecond)
	ps := r.claude.prompts("claude")
	if len(ps) != 2 {
		t.Fatalf("the lead had %d prompts, want 2:\n%s", len(ps), strings.Join(ps, "\n---\n"))
	}
	has(t, ps[1], `[sub-thread "Docs"] drafted`, `[sub-thread "Tests"] drafted`, "the 2 reports follow")
}
