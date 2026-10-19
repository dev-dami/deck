// Package push encrypts Web Push payloads on the computer (RFC 8291, content
// coding aes128gcm) so the relay and the push service only ever carry
// ciphertext. The relay adds VAPID and delivers; it cannot read the message.
package push

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"

	"github.com/dev-dami/deck/pkg/remote/wire"
)

// recordSize is the single record size the body declares. A payload plus its
// delimiter and tag must fit in one record.
const recordSize = 4096

var b64 = base64.RawURLEncoding

// ErrTooLarge is returned for a payload that does not fit wire.MaxPushPayloadBytes.
var ErrTooLarge = errors.New("push: payload too large")

// Keys are a subscription's client keys, base64url as the browser gives them.
type Keys struct {
	P256dh string
	Auth   string
}

// Encrypt returns the request body for a push to the subscription. random is
// crypto/rand unless a test needs a fixed ephemeral key and salt.
func Encrypt(payload []byte, k Keys, random io.Reader) ([]byte, error) {
	if len(payload) > wire.MaxPushPayloadBytes {
		return nil, ErrTooLarge
	}
	if random == nil {
		random = rand.Reader
	}
	ephemeral, err := ecdh.P256().GenerateKey(random)
	if err != nil {
		return nil, err
	}
	salt := make([]byte, 16)
	if _, err := io.ReadFull(random, salt); err != nil {
		return nil, err
	}
	return encrypt(payload, k, ephemeral, salt)
}

func encrypt(payload []byte, k Keys, as *ecdh.PrivateKey, salt []byte) ([]byte, error) {
	uaRaw, err := b64.DecodeString(k.P256dh)
	if err != nil {
		return nil, fmt.Errorf("push: p256dh is not base64url: %w", err)
	}
	ua, err := ecdh.P256().NewPublicKey(uaRaw)
	if err != nil {
		return nil, fmt.Errorf("push: p256dh is not a P-256 point: %w", err)
	}
	authSecret, err := b64.DecodeString(k.Auth)
	if err != nil || len(authSecret) != 16 {
		return nil, errors.New("push: auth must be 16 bytes, base64url")
	}
	secret, err := as.ECDH(ua)
	if err != nil {
		return nil, err
	}
	asPub := as.PublicKey().Bytes()

	// RFC 8291 §3.4: mix the shared secret with the subscription's auth secret.
	prkKey, err := hkdf.Extract(sha256.New, secret, authSecret)
	if err != nil {
		return nil, err
	}
	keyInfo := append([]byte("WebPush: info\x00"), uaRaw...)
	keyInfo = append(keyInfo, asPub...)
	ikm, err := hkdf.Expand(sha256.New, prkKey, string(keyInfo), 32)
	if err != nil {
		return nil, err
	}

	// RFC 8188: derive the content key and nonce from the salt.
	prk, err := hkdf.Extract(sha256.New, ikm, salt)
	if err != nil {
		return nil, err
	}
	cek, err := hkdf.Expand(sha256.New, prk, "Content-Encoding: aes128gcm\x00", 16)
	if err != nil {
		return nil, err
	}
	nonce, err := hkdf.Expand(sha256.New, prk, "Content-Encoding: nonce\x00", 12)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(cek)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}

	// One record: the payload and the 0x02 "last record" delimiter.
	plain := append(append([]byte(nil), payload...), 0x02)
	if len(plain)+gcm.Overhead() > recordSize {
		return nil, ErrTooLarge
	}
	header := make([]byte, 0, 16+4+1+len(asPub))
	header = append(header, salt...)
	header = binary.BigEndian.AppendUint32(header, recordSize)
	header = append(header, byte(len(asPub)))
	header = append(header, asPub...)
	return gcm.Seal(header, nonce, plain, nil), nil
}

// Decrypt is the user agent's side of Encrypt: it opens a body with the
// subscription's private key and auth secret. Browsers do this natively; it
// exists so tests of code that sends pushes can read them.
func Decrypt(body []byte, ua *ecdh.PrivateKey, authSecret []byte) ([]byte, error) {
	if len(body) < 21 {
		return nil, errors.New("push: body too short")
	}
	salt := body[:16]
	idlen := int(body[20])
	if len(body) < 21+idlen+aesGCMTag {
		return nil, errors.New("push: body too short")
	}
	asRaw := body[21 : 21+idlen]
	as, err := ecdh.P256().NewPublicKey(asRaw)
	if err != nil {
		return nil, err
	}
	secret, err := ua.ECDH(as)
	if err != nil {
		return nil, err
	}
	prkKey, err := hkdf.Extract(sha256.New, secret, authSecret)
	if err != nil {
		return nil, err
	}
	info := append([]byte("WebPush: info\x00"), ua.PublicKey().Bytes()...)
	info = append(info, asRaw...)
	ikm, err := hkdf.Expand(sha256.New, prkKey, string(info), 32)
	if err != nil {
		return nil, err
	}
	prk, err := hkdf.Extract(sha256.New, ikm, salt)
	if err != nil {
		return nil, err
	}
	cek, err := hkdf.Expand(sha256.New, prk, "Content-Encoding: aes128gcm\x00", 16)
	if err != nil {
		return nil, err
	}
	nonce, err := hkdf.Expand(sha256.New, prk, "Content-Encoding: nonce\x00", 12)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(cek)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	plain, err := gcm.Open(nil, nonce, body[21+idlen:], nil)
	if err != nil {
		return nil, err
	}
	if len(plain) == 0 || plain[len(plain)-1] != 0x02 {
		return nil, errors.New("push: missing record delimiter")
	}
	return plain[:len(plain)-1], nil
}

const aesGCMTag = 16
