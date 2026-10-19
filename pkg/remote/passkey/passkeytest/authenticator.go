// Package passkeytest is a software WebAuthn authenticator (ES256, attestation
// "none") for tests of code that verifies passkeys.
package passkeytest

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"

	"github.com/dev-dami/deck/pkg/remote/wire"
)

var b64 = base64.RawURLEncoding

// Authenticator holds one credential.
type Authenticator struct {
	key   *ecdsa.PrivateKey
	ID    []byte
	count uint32
	// RPID and Origin default to app.example.dev.
	RPID, Origin string
	// SkipUV leaves the user-verified flag out of assertions.
	SkipUV bool
}

// New makes an authenticator with a fresh key.
func New() *Authenticator {
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		panic(err)
	}
	id := make([]byte, 16)
	_, _ = rand.Read(id)
	return &Authenticator{key: k, ID: id, RPID: "app.example.dev", Origin: "https://app.example.dev"}
}

func head(major byte, n uint64) []byte {
	switch {
	case n < 24:
		return []byte{major<<5 | byte(n)}
	case n < 256:
		return []byte{major<<5 | 24, byte(n)}
	}
	return binary.BigEndian.AppendUint16([]byte{major<<5 | 25}, uint16(n))
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

func (a *Authenticator) cose() []byte {
	x, y := a.key.X.FillBytes(make([]byte, 32)), a.key.Y.FillBytes(make([]byte, 32))
	return cMap(cInt(1), cInt(2), cInt(3), cInt(-7), cInt(-1), cInt(1), cInt(-2), cBytes(x), cInt(-3), cBytes(y))
}

func authHead(rp string, flags byte, count uint32) []byte {
	h := sha256.Sum256([]byte(rp))
	out := append(h[:], flags)
	return binary.BigEndian.AppendUint32(out, count)
}

func clientJSON(typ string, challenge []byte, origin string) []byte {
	raw, _ := json.Marshal(map[string]any{"type": typ, "challenge": b64.EncodeToString(challenge), "origin": origin, "crossOrigin": false})
	return raw
}

// Register answers a registration challenge: the client data and the
// attestation object, as the browser would hand them over.
func (a *Authenticator) Register(challenge []byte) (clientDataJSON, attestationObject []byte) {
	data := authHead(a.RPID, 0x01|0x04|0x40, 0)
	data = append(data, make([]byte, 16)...)
	data = binary.BigEndian.AppendUint16(data, uint16(len(a.ID)))
	data = append(data, a.ID...)
	data = append(data, a.cose()...)
	att := cMap(cText("fmt"), cText("none"), cText("attStmt"), cMap(), cText("authData"), cBytes(data))
	return clientJSON("webauthn.create", challenge, a.Origin), att
}

// Assert signs a challenge, counting up like a hardware key.
func (a *Authenticator) Assert(challenge []byte) wire.Assertion {
	a.count++
	flags := byte(0x01 | 0x04)
	if a.SkipUV {
		flags = 0x01
	}
	ad := authHead(a.RPID, flags, a.count)
	cd := clientJSON("webauthn.get", challenge, a.Origin)
	h := sha256.Sum256(cd)
	digest := sha256.Sum256(append(bytes.Clone(ad), h[:]...))
	sig, err := ecdsa.SignASN1(rand.Reader, a.key, digest[:])
	if err != nil {
		panic(err)
	}
	return wire.Assertion{
		CredentialID:      b64.EncodeToString(a.ID),
		AuthenticatorData: b64.EncodeToString(ad),
		ClientDataJSON:    b64.EncodeToString(cd),
		Signature:         b64.EncodeToString(sig),
	}
}
