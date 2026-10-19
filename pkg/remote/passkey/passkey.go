// Package passkey verifies the WebAuthn registrations and assertions that
// gate destructive approvals and yolo (docs/mobile-contract.md §7). It is a
// narrow verifier for attestation "none", ES256 and EdDSA credentials, with
// user verification required; it is not a general WebAuthn library.
package passkey

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"slices"
	"strings"

	"github.com/dev-dami/deck/pkg/remote/wire"
)

// COSE algorithm and key constants.
const (
	algES256  = -7
	algEdDSA  = -8
	ktyOKP    = 1
	ktyEC2    = 2
	crvP256   = 1
	crvEd2551 = 6
)

// authenticator data flags.
const (
	flagUP = 0x01
	flagUV = 0x04
	flagAT = 0x40
)

// Errors a caller maps to wire.CodeAssertionInvalid.
var (
	ErrInvalid  = errors.New("passkey: invalid")
	ErrReplayed = errors.New("passkey: assertion replayed")
)

func invalid(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, a...))
}

var b64 = base64.RawURLEncoding

// Verifier checks credentials for one relying party.
type Verifier struct {
	// RPID is the relying party id, the hosted app's host (app.example.dev).
	RPID string
	// Origin is the app's origin, https://app.example.dev.
	Origin string
	// AndroidOrigins pins explicitly trusted app signing certificates. It never
	// accepts arbitrary native origins or changes the relying party checks.
	AndroidOrigins []string
}

// ValidAndroidOrigin accepts only canonical unpadded SHA-256 certificate hashes.
func ValidAndroidOrigin(origin string) bool {
	const prefix = "android:apk-key-hash:"
	if !strings.HasPrefix(origin, prefix) {
		return false
	}
	raw := strings.TrimPrefix(origin, prefix)
	hash, err := b64.DecodeString(raw)
	return err == nil && len(hash) == sha256.Size && b64.EncodeToString(hash) == raw
}

// Credential is a registered passkey. Key is the COSE_Key as the
// authenticator produced it; it is what the device store keeps.
type Credential struct {
	ID  []byte
	Key []byte
}

// Challenge is what a phone must sign for an action: SHA-256 over
// "deck-assert-v1" | cid | device id | action digest | nonce, each part
// followed by a zero byte so the parts cannot run into each other.
func Challenge(cid, deviceID, actionDigest, nonce string) []byte {
	h := sha256.New()
	h.Write([]byte("deck-assert-v1"))
	for _, p := range []string{cid, deviceID, actionDigest, nonce} {
		h.Write([]byte{0})
		h.Write([]byte(p))
	}
	return h.Sum(nil)
}

type clientData struct {
	Type        string `json:"type"`
	Challenge   string `json:"challenge"`
	Origin      string `json:"origin"`
	CrossOrigin bool   `json:"crossOrigin"`
}

func (v Verifier) checkClientData(raw []byte, wantType string, challenge []byte) error {
	var cd clientData
	if err := json.Unmarshal(raw, &cd); err != nil {
		return invalid("client data is not JSON")
	}
	if cd.Type != wantType {
		return invalid("wrong ceremony type")
	}
	got, err := b64.DecodeString(cd.Challenge)
	if err != nil || !bytes.Equal(got, challenge) {
		return invalid("challenge does not match")
	}
	trustedAndroid := ValidAndroidOrigin(cd.Origin) && slices.Contains(v.AndroidOrigins, cd.Origin)
	if (cd.Origin != v.Origin && !trustedAndroid) || cd.CrossOrigin {
		return invalid("wrong origin")
	}
	return nil
}

// authData is the fixed head of authenticator data.
type authData struct {
	rpIDHash  [32]byte
	flags     byte
	signCount uint32
	rest      []byte
}

func parseAuthData(b []byte) (authData, error) {
	if len(b) < 37 {
		return authData{}, invalid("authenticator data too short")
	}
	var a authData
	copy(a.rpIDHash[:], b[:32])
	a.flags = b[32]
	a.signCount = binary.BigEndian.Uint32(b[33:37])
	a.rest = b[37:]
	return a, nil
}

func (v Verifier) checkAuthData(a authData) error {
	if a.rpIDHash != sha256.Sum256([]byte(v.RPID)) {
		return invalid("wrong relying party")
	}
	if a.flags&flagUP == 0 {
		return invalid("user presence missing")
	}
	if a.flags&flagUV == 0 {
		return invalid("user verification missing")
	}
	return nil
}

