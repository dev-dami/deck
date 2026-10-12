// Package auth holds the paired-device store, one-time pairing codes and the
// failed-attempt limiter behind `deck serve`.
//
// Secrets are never stored: devices.json keeps sha256(secret), so a leaked
// file cannot be replayed as a token.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/dev-dami/deck/internal/lock"
)

var (
	ErrInvalidToken = errors.New("invalid token")
	ErrInvalidCode  = errors.New("invalid or expired pairing code")
	ErrNotFound     = errors.New("device not found")
)

// CodeTTL is how long a pairing code stays valid.
const CodeTTL = 10 * time.Minute

// seenGranularity bounds how often last_seen rewrites the file.
const seenGranularity = time.Minute

const lockWait = 5 * time.Second

// Device is a paired phone.
type Device struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Hash      string    `json:"hash"`
	CreatedAt time.Time `json:"created_at"`
	LastSeen  time.Time `json:"last_seen"`
	LastAddr  string    `json:"last_addr,omitempty"`
}

type file struct {
	Devices []Device `json:"devices"`
}

// Store persists devices under dir and holds pairing codes in memory.
type Store struct {
	dir string
	now func() time.Time

	mu    sync.Mutex
	codes map[string]time.Time
}

// Option customizes a Store; tests use it to pin the clock.
type Option func(*Store)

// WithClock replaces time.Now.
func WithClock(now func() time.Time) Option { return func(s *Store) { s.now = now } }

// New returns a Store keeping devices.json in dir, creating dir if needed.
func New(dir string, opts ...Option) (*Store, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create remote dir: %w", err)
	}
	s := &Store{dir: dir, now: time.Now, codes: map[string]time.Time{}}
	for _, o := range opts {
		o(s)
	}
	return s, nil
}

func (s *Store) path() string { return filepath.Join(s.dir, "devices.json") }

// withFile runs fn on the freshly loaded device list under the cross-process
// lock, and saves the list if fn reports a change. Several `deck serve`
// processes (one per project) share this file.
func (s *Store) withFile(fn func(*file) (changed bool, err error)) error {
	ctx, cancel := context.WithTimeout(context.Background(), lockWait)
	defer cancel()
	release, err := lock.File(ctx, s.path()+".lock")
	if err != nil {
		return fmt.Errorf("lock devices: %w", err)
	}
	defer release()

	var f file
	data, err := os.ReadFile(s.path())
	switch {
	case errors.Is(err, os.ErrNotExist):
	case err != nil:
		return fmt.Errorf("read devices: %w", err)
	default:
		if err := json.Unmarshal(data, &f); err != nil {
			return fmt.Errorf("parse devices: %w", err)
		}
	}
	changed, err := fn(&f)
	if err != nil || !changed {
		return err
	}
	out, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(s.path(), out)
}

func writeAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "devices.*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	fail := func(err error) error {
		tmp.Close()
		os.Remove(name)
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		return fail(err)
	}
	if err := tmp.Chmod(0o600); err != nil {
		return fail(err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return err
	}
	if err := os.Rename(name, path); err != nil {
		os.Remove(name)
		return err
	}
	return nil
}

func random(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func digest(s string) string {
	sum := sha256.Sum256([]byte(s))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// NewCode mints a single-use pairing code valid for CodeTTL.
func (s *Store) NewCode() (string, error) {
	code, err := random(32)
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	for h, exp := range s.codes {
		if now.After(exp) {
			delete(s.codes, h)
		}
	}
	s.codes[digest(code)] = now.Add(CodeTTL)
	return code, nil
}

// takeCode consumes code; of two racing callers exactly one succeeds.
func (s *Store) takeCode(code string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	h := digest(code)
	exp, ok := s.codes[h]
	if !ok {
		return false
	}
	delete(s.codes, h)
	return !s.now().After(exp)
}

// Pair consumes a pairing code and registers a device. The returned token is
// shown to the caller once: only its hash is kept.
func (s *Store) Pair(code, name, addr string) (token string, dev Device, err error) {
	if !s.takeCode(code) {
		return "", Device{}, ErrInvalidCode
	}
	name = strings.TrimSpace(name)
	if name == "" {
		name = "Phone"
	}
	if len(name) > 64 {
		name = name[:64]
	}
	id, err := random(9)
	if err != nil {
		return "", Device{}, err
	}
	secret, err := random(32)
	if err != nil {
		return "", Device{}, err
	}
	now := s.now()
	dev = Device{ID: id, Name: name, Hash: digest(secret), CreatedAt: now, LastSeen: now, LastAddr: addr}
	err = s.withFile(func(f *file) (bool, error) {
		f.Devices = append(f.Devices, dev)
		return true, nil
	})
	if err != nil {
		return "", Device{}, err
	}
	return id + "." + secret, dev, nil
}

// Verify checks a "<deviceID>.<secret>" token against the file on disk, so a
// revoke made by another process applies on the next connect.
func (s *Store) Verify(token, addr string) (Device, error) {
	id, secret, ok := strings.Cut(token, ".")
	if !ok || id == "" || secret == "" {
		return Device{}, ErrInvalidToken
	}
	want := digest(secret)
	var found Device
	err := s.withFile(func(f *file) (bool, error) {
		for i := range f.Devices {
			d := &f.Devices[i]
			if d.ID != id {
				continue
			}
			if subtle.ConstantTimeCompare([]byte(d.Hash), []byte(want)) != 1 {
				return false, ErrInvalidToken
			}
			found = *d
			now := s.now()
			if now.Sub(d.LastSeen) < seenGranularity && d.LastAddr == addr {
				return false, nil
			}
			d.LastSeen, d.LastAddr = now, addr
			found = *d
			return true, nil
		}
		return false, ErrInvalidToken
	})
	if err != nil {
		return Device{}, err
	}
	return found, nil
}

// List returns every paired device.
func (s *Store) List() ([]Device, error) {
	var out []Device
	err := s.withFile(func(f *file) (bool, error) {
		out = append(out, f.Devices...)
		return false, nil
	})
	return out, err
}

// Revoke removes one device.
func (s *Store) Revoke(id string) error {
	return s.withFile(func(f *file) (bool, error) {
		for i, d := range f.Devices {
			if d.ID == id {
				f.Devices = append(f.Devices[:i], f.Devices[i+1:]...)
				return true, nil
			}
		}
		return false, ErrNotFound
	})
}

// RevokeAll removes every device and reports how many there were.
func (s *Store) RevokeAll() (int, error) {
	n := 0
	err := s.withFile(func(f *file) (bool, error) {
		n = len(f.Devices)
		f.Devices = nil
		return n > 0, nil
	})
	return n, err
}
