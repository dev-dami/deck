// Package sub is the Source Update Bridge: a content-addressed store of file
// chunks, snapshots of work trees, and capsules — immutable change sets an
// agent captures in its tree and another verifies and applies in its own.
//
// Everything is addressed by the blake3 hash of its bytes, so an object is
// written once however many snapshots, capsules or agents refer to it, and a
// small edit to a large file costs only the chunks it touched.
package sub

import (
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/zeebo/blake3"
)

// ID names an object: "blake3:" and the hex hash of its bytes.
type ID string

const idPrefix = "blake3:"

// Sum is the ID of b.
func Sum(b []byte) ID {
	h := blake3.Sum256(b)
	return ID(idPrefix + hex.EncodeToString(h[:]))
}

// Valid reports whether id is well formed.
func (id ID) Valid() bool {
	h, ok := strings.CutPrefix(string(id), idPrefix)
	if !ok || len(h) != 64 {
		return false
	}
	_, err := hex.DecodeString(h)
	return err == nil
}

// Short is the first 12 hex digits, for display.
func (id ID) Short() string {
	h := strings.TrimPrefix(string(id), idPrefix)
	return h[:min(12, len(h))]
}

// ErrNotFound is returned for an object the store does not have.
var ErrNotFound = errors.New("sub: object not found")

// Store is a directory of objects, shared by every work tree on the machine:
// handing an ID to another agent here moves no bytes at all.
type Store struct {
	dir string
}

// Open opens (creating if needed) the store in dir.
func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(filepath.Join(dir, "objects"), 0o755); err != nil {
		return nil, err
	}
	return &Store{dir: dir}, nil
}

// Dir is the store's directory.
func (s *Store) Dir() string { return s.dir }

func (s *Store) path(id ID) (string, error) {
	if !id.Valid() {
		return "", fmt.Errorf("sub: bad id %q", id)
	}
	h := strings.TrimPrefix(string(id), idPrefix)
	return filepath.Join(s.dir, "objects", h[:2], h[2:]), nil
}

// Has reports whether the store holds id.
func (s *Store) Has(id ID) bool {
	p, err := s.path(id)
	if err != nil {
		return false
	}
	_, err = os.Stat(p)
	return err == nil
}

// Put stores b and returns its ID. Storing what is already there is a stat.
func (s *Store) Put(b []byte) (ID, error) {
	id := Sum(b)
	p, _ := s.path(id)
	if _, err := os.Stat(p); err == nil {
		return id, nil
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return "", err
	}
	if err := writeAtomic(p, b, 0o444); err != nil {
		return "", err
	}
	return id, nil
}

// Get reads id, checking the bytes still hash to it.
func (s *Store) Get(id ID) ([]byte, error) {
	p, err := s.path(id)
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, id.Short())
	}
	if err != nil {
		return nil, err
	}
	if Sum(b) != id {
		return nil, fmt.Errorf("sub: object %s is corrupt", id.Short())
	}
	return b, nil
}

// writeAtomic writes b to a temporary file beside p and renames it into
// place, so a reader sees all of b or nothing.
func writeAtomic(p string, b []byte, mode os.FileMode) error {
	f, err := os.CreateTemp(filepath.Dir(p), ".sub-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	_, err = f.Write(b)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Chmod(tmp, mode)
	}
	if err == nil {
		err = os.Rename(tmp, p)
	}
	if err != nil {
		os.Remove(tmp)
		if _, serr := os.Stat(p); serr == nil && strings.Contains(p, "objects") {
			return nil // another writer stored the same object first
		}
	}
	return err
}
