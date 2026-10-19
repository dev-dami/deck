package queue

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/dev-dami/deck/pkg/remote/wire"
)

var t0 = time.Date(2026, 10, 14, 21, 0, 0, 0, time.UTC)

func act(qid, dev string, created time.Time) wire.QueuedAction {
	return wire.QueuedAction{
		QID: qid, DeviceID: dev, CreatedAt: created, ExpiresAt: created.Add(8 * time.Hour),
		PJ: "prj_a", SessionID: "s1", Kind: wire.QKindChatSend, Payload: json.RawMessage(`{"text":"go on"}`),
	}
}

func open(t *testing.T) *Store {
	t.Helper()
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestEnqueueDedupsAndKeepsAppliedQIDs(t *testing.T) {
	s := open(t)
	ok, err := s.Enqueue(act("q1", "d1", t0), t0)
	if err != nil || !ok {
		t.Fatalf("first enqueue: %v %v", ok, err)
	}
	if ok, err := s.Enqueue(act("q1", "d1", t0), t0); err != nil || ok {
		t.Fatalf("redelivery of a waiting action: %v %v", ok, err)
	}
	if ok, err := s.Complete(wire.Receipt{QID: "q1", Status: wire.ReceiptApplied, At: t0}, "d1"); err != nil || !ok {
		t.Fatalf("complete: %v %v", ok, err)
	}
	// Exactly-once effect: a redelivery after it was applied is still a no-op.
	if ok, err := s.Enqueue(act("q1", "d1", t0), t0); err != nil || ok {
		t.Fatalf("redelivery of an applied action: %v %v", ok, err)
	}
	if p, _ := s.Pending(); len(p) != 0 {
		t.Fatalf("pending %v", p)
	}
}

func TestPendingIsFIFOByCreationThenArrival(t *testing.T) {
	s := open(t)
	// Arrival order differs from creation order, as after a flaky connection.
	for _, a := range []wire.QueuedAction{
		act("late", "d1", t0.Add(3*time.Minute)),
		act("early", "d1", t0),
		act("tie-a", "d2", t0.Add(time.Minute)),
		act("tie-b", "d2", t0.Add(time.Minute)),
	} {
		if _, err := s.Enqueue(a, t0); err != nil {
			t.Fatal(err)
		}
	}
	p, err := s.Pending()
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, a := range p {
		got = append(got, a.QID)
	}
	want := []string{"early", "tie-a", "tie-b", "late"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("order %v, want %v", got, want)
	}
}

func TestPerDeviceCap(t *testing.T) {
	s := open(t)
	for i := 0; i < wire.MaxQueuedPerDevice; i++ {
		if _, err := s.Enqueue(act(fmt.Sprint("q", i), "d1", t0), t0); err != nil {
			t.Fatalf("action %d: %v", i, err)
		}
	}
	if _, err := s.Enqueue(act("one-too-many", "d1", t0), t0); !errors.Is(err, ErrFull) {
		t.Fatalf("got %v", err)
	}
	if ok, err := s.Enqueue(act("other-phone", "d2", t0), t0); err != nil || !ok {
		t.Fatalf("another phone was blocked: %v %v", ok, err)
	}
	// Completing one frees a slot.
	s.Complete(wire.Receipt{QID: "q0", Status: wire.ReceiptApplied, At: t0}, "d1")
	if ok, err := s.Enqueue(act("now-fits", "d1", t0), t0); err != nil || !ok {
		t.Fatalf("slot not freed: %v %v", ok, err)
	}
}

func TestCompleteStoresTheReceiptAtomicallyAndOnce(t *testing.T) {
	s := open(t)
	s.Enqueue(act("q1", "d1", t0), t0)
	r := wire.Receipt{QID: "q1", Status: wire.ReceiptDropped, Reason: wire.ReasonExpired, At: t0.Add(time.Hour)}
	if ok, _ := s.Complete(r, "d1"); !ok {
		t.Fatal("not completed")
	}
	if ok, _ := s.Complete(wire.Receipt{QID: "q1", Status: wire.ReceiptApplied, At: t0.Add(2 * time.Hour)}, "d1"); ok {
		t.Fatal("an action was completed twice")
	}
	got, err := s.Receipts("d1", 10)
	if err != nil || len(got) != 1 || got[0].Status != wire.ReceiptDropped || got[0].Reason != wire.ReasonExpired || !got[0].At.Equal(r.At) {
		t.Fatalf("receipts %+v %v", got, err)
	}
	if other, _ := s.Receipts("d2", 10); len(other) != 0 {
		t.Fatal("another device sees the receipt")
	}
	if ok, _ := s.Complete(wire.Receipt{QID: "never-queued", Status: wire.ReceiptApplied, At: t0}, "d1"); ok {
		t.Fatal("a receipt was written for an action that never existed")
	}
}

func TestCancelOnlyByTheOwner(t *testing.T) {
	s := open(t)
	s.Enqueue(act("q1", "d1", t0), t0)
	if ok, err := s.Cancel("d2", "q1", t0); err != nil || ok {
		t.Fatalf("another phone cancelled it: %v %v", ok, err)
	}
	if ok, err := s.Cancel("d1", "q1", t0); err != nil || !ok {
		t.Fatalf("owner cancel: %v %v", ok, err)
	}
	if ok, _ := s.Cancel("d1", "q1", t0); ok {
		t.Fatal("cancelled twice")
	}
	r, _ := s.Receipts("d1", 5)
	if len(r) != 1 || r[0].Reason != wire.ReasonCancelled {
		t.Fatalf("receipts %+v", r)
	}
	if ok, _ := s.Cancel("d1", "unknown", t0); ok {
		t.Fatal("cancelled an unknown action")
	}
}

func TestSurvivesRestart(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	s.Enqueue(act("q1", "d1", t0), t0)
	s.Enqueue(act("q2", "d1", t0.Add(time.Second)), t0)
	s.Complete(wire.Receipt{QID: "q1", Status: wire.ReceiptApplied, At: t0}, "d1")
	s.Close()

	s, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	p, _ := s.Pending()
	if len(p) != 1 || p[0].QID != "q2" {
		t.Fatalf("pending after restart: %v", p)
	}
	if ok, _ := s.Enqueue(act("q1", "d1", t0), t0); ok {
		t.Fatal("an applied qid was forgotten across a restart")
	}
}

func TestPruneKeepsRecentAndDropsOld(t *testing.T) {
	s := open(t)
	s.Enqueue(act("old", "d1", t0), t0)
	s.Complete(wire.Receipt{QID: "old", Status: wire.ReceiptApplied, At: t0}, "d1")
	recent := t0.Add(6 * 24 * time.Hour)
	s.Enqueue(act("recent", "d1", recent), recent)
	s.Complete(wire.Receipt{QID: "recent", Status: wire.ReceiptApplied, At: recent}, "d1")
	s.Enqueue(act("waiting", "d1", t0), t0)

	if err := s.Prune(t0.Add(8 * 24 * time.Hour)); err != nil {
		t.Fatal(err)
	}
	if ok, _ := s.Enqueue(act("old", "d1", t0), t0.Add(8*24*time.Hour)); !ok {
		t.Fatal("an applied qid older than 7 days should have been forgotten")
	}
	if ok, _ := s.Enqueue(act("recent", "d1", recent), recent); ok {
		t.Fatal("a recent applied qid was forgotten")
	}
	if p, _ := s.Pending(); len(p) < 2 {
		t.Fatal("a waiting action was pruned")
	}
}

func TestForgetDevice(t *testing.T) {
	s := open(t)
	s.Enqueue(act("q1", "d1", t0), t0)
	s.Enqueue(act("q2", "d2", t0), t0)
	s.Complete(wire.Receipt{QID: "q2", Status: wire.ReceiptApplied, At: t0}, "d2")
	if err := s.ForgetDevice("d1"); err != nil {
		t.Fatal(err)
	}
	if p, _ := s.Pending(); len(p) != 0 {
		t.Fatalf("revoked phone's actions survived: %v", p)
	}
	if r, _ := s.Receipts("d2", 5); len(r) != 1 {
		t.Fatal("another phone's receipts were removed")
	}
}

func TestLastReceived(t *testing.T) {
	s := open(t)
	if got, _ := s.LastReceived(); !got.IsZero() {
		t.Fatalf("empty queue reports %v", got)
	}
	s.Enqueue(act("q1", "d1", t0), t0.Add(time.Minute))
	got, err := s.LastReceived()
	if err != nil || !got.Equal(t0.Add(time.Minute)) {
		t.Fatalf("got %v %v", got, err)
	}
}

func TestCancelAnyIgnoresWhoQueuedIt(t *testing.T) {
	s := open(t)
	s.Enqueue(act("q1", "d1", t0), t0)
	dev, ok, err := s.CancelAny("q1", t0)
	if err != nil || !ok || dev != "d1" {
		t.Fatalf("cancel: %q %v %v", dev, ok, err)
	}
	if _, ok, _ := s.CancelAny("q1", t0); ok {
		t.Fatal("cancelled twice")
	}
	if _, ok, _ := s.CancelAny("nope", t0); ok {
		t.Fatal("cancelled an unknown action")
	}
	r, _ := s.Receipts("d1", 5)
	if len(r) != 1 || r[0].Reason != wire.ReasonCancelled {
		t.Fatalf("receipt %+v", r)
	}
}
