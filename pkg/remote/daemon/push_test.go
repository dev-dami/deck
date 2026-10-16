package daemon

import (
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/dev-dami/deck/pkg/remote"
	"github.com/dev-dami/deck/pkg/remote/devstore"
	"github.com/dev-dami/deck/pkg/remote/e2e"
	"github.com/dev-dami/deck/pkg/remote/push"
	"github.com/dev-dami/deck/pkg/remote/wire"
)

type sent struct {
	hash   string
	body   []byte
	ttl    time.Duration
	urgent bool
}

type fakePusher struct {
	mu   sync.Mutex
	got  []sent
	fail bool
}

func (f *fakePusher) Push(h string, b []byte, ttl time.Duration, urgent bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.got = append(f.got, sent{h, b, ttl, urgent})
	if f.fail {
		return errors.New("relay down")
	}
	return nil
}

func (f *fakePusher) all() []sent {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]sent(nil), f.got...)
}

// subscription is a browser's push subscription: keys the daemon encrypts to.
type subscription struct {
	priv *ecdh.PrivateKey
	auth []byte
}

func newSub(t *testing.T) subscription {
	t.Helper()
	k, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	auth := make([]byte, 16)
	_, _ = rand.Read(auth)
	return subscription{k, auth}
}

func (s subscription) params(approvals, done bool) wire.PushRegisterParams {
	b := base64.RawURLEncoding
	return wire.PushRegisterParams{P256dh: b.EncodeToString(s.priv.PublicKey().Bytes()), Auth: b.EncodeToString(s.auth), Approvals: approvals, Done: done}
}

