package passkey

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"testing"

	"github.com/dev-dami/deck/pkg/remote/wire"
)

const (
	rpID   = "app.example.dev"
	origin = "https://app.example.dev"
)

// --- a software authenticator -------------------------------------------

func head(major byte, n uint64) []byte {
	switch {
	case n < 24:
		return []byte{major<<5 | byte(n)}
	case n < 256:
		return []byte{major<<5 | 24, byte(n)}
	case n < 65536:
		return binary.BigEndian.AppendUint16([]byte{major<<5 | 25}, uint16(n))
	}
	return binary.BigEndian.AppendUint32([]byte{major<<5 | 26}, uint32(n))
}

func cInt(n int64) []byte {
	if n >= 0 {
		return head(0, uint64(n))
	}
	return head(1, uint64(-1-n))
}
func cBytes(b []byte) []byte { return append(head(2, uint64(len(b))), b...) }
func cText(s string) []byte  { return append(head(3, uint64(len(s))), s...) }
func cMap(kv ...[]byte) []byte {
	out := head(5, uint64(len(kv)/2))
	for _, x := range kv {
		out = append(out, x...)
	}
	return out
}

type authenticator struct {
	es    *ecdsa.PrivateKey
	ed    ed25519.PrivateKey
	id    []byte
	count uint32
}

func newES256(t *testing.T) *authenticator {
	t.Helper()
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return &authenticator{es: k, id: bytes.Repeat([]byte{7}, 16)}
}

func newEdDSA(t *testing.T) *authenticator {
	t.Helper()
	_, k, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return &authenticator{ed: k, id: bytes.Repeat([]byte{8}, 16)}
}

func (a *authenticator) cose() []byte {
	if a.es != nil {
		x, y := a.es.X.FillBytes(make([]byte, 32)), a.es.Y.FillBytes(make([]byte, 32))
		return cMap(cInt(1), cInt(2), cInt(3), cInt(-7), cInt(-1), cInt(1), cInt(-2), cBytes(x), cInt(-3), cBytes(y))
	}
	return cMap(cInt(1), cInt(1), cInt(3), cInt(-8), cInt(-1), cInt(6), cInt(-2), cBytes(a.ed.Public().(ed25519.PublicKey)))
}

func authHead(rp string, flags byte, count uint32) []byte {
	h := sha256.Sum256([]byte(rp))
	out := append(h[:], flags)
	return binary.BigEndian.AppendUint32(out, count)
}

func clientJSON(typ string, challenge []byte, org string) []byte {
	raw, _ := json.Marshal(map[string]any{"type": typ, "challenge": b64.EncodeToString(challenge), "origin": org, "crossOrigin": false})
	return raw
}

type regOpts struct {
	rp     string
	flags  byte
	org    string
	format string
}

func (a *authenticator) register(challenge []byte, o regOpts) (cd, att []byte) {
	if o.rp == "" {
		o.rp = rpID
	}
	if o.flags == 0 {
		o.flags = flagUP | flagUV | flagAT
	}
	if o.org == "" {
		o.org = origin
	}
	if o.format == "" {
		o.format = "none"
	}
	data := authHead(o.rp, o.flags, 0)
	data = append(data, make([]byte, 16)...) // aaguid
	data = binary.BigEndian.AppendUint16(data, uint16(len(a.id)))
	data = append(data, a.id...)
	data = append(data, a.cose()...)
	att = cMap(cText("fmt"), cText(o.format), cText("attStmt"), cMap(), cText("authData"), cBytes(data))
	return clientJSON("webauthn.create", challenge, o.org), att
}

type asOpts struct {
	rp    string
	flags byte
	org   string
	typ   string
	count *uint32
}

func (a *authenticator) assert(challenge []byte, o asOpts) wire.Assertion {
	if o.rp == "" {
		o.rp = rpID
	}
	if o.flags == 0 {
		o.flags = flagUP | flagUV
	}
	if o.org == "" {
		o.org = origin
	}
	if o.typ == "" {
		o.typ = "webauthn.get"
	}
	a.count++
	count := a.count
	if o.count != nil {
		count = *o.count
	}
	ad := authHead(o.rp, o.flags, count)
	cd := clientJSON(o.typ, challenge, o.org)
	h := sha256.Sum256(cd)
	signed := append(bytes.Clone(ad), h[:]...)
	var sig []byte
	if a.es != nil {
		d := sha256.Sum256(signed)
		sig, _ = ecdsa.SignASN1(rand.Reader, a.es, d[:])
	} else {
		sig = ed25519.Sign(a.ed, signed)
	}
	return wire.Assertion{
		CredentialID:      b64.EncodeToString(a.id),
		AuthenticatorData: b64.EncodeToString(ad),
		ClientDataJSON:    b64.EncodeToString(cd),
		Signature:         b64.EncodeToString(sig),
	}
}

