package daemon

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"slices"
	"sync"
	"time"

	"github.com/dev-dami/deck/pkg/remote/devstore"
	"github.com/dev-dami/deck/pkg/remote/passkey"
	"github.com/dev-dami/deck/pkg/remote/policy"
	"github.com/dev-dami/deck/pkg/remote/projects"
	"github.com/dev-dami/deck/pkg/remote/wire"
)

const (
	nonceTTL    = 2 * time.Minute
	regTTL      = 5 * time.Minute
	maxNonces   = 16
	maxPasskeys = 5
)

// PasskeyService is the part of a Gate that serves the passkey.* and
// assert.challenge methods; a gate without it leaves them unavailable.
type PasskeyService interface {
	Begin(dev devstore.Device) (wire.PasskeyBeginResult, *wire.Error)
	Finish(dev devstore.Device, p wire.PasskeyFinishParams) *wire.Error
	List(dev devstore.Device) []wire.PasskeyInfo
	Remove(dev devstore.Device, id string) *wire.Error
	Challenge(dev devstore.Device, actionDigest string) (string, *wire.Error)
}

// GateConfig is what a PolicyGate needs.
type GateConfig struct {
	CID      string
	Devices  *devstore.Store
	Projects *projects.Registry
	Verifier passkey.Verifier
	Now      func() time.Time
}

// PolicyGate is the real Gate: the classifier decides the tier, and a
// destructive allow or a yolo grant needs a fresh WebAuthn assertion over the
// action (contract §7).
type PolicyGate struct {
	cfg GateConfig

	mu     sync.Mutex
	nonces map[string][]nonce // by device id
	regs   map[string]reg
}

type nonce struct {
	digest, value string
	exp           time.Time
}

type reg struct {
	challenge []byte
	exp       time.Time
}

// NewGate returns a PolicyGate.
func NewGate(cfg GateConfig) *PolicyGate {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &PolicyGate{cfg: cfg, nonces: map[string][]nonce{}, regs: map[string]reg{}}
}

var _ Gate = (*PolicyGate)(nil)
var _ PasskeyService = (*PolicyGate)(nil)

// YoloDigest is the action digest a yolo grant is signed over.
func YoloDigest(pj, sessionID string) string {
	sum := sha256.Sum256([]byte("mode.set|yolo|" + pj + "|" + sessionID))
	return hex.EncodeToString(sum[:])
}

func (g *PolicyGate) Tier(pj string, a wire.Approval) string {
	root := ""
	if p, err := g.cfg.Projects.Get(pj); err == nil {
		root = p.Path
	}
	return policy.Classify(root, a)
}

func werr(code, msg string) *wire.Error { return &wire.Error{Code: code, Msg: msg} }

func (g *PolicyGate) AllowAnswer(_ context.Context, dev devstore.Device, a wire.ApprovalV3, req wire.ApprovalsAnswer3Params) *wire.Error {
	if req.Decision == wire.DecisionDeny || a.Tier == wire.TierNormal {
		return nil
	}
	return g.verify(dev, a.Digest, req.Assertion, "")
}

func (g *PolicyGate) AllowYolo(_ context.Context, dev devstore.Device, pj string, req wire.ModeSet3Params) *wire.Error {
	if !dev.AllowYolo {
		return werr(wire.CodeYoloDisabled, "Yolo is not allowed for this phone.")
	}
	return g.verify(dev, YoloDigest(pj, req.SessionID), req.Assertion, "")
}

// VerifyQueued checks an assertion for a queued action, where the nonce is the
// action's qid (the phone could not ask for one while the computer was away).
func (g *PolicyGate) VerifyQueued(dev devstore.Device, actionDigest, qid string, as *wire.Assertion) *wire.Error {
	return g.verify(dev, actionDigest, as, qid)
}

// verify checks as against a live nonce for the digest, or against fixedNonce
// when one is given. A nonce is spent by a good assertion.
func (g *PolicyGate) verify(dev devstore.Device, digest string, as *wire.Assertion, fixedNonce string) *wire.Error {
	if as == nil {
		return werr(wire.CodeAssertionRequired, "This needs your fingerprint or face.")
	}
	fresh, err := g.cfg.Devices.Get(dev.ID)
	if err != nil {
		return werr(wire.CodeForbidden, "This phone is no longer paired.")
	}
	idx := slices.IndexFunc(fresh.Passkeys, func(p devstore.Passkey) bool { return p.ID == as.CredentialID })
	if idx < 0 {
		return werr(wire.CodeAssertionInvalid, "That passkey is not registered on this computer.")
	}
	pk := fresh.Passkeys[idx]

	candidates := []string{fixedNonce}
	if fixedNonce == "" {
		candidates = g.liveNonces(dev.ID, digest)
	}
	for _, n := range candidates {
		ch := passkey.Challenge(g.cfg.CID, dev.ID, digest, n)
		count, err := g.cfg.Verifier.VerifyAssertion(pk.PublicKey, pk.SignCount, ch, *as)
		if errors.Is(err, passkey.ErrReplayed) {
			return werr(wire.CodeAssertionInvalid, "That confirmation was already used.")
		}
		if err != nil {
			continue
		}
		if fixedNonce == "" {
			g.spend(dev.ID, n)
		}
		if err := g.cfg.Devices.SetSignCount(dev.ID, pk.ID, count); err != nil {
			return werr(wire.CodeForbidden, "This phone is no longer paired.")
		}
		return nil
	}
	if len(candidates) == 0 {
		return werr(wire.CodeExpired, "The confirmation timed out. Try again.")
	}
	return werr(wire.CodeAssertionInvalid, "The confirmation did not check out.")
}

