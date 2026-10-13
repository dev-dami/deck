package session

import (
	"context"
	"strings"
	"testing"

	"github.com/dev-dami/deck/internal/deck/agent"
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

// Applying a capsule writes the agent's tree, so it is asked like an edit:
// plan mode refuses, ask mode waits for the user, yolo just applies.
func TestApplyIsApprovedLikeAnEdit(t *testing.T) {
	repo := gitRepo(t)
	r := newRigWith(t, t.TempDir(), false, trackingCfg(), []fake.Script{turn(say("ok"), finish())}, nil)
	tid, err := r.m.NewThread(repo)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.m.Post(tid, "go"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "claude's turn", func() bool { return r.c.turnDones("claude") >= 1 })
	th := r.m.thread(tid)
	th.mu.Lock()
	a := th.members[0]
	th.mu.Unlock()

	// A capsule made in another tree of the project.
	ctx := context.Background()
	s := r.m.SubStore()
	other := gitRepo(t)
	base, err := s.Snap(ctx, other)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, other, "a.txt", "from the capsule\n")
	id, err := s.Capture(ctx, other, base, "", sub.Meta{Source: "elsewhere"})
	if err != nil || id == "" {
		t.Fatalf("capture: %v", err)
	}
	b := subBoard{m: r.m, a: a}
	args := map[string]any{"id": string(id)}

	if err := r.m.SetMode(tid, agent.ModePlan); err != nil {
		t.Fatal(err)
	}
	if _, err := b.SubOp(ctx, "sub.patch.apply", args); err == nil || readFile(t, repo, "a.txt") != "one\ntwo\n" {
		t.Fatalf("plan mode applied: %v", err)
	}

	if err := r.m.SetMode(tid, agent.ModeAsk); err != nil {
		t.Fatal(err)
	}
	before := len(r.c.approvals())
	done := make(chan error, 1)
	go func() {
		_, err := b.SubOp(ctx, "sub.patch.apply", args)
		done <- err
	}()
	waitFor(t, "the approval", func() bool { return len(r.c.approvals()) > before })
	ap := r.c.approvals()[before]
	if ap.ToolName != "sub.patch.apply" || !strings.Contains(ap.Summary, "a.txt") {
		t.Fatalf("approval = %+v", ap.ApprovalRequest)
	}
	ap.Respond(agent.Decision{Allow: false})
	if err := <-done; err == nil || !strings.Contains(err.Error(), "declined") || readFile(t, repo, "a.txt") != "one\ntwo\n" {
		t.Fatalf("declined apply: %v", err)
	}

	if err := r.m.SetMode(tid, agent.ModeYolo); err != nil {
		t.Fatal(err)
	}
	if _, err := b.SubOp(ctx, "sub.patch.apply", args); err != nil || readFile(t, repo, "a.txt") != "from the capsule\n" {
		t.Fatalf("yolo apply: %v", err)
	}
	// A reviewer's capture now builds on the applied capsule.
	writeFile(t, repo, "a.txt", "fixed\n")
	out, err := b.SubOp(ctx, "sub.patch.capture", nil)
	if err != nil {
		t.Fatal(err)
	}
	fix := sub.ID(strings.Fields(strings.TrimPrefix(out, "capsule "))[0])
	fix = sub.ID(strings.TrimSuffix(string(fix), ":"))
	chain, err := s.Chain(fix)
	if err != nil || len(chain) != 2 {
		t.Fatalf("fix chain = %d: %v (%q)", len(chain), err, out)
	}
}