func (s subscription) open(t *testing.T, body []byte) wire.PushMessage {
	t.Helper()
	plain, err := push.Decrypt(body, s.priv, s.auth)
	if err != nil {
		t.Fatalf("the phone cannot read the push: %v", err)
	}
	var m wire.PushMessage
	if err := json.Unmarshal(plain, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

// pushRig is a rig whose phone registered for pushes and then went away.
func pushRig(t *testing.T, approvals, done bool) (*rig, *fakePusher, subscription) {
	t.Helper()
	r := newRig(t)
	p := &fakePusher{}
	r.d.cfg.Pusher = p
	sub := newSub(t)
	cl, _ := r.connect(nil)
	okResult[wire.Empty](t, cl.call("", wire.MPushRegister, sub.params(approvals, done)))
	cl.cancel()
	<-cl.done
	return r, p, sub
}

func (r *rig) approvalEvent(a wire.Approval) {
	r.fake.Get("s1").Approvals = append(r.fake.Get("s1").Approvals, a)
	r.fake.Inject(remote.Event{Name: wire.EvApprovalRequested, SessionID: a.SessionID, Payload: a})
}

func waitSent(t *testing.T, p *fakePusher, n int) []sent {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for len(p.all()) < n {
		if time.Now().After(deadline) {
			t.Fatalf("expected %d pushes, got %d", n, len(p.all()))
		}
		time.Sleep(10 * time.Millisecond)
	}
	return p.all()
}

func noMore(t *testing.T, p *fakePusher, n int) {
	t.Helper()
	time.Sleep(300 * time.Millisecond)
	if got := len(p.all()); got != n {
		t.Fatalf("%d pushes, want %d", got, n)
	}
}

func TestRegisterValidatesKeysAndCanUnregister(t *testing.T) {
	r := newRig(t)
	cl, _ := r.connect(nil)
	wantErr(t, cl.call("", wire.MPushRegister, wire.PushRegisterParams{P256dh: "junk", Auth: "junk", Approvals: true}), wire.CodeInvalid)
	if d, _ := r.devs.Get(r.device.ID); d.Push != nil {
		t.Fatal("invalid keys were stored")
	}
	sub := newSub(t)
	okResult[wire.Empty](t, cl.call("", wire.MPushRegister, sub.params(true, false)))
	if d, _ := r.devs.Get(r.device.ID); d.Push == nil || !d.Push.Approvals || d.Push.Done {
		t.Fatalf("stored %+v", d.Push)
	}
	okResult[wire.Empty](t, cl.call("", wire.MPushRegister, wire.PushRegisterParams{}))
	if d, _ := r.devs.Get(r.device.ID); d.Push != nil {
		t.Fatal("unregistering left keys behind")
	}
}

func TestApprovalPushIsEncryptedAndCarriesWhatTheLockScreenNeeds(t *testing.T) {
	r, p, sub := pushRig(t, true, false)
	a := wire.Approval{ID: "apr1", SessionID: "s1", Kind: "command", Command: "go test ./...", CreatedAt: time.Now()}
	r.approvalEvent(a)
	got := waitSent(t, p, 1)[0]

	if got.hash != r.device.RelayTokenHash || !got.urgent || got.ttl != approvalPushTTL {
		t.Fatalf("delivery %+v", got)
	}
	if string(got.body) == "" || json.Valid(got.body) {
		t.Fatal("the body must be ciphertext, not JSON")
	}
	m := sub.open(t, got.body)
	if m.CID != r.ident.CID() {
		t.Fatalf("push routes to %q, want %q", m.CID, r.ident.CID())
	}
	if m.Kind != wire.PushApproval || m.PJ != r.pj || m.SessionID != "s1" || m.Title != "deck" || m.Body != "Run: go test ./..." {
		t.Fatalf("message %+v", m)
	}
	if m.ApprovalID != "apr1" || m.Digest != Digest(a) || m.Tier != wire.TierDestructive {
		// The default gate calls everything destructive; the service worker
		// must not offer an Approve action for it.
		t.Fatalf("approval fields %+v", m)
	}
}

func TestNormalTierPushLetsTheWorkerApprove(t *testing.T) {
	r, p, sub := pushRig(t, true, false)
	r.d.cfg.Gate = NewGate(GateConfig{CID: r.ident.CID(), Devices: r.devs, Projects: r.reg})
	r.approvalEvent(wire.Approval{ID: "apr1", SessionID: "s1", Kind: "command", Command: "go test ./...", CreatedAt: time.Now()})
	r.approvalEvent(wire.Approval{ID: "apr2", SessionID: "s1", Kind: "command", Command: "rm -rf build", CreatedAt: time.Now()})
	got := waitSent(t, p, 2)
	tiers := map[string]string{}
	for _, s := range got {
		m := sub.open(t, s.body)
		tiers[m.ApprovalID] = m.Tier
	}
	if tiers["apr1"] != wire.TierNormal || tiers["apr2"] != wire.TierDestructive {
		t.Fatalf("tiers %v", tiers)
	}
}

func TestNoPushWhileThePhoneIsConnectedOrDidNotAsk(t *testing.T) {
	r, p, _ := pushRig(t, true, false)
	// Connected: the phone sees it live.
	cl, _ := r.connect(nil)
	r.approvalEvent(wire.Approval{ID: "a1", SessionID: "s1", Kind: "command", Command: "x", CreatedAt: time.Now()})
	noMore(t, p, 0)
	cl.cancel()
	<-cl.done

	// Done pushes were not asked for.
	r.fake.Inject(remote.Event{Name: wire.EvBusy, SessionID: "s1", Payload: wire.BusyChanged{SessionID: "s1", Busy: false}})
	noMore(t, p, 0)

	// Disconnected again: now it is told.
	r.approvalEvent(wire.Approval{ID: "a2", SessionID: "s1", Kind: "command", Command: "y", CreatedAt: time.Now()})
	waitSent(t, p, 1)
}

func TestDonePushOnlyWhenTrulyFinished(t *testing.T) {
	r, p, sub := pushRig(t, false, true)
	r.fake.Get("s1").Session.Title = "Fix flaky queue test"

	r.fake.Inject(remote.Event{Name: wire.EvBusy, SessionID: "s1", Payload: wire.BusyChanged{SessionID: "s1", Busy: true}})
	noMore(t, p, 0) // starting is not finishing

	r.fake.Get("s1").Approvals = []wire.Approval{{ID: "a", SessionID: "s1", Kind: "command", Command: "x", CreatedAt: time.Now()}}
	r.fake.Inject(remote.Event{Name: wire.EvBusy, SessionID: "s1", Payload: wire.BusyChanged{SessionID: "s1", Busy: false}})
	noMore(t, p, 0) // stopped to wait for an answer, which has its own push

	r.fake.Get("s1").Approvals = nil
	r.fake.Inject(remote.Event{Name: wire.EvBusy, SessionID: "s1", Payload: wire.BusyChanged{SessionID: "s1", Busy: false}})
	got := waitSent(t, p, 1)[0]
	if got.urgent || got.ttl != donePushTTL {
		t.Fatalf("delivery %+v", got)
	}
	m := sub.open(t, got.body)
	if m.Kind != wire.PushDone || m.Body != "Fix flaky queue test finished" || m.ApprovalID != "" {
		t.Fatalf("message %+v", m)
	}
}

func TestOneFailingPushDoesNotStopTheOthers(t *testing.T) {
	r, p, _ := pushRig(t, true, false)
	p.fail = true
	second := newSub(t)
	// A second phone.
	phone2, _ := e2e.GenerateKeypair(rand.Reader)
	dev2, _, err := r.devs.Add("Tablet", phone2.Public)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.devs.SetPush(dev2.ID, &devstore.PushPrefs{P256dh: second.params(true, false).P256dh, Auth: second.params(true, false).Auth, Approvals: true}); err != nil {
		t.Fatal(err)
	}
	r.approvalEvent(wire.Approval{ID: "a1", SessionID: "s1", Kind: "command", Command: "x", CreatedAt: time.Now()})
	got := waitSent(t, p, 2)
	hashes := map[string]bool{}
	for _, s := range got {
		hashes[s.hash] = true
	}
	if len(hashes) != 2 {
		t.Fatalf("pushed to %v", hashes)
	}
}

func TestPushBodyIsOneLineAndBounded(t *testing.T) {
	r, p, sub := pushRig(t, true, false)
	r.approvalEvent(wire.Approval{ID: "a1", SessionID: "s1", Kind: "command", CreatedAt: time.Now(),
		Command: "echo one\n\techo   two " + string(make([]rune, 0)) + repeat("x", 400)})
	m := sub.open(t, waitSent(t, p, 1)[0].body)
	if len([]rune(m.Body)) > pushBodyRunes+len("Run: ") || containsAny(m.Body, "\n\t") {
		t.Fatalf("body %q", m.Body)
	}
}

func repeat(s string, n int) string {
	out := ""
	for range n {
		out += s
	}
	return out
}

func containsAny(s, chars string) bool {
	for _, c := range chars {
		for _, x := range s {
			if x == c {
				return true
			}
		}
	}
	return false
}
