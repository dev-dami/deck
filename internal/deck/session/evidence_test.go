package session

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/dev-dami/deck/internal/deck/agent"
	"github.com/dev-dami/deck/internal/deck/agent/fake"
	"github.com/dev-dami/deck/internal/deck/store"
)

var toolSeq atomic.Int64

// ran is a command tool's result: cmd ran and exited 0 (ok) or not.
func ran(cmd string, ok bool) fake.Step {
	in, _ := json.Marshal(map[string]string{"command": cmd})
	st := agent.ToolOK
	if !ok {
		st = agent.ToolError
	}
	return fake.Step{Event: agent.Event{Kind: agent.KindToolResult, Tool: &agent.Tool{
		ID: fmt.Sprintf("bash-%d", toolSeq.Add(1)), Name: "Bash", Title: cmd, Input: in, Status: st,
	}}}
}

// op is an execute call of a Deck op.
func op(name string, args map[string]any) fake.Step {
	raw, _ := json.Marshal(args)
	return fake.Step{Exec: &fake.Exec{Op: name, Args: raw}}
}

// statusOf is task id's status, "" while there is no such task.
func statusOf(r *rig, tid, id string) store.TaskStatus {
	tk, _ := r.m.Task(tid, id)
	return tk.Status
}

func TestCheckMatches(t *testing.T) {
	for _, c := range []struct {
		cmd  string
		want bool
	}{
		{"go test ./pkg/x", true},
		{"go  test ./pkg/x -race -count=1", true},
		{"cd /repo && go test ./pkg/x && echo ok", true},
		{"bash -lc 'go test ./pkg/x'", true},
		{"go test ./pkg/xyz", false},
		{"go test ./pkg/x | tail -5", false},
		{"go test ./pkg/x || true", false},
		{"go test ./pkg/x; echo done", false},
		{"! go test ./pkg/x", false},
		{"go vet ./pkg/x", false},
	} {
		if got := checkMatches(c.cmd, "go test ./pkg/x"); got != c.want {
			t.Errorf("checkMatches(%q) = %v", c.cmd, got)
		}
	}
}

// task.done on a task with a check is weighed when the turn ends: done when
// the check passed after the last change, else to review and back to its
// owner, who runs it and reports again.
func TestDoneNeedsAPassingRunOfTheCheck(t *testing.T) {
	done := func(result string) fake.Step { return op("task.done", map[string]any{"id": "t1", "result": result}) }
	r := newRig(t, []fake.Script{
		// A pass, then a change after it: no evidence.
		turn(op("task.create", map[string]any{"title": "fix x", "owner": "me", "check": "go test ./pkg/x"}),
			edited("pkg/x/x.go", 1, 1), ran("go test ./pkg/x", true), edited("pkg/x/x.go", 1, 1),
			done("fixed"), say("fixed"), finish()),
		// Sent back: a failing run is no evidence either.
		turn(ran("go test ./pkg/x", false), done("fixed"), say("fixed"), finish()),
		// The check passes after the last change; task.done comes first, the
		// run after it in the same turn still counts.
		turn(edited("pkg/x/x.go", 1, 1), done("fixed, tests pass"), ran("go test ./pkg/x -count=1", true), say("fixed"), finish()),
	}, nil)
	tid := r.thread(t)
	r.add(t, tid, "claude")
	if err := r.m.postAt(tid, "$claude fix x"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "t1 done", func() bool { return statusOf(r, tid, "t1") == store.TaskDone })
	tk := taskOf(t, r, tid, "t1")
	if tk.Result != "fixed, tests pass" || tk.Reviews != 2 {
		t.Fatalf("task = %+v", tk)
	}
	res := r.c.toolResults("claude", "deck")
	has(t, res[1], "noted: t1 is done once Deck sees a passing run of `go test ./pkg/x` after your last change")
	ps := r.claude.prompts("claude")
	has(t, ps[1], "is not done yet: no passing run of `go test ./pkg/x` since the last change (round 1 of 2)")
	has(t, ps[2], "(round 2 of 2)")
	var moves []string
	for _, m := range r.c.all() {
		if c, ok := m.(TaskChangedMsg); ok && c.Task.ID == "t1" && c.From != c.Task.Status {
			moves = append(moves, string(c.Task.Status))
		}
	}
	if got := strings.Join(moves, ","); !strings.HasSuffix(got, "running,review,running,review,running,done") {
		t.Fatalf("moves = %s", got)
	}
	waitFor(t, "claude's third turn", func() bool { return r.c.turnDones("claude") == 3 })
	for i, want := range []agent.TurnEnd{agent.TurnContinue, agent.TurnContinue, agent.TurnDone} {
		if got := endOf(t, r, "claude", i); got != want {
			t.Errorf("turn %d ended %q, want %q", i+1, got, want)
		}
	}
}

