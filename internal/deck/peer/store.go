package peer

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/dev-dami/deck/internal/deck/config"
)

// StoreFile is the pinned-peer list in the peers directory.
const StoreFile = "pinned.json"

// Pinned is one paired Deck: the only key allowed to connect as Name, and the
// only host key accepted when dialing it.
type Pinned struct {
	Name        string    `json:"name"`
	Addr        string    `json:"addr,omitempty"` // empty for a peer that only dials in
	PublicKey   string    `json:"public_key"`     // authorized_keys format
	Fingerprint string    `json:"fingerprint"`    // SHA256 of PublicKey
	Role        string    `json:"role"`
	Added       time.Time `json:"added"`

	key ssh.PublicKey
}

// Key is the parsed public key.
func (p Pinned) Key() ssh.PublicKey { return p.key }

// NewPinned builds a pin for key.
func NewPinned(name, addr, role string, key ssh.PublicKey) Pinned {
	if role == "" {
		role = config.PeerRoleObserver
	}
	return Pinned{
		Name:        name,
		Addr:        addr,
		PublicKey:   strings.TrimSpace(string(ssh.MarshalAuthorizedKey(key))),
		Fingerprint: Fingerprint(key),
		Role:        role,
		Added:       time.Now().UTC().Truncate(time.Second),
		key:         key,
	}
}

// Store is the pinned-peer list on disk. Every read goes to the file, so a
// peer removed with `deck peer remove` is refused by a running Deck at its
// next connection attempt and its next call, and its open links are closed
// by the Manager's sweep. A file or directory others can write is refused.
type Store struct {
	path string
	mu   sync.Mutex
}

// OpenStore returns the store in dir. Nothing is read until it is used.
func OpenStore(dir string) *Store {
	return &Store{path: filepath.Join(dir, StoreFile)}
}

// Path is the store's file.
func (s *Store) Path() string { return s.path }

type storeFile struct {
	Version int      `json:"version"`
	Peers   []Pinned `json:"peers"`
}

// load reads and checks every entry; a bad entry fails the whole load, so a
// damaged file never lets anything in.
func (s *Store) load() ([]Pinned, error) {
	data, err := os.ReadFile(s.path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("peer: read %s: %w", s.path, err)
	}
	if err := checkLoad(s.path, false); err != nil {
		return nil, err
	}
	var f storeFile
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("peer: parse %s: %w", s.path, err)
	}
	seen := map[string]bool{}
	for i := range f.Peers {
		p := &f.Peers[i]
		if !config.ValidPeerName(p.Name) || seen[p.Name] {
			return nil, fmt.Errorf("peer: %s: bad or duplicate name %q", s.path, p.Name)
		}
		seen[p.Name] = true
		k, _, _, _, err := ssh.ParseAuthorizedKey([]byte(p.PublicKey))
		if err != nil {
			return nil, fmt.Errorf("peer: %s: key of %q: %w", s.path, p.Name, err)
		}
		if k.Type() != ssh.KeyAlgoED25519 {
			return nil, fmt.Errorf("peer: %s: key of %q is %s, want ed25519", s.path, p.Name, k.Type())
		}
		if Fingerprint(k) != p.Fingerprint {
			return nil, fmt.Errorf("peer: %s: fingerprint of %q does not match its key", s.path, p.Name)
		}
		p.key = k
	}
	return f.Peers, nil
}

func (s *Store) save(peers []Pinned) error {
	sort.Slice(peers, func(i, j int) bool { return peers[i].Name < peers[j].Name })
	data, err := json.MarshalIndent(storeFile{Version: 1, Peers: peers}, "", "  ")
	if err != nil {
		return err
	}
	return writePrivateFile(s.path, append(data, '\n'), false)
}

// List returns every pinned peer, sorted by name.
func (s *Store) List() ([]Pinned, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.load()
}

// Get returns the peer pinned under name.
func (s *Store) Get(name string) (Pinned, bool, error) {
	peers, err := s.List()
	if err != nil {
		return Pinned{}, false, err
	}
	for _, p := range peers {
		if p.Name == name {
			return p, true, nil
		}
	}
	return Pinned{}, false, nil
}

// ByKey returns the peer pinned with key.
func (s *Store) ByKey(key ssh.PublicKey) (Pinned, bool, error) {
	peers, err := s.List()
	if err != nil {
		return Pinned{}, false, err
	}
	for _, p := range peers {
		if sameKey(p.key, key) {
			return p, true, nil
		}
	}
	return Pinned{}, false, nil
}

// ErrPinTaken is returned by Pin when a different key is already pinned
// under the name.
var ErrPinTaken = errors.New("peer: a different key is already pinned under that name")

// Pin adds p, or updates the pin of the same name and key (e.g. its address).
// A different key already pinned under the name is refused with ErrPinTaken:
// only Replace swaps a peer's key. A key already pinned under another name is
// refused too: one Deck has one name here.
func (s *Store) Pin(p Pinned) error { return s.pin(p, false) }

// Replace is Pin that may also swap the key pinned under p.Name, for a peer
// that really made a new key (re-paired with --replace).
func (s *Store) Replace(p Pinned) error { return s.pin(p, true) }

func (s *Store) pin(p Pinned, replace bool) error {
	if !config.ValidPeerName(p.Name) {
		return fmt.Errorf("peer: bad name %q: use 1-32 of a-z, 0-9, '-', '_', '.'", p.Name)
	}
	if p.key == nil {
		return errors.New("peer: pin has no key")
	}
	// load refuses anything else, so pinning it would lock every peer out.
	if p.key.Type() != ssh.KeyAlgoED25519 {
		return fmt.Errorf("peer: key of %q is %s, want ed25519", p.Name, p.key.Type())
	}
	if p.Addr != "" {
		if err := config.ValidPeerAddr(p.Addr); err != nil {
			return fmt.Errorf("peer: bad address %q: %v", p.Addr, err)
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	peers, err := s.load()
	if err != nil {
		return err
	}
	out := peers[:0]
	for _, q := range peers {
		if q.Name == p.Name {
			if !replace && !sameKey(q.key, p.key) {
				return fmt.Errorf("%w (%q is pinned as %s)", ErrPinTaken, q.Name, q.Fingerprint)
			}
			continue
		}
		if sameKey(q.key, p.key) {
			return fmt.Errorf("peer: that key is already pinned as %q", q.Name)
		}
		out = append(out, q)
	}
	return s.save(append(out, p))
}

// Remove unpins name; it reports whether it was pinned.
func (s *Store) Remove(name string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	peers, err := s.load()
	if err != nil {
		return false, err
	}
	out := peers[:0]
	found := false
	for _, p := range peers {
		if p.Name == name {
			found = true
			continue
		}
		out = append(out, p)
	}
	if !found {
		return false, nil
	}
	return true, s.save(out)
}
