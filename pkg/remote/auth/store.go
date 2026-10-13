// Package auth holds the paired-device store, the one-time pairing code,
// the per-source and per-device limits and the LAN certificate behind
// `deck remote` (docs/remote-contract.md R1, R3, R9, R16).
//
// Everything lives under <data dir>/remote, private to the current user:
// devices.json (the server id and the devices, with sha256 of each secret,
// never the secret), pair.json (the armed code's hash), tls.{crt,key} and
// server.json (where the running server listens, for `deck remote pair`).
// Writes are atomic and serialised by remote.lock across processes; reads
// never take the lock.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/dev-dami/deck/internal/deck/peer"
	"github.com/dev-dami/deck/internal/lock"
	"github.com/dev-dami/deck/pkg/remote/wire"
)

// Errors a Store reports.
var (
	ErrInvalidToken = errors.New("invalid token")
	ErrExpired      = errors.New("token expired")
	ErrInvalidCode  = errors.New("invalid or expired pairing code")
	ErrNotFound     = errors.New("device not found")
	ErrLocked       = errors.New("the device store is busy")
)

// File names under the store's directory.
const (
	DevicesFile = "devices.json"
	PairFile    = "pair.json"
	ServerFile  = "server.json"
	CertFile    = "tls.crt"
	KeyFile     = "tls.key"
	lockFile    = "remote.lock"
)

// lockWait bounds how long a write waits for the cross-process lock.
const lockWait = 5 * time.Second

// Device is a paired phone as devices.json keeps it.
type Device struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Hash      string    `json:"hash"`
	CreatedAt time.Time `json:"created_at"`
	LastSeen  time.Time `json:"last_seen,omitzero"`
	AllowYolo bool      `json:"allow_yolo,omitempty"`
}

// lastActive is when the device was last used: its last connection, or
// its pairing.
func (d Device) lastActive() time.Time {
	if d.LastSeen.IsZero() {
		return d.CreatedAt
	}
	return d.LastSeen
}

// ExpiresAt is when the key stops working unless the device connects.
func (d Device) ExpiresAt() time.Time { return d.lastActive().Add(wire.DeviceKeyExpiry) }

// Wire converts the record for the phone; self marks the caller's device.
func (d Device) Wire(self bool) wire.Device {
	return wire.Device{
		ID: d.ID, Name: d.Name, CreatedAt: d.CreatedAt, LastSeen: d.LastSeen,
		ExpiresAt: d.ExpiresAt(), AllowYolo: d.AllowYolo, Self: self,
	}
}

// file is devices.json.
type file struct {
	ServerID string   `json:"server_id"`
	Devices  []Device `json:"devices"`
}

// Store keeps the devices of one data dir.
type Store struct {
	dir      string
	now      func() time.Time
	serverID string

	mu      sync.Mutex
	pending map[string]time.Time // last-seen times not yet written
	written map[string]time.Time // last-seen times written by this process
}

// Option customizes a Store; tests use it to pin the clock.
type Option func(*Store)

// WithClock replaces time.Now.
func WithClock(now func() time.Time) Option { return func(s *Store) { s.now = now } }

// Dir is the store directory of a data dir.
func Dir(dataDir string) string { return filepath.Join(dataDir, "remote") }

// Open returns the Store of dir (the directory Dir names), creating it and
// its server id when missing.
func Open(dir string, opts ...Option) (*Store, error) {
	s := &Store{dir: dir, now: time.Now, pending: map[string]time.Time{}, written: map[string]time.Time{}}
	for _, o := range opts {
		o(s)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create remote dir: %w", err)
	}
	if err := peer.RestrictPrivate(dir, true); err != nil {
		return nil, fmt.Errorf("restrict remote dir: %w", err)
	}
	f, err := s.read()
	if err != nil {
		return nil, err
	}
	if f.ServerID == "" {
		err = s.update(func(f *file) (bool, error) {
			if f.ServerID != "" {
				return false, nil
			}
			id, err := random(16)
			if err != nil {
				return false, err
			}
			f.ServerID = id
			return true, nil
		})
		if err != nil {
			return nil, err
		}
		if f, err = s.read(); err != nil {
			return nil, err
		}
	}
	s.serverID = f.ServerID
	return s, nil
}

// ServerID is the random id of this data dir; tokens are bound to it.
func (s *Store) ServerID() string { return s.serverID }

// Dir is the store directory.
func (s *Store) Dir() string { return s.dir }

func (s *Store) path(name string) string { return filepath.Join(s.dir, name) }

// read loads devices.json without the lock: writes are atomic renames.
func (s *Store) read() (file, error) {
	var f file
	data, err := os.ReadFile(s.path(DevicesFile))
	switch {
	case errors.Is(err, os.ErrNotExist):
		return f, nil
	case err != nil:
		return f, fmt.Errorf("read devices: %w", err)
	}
	if err := json.Unmarshal(data, &f); err != nil {
		return f, fmt.Errorf("parse devices: %w", err)
	}
	return f, nil
}

// withLock runs fn under the cross-process lock, failing with ErrLocked
// when it cannot be taken in lockWait.
func (s *Store) withLock(fn func() error) error {
	ctx, cancel := context.WithTimeout(context.Background(), lockWait)
	defer cancel()
	release, err := lock.File(ctx, s.path(lockFile))
	if err != nil {
		return fmt.Errorf("%w: %v", ErrLocked, err)
	}
	defer release()
	return fn()
}

// update loads the file under the lock, applies fn and writes it back when
// fn reports a change.
func (s *Store) update(fn func(*file) (changed bool, err error)) error {
	return s.withLock(func() error {
		f, err := s.read()
		if err != nil {
			return err
		}
		changed, err := fn(&f)
		if err != nil || !changed {
			return err
		}
		return s.write(DevicesFile, f)
	})
}

// write marshals v and writes it atomically, private to the user.
func (s *Store) write(name string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return peer.WritePrivateFile(s.path(name), append(data, '\n'))
}

func random(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// digest binds a secret to this data dir: a devices.json copied elsewhere
// verifies nothing.
func (s *Store) digest(secret string) string {
	sum := sha256.Sum256([]byte(s.serverID + "\x00" + secret))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// List returns every paired device, oldest first.
func (s *Store) List() ([]Device, error) {
	f, err := s.read()
	if err != nil {
		return nil, err
	}
	return append([]Device(nil), f.Devices...), nil
}

// Get returns one device.
func (s *Store) Get(id string) (Device, error) {
	f, err := s.read()
	if err != nil {
		return Device{}, err
	}
	for _, d := range f.Devices {
		if d.ID == id {
			return d, nil
		}
	}
	return Device{}, ErrNotFound
}

// Revoke removes one device; a running server drops it within seconds.
func (s *Store) Revoke(id string) error {
	return s.update(func(f *file) (bool, error) {
		for i, d := range f.Devices {
			if d.ID == id {
				f.Devices = append(f.Devices[:i], f.Devices[i+1:]...)
				return true, nil
			}
		}
		return false, ErrNotFound
	})
}

// SetAllowYolo grants or withdraws yolo for one device (R15).
func (s *Store) SetAllowYolo(id string, on bool) error {
	return s.update(func(f *file) (bool, error) {
		for i := range f.Devices {
			if f.Devices[i].ID == id {
				changed := f.Devices[i].AllowYolo != on
				f.Devices[i].AllowYolo = on
				return changed, nil
			}
		}
		return false, ErrNotFound
	})
}
