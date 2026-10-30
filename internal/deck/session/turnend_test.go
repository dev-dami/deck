package session

import (
	"testing"

	"github.com/dev-dami/deck/internal/deck/agent"
	"github.com/dev-dami/deck/internal/deck/agent/fake"
	"github.com/dev-dami/deck/internal/deck/store"
)

// endOf is how member id's n-th turn ended, as its turn_done says.
func endOf(t *testing.T, r *rig, id string, n int) agent.TurnEnd {
	t.Helper()
	evs := r.c.events(func(e EventMsg) bool { return e.AgentID == id && e.Kind == agent.KindTurnDone })
	if n >= len(evs) {
		t.Fatalf("%s has %d turn_done events, want #%d", id, len(evs), n)
	}
	return evs[n].End
}

func TestTurnEnds(t *testing.T) {
	r := newRig(t, []fake.Script{
		turn(say("Here is the plan."), finish()),                                                        // continue
		turn(say("I can do it two ways.\n\nWhich do you want?"), finish()),                              // blocked: asks the user
		turn(contractTo("codex", "Review the plan", "what to change"), say("handed it over"), finish()), // handoff
		turn(op("task.create", map[string]any{"title": "note it", "owner": "me"}),
			op("task.done", map[string]any{"id": "t2", "result": "noted"}), say("noted"), finish()), // done
		turn(op("task.create", map[string]any{"title": "deploy", "owner": "me"}),
			op("task.update", map[string]any{"id": "t3", "status": "blocked", "note": "needs the user's token"}), say("blocked on a token"), finish()), // blocked
	}, []fake.Script{
		turn(say("looks fine"), finish()),
	})
	tid := r.thread(t)
	r.add(t, tid, "claude")
	r.add(t, tid, "codex")
	want := []agent.TurnEnd{agent.TurnContinue, agent.TurnBlocked, agent.TurnHandoff, agent.TurnDone, agent.TurnBlocked}
	for i := range want {
		if err := r.m.postAt(tid, "$claude next"); err != nil {
			t.Fatal(err)
		}
		waitFor(t, "claude's turn", func() bool { return r.c.turnDones("claude") >= i+1 })
	}
	// The hand-off's task was settled by codex's reply, and that turn of
	// codex's ended done.
	waitFor(t, "codex", func() bool { return r.c.turnDones("codex") == 1 })
	waitFor(t, "t1 done", func() bool { return taskOf(t, r, tid, "t1").Status == store.TaskDone })
	if got := endOf(t, r, "codex", 0); got != agent.TurnDone {
		t.Errorf("codex's turn end = %q", got)
	}
	for i, w := range want {
		if got := endOf(t, r, "claude", i); got != w {
			t.Errorf("claude's turn %d ended %q, want %q", i+1, got, w)
		}
	}
	// The log keeps it.
	r.m.Flush()
	entries, _, _ := r.st.LoadEntries(tid)
	var logged []agent.TurnEnd
	for _, e := range entries {
		if e.Type == store.EntryEvent && e.Kind == agent.KindTurnDone && e.AgentID == "claude" {
			logged = append(logged, e.End)
		}
	}
	if len(logged) < len(want) || logged[1] != agent.TurnBlocked {
		t.Fatalf("logged ends = %v", logged)
	}
}

func TestEndsAsking(t *testing.T) {
	for reply, want := range map[string]bool{
		"Shall I push it?":           true,
		"Done.\n\n**Push it now?**":  true,
		"Is it fine? I checked. Yes": false,
		"":                           false,
	} {
		if got := endsAsking(reply); got != want {
			t.Errorf("endsAsking(%q) = %v", reply, got)
		}
	}
}
