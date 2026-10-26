package claude

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/dev-dami/deck/internal/deck/agent"
)

// TestChangeDuringRestartJoinsIt covers a model and an effort picked one
// after the other while idle: the effort lands while the model's restart is
// stopping the old process, so the new process starts with both and no
// second restart (another cold start) follows.
func TestChangeDuringRestartJoinsIt(t *testing.T) {
	ad, spec, argsFile := newFake(t)
	a := ad.(*adapter)
	r, err := a.Start(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })
	if err := r.Send("first"); err != nil {
		t.Fatal(err)
	}
	waitKind(t, r, agent.KindTurnDone)

	var once sync.Once
	a.beforeRespawn = func() {
		once.Do(func() {
			if err := r.(*run).SetEffort("high"); err != nil {
				t.Error(err)
			}
		})
	}
	if err := r.(agent.ModelSetter).SetModel("opus"); err != nil {
		t.Fatal(err)
	}

	all := launches(t, argsFile, 2)
	if v, _ := argAfter(all[1], "--model"); v != "opus" {
		t.Errorf("model %q", v)
	}
	if v, _ := argAfter(all[1], "--effort"); v != "high" {
		t.Errorf("effort %q", v)
	}
	if err := r.Send("again"); err != nil {
		t.Fatal(err)
	}
	waitKind(t, r, agent.KindTurnDone)
	time.Sleep(200 * time.Millisecond)
	if n := launchCount(t, argsFile); n != 2 {
		t.Fatalf("launches %d want 2 (one restart for both changes)", n)
	}
}

// settle widens restartSettle for one test.
func settle(t *testing.T, d time.Duration) {
	old := restartSettle
	restartSettle = d
	t.Cleanup(func() { restartSettle = old })
}

// TestIdleChangesSettleIntoOneRestart covers a model and then an effort
// picked a moment apart while idle: both land in one restart, which is
// announced (KindRestarting naming what changed, then KindRestarted).
func TestIdleChangesSettleIntoOneRestart(t *testing.T) {
	settle(t, 500*time.Millisecond)
	r, argsFile := startFake(t, nil)
	if err := r.Send("first"); err != nil {
		t.Fatal(err)
	}
	waitKind(t, r, agent.KindTurnDone)

	if err := r.(agent.ModelSetter).SetModel("opus"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	if err := r.(agent.EffortSetter).SetEffort("high"); err != nil {
		t.Fatal(err)
	}
	evs := waitKind(t, r, agent.KindRestarted)
	if evs[0].Kind != agent.KindRestarting || evs[0].Text != "model" {
		t.Fatalf("first event %s %q: %v", evs[0].Kind, evs[0].Text, kinds(evs))
	}
	noExitOrDone(t, evs, 0)
	all := launches(t, argsFile, 2)
	if v, _ := argAfter(all[1], "--model"); v != "opus" {
		t.Errorf("model %q", v)
	}
	if v, _ := argAfter(all[1], "--effort"); v != "high" {
		t.Errorf("effort %q", v)
	}
	time.Sleep(200 * time.Millisecond)
	if n := launchCount(t, argsFile); n != 2 {
		t.Fatalf("launches %d want 2 (one restart for both changes)", n)
	}
}

// TestChangeBackBeforeRestartSkipsIt covers a change undone while the
// restart settles: the process already runs with those flags, so it is
// kept, and the held prompt goes to it.
func TestChangeBackBeforeRestartSkipsIt(t *testing.T) {
	settle(t, 500*time.Millisecond)
	r, argsFile := startFake(t, nil)
	if err := r.Send("first"); err != nil {
		t.Fatal(err)
	}
	waitKind(t, r, agent.KindTurnDone)

	ms := r.(agent.ModelSetter)
	if err := ms.SetModel("opus"); err != nil {
		t.Fatal(err)
	}
	if err := ms.SetModel("sonnet"); err != nil {
		t.Fatal(err)
	}
	if err := r.Send("held"); err != nil {
		t.Fatal(err)
	}
	evs := waitKind(t, r, agent.KindTurnDone)
	noExitOrDone(t, evs, 1)
	if n := launchCount(t, argsFile); n != 1 {
		t.Fatalf("launches %d want 1", n)
	}
}
