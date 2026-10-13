package session

import (
	"context"
	"strings"
	"testing"

	"github.com/dev-dami/deck/internal/deck/agent/fake"
	"github.com/dev-dami/deck/internal/deck/sub"
)

// An agent captures what it changed since the user's message, the capsule
// lands on its task, and another tree of the same project applies it.
func TestCaptureFromCheckpointAndApplyElsewhere(t *testing.T) {
	repo := gitRepo(t)
	r := newRigWith(t, t.TempDir(), false, trackingCfg(), []fake.Script{turn(say("ok"), finish())}, nil)
	tid, err := r.m.NewThread(repo)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.m.Post(tid, "change a"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "claude's turn", func() bool { return r.c.turnDones("claude") >= 1 })

	th := r.m.thread(tid)
	th.mu.Lock()
	a := th.members[0]
	th.mu.Unlock()
	tk, err := r.m.addTask(th, TaskSpec{Title: "change a", Owner: a.id})
	if err != nil {
		t.Fatal(err)
	}

	writeFile(t, repo, "a.txt", "ONE\ntwo\n")
	writeFile(t, repo, "b.txt", "new\n")
	b := subBoard{m: r.m, a: a}
	ctx := context.Background()
	out, err := b.SubOp(ctx, "sub.patch.capture", map[string]any{"task": tk.ID})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "2 files (+1 ~1 -0)") || !strings.Contains(out, "the checkpoint") {
		t.Fatalf("capture said %q", out)
	}
	got, _ := r.m.Task(tid, tk.ID)
	if len(got.Capsules) != 1 {
		t.Fatalf("task capsules = %v", got.Capsules)
	}
	id := got.Capsules[0]

	// Nothing more changed: a second capture says so.
	if out, _ := b.SubOp(ctx, "sub.patch.capture", map[string]any{"base": ""}); !strings.Contains(out, "since") {
		t.Fatalf("second capture said %q", out)
	}

	other := gitRepo(t)
	if err := r.m.SubStore().Apply(ctx, other, sub.ID(id)); err != nil {
		t.Fatal(err)
	}
	if readFile(t, other, "a.txt") != "ONE\ntwo\n" || readFile(t, other, "b.txt") != "new\n" {
		t.Fatal("capsule did not apply in the other tree")
	}

	// The handover note names the capsule and how to use it.
	note, err := r.m.handCapsule(th, nil, "", id)
	if err != nil || !strings.Contains(note, id) {
		t.Fatalf("note %q: %v", note, err)
	}
	if _, err := r.m.handCapsule(th, nil, "", "blake3:nope"); err == nil {
		t.Fatal("a bad capsule id was accepted")
	}

	// Checkpoints are SUB snapshots now, and Changes reads them.
	cs, err := r.m.Changes(ctx, tid, "")
	if err != nil || !cs.Git || len(cs.Files) != 2 {
		t.Fatalf("changes: %+v %v", cs.Files, err)
	}
}
