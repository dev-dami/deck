package daemon

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/dev-dami/deck/pkg/remote/e2e"
	"github.com/dev-dami/deck/pkg/remote/passkey"
	"github.com/dev-dami/deck/pkg/remote/passkey/passkeytest"
	"github.com/dev-dami/deck/pkg/remote/queue"
	"github.com/dev-dami/deck/pkg/remote/wire"
)

// queueRig is a rig whose daemon has an offline queue with a short settle.
func queueRig(t *testing.T) *rig {
	t.Helper()
	r := newRig(t)
	q, err := queue.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { q.Close() })
	r.d.cfg.Queue = q
	r.d.cfg.QueueSettle = time.Millisecond
	return r
}

var qn int

// mail seals an action the way the phone does and hands it to the daemon.
func (r *rig) mail(t *testing.T, a wire.QueuedAction) error {
	t.Helper()
	return r.mailAs(t, r.phone, r.device.RelayTokenHash, a)
}

func (r *rig) mailAs(t *testing.T, phone e2e.Keypair, tokenHash string, a wire.QueuedAction) error {
	t.Helper()
	raw, _ := json.Marshal(a)
	sealed, err := e2e.SealK(phone, r.ident.StaticPublic(), r.ident.CID(), raw, nil)
	if err != nil {
		t.Fatal(err)
	}
	return r.d.HandleMail(context.Background(), a.QID, tokenHash, sealed)
}

func (r *rig) action(kind string, payload any, mut ...func(*wire.QueuedAction)) wire.QueuedAction {
	qn++
	raw, _ := json.Marshal(payload)
	now := time.Now()
	a := wire.QueuedAction{
		QID: fmt.Sprintf("q%03d", qn), DeviceID: r.device.ID, CreatedAt: now, ExpiresAt: now.Add(time.Hour),
		PJ: r.pj, SessionID: "s1", Kind: kind, Payload: raw,
	}
	for _, m := range mut {
		m(&a)
	}
	return a
}

func (r *rig) drain(t *testing.T) {
	t.Helper()
	time.Sleep(5 * time.Millisecond)
	r.d.drainQueue(context.Background())
}