var v = Verifier{RPID: rpID, Origin: origin}

func register(t *testing.T, a *authenticator) Credential {
	t.Helper()
	ch := bytes.Repeat([]byte{1}, 32)
	cd, att := a.register(ch, regOpts{})
	cred, err := v.ParseRegistration(ch, cd, att)
	if err != nil {
		t.Fatalf("registration: %v", err)
	}
	if !bytes.Equal(cred.ID, a.id) {
		t.Fatal("credential id differs")
	}
	return cred
}

// --- tests --------------------------------------------------------------

func TestRegistrationAndAssertionForBothAlgorithms(t *testing.T) {
	for name, a := range map[string]*authenticator{"ES256": newES256(t), "EdDSA": newEdDSA(t)} {
		t.Run(name, func(t *testing.T) {
			cred := register(t, a)
			ch := Challenge("cid", "dev_1", "digest", "nonce-1")
			count, err := v.VerifyAssertion(cred.Key, 0, ch, a.assert(ch, asOpts{}))
			if err != nil || count != 1 {
				t.Fatalf("assertion: %d %v", count, err)
			}
		})
	}
}

func TestAssertionRejections(t *testing.T) {
	a := newES256(t)
	cred := register(t, a)
	other := newES256(t)
	ch := Challenge("cid", "dev_1", "digest", "n")

	cases := []struct {
		name string
		key  []byte
		ch   []byte
		as   wire.Assertion
		want error
	}{
		{"wrong challenge", cred.Key, Challenge("cid", "dev_1", "digest", "other-nonce"), a.assert(ch, asOpts{}), ErrInvalid},
		{"wrong origin", cred.Key, ch, a.assert(ch, asOpts{org: "https://evil.example"}), ErrInvalid},
		{"wrong relying party", cred.Key, ch, a.assert(ch, asOpts{rp: "evil.example"}), ErrInvalid},
		{"no user verification", cred.Key, ch, a.assert(ch, asOpts{flags: flagUP}), ErrInvalid},
		{"no user presence", cred.Key, ch, a.assert(ch, asOpts{flags: flagUV}), ErrInvalid},
		{"registration type reused", cred.Key, ch, a.assert(ch, asOpts{typ: "webauthn.create"}), ErrInvalid},
		{"another key signed", other.cose(), ch, a.assert(ch, asOpts{}), ErrInvalid},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := v.VerifyAssertion(c.key, 0, c.ch, c.as)
			if !errors.Is(err, c.want) {
				t.Fatalf("got %v, want %v", err, c.want)
			}
		})
	}

	t.Run("tampered signature", func(t *testing.T) {
		as := a.assert(ch, asOpts{})
		sig, _ := b64.DecodeString(as.Signature)
		sig[len(sig)-1] ^= 1
		as.Signature = b64.EncodeToString(sig)
		if _, err := v.VerifyAssertion(cred.Key, 0, ch, as); !errors.Is(err, ErrInvalid) {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("tampered authenticator data", func(t *testing.T) {
		as := a.assert(ch, asOpts{})
		ad, _ := b64.DecodeString(as.AuthenticatorData)
		ad[36] ^= 1 // the counter is signed
		as.AuthenticatorData = b64.EncodeToString(ad)
		if _, err := v.VerifyAssertion(cred.Key, 0, ch, as); !errors.Is(err, ErrInvalid) {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("not base64", func(t *testing.T) {
		as := a.assert(ch, asOpts{})
		as.Signature = "!!!"
		if _, err := v.VerifyAssertion(cred.Key, 0, ch, as); !errors.Is(err, ErrInvalid) {
			t.Fatalf("got %v", err)
		}
	})
}

func TestCounterReplay(t *testing.T) {
	a := newES256(t)
	cred := register(t, a)
	ch := Challenge("c", "d", "x", "n")
	as := a.assert(ch, asOpts{}) // counter 1
	if n, err := v.VerifyAssertion(cred.Key, 0, ch, as); err != nil || n != 1 {
		t.Fatalf("first use: %d %v", n, err)
	}
	if _, err := v.VerifyAssertion(cred.Key, 1, ch, as); !errors.Is(err, ErrReplayed) {
		t.Fatalf("replay of the same assertion: %v", err)
	}
	// An authenticator that never counts is allowed: 0 against 0.
	zero := uint32(0)
	as0 := a.assert(ch, asOpts{count: &zero})
	if n, err := v.VerifyAssertion(cred.Key, 0, ch, as0); err != nil || n != 0 {
		t.Fatalf("non-counting authenticator: %d %v", n, err)
	}
	// ...but not once a counter has been seen.
	if _, err := v.VerifyAssertion(cred.Key, 5, ch, as0); !errors.Is(err, ErrReplayed) {
		t.Fatalf("counter reset to zero after 5: %v", err)
	}
}

func TestRegistrationRejections(t *testing.T) {
	a := newES256(t)
	ch := bytes.Repeat([]byte{1}, 32)
	cases := map[string]regOpts{
		"wrong rp":                    {rp: "evil.example"},
		"no uv":                       {flags: flagUP | flagAT},
		"no attested key":             {flags: flagUP | flagUV},
		"wrong origin":                {org: "https://evil.example"},
		"real attestation (not none)": {format: "packed"},
	}
	for name, o := range cases {
		t.Run(name, func(t *testing.T) {
			cd, att := a.register(ch, o)
			if _, err := v.ParseRegistration(ch, cd, att); !errors.Is(err, ErrInvalid) {
				t.Fatalf("got %v", err)
			}
		})
	}
	t.Run("wrong challenge", func(t *testing.T) {
		cd, att := a.register(bytes.Repeat([]byte{2}, 32), regOpts{})
		if _, err := v.ParseRegistration(ch, cd, att); !errors.Is(err, ErrInvalid) {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("assertion type", func(t *testing.T) {
		_, att := a.register(ch, regOpts{})
		if _, err := v.ParseRegistration(ch, clientJSON("webauthn.get", ch, origin), att); !errors.Is(err, ErrInvalid) {
			t.Fatalf("got %v", err)
		}
	})
}

func TestUnsupportedAndBrokenKeys(t *testing.T) {
	rsa := cMap(cInt(1), cInt(3), cInt(3), cInt(-257), cInt(-1), cBytes([]byte{1}), cInt(-2), cBytes([]byte{1}))
	off := cMap(cInt(1), cInt(2), cInt(3), cInt(-7), cInt(-1), cInt(1), cInt(-2), cBytes(bytes.Repeat([]byte{1}, 32)), cInt(-3), cBytes(bytes.Repeat([]byte{2}, 32)))
	for name, key := range map[string][]byte{"rsa": rsa, "point off the curve": off, "empty": nil, "junk": {0xff, 0x00}} {
		t.Run(name, func(t *testing.T) {
			ch := Challenge("c", "d", "x", "n")
			as := newES256(t).assert(ch, asOpts{})
			if _, err := v.VerifyAssertion(key, 0, ch, as); !errors.Is(err, ErrInvalid) {
				t.Fatalf("got %v", err)
			}
		})
	}
}

func TestChallengeDependsOnEveryPart(t *testing.T) {
	base := Challenge("cid", "dev", "dig", "n")
	for name, c := range map[string][]byte{
		"cid":    Challenge("cid2", "dev", "dig", "n"),
		"device": Challenge("cid", "dev2", "dig", "n"),
		"digest": Challenge("cid", "dev", "dig2", "n"),
		"nonce":  Challenge("cid", "dev", "dig", "n2"),
		// Parts must not be able to run into each other.
		"boundary": Challenge("ci", "ddev", "dig", "n"),
	} {
		if bytes.Equal(base, c) {
			t.Errorf("challenge ignores %s", name)
		}
	}
	if len(base) != 32 {
		t.Fatalf("challenge is %d bytes", len(base))
	}
}

func TestCBORRefusesWhatItDoesNotUnderstand(t *testing.T) {
	for name, in := range map[string][]byte{
		"empty":            nil,
		"truncated string": {0x63, 'a'},
		"indefinite":       {0x5f, 0xff},
		"tag":              {0xc1, 0x01},
		"float":            {0xfb, 0, 0, 0, 0, 0, 0, 0, 0},
		"huge array":       {0x9a, 0xff, 0xff, 0xff, 0xff},
		"deep nesting":     bytes.Repeat([]byte{0x81}, 40),
	} {
		t.Run(name, func(t *testing.T) {
			if _, _, err := decodeCBOR(in); err == nil {
				t.Fatal("accepted")
			}
		})
	}
}

func FuzzParseRegistration(f *testing.F) {
	a := &authenticator{es: mustKey(), id: []byte{1}}
	ch := bytes.Repeat([]byte{1}, 32)
	cd, att := a.register(ch, regOpts{})
	f.Add(cd, att)
	f.Fuzz(func(t *testing.T, cd, att []byte) {
		// Must not panic; only the genuine pair may succeed.
		_, _ = v.ParseRegistration(ch, cd, att)
	})
}

func mustKey() *ecdsa.PrivateKey {
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		panic(err)
	}
	return k
}