// ParseRegistration verifies a registration with attestation "none" against
// the challenge the computer issued and returns the credential to store.
func (v Verifier) ParseRegistration(challenge, clientDataJSON, attestationObject []byte) (Credential, error) {
	if err := v.checkClientData(clientDataJSON, "webauthn.create", challenge); err != nil {
		return Credential{}, err
	}
	obj, rest, err := decodeCBOR(attestationObject)
	if err != nil || len(rest) != 0 || obj.kind != 5 {
		return Credential{}, invalid("attestation object is malformed")
	}
	if f, ok := obj.getText("fmt"); !ok || f.kind != 3 || f.s != "none" {
		return Credential{}, invalid("only attestation none is accepted")
	}
	ad, ok := obj.getText("authData")
	if !ok || ad.kind != 2 {
		return Credential{}, invalid("authenticator data missing")
	}
	a, err := parseAuthData(ad.b)
	if err != nil {
		return Credential{}, err
	}
	if err := v.checkAuthData(a); err != nil {
		return Credential{}, err
	}
	if a.flags&flagAT == 0 || len(a.rest) < 18 {
		return Credential{}, invalid("no credential in the registration")
	}
	idLen := int(binary.BigEndian.Uint16(a.rest[16:18]))
	if idLen == 0 || idLen > 1023 || len(a.rest) < 18+idLen {
		return Credential{}, invalid("credential id is malformed")
	}
	id := a.rest[18 : 18+idLen]
	keyBytes := a.rest[18+idLen:]
	key, tail, err := decodeCBOR(keyBytes)
	if err != nil || key.kind != 5 {
		return Credential{}, invalid("credential key is malformed")
	}
	if _, err := publicKey(key); err != nil {
		return Credential{}, err
	}
	// Extension data may follow the key; the key itself is what came before.
	keyRaw := keyBytes[:len(keyBytes)-len(tail)]
	return Credential{ID: bytes.Clone(id), Key: bytes.Clone(keyRaw)}, nil
}

// publicKey turns a COSE_Key map into a verification key.
func publicKey(m cborValue) (any, error) {
	kty, _ := m.getInt(1)
	alg, _ := m.getInt(3)
	crv, _ := m.getInt(-1)
	x, hasX := m.getInt(-2)
	switch {
	case kty.n == ktyEC2 && alg.n == algES256 && crv.n == crvP256:
		y, hasY := m.getInt(-3)
		if !hasX || !hasY || len(x.b) != 32 || len(y.b) != 32 {
			return nil, invalid("EC2 key is malformed")
		}
		pub := &ecdsa.PublicKey{Curve: elliptic.P256(), X: new(big.Int).SetBytes(x.b), Y: new(big.Int).SetBytes(y.b)}
		if !pub.Curve.IsOnCurve(pub.X, pub.Y) {
			return nil, invalid("EC2 point is not on the curve")
		}
		return pub, nil
	case kty.n == ktyOKP && alg.n == algEdDSA && crv.n == crvEd2551:
		if !hasX || len(x.b) != ed25519.PublicKeySize {
			return nil, invalid("OKP key is malformed")
		}
		return ed25519.PublicKey(bytes.Clone(x.b)), nil
	}
	return nil, invalid("unsupported key type")
}

// VerifyAssertion checks an assertion against the credential's stored key and
// counter and the challenge the computer expects, and returns the new counter
// to store. A counter that does not advance is ErrReplayed, except that an
// authenticator that never counts (0 and 0, as synced passkeys do) is allowed.
func (v Verifier) VerifyAssertion(key []byte, storedCount uint32, challenge []byte, a wire.Assertion) (uint32, error) {
	authBytes, err1 := b64.DecodeString(a.AuthenticatorData)
	cdBytes, err2 := b64.DecodeString(a.ClientDataJSON)
	sig, err3 := b64.DecodeString(a.Signature)
	if err1 != nil || err2 != nil || err3 != nil {
		return 0, invalid("assertion is not base64url")
	}
	if err := v.checkClientData(cdBytes, "webauthn.get", challenge); err != nil {
		return 0, err
	}
	ad, err := parseAuthData(authBytes)
	if err != nil {
		return 0, err
	}
	if err := v.checkAuthData(ad); err != nil {
		return 0, err
	}
	km, rest, err := decodeCBOR(key)
	if err != nil || len(rest) != 0 || km.kind != 5 {
		return 0, invalid("stored key is malformed")
	}
	pub, err := publicKey(km)
	if err != nil {
		return 0, err
	}
	cdHash := sha256.Sum256(cdBytes)
	signed := append(bytes.Clone(authBytes), cdHash[:]...)
	switch k := pub.(type) {
	case *ecdsa.PublicKey:
		digest := sha256.Sum256(signed)
		if !ecdsa.VerifyASN1(k, digest[:], sig) {
			return 0, invalid("signature does not verify")
		}
	case ed25519.PublicKey:
		if !ed25519.Verify(k, signed, sig) {
			return 0, invalid("signature does not verify")
		}
	}
	if ad.signCount != 0 || storedCount != 0 {
		if ad.signCount <= storedCount {
			return 0, ErrReplayed
		}
	}
	return ad.signCount, nil
}
