package push

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"strings"
	"testing"

	"github.com/dev-dami/deck/pkg/remote/wire"
)

// The worked example of RFC 8291 appendix A.
const (
	rfcPlaintext = "When I grow up, I want to be a watermelon"
	rfcUAPublic  = "BCVxsr7N_eNgVRqvHtD0zTZsEc6-VV-JvLexhqUzORcxaOzi6-AYWXvTBHm4bjyPjs7Vd8pZGH6SRpkNtoIAiw4"
	rfcAuth      = "BTBZMqHH6r4Tts7J_aSIgg"
	rfcASPrivate = "yfWPiYE-n46HLnH0KqZOF1fJJU3MYrct3AELtAQ-oRw"
	rfcSalt      = "DGv6ra1nlYgDCS1FRnbzlw"
	rfcBody      = "DGv6ra1nlYgDCS1FRnbzlwAAEABBBP4z9KsN6nGRTbVYI_c7VJSPQTBtkgcy27mlmlMoZIIgDll6e3vCYLocInmYWAmS6TlzAC8wEqKK6PBru3jl7A_yl95bQpu6cVPTpK4Mqgkf1CXztLVBSt2Ks3oZwbuwXPXLWyouBWLVWGNWQexSgSxsj_Qulcy4a-fN"
)

func TestRFC8291Example(t *testing.T) {
	priv, err := b64.DecodeString(rfcASPrivate)
	if err != nil {
		t.Fatal(err)
	}
	as, err := ecdh.P256().NewPrivateKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	salt, _ := b64.DecodeString(rfcSalt)
	got, err := encrypt([]byte(rfcPlaintext), Keys{P256dh: rfcUAPublic, Auth: rfcAuth}, as, salt)
	if err != nil {
		t.Fatal(err)
	}
	if want := rfcBody; b64.EncodeToString(got) != want {
		t.Fatalf("body differs from the RFC's example:\n got %s\nwant %s", b64.EncodeToString(got), want)
	}
}

// decrypt is the user agent's side, written from the RFC independently of
// encrypt, so a round trip checks more than that the code agrees with itself.
func decrypt(t *testing.T, body []byte, ua *ecdh.PrivateKey, auth []byte) []byte {
	t.Helper()
	salt := body[:16]
	if rs := binary.BigEndian.Uint32(body[16:20]); rs != recordSize {
		t.Fatalf("record size %d", rs)
	}
	idlen := int(body[20])
	asPubRaw := body[21 : 21+idlen]
	ct := body[21+idlen:]
	asPub, err := ecdh.P256().NewPublicKey(asPubRaw)
	if err != nil {
		t.Fatal(err)
	}
	secret, err := ua.ECDH(asPub)
	if err != nil {
		t.Fatal(err)
	}
	prkKey, _ := hkdf.Extract(sha256.New, secret, auth)
	info := append([]byte("WebPush: info\x00"), ua.PublicKey().Bytes()...)
	info = append(info, asPubRaw...)
	ikm, _ := hkdf.Expand(sha256.New, prkKey, string(info), 32)
	prk, _ := hkdf.Extract(sha256.New, ikm, salt)
	cek, _ := hkdf.Expand(sha256.New, prk, "Content-Encoding: aes128gcm\x00", 16)
	nonce, _ := hkdf.Expand(sha256.New, prk, "Content-Encoding: nonce\x00", 12)
	block, _ := aes.NewCipher(cek)
	gcm, _ := cipher.NewGCM(block)
	plain, err := gcm.Open(nil, nonce, ct, nil)
	if err != nil {
		t.Fatalf("the user agent cannot decrypt: %v", err)
	}
	if plain[len(plain)-1] != 0x02 {
		t.Fatal("missing record delimiter")
	}
	return plain[:len(plain)-1]
}

func subscription(t *testing.T) (*ecdh.PrivateKey, []byte, Keys) {
	t.Helper()
	ua, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	auth := make([]byte, 16)
	_, _ = rand.Read(auth)
	return ua, auth, Keys{P256dh: b64.EncodeToString(ua.PublicKey().Bytes()), Auth: b64.EncodeToString(auth)}
}

func TestRoundTripAndFreshKeysEveryTime(t *testing.T) {
	ua, auth, keys := subscription(t)
	payload := []byte(`{"kind":"approval","title":"Run: rm -rf build"}`)
	a, err := Encrypt(payload, keys, nil)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := Encrypt(payload, keys, nil)
	if bytes.Equal(a, b) {
		t.Fatal("two pushes of the same payload are identical: the ephemeral key or salt is reused")
	}
	if got := decrypt(t, a, ua, auth); !bytes.Equal(got, payload) {
		t.Fatalf("decrypted %q", got)
	}
	if bytes.Contains(a, []byte("rm -rf")) {
		t.Fatal("plaintext visible in the body")
	}
}

func TestOnlyTheSubscriptionHolderCanRead(t *testing.T) {
	_, _, keys := subscription(t)
	other, otherAuth, _ := subscription(t)
	body, err := Encrypt([]byte("secret"), keys, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Another browser's keys derive a different content key.
	asPub, _ := ecdh.P256().NewPublicKey(body[21 : 21+int(body[20])])
	secret, _ := other.ECDH(asPub)
	prkKey, _ := hkdf.Extract(sha256.New, secret, otherAuth)
	info := append([]byte("WebPush: info\x00"), other.PublicKey().Bytes()...)
	info = append(info, body[21:21+int(body[20])]...)
	ikm, _ := hkdf.Expand(sha256.New, prkKey, string(info), 32)
	prk, _ := hkdf.Extract(sha256.New, ikm, body[:16])
	cek, _ := hkdf.Expand(sha256.New, prk, "Content-Encoding: aes128gcm\x00", 16)
	nonce, _ := hkdf.Expand(sha256.New, prk, "Content-Encoding: nonce\x00", 12)
	block, _ := aes.NewCipher(cek)
	gcm, _ := cipher.NewGCM(block)
	if _, err := gcm.Open(nil, nonce, body[21+int(body[20]):], nil); err == nil {
		t.Fatal("another subscription decrypted the push")
	}
}

func TestSizeLimit(t *testing.T) {
	_, _, keys := subscription(t)
	if _, err := Encrypt(make([]byte, wire.MaxPushPayloadBytes), keys, nil); err != nil {
		t.Fatalf("a payload at the cap: %v", err)
	}
	if _, err := Encrypt(make([]byte, wire.MaxPushPayloadBytes+1), keys, nil); err != ErrTooLarge {
		t.Fatalf("over the cap: %v", err)
	}
}

func TestBadKeysAreRefused(t *testing.T) {
	_, _, good := subscription(t)
	for name, k := range map[string]Keys{
		"p256dh not base64":  {P256dh: "!!!", Auth: good.Auth},
		"p256dh not a point": {P256dh: b64.EncodeToString(bytes.Repeat([]byte{4}, 65)), Auth: good.Auth},
		"p256dh too short":   {P256dh: "AAAA", Auth: good.Auth},
		"auth wrong length":  {P256dh: good.P256dh, Auth: "AAAA"},
		"auth not base64":    {P256dh: good.P256dh, Auth: strings.Repeat("!", 22)},
		"empty":              {},
	} {
		if _, err := Encrypt([]byte("x"), k, nil); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}
