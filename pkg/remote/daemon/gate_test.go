package daemon

import (
	"testing"
	"time"

	"github.com/dev-dami/deck/pkg/remote/passkey"
	"github.com/dev-dami/deck/pkg/remote/passkey/passkeytest"
	"github.com/dev-dami/deck/pkg/remote/wire"
)

func gateRig(t *testing.T) (*rig, *phoneConn, *passkeytest.Authenticator) {
	t.Helper()
	r := newRig(t)
	r.d.cfg.Gate = NewGate(GateConfig{
		CID: r.ident.CID(), Devices: r.devs, Projects: r.reg,
		Verifier: passkey.Verifier{RPID: "app.example.dev", Origin: "https://app.example.dev"},
	})
	cl, _ := r.connect(nil)
	return r, cl, passkeytest.New()
}

func registerPasskey(t *testing.T, cl *phoneConn, auth *passkeytest.Authenticator) {
	t.Helper()
	begin := okResult[wire.PasskeyBeginResult](t, cl.call("", wire.MPasskeyBegin, nil))
	if begin.RPID != "app.example.dev" || begin.UserID == "" {
		t.Fatalf("begin %+v", begin)
	}
	ch, err := b64url.DecodeString(begin.Challenge)
	if err != nil || len(ch) != 32 {
		t.Fatalf("challenge %q", begin.Challenge)
	}
	cd, att := auth.Register(ch)
	okResult[wire.Empty](t, cl.call("", wire.MPasskeyFinish, wire.PasskeyFinishParams{
		CredentialID:      b64url.EncodeToString(auth.ID),
		ClientDataJSON:    b64url.EncodeToString(cd),
		AttestationObject: b64url.EncodeToString(att),
	}))
}

// sign asks the computer for a nonce over digest and signs it.
func (r *rig) sign(t *testing.T, cl *phoneConn, auth *passkeytest.Authenticator, digest string) *wire.Assertion {
	t.Helper()
	n := okResult[wire.AssertChallengeResult](t, cl.call("", wire.MAssertChallenge, wire.AssertChallengeParams{ActionDigest: digest}))
	as := auth.Assert(passkey.Challenge(r.ident.CID(), r.device.ID, digest, n.Nonce))
	return &as
}

func seedApprovals(r *rig) {
	r.fake.Get("s1").Approvals = []wire.Approval{
		{ID: "safe", SessionID: "s1", Kind: "command", Command: "go test ./...", CreatedAt: time.Now()},
		{ID: "wipe", SessionID: "s1", Kind: "command", Command: "rm -rf build", CreatedAt: time.Now()},
		{ID: "wipe2", SessionID: "s1", Kind: "command", Command: "git push --force", CreatedAt: time.Now()},
		{ID: "inside", SessionID: "s1", Kind: "edit", Path: "main.go", CreatedAt: time.Now()},
		{ID: "outside", SessionID: "s1", Kind: "edit", Path: "../secrets.txt", CreatedAt: time.Now()},
	}
}

func TestPasskeyLifecycle(t *testing.T) {
	r, cl, auth := gateRig(t)
	_ = r
	if got := okResult[[]wire.PasskeyInfo](t, cl.call("", wire.MPasskeyList, nil)); len(got) != 0 {
		t.Fatalf("fresh device has passkeys: %v", got)
	}
	registerPasskey(t, cl, auth)
	list := okResult[[]wire.PasskeyInfo](t, cl.call("", wire.MPasskeyList, nil))
	if len(list) != 1 || list[0].ID != b64url.EncodeToString(auth.ID) {
		t.Fatalf("list %+v", list)
	}
	// finish without begin, or twice, is refused.
	cd, att := auth.Register(make([]byte, 32))
	wantErr(t, cl.call("", wire.MPasskeyFinish, wire.PasskeyFinishParams{
		CredentialID: b64url.EncodeToString(auth.ID), ClientDataJSON: b64url.EncodeToString(cd), AttestationObject: b64url.EncodeToString(att),
	}), wire.CodeExpired)
	okResult[wire.Empty](t, cl.call("", wire.MPasskeyRemove, wire.PasskeyRemoveParams{ID: list[0].ID}))
	if got := okResult[[]wire.PasskeyInfo](t, cl.call("", wire.MPasskeyList, nil)); len(got) != 0 {
		t.Fatalf("passkey not removed: %v", got)
	}
}

