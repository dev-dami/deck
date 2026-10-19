package session

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/dev-dami/deck/internal/deck/agent"
	"github.com/dev-dami/deck/internal/deck/agent/fake"
	"github.com/dev-dami/deck/internal/deck/sub"
)

func fileChange(p string) fake.Step {
	return fake.Step{Event: agent.Event{Kind: agent.KindFileChange, Diff: &agent.FileDiff{Path: p}}}
}

func TestLeaseOps(t *testing.T) {
	r := newRig(t, nil, nil)
	tid := r.thread(t)
	r.add(t, tid, "claude")
	r.add(t, tid, "codex")
	claude, codex := r.m.get("claude"), r.m.get("codex")

	out, err := r.m.acquireLease(claude, "internal/**, go.mod", 0, true, "moving the store")
	if err != nil {
		t.Fatal(err)
	}
	has(t, out, "leased internal/**, go.mod until", "exclusive")
	out, err = r.m.acquireLease(codex, "internal/store/x.go", 5, false, "")
	if err != nil {
		t.Fatal(err)
	}
	has(t, out, "also held", "claude holds internal/**, go.mod (exclusive) until", "moving the store")

	for _, bad := range []string{"/etc/passwd", "../x", "a/../../b", "a[", " , "} {
		if _, err := r.m.acquireLease(codex, bad, 0, false, ""); err == nil {
			t.Errorf("lease on %q", bad)
		}
	}
	has(t, r.m.listLeases(codex), "claude holds internal/**", "codex holds internal/store/x.go")
	if got := r.m.Leases(tid); len(got) != 2 || got[0].Member != "claude" || !got[0].Exclusive {
		t.Fatalf("Leases = %+v", got)
	}

	// Writes into the shared tree: another's exclusive lease blocks, one's own
	// and a shared lease do not.
	th := r.m.thread(tid)
	th.mu.Lock()
	blockCodex := th.leaseBlock(codex, []string{"internal/store/x.go"})
	blockClaude := th.leaseBlock(claude, []string{"internal/store/x.go", "go.mod"})
	th.mu.Unlock()
	if blockCodex == nil || !strings.Contains(blockCodex.Error(), "internal/store/x.go is leased: claude holds") {
		t.Fatalf("codex into claude's exclusive lease: %v", blockCodex)
	}
	if blockClaude != nil {
		t.Fatalf("claude blocked by a shared lease or its own: %v", blockClaude)
	}

	has(t, r.m.releaseLeases(codex, ""), "released 1")
	// A lease that ran out is gone.
	th.mu.Lock()
	th.leases[0].until = time.Now().Add(-time.Second)
	th.mu.Unlock()
	if got := r.m.listLeases(codex); got != "no leases" {
		t.Fatalf("after expiry: %q", got)
	}
}

func TestExclusiveLeaseRefusesCapsuleApply(t *testing.T) {
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
	r.add(t, tid, "codex")
	if err := r.m.SetMode(tid, agent.ModeYolo); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	s := r.m.SubStore()
	other := gitRepo(t)
	base, err := s.Snap(ctx, other)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, other, "a.txt", "from the capsule\n")
	id, err := s.Capture(ctx, other, base, "", sub.Meta{Source: "elsewhere"})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := r.m.acquireLease(r.m.get("codex"), "*.txt", 0, true, "rewriting it"); err != nil {
		t.Fatal(err)
	}
	b := subBoard{m: r.m, a: r.m.get("claude")}
	args := map[string]any{"id": string(id)}
	if _, err := b.SubOp(ctx, "sub.patch.apply", args); err == nil || !strings.Contains(err.Error(), "a.txt is leased: codex holds *.txt") {
		t.Fatalf("apply into an exclusive lease: %v", err)
	}
	if got := readFile(t, repo, "a.txt"); got != "one\ntwo\n" {
		t.Fatalf("written anyway: %q", got)
	}
	r.m.releaseLeases(r.m.get("codex"), "*.txt")
	if _, err := b.SubOp(ctx, "sub.patch.apply", args); err != nil || readFile(t, repo, "a.txt") != "from the capsule\n" {
		t.Fatalf("after the release: %v", err)
	}
}

func TestOverlappingEditsAreReported(t *testing.T) {
	r := newRig(t,
		[]fake.Script{turn(fileChange("a.go"), say("edited"), finish()), turn(say("noted"), finish())},
		[]fake.Script{turn(fileChange("a.go"), fileChange("b.go"), say("edited too"), finish())})
	tid := r.thread(t)
	r.add(t, tid, "claude")
	r.add(t, tid, "codex")
	if _, err := r.m.acquireLease(r.m.get("claude"), "b.go", 0, false, "next up"); err != nil {
		t.Fatal(err)
	}

	_ = r.m.Post(tid, "$claude go")
	waitFor(t, "claude", func() bool { return r.c.turnDones("claude") == 1 })
	_ = r.m.Post(tid, "$codex go")
	waitFor(t, "codex", func() bool { return r.c.turnDones("codex") == 1 })

	notes := func(sub string) int {
		return r.c.count(func(m tea.Msg) bool { n, ok := m.(NoticeMsg); return ok && strings.Contains(n.Text, sub) })
	}
	waitFor(t, "the overlap notice", func() bool { return notes("claude and codex both changed a.go") == 1 })
	waitFor(t, "the lease notice", func() bool { return notes("codex changed b.go, which claude holds b.go") == 1 })

	// claude sees both in its next prompt.
	_ = r.m.Post(tid, "$claude anything new?")
	waitFor(t, "claude again", func() bool { return r.c.turnDones("claude") == 2 })
	p := r.claude.prompts("claude")[1]
	has(t, p, "[deck] claude and codex both changed a.go", "[deck] codex changed b.go")
}

func TestLeaseOpsThroughTheHub(t *testing.T) {
	r := newRig(t, nil, []fake.Script{turn(
		fake.Step{Exec: &fake.Exec{Op: "lease.acquire", Args: []byte(`{"paths": "cmd/*.go", "exclusive": true, "minutes": 30}`)}},
		fake.Step{Exec: &fake.Exec{Op: "lease.list"}},
		say("ok"), finish())})
	tid := r.thread(t)
	r.add(t, tid, "codex")
	_ = r.m.Post(tid, "$codex go")
	waitFor(t, "codex", func() bool { return r.c.turnDones("codex") == 1 })
	res := r.c.toolResults("codex", "deck")
	if len(res) != 2 {
		t.Fatalf("results = %q", res)
	}
	has(t, res[0], "leased cmd/*.go until", "exclusive")
	has(t, res[1], "codex holds cmd/*.go (exclusive)")
}