func (r *rig) receipt(t *testing.T, qid string) wire.Receipt {
	t.Helper()
	rs, err := r.d.cfg.Queue.Receipts(r.device.ID, 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, x := range rs {
		if x.QID == qid {
			return x
		}
	}
	t.Fatalf("no receipt for %s (have %v)", qid, rs)
	return wire.Receipt{}
}

func wantReceipt(t *testing.T, r wire.Receipt, status, reason string) {
	t.Helper()
	if r.Status != status || r.Reason != reason {
		t.Fatalf("receipt %s/%s, want %s/%s", r.Status, r.Reason, status, reason)
	}
}

func TestQueuedPromptRunsOnWakeWithAReceipt(t *testing.T) {
	r := queueRig(t)
	cl, _ := r.connect(nil)
	a := r.action(wire.QKindChatSend, map[string]string{"text": "carry on with the migration"})
	if err := r.mail(t, a); err != nil {
		t.Fatal(err)
	}
	if len(r.fake.Sent()) != 0 {
		t.Fatal("ran before the engine did")
	}
	r.drain(t)
	sent := r.fake.Sent()
	if len(sent) != 1 || sent[0].Text != "carry on with the migration" || sent[0].SessionID != "s1" {
		t.Fatalf("sent %+v", sent)
	}
	wantReceipt(t, r.receipt(t, a.QID), wire.ReceiptApplied, "")

	// A phone that is connected is told. (Events are read first: call() skips
	// past them while it waits for a response.)
	for {
		f := cl.recv()
		if f.E == wire.EvReceiptsChanged {
			var got []wire.Receipt
			if json.Unmarshal(f.P, &got) != nil || len(got) != 1 || got[0].QID != a.QID {
				t.Fatalf("receipts.changed payload %s", f.P)
			}
			break
		}
	}
	rs := okResult[[]wire.Receipt](t, cl.call("", wire.MQueueStatus, nil))
	if len(rs) != 1 || rs[0].QID != a.QID {
		t.Fatalf("queue.status %+v", rs)
	}
}

func TestRedeliveryHasNoSecondEffect(t *testing.T) {
	r := queueRig(t)
	a := r.action(wire.QKindChatSend, map[string]string{"text": "once"})
	for range 3 {
		if err := r.mail(t, a); err != nil {
			t.Fatal(err)
		}
	}
	r.drain(t)
	if err := r.mail(t, a); err != nil { // redelivered after it ran
		t.Fatal(err)
	}
	r.drain(t)
	if n := len(r.fake.Sent()); n != 1 {
		t.Fatalf("applied %d times", n)
	}
}

func TestForgedAndMisaddressedMailIsDroppedButAcked(t *testing.T) {
	r := queueRig(t)
	stranger, _ := e2e.GenerateKeypair(rand.Reader)
	a := r.action(wire.QKindChatSend, map[string]string{"text": "evil"})

	// Sealed by a key the computer has not pinned, claiming to be our phone.
	if err := r.mailAs(t, stranger, r.device.RelayTokenHash, a); err != nil {
		t.Fatalf("a forged mail must be acked (nil), got %v", err)
	}
	// From an unknown token.
	if err := r.mailAs(t, r.phone, "no-such-hash", a); err != nil {
		t.Fatal(err)
	}
	// A real phone naming another device in the action.
	b := r.action(wire.QKindChatSend, map[string]string{"text": "spoof"}, func(a *wire.QueuedAction) { a.DeviceID = "dev_other" })
	if err := r.mail(t, b); err != nil {
		t.Fatal(err)
	}
	// An action that may never be queued.
	c := r.action("agent.interrupt", map[string]string{})
	if err := r.mail(t, c); err != nil {
		t.Fatal(err)
	}
	r.drain(t)
	if len(r.fake.Sent()) != 0 {
		t.Fatalf("something ran: %+v", r.fake.Sent())
	}
	if p, _ := r.d.cfg.Queue.Pending(); len(p) != 0 {
		t.Fatalf("pending %v", p)
	}
}

func TestExpiredAndMissingTargetsAreDroppedWithReasons(t *testing.T) {
	r := queueRig(t)
	expired := r.action(wire.QKindChatSend, map[string]string{"text": "stale"}, func(a *wire.QueuedAction) {
		a.CreatedAt = time.Now().Add(-3 * time.Hour)
		a.ExpiresAt = time.Now().Add(-time.Hour)
	})
	gone := r.action(wire.QKindChatSend, map[string]string{"text": "x"}, func(a *wire.QueuedAction) { a.SessionID = "deleted" })
	for _, a := range []wire.QueuedAction{expired, gone} {
		if err := r.mail(t, a); err != nil {
			t.Fatal(err)
		}
	}
	r.drain(t)
	wantReceipt(t, r.receipt(t, expired.QID), wire.ReceiptDropped, wire.ReasonExpired)
	wantReceipt(t, r.receipt(t, gone.QID), wire.ReceiptDropped, wire.ReasonSessionGone)
	if len(r.fake.Sent()) != 0 {
		t.Fatal("a dropped action ran")
	}
}

func TestExpiryBeyondTheCapIsRefused(t *testing.T) {
	r := queueRig(t)
	a := r.action(wire.QKindChatSend, map[string]string{"text": "x"}, func(a *wire.QueuedAction) {
		a.CreatedAt = time.Now().Add(20 * time.Hour)
		a.ExpiresAt = time.Now().Add(43 * time.Hour) // a phone clock far ahead
	})
	if err := r.mail(t, a); err != nil {
		t.Fatal(err)
	}
	wantReceipt(t, r.receipt(t, a.QID), wire.ReceiptDropped, wire.ReasonExpired)
}

func TestActionsRunInTheOrderThePhoneMadeThem(t *testing.T) {
	r := queueRig(t)
	base := time.Now()
	mk := func(text string, off time.Duration) wire.QueuedAction {
		return r.action(wire.QKindChatSend, map[string]string{"text": text}, func(a *wire.QueuedAction) {
			a.CreatedAt = base.Add(off)
			a.ExpiresAt = base.Add(off + time.Hour)
		})
	}
	// Delivered third, first, second.
	third, first, second := mk("three", 2*time.Minute), mk("one", 0), mk("two", time.Minute)
	for _, a := range []wire.QueuedAction{third, first, second} {
		if err := r.mail(t, a); err != nil {
			t.Fatal(err)
		}
	}
	r.drain(t)
	var got []string
	for _, s := range r.fake.Sent() {
		got = append(got, s.Text)
	}
	if fmt.Sprint(got) != "[one two three]" {
		t.Fatalf("order %v", got)
	}
}

func TestQueuedCancelStopsAnEarlierAction(t *testing.T) {
	r := queueRig(t)
	send := r.action(wire.QKindChatSend, map[string]string{"text": "never mind"})
	cancel := r.action(wire.QKindCancel, wire.QueueCancelPayload{QID: send.QID}, func(a *wire.QueuedAction) {
		a.PJ, a.SessionID = "", ""
		a.CreatedAt = send.CreatedAt.Add(time.Second)
		a.ExpiresAt = a.CreatedAt.Add(time.Hour)
	})
	for _, a := range []wire.QueuedAction{send, cancel} {
		if err := r.mail(t, a); err != nil {
			t.Fatal(err)
		}
	}
	r.drain(t)
	if len(r.fake.Sent()) != 0 {
		t.Fatal("a cancelled action ran")
	}
	wantReceipt(t, r.receipt(t, send.QID), wire.ReceiptDropped, wire.ReasonCancelled)
	wantReceipt(t, r.receipt(t, cancel.QID), wire.ReceiptApplied, "")
}

func TestActionWaitsForItsProjectAndRunsWhenItReturns(t *testing.T) {
	r := queueRig(t)
	r.d.Detach(r.pj, r.fake)
	a := r.action(wire.QKindChatSend, map[string]string{"text": "when you are back"})
	if err := r.mail(t, a); err != nil {
		t.Fatal(err)
	}
	r.drain(t)
	if p, _ := r.d.cfg.Queue.Pending(); len(p) != 1 {
		t.Fatal("an action for an offline project should keep waiting")
	}
	r.d.Attach(r.pj, r.fake)
	r.drain(t)
	if len(r.fake.Sent()) != 1 {
		t.Fatalf("sent %+v", r.fake.Sent())
	}
	wantReceipt(t, r.receipt(t, a.QID), wire.ReceiptApplied, "")
}

func TestVetoCancelsOnlyTheNamedActions(t *testing.T) {
	r := queueRig(t)
	keep := r.action(wire.QKindChatSend, map[string]string{"text": "keep"})
	drop := r.action(wire.QKindChatSend, map[string]string{"text": "drop"})
	var seen int
	r.d.cfg.Veto = func(_ context.Context, batch []wire.QueuedAction) map[string]bool {
		seen = len(batch)
		return map[string]bool{drop.QID: true}
	}
	for _, a := range []wire.QueuedAction{keep, drop} {
		if err := r.mail(t, a); err != nil {
			t.Fatal(err)
		}
	}
	r.drain(t)
	if seen != 2 {
		t.Fatalf("veto saw %d actions", seen)
	}
	if s := r.fake.Sent(); len(s) != 1 || s[0].Text != "keep" {
		t.Fatalf("sent %+v", s)
	}
	wantReceipt(t, r.receipt(t, drop.QID), wire.ReceiptDropped, wire.ReasonVetoed)
}

func TestFullQueueGetsAReceiptNotSilence(t *testing.T) {
	r := queueRig(t)
	for i := 0; i < wire.MaxQueuedPerDevice; i++ {
		if err := r.mail(t, r.action(wire.QKindChatSend, map[string]string{"text": "x"})); err != nil {
			t.Fatal(err)
		}
	}
	extra := r.action(wire.QKindChatSend, map[string]string{"text": "one too many"})
	if err := r.mail(t, extra); err != nil {
		t.Fatal(err)
	}
	wantReceipt(t, r.receipt(t, extra.QID), wire.ReceiptDropped, wire.ReasonBusy)
}

func TestQueuedContinueAndNewSession(t *testing.T) {
	r := queueRig(t)
	busy := r.fake.AddSession("busy", "Busy")
	busy.Session.Busy = true
	cont := r.action(wire.QKindChatContinue, struct{}{})
	contBusy := r.action(wire.QKindChatContinue, struct{}{}, func(a *wire.QueuedAction) { a.SessionID = "busy" })
	newS := r.action(wire.QKindSessionNew, wire.SessionNewPayload{Title: "From the train", Text: "start on the api"}, func(a *wire.QueuedAction) { a.SessionID = "" })
	for _, a := range []wire.QueuedAction{cont, contBusy, newS} {
		if err := r.mail(t, a); err != nil {
			t.Fatal(err)
		}
	}
	r.drain(t)
	wantReceipt(t, r.receipt(t, cont.QID), wire.ReceiptApplied, "")
	wantReceipt(t, r.receipt(t, contBusy.QID), wire.ReceiptDropped, wire.ReasonBusy)
	wantReceipt(t, r.receipt(t, newS.QID), wire.ReceiptApplied, "")
	sent := r.fake.Sent()
	if len(sent) != 1 || sent[0].Text != "start on the api" || sent[0].SessionID == "s1" {
		t.Fatalf("new session prompt: %+v", sent)
	}
}

func approvalAction(r *rig, id, digest, decision string, as *wire.Assertion) wire.QueuedAction {
	return r.action(wire.QKindApproval, map[string]string{"decision": decision}, func(a *wire.QueuedAction) {
		a.Precond = &wire.Precond{ApprovalID: id, Digest: digest}
		a.Assertion = as
	})
}

func TestQueuedApprovalsBindToWhatThePersonSaw(t *testing.T) {
	r := queueRig(t)
	g := NewGate(GateConfig{
		CID: r.ident.CID(), Devices: r.devs, Projects: r.reg,
		Verifier: passkey.Verifier{RPID: "app.example.dev", Origin: "https://app.example.dev"},
	})
	r.d.cfg.Gate = g
	seedApprovals(r)
	auth := passkeytest.New()
	cl, _ := r.connect(nil)
	registerPasskey(t, cl, auth)

	list := okResult[[]wire.ApprovalV3](t, cl.call(r.pj, wire.MApprovalsList, wire.ApprovalsListParams{SessionID: "s1"}))
	dig := map[string]string{}
	for _, a := range list {
		dig[a.ID] = a.Digest
	}

	safe := approvalAction(r, "safe", dig["safe"], wire.DecisionAllow, nil)
	changed := approvalAction(r, "inside", dig["wipe"], wire.DecisionAllow, nil) // digest of another request
	gone := approvalAction(r, "vanished", dig["safe"], wire.DecisionAllow, nil)
	noAssertion := approvalAction(r, "wipe", dig["wipe"], wire.DecisionAllow, nil)
	badAssertion := approvalAction(r, "wipe2", dig["wipe2"], wire.DecisionAllow, nil)
	badAs := auth.Assert(passkey.Challenge(r.ident.CID(), r.device.ID, dig["wipe2"], "some-other-qid"))
	badAssertion.Assertion = &badAs
	goodDestructive := approvalAction(r, "outside", dig["outside"], wire.DecisionAllow, nil)
	goodAs := auth.Assert(passkey.Challenge(r.ident.CID(), r.device.ID, dig["outside"], goodDestructive.QID))
	goodDestructive.Assertion = &goodAs

	all := []wire.QueuedAction{safe, changed, gone, noAssertion, badAssertion, goodDestructive}
	for _, a := range all {
		if err := r.mail(t, a); err != nil {
			t.Fatal(err)
		}
	}
	r.drain(t)

	wantReceipt(t, r.receipt(t, safe.QID), wire.ReceiptApplied, "")
	wantReceipt(t, r.receipt(t, changed.QID), wire.ReceiptDropped, wire.ReasonDigestMismatch)
	wantReceipt(t, r.receipt(t, gone.QID), wire.ReceiptDropped, wire.ReasonApprovalGone)
	wantReceipt(t, r.receipt(t, noAssertion.QID), wire.ReceiptDropped, wire.ReasonAssertion)
	wantReceipt(t, r.receipt(t, badAssertion.QID), wire.ReceiptDropped, wire.ReasonAssertion)
	wantReceipt(t, r.receipt(t, goodDestructive.QID), wire.ReceiptApplied, "")

	got := r.fake.Answered()
	if len(got) != 2 || got["safe"] != wire.DecisionAllow || got["outside"] != wire.DecisionAllow {
		t.Fatalf("answered %v", got)
	}
}

func TestQueuedApprovalsFailClosedWithoutAGate(t *testing.T) {
	r := queueRig(t) // default gate: every approval is destructive and unverifiable
	seedApprovals(r)
	cl, _ := r.connect(nil)
	list := okResult[[]wire.ApprovalV3](t, cl.call(r.pj, wire.MApprovalsList, wire.ApprovalsListParams{SessionID: "s1"}))
	var digest string
	for _, a := range list {
		if a.ID == "safe" {
			digest = a.Digest
		}
	}
	allow := approvalAction(r, "safe", digest, wire.DecisionAllow, nil)
	deny := approvalAction(r, "wipe", func() string {
		for _, a := range list {
			if a.ID == "wipe" {
				return a.Digest
			}
		}
		return ""
	}(), wire.DecisionDeny, nil)
	for _, a := range []wire.QueuedAction{allow, deny} {
		if err := r.mail(t, a); err != nil {
			t.Fatal(err)
		}
	}
	r.drain(t)
	wantReceipt(t, r.receipt(t, allow.QID), wire.ReceiptDropped, wire.ReasonAssertion)
	wantReceipt(t, r.receipt(t, deny.QID), wire.ReceiptApplied, "")
	if got := r.fake.Answered(); len(got) != 1 || got["wipe"] != wire.DecisionDeny {
		t.Fatalf("answered %v", got)
	}
}

func TestRunQueueLoopExecutesOnItsOwn(t *testing.T) {
	r := queueRig(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { r.d.RunQueue(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })

	a := r.action(wire.QKindChatSend, map[string]string{"text": "background"})
	if err := r.mail(t, a); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for len(r.fake.Sent()) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the loop never ran the action")
		}
		time.Sleep(20 * time.Millisecond)
	}
}
