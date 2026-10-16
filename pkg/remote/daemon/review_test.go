package daemon

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dev-dami/deck/pkg/remote/remotetest"
	"github.com/dev-dami/deck/pkg/remote/wire"
)

// uiFake is a provider with a screen, like the terminal UI.
type uiFake struct {
	*remotetest.Fake
	mu    sync.Mutex
	notes []string
}

func (u *uiFake) HasUI() bool { return true }

func (u *uiFake) Notify(text string) error {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.notes = append(u.notes, text)
	return nil
}

func (u *uiFake) noted() []string {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]string(nil), u.notes...)
}

func uiRig(t *testing.T) (*rig, *uiFake) {
	t.Helper()
	r := queueRig(t)
	ui := &uiFake{Fake: r.fake}
	r.d.Detach(r.pj, r.fake)
	r.d.Attach(r.pj, ui)
	r.d.cfg.VetoWindow = 200 * time.Millisecond
	return r, ui
}

func TestScreenIsToldAndGivenTimeToCancel(t *testing.T) {
	r, ui := uiRig(t)
	keep := r.action(wire.QKindChatSend, map[string]string{"text": "keep"})
	drop := r.action(wire.QKindChatSend, map[string]string{"text": "drop"})
	for _, a := range []wire.QueuedAction{keep, drop} {
		if err := r.mail(t, a); err != nil {
			t.Fatal(err)
		}
	}

	done := make(chan struct{})
	go func() { r.drain(t); close(done) }()
	deadline := time.Now().Add(3 * time.Second)
	for len(ui.noted()) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the screen was not told")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if n := ui.noted()[0]; !strings.Contains(n, "2 actions") || !strings.Contains(n, "deck mobile queue cancel") {
		t.Fatalf("notice %q", n)
	}
	if len(r.fake.Sent()) != 0 {
		t.Fatal("ran before the window ended")
	}
	if ok, err := r.d.CancelAction(drop.QID); err != nil || !ok {
		t.Fatalf("cancel: %v %v", ok, err)
	}
	<-done

	if s := r.fake.Sent(); len(s) != 1 || s[0].Text != "keep" {
		t.Fatalf("sent %+v", s)
	}
	wantReceipt(t, r.receipt(t, drop.QID), wire.ReceiptDropped, wire.ReasonCancelled)
	wantReceipt(t, r.receipt(t, keep.QID), wire.ReceiptApplied, "")
}

func TestWithoutAScreenNothingWaits(t *testing.T) {
	r := queueRig(t)
	r.d.cfg.VetoWindow = 5 * time.Second
	a := r.action(wire.QKindChatSend, map[string]string{"text": "now"})
	if err := r.mail(t, a); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	r.drain(t)
	if time.Since(start) > time.Second {
		t.Fatalf("a headless project waited %v", time.Since(start))
	}
	if len(r.fake.Sent()) != 1 {
		t.Fatal("did not run")
	}
}

func TestCancelAfterTheWindowChangesNothing(t *testing.T) {
	r, _ := uiRig(t)
	a := r.action(wire.QKindChatSend, map[string]string{"text": "x"})
	if err := r.mail(t, a); err != nil {
		t.Fatal(err)
	}
	r.drain(t)
	if ok, _ := r.d.CancelAction(a.QID); ok {
		t.Fatal("cancelled an action that already ran")
	}
	wantReceipt(t, r.receipt(t, a.QID), wire.ReceiptApplied, "")
}

func TestPendingActionsListsWhatWaits(t *testing.T) {
	r := queueRig(t)
	r.d.Detach(r.pj, r.fake) // keep the action waiting
	a := r.action(wire.QKindChatSend, map[string]string{"text": "later"})
	if err := r.mail(t, a); err != nil {
		t.Fatal(err)
	}
	got, err := r.d.PendingActions()
	if err != nil || len(got) != 1 || got[0].QID != a.QID {
		t.Fatalf("%v %v", got, err)
	}
	if ok, _ := r.d.CancelAction("no-such"); ok {
		t.Fatal("cancelled an unknown action")
	}
	_ = context.Background()
}
