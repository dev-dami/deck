// Package identity is a computer's long-term keys for Deck Mobile: the
// Ed25519 key that proves it owns its relay id, and the X25519 static key of
// the Noise channel (docs/mobile-contract.md §3, §5).
package identity

import (
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/dev-dami/deck/internal/deck/peer"
)

const fileName = "identity.json"

// cidLen is the length of a computer id: the first 22 characters of the
// unpadded base64url of sha256(relay public key), 132 bits.
const cidLen = 22

// Identity holds both private keys. Its zero value is not usable; get one
// from LoadOrCreate.
type Identity struct {
	relay  ed25519.PrivateKey
	static *ecdh.PrivateKey
}

type stored struct {
	RelayKey  string `json:"relay_key"`
	StaticKey string `json:"static_key"`
}

// DefaultDir is ~/.deck/remote, the computer-wide state directory. It is not
// under ~/.deck/peers (remote-contract R5).
func DefaultDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("find home directory: %w", err)
	}
	return filepath.Join(home, ".deck", "remote"), nil
}

// LoadOrCreate reads dir/identity.json, or makes new keys and writes the file
// restricted to the current user. The daemon's single-instance lock is what
// keeps two processes from creating it at once.
func LoadOrCreate(dir string) (*Identity, error) {
	path := filepath.Join(dir, fileName)
	raw, err := os.ReadFile(path)
	switch {
	case err == nil:
		return decode(raw)
	case !errors.Is(err, fs.ErrNotExist):
		return nil, fmt.Errorf("read identity: %w", err)
	}
	id, err := generate()
	if err != nil {
		return nil, err
	}
	raw, err = json.Marshal(stored{
		RelayKey:  b64.EncodeToString(id.relay.Seed()),
		StaticKey: b64.EncodeToString(id.static.Bytes()),
	})
	if err != nil {
		return nil, err
	}
	if err := peer.WritePrivateFile(path, raw); err != nil {
		return nil, fmt.Errorf("write identity: %w", err)
	}
	return id, nil
}

var b64 = base64.RawURLEncoding

func generate() (*Identity, error) {
	_, relay, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate relay key: %w", err)
	}
	static, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate static key: %w", err)
	}
	return &Identity{relay: relay, static: static}, nil
}

func decode(raw []byte) (*Identity, error) {
	var s stored
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, fmt.Errorf("identity file is not valid: %w", err)
	}
	seed, err := b64.DecodeString(s.RelayKey)
	if err != nil || len(seed) != ed25519.SeedSize {
		return nil, errors.New("identity file has a bad relay key")
	}
	sk, err := b64.DecodeString(s.StaticKey)
	if err != nil {
		return nil, errors.New("identity file has a bad static key")
	}
	static, err := ecdh.X25519().NewPrivateKey(sk)
	if err != nil {
		return nil, errors.New("identity file has a bad static key")
	}
	return &Identity{relay: ed25519.NewKeyFromSeed(seed), static: static}, nil
}

// RelayPublic is the key the relay registers a computer under.
func (i *Identity) RelayPublic() ed25519.PublicKey {
	return i.relay.Public().(ed25519.PublicKey)
}

// CID is the computer id, stable for the life of the file.
func (i *Identity) CID() string { return CIDOf(i.RelayPublic()) }

// CIDOf derives the computer id of a relay public key.
func CIDOf(pub ed25519.PublicKey) string {
	sum := sha256.Sum256(pub)
	return b64.EncodeToString(sum[:])[:cidLen]
}

// Sign signs msg with the relay key.
func (i *Identity) Sign(msg []byte) []byte { return ed25519.Sign(i.relay, msg) }

// StaticPublic is the Noise static public key shown in the pairing QR.
func (i *Identity) StaticPublic() []byte { return i.static.PublicKey().Bytes() }

// StaticPrivate is the Noise static private key. It leaves this package only
// to be handed to the Noise handshake.
func (i *Identity) StaticPrivate() []byte { return i.static.Bytes() }
