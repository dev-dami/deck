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
