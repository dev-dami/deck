package peer

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/subtle"
	"encoding/pem"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"golang.org/x/crypto/ssh"
)

// IdentityFile is the name of the private key file in the peers directory.
const IdentityFile = "id_ed25519"

// ErrNoIdentity is returned by LoadIdentity when no key has been made yet.
var ErrNoIdentity = errors.New("peer: no identity yet (run deck peer fingerprint to create one)")

// Identity is this Deck instance's ed25519 key. The same key authenticates
// this Deck when it dials a peer and serves as its SSH host key when a peer
// dials it, so a pinned peer is one fingerprint in both directions.
type Identity struct {
	Signer ssh.Signer
	Path   string
}

// PublicKey is the identity's public key.
func (id *Identity) PublicKey() ssh.PublicKey { return id.Signer.PublicKey() }

// Fingerprint is the identity's SHA256 fingerprint ("SHA256:...").
func (id *Identity) Fingerprint() string { return Fingerprint(id.PublicKey()) }

// Fingerprint formats a public key's SHA256 fingerprint the way OpenSSH
// does: "SHA256:" and unpadded base64.
func Fingerprint(k ssh.PublicKey) string { return ssh.FingerprintSHA256(k) }

// sameKey reports whether a and b are the same public key.
func sameKey(a, b ssh.PublicKey) bool {
	if a == nil || b == nil {
		return false
	}
	return subtle.ConstantTimeCompare(a.Marshal(), b.Marshal()) == 1
}

// LoadOrCreateIdentity loads the identity in dir, making a new ed25519 key on
// first use. The directory and key file are restricted to the current user.
func LoadOrCreateIdentity(dir string) (*Identity, error) {
	id, err := LoadIdentity(dir)
	if !errors.Is(err, ErrNoIdentity) {
		return id, err
	}
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	block, err := ssh.MarshalPrivateKey(priv, "deck peer identity")
	if err != nil {
		return nil, err
	}
	path := filepath.Join(dir, IdentityFile)
	err = writePrivateFile(path, pem.EncodeToMemory(block), true)
	if err != nil && !errors.Is(err, fs.ErrExist) {
		return nil, fmt.Errorf("peer: write identity: %w", err)
	}
	// Either ours or one another process made at the same moment.
	return LoadIdentity(dir)
}

// LoadIdentity loads the identity in dir without creating one; it returns
// ErrNoIdentity when there is none.
func LoadIdentity(dir string) (*Identity, error) {
	path := filepath.Join(dir, IdentityFile)
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, ErrNoIdentity
	}
	if err != nil {
		return nil, fmt.Errorf("peer: read identity: %w", err)
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return nil, fmt.Errorf("peer: identity %s is empty", path)
	}
	signer, err := ssh.ParsePrivateKey(data)
	if err != nil {
		return nil, fmt.Errorf("peer: parse identity %s: %w", path, err)
	}
	if signer.PublicKey().Type() != ssh.KeyAlgoED25519 {
		return nil, fmt.Errorf("peer: identity %s is %s, want ed25519", path, signer.PublicKey().Type())
	}
	return &Identity{Signer: signer, Path: path}, nil
}