func TestRegistrationForAnotherOriginIsRefused(t *testing.T) {
	_, cl, auth := gateRig(t)
	auth.Origin = "https://evil.example"
	begin := okResult[wire.PasskeyBeginResult](t, cl.call("", wire.MPasskeyBegin, nil))
	ch, _ := b64url.DecodeString(begin.Challenge)
	cd, att := auth.Register(ch)
	wantErr(t, cl.call("", wire.MPasskeyFinish, wire.PasskeyFinishParams{
		CredentialID: b64url.EncodeToString(auth.ID), ClientDataJSON: b64url.EncodeToString(cd), AttestationObject: b64url.EncodeToString(att),
	}), wire.CodeAssertionInvalid)
	if got := okResult[[]wire.PasskeyInfo](t, cl.call("", wire.MPasskeyList, nil)); len(got) != 0 {
		t.Fatal("a registration for the wrong origin was stored")
	}
}

func TestTiersFromTheClassifier(t *testing.T) {
	r, cl, _ := gateRig(t)
	seedApprovals(r)
	list := okResult[[]wire.ApprovalV3](t, cl.call(r.pj, wire.MApprovalsList, wire.ApprovalsListParams{SessionID: "s1"}))
	want := map[string]string{
		"safe": wire.TierNormal, "wipe": wire.TierDestructive, "wipe2": wire.TierDestructive,
		"inside": wire.TierNormal, "outside": wire.TierDestructive,
	}
	for _, a := range list {
		if a.Tier != want[a.ID] {
			t.Errorf("%s: tier %s, want %s", a.ID, a.Tier, want[a.ID])
		}
	}
	if len(list) != len(want) {
		t.Fatalf("got %d approvals", len(list))
	}
}

func TestNormalAllowNeedsNoAssertionDeniedAlwaysWorks(t *testing.T) {
	r, cl, _ := gateRig(t)
	seedApprovals(r)
	okResult[wire.ApprovalsAnswerResult](t, cl.call(r.pj, wire.MApprovalsAnswer, wire.ApprovalsAnswer3Params{SessionID: "s1", ID: "safe", Decision: wire.DecisionAllow}))
	okResult[wire.ApprovalsAnswerResult](t, cl.call(r.pj, wire.MApprovalsAnswer, wire.ApprovalsAnswer3Params{SessionID: "s1", ID: "wipe", Decision: wire.DecisionDeny}))
	got := r.fake.Answered()
	if got["safe"] != wire.DecisionAllow || got["wipe"] != wire.DecisionDeny {
		t.Fatalf("answered %v", got)
	}
}

func TestDestructiveAllowNeedsAFreshAssertion(t *testing.T) {
	r, cl, auth := gateRig(t)
	seedApprovals(r)
	allow := func(id string, as *wire.Assertion) wire.Frame3 {
		return cl.call(r.pj, wire.MApprovalsAnswer, wire.ApprovalsAnswer3Params{SessionID: "s1", ID: id, Decision: wire.DecisionAllow, Assertion: as})
	}

	wantErr(t, allow("wipe", nil), wire.CodeAssertionRequired)

	registerPasskey(t, cl, auth)
	list := okResult[[]wire.ApprovalV3](t, cl.call(r.pj, wire.MApprovalsList, wire.ApprovalsListParams{SessionID: "s1"}))
	digests := map[string]string{}
	for _, a := range list {
		digests[a.ID] = a.Digest
	}

	// Asking for a nonce is not enough: the assertion must exist and verify.
	wantErr(t, allow("wipe", nil), wire.CodeAssertionRequired)

	// An assertion signed for a different approval does not carry over: no
	// nonce was issued for this one.
	other := r.sign(t, cl, auth, digests["wipe2"])
	wantErr(t, allow("wipe", other), wire.CodeExpired)
	if len(r.fake.Answered()) != 0 {
		t.Fatalf("a mismatched assertion let something through: %v", r.fake.Answered())
	}

	good := r.sign(t, cl, auth, digests["wipe"])
	okResult[wire.ApprovalsAnswerResult](t, allow("wipe", good))
	if r.fake.Answered()["wipe"] != wire.DecisionAllow {
		t.Fatalf("answered %v", r.fake.Answered())
	}
	// The same assertion cannot be used again, even for a re-raised approval.
	r.fake.Get("s1").Approvals = append(r.fake.Get("s1").Approvals,
		wire.Approval{ID: "wipe", SessionID: "s1", Kind: "command", Command: "rm -rf build", CreatedAt: time.Now()})
	wantErr(t, allow("wipe", good), wire.CodeExpired)
}

