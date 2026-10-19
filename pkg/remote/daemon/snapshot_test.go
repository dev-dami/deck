package daemon

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dev-dami/deck/pkg/remote/e2e"
	"github.com/dev-dami/deck/pkg/remote/wire"
)

type sink struct {
	mu   sync.Mutex
	got  map[string][]byte
	n    int
	fail map[string]bool
}

func (s *sink) Snapshot(h string, data []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.n++
	if s.fail[h] {
		return errors.New("relay down")
	}
	if s.got == nil {
		s.got = map[string][]byte{}
	}
	s.got[h] = data
	return nil
}

func (s *sink) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.n
}

func openSnapshot(t *testing.T, r *rig, sealed []byte) wire.Snapshot {
	t.Helper()
	raw, err := e2e.OpenK(r.phone, r.ident.StaticPublic(), r.ident.CID(), sealed)
	if err != nil {
		t.Fatalf("the phone could not open the snapshot: %v", err)
	}
	var s wire.Snapshot
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestSnapshotHoldsInboxApprovalsAndReceiptsAndOnlyThePhoneCanOpenIt(t *testing.T) {
	r := queueRig(t)
	seedApprovals(r)
	// One receipt on file.
	a := r.action(wire.QKindChatSend, map[string]string{"text": "x"})
	if err := r.mail(t, a); err != nil {
		t.Fatal(err)
	}
	r.drain(t)

	dev, _ := r.devs.Get(r.device.ID)
	snap := r.d.BuildSnapshot(context.Background(), dev)
	if len(snap.Inbox) == 0 || len(snap.Approvals) != 5 || len(snap.Receipts) != 1 || snap.Receipts[0].QID != a.QID {
		t.Fatalf("snapshot %+v", snap)
	}
	for _, p := range snap.Approvals {
		if p.PJ != r.pj || p.Approval.Digest == "" || p.Approval.Tier == "" {
			t.Fatalf("approval %+v", p)
		}
	}

	sealed, err := r.d.SealSnapshot(dev, snap)
	if err != nil {
		t.Fatal(err)
	}
	if got := openSnapshot(t, r, sealed); len(got.Approvals) != 5 || len(got.Receipts) != 1 {
		t.Fatalf("opened %+v", got)
	}
	stranger, _ := e2e.GenerateKeypair(rand.Reader)
	if _, err := e2e.OpenK(stranger, r.ident.StaticPublic(), r.ident.CID(), sealed); err == nil {
		t.Fatal("another device opened the snapshot")
	}
	if strings.Contains(string(sealed), "migration") || strings.Contains(string(sealed), "rm -rf") {
		t.Fatal("snapshot contents are visible in the sealed bytes")
	}
}

func TestSnapshotIsTrimmedToFitOneMessage(t *testing.T) {
	r := queueRig(t)
	big := strings.Repeat("+x\n", 40000)
	var many []wire.Approval
	for i := 0; i < 30; i++ {
		many = append(many, wire.Approval{ID: "a" + string(rune('A'+i)), SessionID: "s1", Kind: "edit", Path: "main.go", Diff: big, Raw: big, CreatedAt: time.Now()})
	}
	r.fake.Get("s1").Approvals = many

	dev, _ := r.devs.Get(r.device.ID)
	snap := r.d.BuildSnapshot(context.Background(), dev)
	if n := size(snap); n > maxSnapshotPlain {
		t.Fatalf("snapshot is %d bytes", n)
	}
	if len(snap.Approvals) == 0 || snap.Approvals[0].Approval.Diff != "" || !snap.Approvals[0].Approval.DiffTruncated {
		t.Fatalf("approvals should keep their place but lose their bodies: %+v", snap.Approvals)
	}
	if _, err := r.d.SealSnapshot(dev, snap); err != nil {
		t.Fatalf("trimmed snapshot does not seal: %v", err)
	}
}

func TestFitDropsLeastUsefulFirst(t *testing.T) {
	s := wire.Snapshot{Inbox: []wire.InboxItem{{Title: "keep me", Summary: "x"}}}
	for i := 0; i < 500; i++ {
		s.Receipts = append(s.Receipts, wire.Receipt{QID: strings.Repeat("q", 200), Status: wire.ReceiptApplied})
	}
	for i := 0; i < 300; i++ {
		s.Inbox = append(s.Inbox, wire.InboxItem{Title: strings.Repeat("t", 300), Summary: strings.Repeat("s", 280)})
	}
	got := fit(s)
	if size(got) > maxSnapshotPlain {
		t.Fatalf("still %d bytes", size(got))
	}
	if len(got.Inbox) == 0 || got.Inbox[0].Title != "keep me" {
		t.Fatal("the inbox head was dropped")
	}
	if len(got.Receipts) >= 500 {
		t.Fatal("receipts were not trimmed first")
	}
}

func TestPushSnapshotsReachesEveryPhoneEvenIfOneFails(t *testing.T) {
	r := queueRig(t)
	second, _ := e2e.GenerateKeypair(rand.Reader)
	dev2, _, err := r.devs.Add("Tablet", second.Public)
	if err != nil {
		t.Fatal(err)
	}
	s := &sink{fail: map[string]bool{r.device.RelayTokenHash: true}}
	r.d.cfg.Snapshots = s
	r.d.PushSnapshots(context.Background())
	if s.count() != 2 {
		t.Fatalf("sink called %d times", s.count())
	}
	raw, err := e2e.OpenK(second, r.ident.StaticPublic(), r.ident.CID(), s.got[dev2.RelayTokenHash])
	if err != nil {
		t.Fatalf("the second phone could not open its snapshot: %v", err)
	}
	var snap wire.Snapshot
	if json.Unmarshal(raw, &snap) != nil {
		t.Fatal("not a snapshot")
	}
}

func TestSnapshotsAreDebounced(t *testing.T) {
	r := queueRig(t)
	s := &sink{}
	r.d.cfg.Snapshots = s
	r.d.cfg.SnapshotDebounce = 80 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { r.d.RunSnapshots(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })

	for range 5 {
		r.d.snapshotDirty()
		time.Sleep(5 * time.Millisecond)
	}
	time.Sleep(400 * time.Millisecond)
	if n := s.count(); n != 1 {
		t.Fatalf("a burst of changes wrote %d snapshots, want 1 (one phone)", n)
	}
	r.d.Refresh()
	time.Sleep(300 * time.Millisecond)
	if n := s.count(); n != 2 {
		t.Fatalf("a refresh did not write a snapshot (count %d)", n)
	}
}

func TestNoSinkMeansNoSnapshots(t *testing.T) {
	r := queueRig(t)
	r.d.PushSnapshots(context.Background()) // must not panic
}