func (g *PolicyGate) liveNonces(devID, digest string) []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	now := g.cfg.Now()
	live := g.nonces[devID][:0]
	var out []string
	for _, n := range g.nonces[devID] {
		if n.exp.After(now) {
			live = append(live, n)
			if n.digest == digest {
				out = append(out, n.value)
			}
		}
	}
	g.nonces[devID] = live
	return out
}

func (g *PolicyGate) spend(devID, value string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.nonces[devID] = slices.DeleteFunc(g.nonces[devID], func(n nonce) bool { return n.value == value })
}

func random(n int) []byte {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return b
}

// Challenge issues the nonce a phone signs together with the action digest.
func (g *PolicyGate) Challenge(dev devstore.Device, actionDigest string) (string, *wire.Error) {
	if len(actionDigest) != 64 {
		return "", werr(wire.CodeInvalid, "action_digest is a 64-character hex digest")
	}
	n := nonce{digest: actionDigest, value: b64url.EncodeToString(random(16)), exp: g.cfg.Now().Add(nonceTTL)}
	g.mu.Lock()
	defer g.mu.Unlock()
	list := append(g.nonces[dev.ID], n)
	if len(list) > maxNonces {
		list = list[len(list)-maxNonces:]
	}
	g.nonces[dev.ID] = list
	return n.value, nil
}

// Begin starts a passkey registration for the device.
func (g *PolicyGate) Begin(dev devstore.Device) (wire.PasskeyBeginResult, *wire.Error) {
	fresh, err := g.cfg.Devices.Get(dev.ID)
	if err != nil {
		return wire.PasskeyBeginResult{}, werr(wire.CodeForbidden, "This phone is no longer paired.")
	}
	if len(fresh.Passkeys) >= maxPasskeys {
		return wire.PasskeyBeginResult{}, werr(wire.CodeInvalid, "Remove a passkey before adding another.")
	}
	ch := random(32)
	g.mu.Lock()
	g.regs[dev.ID] = reg{challenge: ch, exp: g.cfg.Now().Add(regTTL)}
	g.mu.Unlock()
	return wire.PasskeyBeginResult{
		Challenge: b64url.EncodeToString(ch),
		RPID:      g.cfg.Verifier.RPID,
		UserID:    b64url.EncodeToString([]byte(dev.ID)),
	}, nil
}

// Finish completes a registration begun with Begin.
func (g *PolicyGate) Finish(dev devstore.Device, p wire.PasskeyFinishParams) *wire.Error {
	g.mu.Lock()
	r, ok := g.regs[dev.ID]
	delete(g.regs, dev.ID)
	g.mu.Unlock()
	if !ok || !r.exp.After(g.cfg.Now()) {
		return werr(wire.CodeExpired, "The registration timed out. Start again.")
	}
	cd, err1 := b64url.DecodeString(p.ClientDataJSON)
	att, err2 := b64url.DecodeString(p.AttestationObject)
	if err1 != nil || err2 != nil {
		return werr(wire.CodeInvalid, "The registration is not valid.")
	}
	cred, err := g.cfg.Verifier.ParseRegistration(r.challenge, cd, att)
	if err != nil {
		return werr(wire.CodeAssertionInvalid, "The registration did not check out.")
	}
	id := b64url.EncodeToString(cred.ID)
	if p.CredentialID != id {
		return werr(wire.CodeInvalid, "The registration does not match its credential.")
	}
	if err := g.cfg.Devices.AddPasskey(dev.ID, devstore.Passkey{ID: id, PublicKey: cred.Key, CreatedAt: g.cfg.Now()}); err != nil {
		return werr(wire.CodeForbidden, "This phone is no longer paired.")
	}
	return nil
}

// List returns the device's registered passkeys.
func (g *PolicyGate) List(dev devstore.Device) []wire.PasskeyInfo {
	out := []wire.PasskeyInfo{}
	if fresh, err := g.cfg.Devices.Get(dev.ID); err == nil {
		for _, p := range fresh.Passkeys {
			out = append(out, wire.PasskeyInfo{ID: p.ID, CreatedAt: p.CreatedAt})
		}
	}
	return out
}

// Remove deletes one of the device's passkeys.
func (g *PolicyGate) Remove(dev devstore.Device, id string) *wire.Error {
	if err := g.cfg.Devices.RemovePasskey(dev.ID, id); err != nil {
		return werr(wire.CodeNotFound, "That passkey is not registered.")
	}
	return nil
}
