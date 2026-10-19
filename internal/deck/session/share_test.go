package session

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/dev-dami/deck/internal/deck/agent"
	"github.com/dev-dami/deck/internal/deck/agent/fake"
)

func TestSharedThreadRoles(t *testing.T) {
	r := newRig(t, []fake.Script{
		turn(say("hello from claude"), finish()),
		turn(blockStep("ap"), say("ran it"), finish()),
	}, nil)
	tid := r.thread(t)
	r.add(t, tid, "claude")
	_ = r.m.Post(tid, "$claude say hi")
	waitFor(t, "the first reply", func() bool { return r.c.turnDones(r.m.Threads()[0].Members[0].ID) >= 1 })
	ctx := context.Background()

	if _, err := r.m.ReadShared(ctx, "vps", tid, 0, 0); err == nil || !strings.Contains(err.Error(), "no such shared thread") {
		t.Fatalf("read of a thread not shared: %v", err)
	}
	if err := r.m.SetShare(tid, "vps", "boss"); err == nil {
		t.Fatal("an unknown role")
	}
	if err := r.m.SetShare(tid, "vps", ShareObserver); err != nil {
		t.Fatal(err)
	}
	if got := r.m.SharedWith("vps"); len(got) != 1 || got[0].ID != tid || got[0].Role != ShareObserver || got[0].Members[0] != "claude" {
		t.Fatalf("SharedWith = %+v", got)
	}
	if len(r.m.SharedWith("pc")) != 0 {
		t.Fatal("shared with a teammate it was not shared with")
	}
	mr, err := r.m.ReadShared(ctx, "vps", tid, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	has(t, strings.Join(mr.Lines, "\n"), "say hi", "hello from claude")
	if err := r.m.PostShared("vps", tid, "hi"); err == nil || !strings.Contains(err.Error(), "needs participant") {
		t.Fatalf("an observer posted: %v", err)
	}

	// A read with nothing new waits for the thread to grow.
	got := make(chan Mirror, 1)
	go func() {
		m2, _ := r.m.ReadShared(ctx, "vps", tid, mr.Next, 10*time.Second)
		got <- m2
	}()
	time.Sleep(50 * time.Millisecond)
	if err := r.m.SetShare(tid, "vps", ShareParticipant); err != nil {
		t.Fatal(err)
	}
	if err := r.m.SetMode(tid, agent.ModeYolo); err != nil {
		t.Fatal(err)
	}
	if err := r.m.PostShared("vps", tid, "run $codex×3 make please"); err != nil {
		t.Fatal(err)
	}
	select {
	case m2 := <-got:
		has(t, strings.Join(m2.Lines, "\n"), "[vps] run ＄codex×3 make please")
	case <-time.After(10 * time.Second):
		t.Fatal("the waiting read did not wake")
	}
	if info, _ := r.m.Thread(tid); info.Mode != agent.ModeAsk {
		t.Fatalf("a teammate's post ran in %s mode", info.Mode)
	}
	if n := len(r.m.Threads()[0].Members); n != 1 {
		t.Fatalf("a teammate's post added agents: %d members", n)
	}
	lead := r.m.Threads()[0].Members[0].ID
	ap := approvalOf(t, r, lead)
	has(t, strings.Join(r.claude.prompts(lead), ""), "[vps] run ＄codex×3 make please")

	// Only an editor answers approvals.
	if err := r.m.AnswerShared("vps", tid, ap.ID, true); err == nil {
		t.Fatal("a participant answered an approval")
	}
	if err := r.m.SetShare(tid, "vps", ShareEditor); err != nil {
		t.Fatal(err)
	}
	mr, _ = r.m.ReadShared(ctx, "vps", tid, 0, 0)
	if len(mr.Pending) != 1 || mr.Pending[0].ID != ap.ID {
		t.Fatalf("pending = %+v", mr.Pending)
	}
	if err := r.m.AnswerShared("vps", tid, ap.ID, true); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the turn ends", func() bool { return r.c.turnDones(lead) >= 2 })
	if err := r.m.AnswerShared("vps", tid, ap.ID, true); err == nil {
		t.Fatal("answered twice")
	}

	// Posts are limited, and the share ends.
	for i := 0; i < maxSharedPosts; i++ {
		_ = r.m.PostShared("vps", tid, "more")
	}
	if err := r.m.PostShared("vps", tid, "more"); err == nil || !strings.Contains(err.Error(), "slow down") {
		t.Fatalf("no limit: %v", err)
	}
	if err := r.m.SetShare(tid, "vps", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := r.m.ReadShared(ctx, "vps", tid, 0, 0); err == nil {
		t.Fatal("read after the share ended")
	}
	if sh := r.m.snapshot(r.m.thread(tid)).Shares; len(sh) != 0 {
		t.Fatalf("stored shares = %+v", sh)
	}
}