func TestUnregisteredOrWeakAssertionsAreRefused(t *testing.T) {
	r, cl, auth := gateRig(t)
	seedApprovals(r)
	registerPasskey(t, cl, auth)
	list := okResult[[]wire.ApprovalV3](t, cl.call(r.pj, wire.MApprovalsList, wire.ApprovalsListParams{SessionID: "s1"}))
	var digest string
	for _, a := range list {
		if a.ID == "wipe" {
			digest = a.Digest
		}
	}
	allow := func(as *wire.Assertion) wire.Frame3 {
		return cl.call(r.pj, wire.MApprovalsAnswer, wire.ApprovalsAnswer3Params{SessionID: "s1", ID: "wipe", Decision: wire.DecisionAllow, Assertion: as})
	}

	stranger := passkeytest.New() // never registered
	n := okResult[wire.AssertChallengeResult](t, cl.call("", wire.MAssertChallenge, wire.AssertChallengeParams{ActionDigest: digest}))
	as := stranger.Assert(passkey.Challenge(r.ident.CID(), r.device.ID, digest, n.Nonce))
	wantErr(t, allow(&as), wire.CodeAssertionInvalid)

	auth.SkipUV = true // presence without a fingerprint or face
	weak := r.sign(t, cl, auth, digest)
	wantErr(t, allow(weak), wire.CodeAssertionInvalid)
	if len(r.fake.Answered()) != 0 {
		t.Fatalf("answered %v", r.fake.Answered())
	}
}

func TestYoloNeedsGrantAndAssertion(t *testing.T) {
	r, cl, auth := gateRig(t)
	set := func(as *wire.Assertion) wire.Frame3 {
		return cl.call(r.pj, wire.MModeSet, wire.ModeSet3Params{SessionID: "s1", Mode: wire.ModeYolo, Assertion: as})
	}
	registerPasskey(t, cl, auth)

	// A valid assertion without the per-device grant is still refused.
	wantErr(t, set(r.sign(t, cl, auth, YoloDigest(r.pj, "s1"))), wire.CodeYoloDisabled)

	if err := r.devs.SetYolo(r.device.ID, true); err != nil {
		t.Fatal(err)
	}
	wantErr(t, set(nil), wire.CodeAssertionRequired)
	// An assertion for another session's yolo does not apply here.
	wantErr(t, set(r.sign(t, cl, auth, YoloDigest(r.pj, "s2"))), wire.CodeAssertionInvalid)
	if len(r.fake.Modes()) != 0 {
		t.Fatalf("yolo got through early: %v", r.fake.Modes())
	}

	res := okResult[wire.ModeResult](t, set(r.sign(t, cl, auth, YoloDigest(r.pj, "s1"))))
	if res.Mode != wire.ModeYolo || len(r.fake.Modes()) != 1 {
		t.Fatalf("yolo not set: %+v %v", res, r.fake.Modes())
	}
}

func TestChallengeInputIsValidated(t *testing.T) {
	_, cl, _ := gateRig(t)
	wantErr(t, cl.call("", wire.MAssertChallenge, wire.AssertChallengeParams{ActionDigest: "short"}), wire.CodeInvalid)
}

func TestQueuedAssertionBindsToTheQID(t *testing.T) {
	r, _, auth := gateRig(t)
	cl, _ := r.connect(nil)
	registerPasskey(t, cl, auth)
	g := r.d.cfg.Gate.(*PolicyGate)
	dev, _ := r.devs.Get(r.device.ID)
	digest := YoloDigest(r.pj, "s1")

	as := auth.Assert(passkey.Challenge(r.ident.CID(), dev.ID, digest, "qid-1"))
	if werr := g.VerifyQueued(dev, digest, "qid-2", &as); werr == nil {
		t.Fatal("an assertion for one qid verified for another")
	}
	as = auth.Assert(passkey.Challenge(r.ident.CID(), dev.ID, digest, "qid-1"))
	if werr := g.VerifyQueued(dev, digest, "qid-1", &as); werr != nil {
		t.Fatalf("queued assertion: %+v", werr)
	}
	if werr := g.VerifyQueued(dev, digest, "qid-1", &as); werr == nil || werr.Code != wire.CodeAssertionInvalid {
		t.Fatalf("a queued assertion verified twice: %+v", werr)
	}
}