// After store.MaxReviewChanges send-backs, the task is blocked for the user.
func TestMissingEvidenceBlocksAfterTheReviewRounds(t *testing.T) {
	done := op("task.done", map[string]any{"id": "t1", "result": "fixed"})
	r := newRig(t, []fake.Script{
		turn(op("task.create", map[string]any{"title": "fix x", "owner": "me", "check": "go test ./pkg/x"}), done, say("fixed"), finish()),
		turn(done, say("fixed"), finish()),
		turn(done, say("fixed"), finish()),
	}, nil)
	tid := r.thread(t)
	r.add(t, tid, "claude")
	if err := r.m.postAt(tid, "$claude fix x"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "t1 blocked", func() bool { return statusOf(r, tid, "t1") == store.TaskBlocked })
	if tk := taskOf(t, r, tid, "t1"); !strings.Contains(tk.Error, "review did not converge: after 2 rounds, still no passing run of `go test ./pkg/x`") {
		t.Fatalf("task = %+v", tk)
	}
	if n := r.c.turnDones("claude"); n != 3 {
		t.Fatalf("claude's turns = %d", n)
	}
}

// Tasks without a check finish on task.done at once, as before.
func TestDoneWithoutACheckIsImmediate(t *testing.T) {
	r := newRig(t, []fake.Script{
		turn(op("task.create", map[string]any{"title": "note it", "owner": "me"}),
			op("task.done", map[string]any{"id": "t1", "result": "noted"}), say("noted"), finish()),
	}, nil)
	tid := r.thread(t)
	r.add(t, tid, "claude")
	if err := r.m.postAt(tid, "$claude go"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "claude", func() bool { return r.c.turnDones("claude") == 1 })
	if res := r.c.toolResults("claude", "deck"); len(res) != 2 || !strings.Contains(res[1], "[done]") {
		t.Fatalf("results = %q", res)
	}
}

// A hand-off with a check is settled by the recipient's reply only when the
// recipient ran it; its turn ends done.
func TestHandoffWithACheckSettlesOnEvidence(t *testing.T) {
	r := newRig(t, []fake.Script{
		turn(fake.Step{Message: &agent.Message{To: "codex", Objective: "Fix the parser", Deliverable: "what changed", Check: "go test ./parser"}}, say("handed over"), finish()),
		turn(say("thanks"), finish()),
	}, []fake.Script{
		turn(edited("parser/parser.go", 1, 1), ran("go test ./parser", true), say("fixed the off-by-one"), finish()),
	})
	tid := r.thread(t)
	r.add(t, tid, "claude")
	r.add(t, tid, "codex")
	if err := r.m.postAt(tid, "$claude get the parser fixed"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "claude's second turn", func() bool { return r.c.turnDones("claude") == 2 })
	if tk := taskOf(t, r, tid, "t1"); tk.Status != store.TaskDone || tk.Result != "fixed the off-by-one" {
		t.Fatalf("task = %+v", tk)
	}
	if got := endOf(t, r, "claude", 0); got != agent.TurnHandoff {
		t.Fatalf("claude's turn end = %q", got)
	}
	if got := endOf(t, r, "codex", 0); got != agent.TurnDone {
		t.Fatalf("codex's turn end = %q", got)
	}
}

// Without the run, the reply sends the hand-off back to its owner instead.
func TestHandoffWithACheckIsSentBackWithoutEvidence(t *testing.T) {
	r := newRig(t, []fake.Script{
		turn(fake.Step{Message: &agent.Message{To: "codex", Objective: "Fix the parser", Deliverable: "what changed", Check: "go test ./parser"}}, say("handed over"), finish()),
		turn(say("thanks"), finish()),
	}, []fake.Script{
		turn(edited("parser/parser.go", 1, 1), say("fixed it, I think"), finish()),
		turn(ran("go test ./parser", true), say("tests pass now"), finish()),
	})
	tid := r.thread(t)
	r.add(t, tid, "claude")
	r.add(t, tid, "codex")
	if err := r.m.postAt(tid, "$claude get the parser fixed"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "codex's second turn", func() bool { return r.c.turnDones("codex") == 2 })
	waitFor(t, "task done", func() bool { return statusOf(r, tid, "t1") == store.TaskDone })
	has(t, r.codex.prompts("codex")[1], "is not done yet: no passing run of `go test ./parser` since the last change (round 1 of 2)")
	if got := endOf(t, r, "codex", 0); got != agent.TurnContinue {
		t.Fatalf("codex's first turn end = %q", got)
	}
	if got := endOf(t, r, "codex", 1); got != agent.TurnDone {
		t.Fatalf("codex's second turn end = %q", got)
	}
	if tk := taskOf(t, r, tid, "t1"); tk.Result != "tests pass now" || tk.Reviews != 1 {
		t.Fatalf("task = %+v", tk)
	}
}
