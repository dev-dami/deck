// Package devstore is the computer-wide list of paired phones for Deck Mobile
// (docs/mobile-contract.md §4, §7): each device's pinned Noise static key,
// the hash of its relay token, its expiry, its yolo grant and its passkeys.
//
// Reads come from an immutable in-memory snapshot, so a connect never takes a
// lock or writes the file (remote-contract R16). Writers in the daemon and
// CLI serialize through a file lock and merge against the latest disk state.
package devstore

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dev-dami/deck/internal/deck/peer"
	"github.com/dev-dami/deck/internal/lock"
	"github.com/dev-dami/deck/pkg/remote/wire"
)

const (
	fileName = "devices.json"
	// TouchInterval is how often a device's last-seen is written (R16).
	TouchInterval = time.Minute
	// DeviceKeyExpiry is how long a device lives without being used.
	DeviceKeyExpiry = 90 * 24 * time.Hour
)

// ErrNotFound is returned for an unknown, revoked or expired device.
var ErrNotFound = errors.New("devstore: no such device")

// Passkey is a registered WebAuthn credential (public key in COSE form).
type Passkey struct {
	ID        string    `json:"id"`
	PublicKey []byte    `json:"public_key"`
	SignCount uint32    `json:"sign_count"`
	CreatedAt time.Time `json:"created_at"`
}

// PushPrefs are the keys the computer encrypts pushes with and the phone's
// choices. The endpoint is not here: it lives only on the relay.
type PushPrefs struct {
	P256dh    string `json:"p256dh"`
	Auth      string `json:"auth"`
	Approvals bool   `json:"approvals"`
	Done      bool   `json:"done"`
}

// Device is one paired phone.
type Device struct {
	ID             string    `json:"id"`
	Name           string    `json:"name"`
	Static         []byte    `json:"static"`
	RelayTokenHash string    `json:"relay_token_hash"`
	CreatedAt      time.Time `json:"created_at"`
	LastSeen       time.Time `json:"last_seen,omitzero"`
	ExpiresAt      time.Time `json:"expires_at"`
	AllowYolo      bool      `json:"allow_yolo"`
	// Pending marks a phone mid-pairing: pinned but not trusted, invisible to
	// every lookup until Confirm.
	Pending bool `json:"pending,omitempty"`
	// Push is the phone's web push subscription keys and what it wants pushed.
	Push     *PushPrefs `json:"push,omitempty"`
	Passkeys []Passkey  `json:"passkeys,omitempty"`
}

type file struct {
	Devices []Device `json:"devices"`
}

// Store is the device list. The zero value is not usable; use Open.
type Store struct {
	path string
	now  func() time.Time

	snap atomic.Pointer[[]Device]

	mu            sync.Mutex // serialises writers
	lastSeen      map[string]time.Time
	mtime         time.Time
	pendingReload bool // mutations may discover changes before the watcher does
}

// Open reads dir/devices.json, or starts empty if there is none.
func Open(dir string) (*Store, error) { return open(dir, time.Now) }

func open(dir string, now func() time.Time) (*Store, error) {
	s := &Store{path: filepath.Join(dir, fileName), now: now, lastSeen: map[string]time.Time{}}
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) load() error {
	st, err := os.Stat(s.path)
	if errors.Is(err, fs.ErrNotExist) {
		empty := []Device{}
		s.snap.Store(&empty)
		s.mtime = time.Time{}
		return nil
	}
	if err != nil {
		return fmt.Errorf("devstore: %w", err)
	}
	raw, err := os.ReadFile(s.path)
	if err != nil {
		return fmt.Errorf("devstore: %w", err)
	}
	var f file
	if err := json.Unmarshal(raw, &f); err != nil {
		return fmt.Errorf("devstore: %s is not valid: %w", fileName, err)
	}
	if f.Devices == nil {
		f.Devices = []Device{}
	}
	s.snap.Store(&f.Devices)
	s.mtime = st.ModTime()
	return nil
}

// Reload re-reads the file if it changed on disk. The daemon calls it from its
// one watcher; it reports whether the list changed.
func (s *Store) Reload() (bool, error) {
	release, err := s.mutation()
	if err != nil {
		return false, err
	}
	defer release()
	changed := s.pendingReload
	s.pendingReload = false
	return changed, nil
}

// mutation holds the in-process and cross-process writer locks while reading
// the latest complete file. Never write a stale published snapshot back.
func (s *Store) mutation() (func(), error) {
	s.mu.Lock()
	if err := os.MkdirAll(filepath.Dir(s.path), 0700); err != nil {
		s.mu.Unlock()
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	release, err := lock.File(ctx, s.path+".lock")
	if err != nil {
		s.mu.Unlock()
		return nil, fmt.Errorf("devstore: waiting for writer: %w", err)
	}
	before := s.devices()
	if err := s.load(); err != nil {
		release()
		s.mu.Unlock()
		return nil, err
	}
	if !reflect.DeepEqual(before, s.devices()) {
		s.pendingReload = true
	}
	return func() { release(); s.mu.Unlock() }, nil
}

func (s *Store) devices() []Device { return *s.snap.Load() }

func (s *Store) live(d Device) bool { return !d.Pending && d.ExpiresAt.After(s.now()) }

// ByStatic finds the device pinned to a Noise static key. It is the check the
// responder runs on every handshake and takes no lock.
func (s *Store) ByStatic(static []byte) (Device, error) {
	for _, d := range s.devices() {
		if subtle.ConstantTimeCompare(d.Static, static) == 1 && s.live(d) {
			return d, nil
		}
	}
	return Device{}, ErrNotFound
}

// ByTokenHash finds the device whose relay token hashes to h, as relay mail
// names its sender.
func (s *Store) ByTokenHash(h string) (Device, error) {
	for _, d := range s.devices() {
		if subtle.ConstantTimeCompare([]byte(d.RelayTokenHash), []byte(h)) == 1 && s.live(d) {
			return d, nil
		}
	}
	return Device{}, ErrNotFound
}

// Get returns a live device by id.
func (s *Store) Get(id string) (Device, error) {
	for _, d := range s.devices() {
		if d.ID == id && s.live(d) {
			return d, nil
		}
	}
	return Device{}, ErrNotFound
}

// List returns every live device.
func (s *Store) List() []Device {
	var out []Device
	for _, d := range s.devices() {
		if s.live(d) {
			out = append(out, d)
		}
	}
	return out
}

// TokenHashes are the relay token hashes of the live devices, as sent to the
// relay in a `devices` frame.
func (s *Store) TokenHashes() []string {
	var out []string
	for _, d := range s.List() {
		out = append(out, d.RelayTokenHash)
	}
	return out
}

// HashToken is the hash the relay stores of a relay token.
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func randomID(prefix string, n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return prefix + base64.RawURLEncoding.EncodeToString(b), nil
}

// Add pins a new device and returns it with its relay token. The token is not
// stored, only its hash, and cannot be retrieved again.
func (s *Store) Add(name string, static []byte) (Device, string, error) {
	return s.add(name, static, false)
}

// AddPending is Add for a pairing in progress: the device is stored but not
// trusted, and does not appear in any lookup, until Confirm. A phone that
// cannot finish the handshake is removed with Revoke.
func (s *Store) AddPending(name string, static []byte) (Device, string, error) {
	return s.add(name, static, true)
}

// Confirm trusts a pending device.
func (s *Store) Confirm(id string) error {
	release, err := s.mutation()
	if err != nil {
		return err
	}
	defer release()
	next := slices.Clone(s.devices())
	for i := range next {
		if next[i].ID == id && next[i].Pending && next[i].ExpiresAt.After(s.now()) {
			next[i].Pending = false
			return s.write(next)
		}
	}
	return ErrNotFound
}

func (s *Store) add(name string, static []byte, pending bool) (Device, string, error) {
	if len(static) != 32 {
		return Device{}, "", errors.New("devstore: a static key is 32 bytes")
	}
	id, err := randomID("dev_", 6)
	if err != nil {
		return Device{}, "", err
	}
	token, err := randomID("rt_", 32)
	if err != nil {
		return Device{}, "", err
	}
	now := s.now()
	d := Device{
		ID: id, Name: name, Static: slices.Clone(static), RelayTokenHash: HashToken(token),
		CreatedAt: now, ExpiresAt: now.Add(wire.DeviceKeyExpiry), Pending: pending,
	}
	release, err := s.mutation()
	if err != nil {
		return Device{}, "", err
	}
	defer release()
	for _, e := range s.devices() {
		if subtle.ConstantTimeCompare(e.Static, static) == 1 && e.ExpiresAt.After(now) {
			return Device{}, "", errors.New("devstore: this phone is already paired")
		}
	}
	if err := s.write(append(slices.Clone(s.devices()), d)); err != nil {
		return Device{}, "", err
	}
	return d, token, nil
}

// Revoke removes a device. Revoking an unknown id is ErrNotFound.
func (s *Store) Revoke(id string) error {
	release, err := s.mutation()
	if err != nil {
		return err
	}
	defer release()
	cur := s.devices()
	next := slices.DeleteFunc(slices.Clone(cur), func(d Device) bool { return d.ID == id })
	if len(next) == len(cur) {
		return ErrNotFound
	}
	delete(s.lastSeen, id)
	return s.write(next)
}

// SetYolo grants or removes the per-device yolo permission (R15).
func (s *Store) SetYolo(id string, allow bool) error {
	return s.update(id, func(d *Device) { d.AllowYolo = allow })
}

// SetPush stores a phone's push keys and choices; nil removes them.
func (s *Store) SetPush(id string, p *PushPrefs) error {
	return s.update(id, func(d *Device) { d.Push = p })
}

// AddPasskey records a credential for a device.
func (s *Store) AddPasskey(id string, p Passkey) error {
	return s.update(id, func(d *Device) {
		d.Passkeys = slices.DeleteFunc(d.Passkeys, func(e Passkey) bool { return e.ID == p.ID })
		d.Passkeys = append(d.Passkeys, p)
	})
}

// RemovePasskey deletes a credential.
func (s *Store) RemovePasskey(id, passkeyID string) error {
	return s.update(id, func(d *Device) {
		d.Passkeys = slices.DeleteFunc(d.Passkeys, func(e Passkey) bool { return e.ID == passkeyID })
	})
}

// SetSignCount advances a credential's counter after an assertion.
func (s *Store) SetSignCount(id, passkeyID string, count uint32) error {
	return s.update(id, func(d *Device) {
		for i := range d.Passkeys {
			if d.Passkeys[i].ID == passkeyID {
				d.Passkeys[i].SignCount = count
			}
		}
	})
}

// Touch records use. It writes the file at most once per TouchInterval per
// device and extends the expiry, and never fails a connect: the caller may
// ignore the error.
func (s *Store) Touch(id string) error {
	now := s.now()
	release, err := s.mutation()
	if err != nil {
		return err
	}
	defer release()
	if _, err := s.Get(id); err != nil {
		return err
	}
	if now.Sub(s.lastSeen[id]) < TouchInterval {
		return nil
	}
	err = s.updateLocked(id, func(d *Device) {
		d.LastSeen = now
		d.ExpiresAt = now.Add(wire.DeviceKeyExpiry)
	})
	if err == nil {
		s.lastSeen[id] = now
	}
	return err
}

func (s *Store) update(id string, f func(*Device)) error {
	release, err := s.mutation()
	if err != nil {
		return err
	}
	defer release()
	return s.updateLocked(id, f)
}
func (s *Store) updateLocked(id string, f func(*Device)) error {
	next := slices.Clone(s.devices())
	for i := range next {
		if next[i].ID == id && s.live(next[i]) {
			// Passkeys is a slice shared with the published snapshot; copy it
			// before the mutation so readers never see a half-written list.
			next[i].Passkeys = slices.Clone(next[i].Passkeys)
			f(&next[i])
			return s.write(next)
		}
	}
	return ErrNotFound
}

// Prune drops expired devices and reports how many it removed.
func (s *Store) Prune() (int, error) {
	release, err := s.mutation()
	if err != nil {
		return 0, err
	}
	defer release()
	cur := s.devices()
	next := slices.DeleteFunc(slices.Clone(cur), func(d Device) bool { return !d.ExpiresAt.After(s.now()) })
	if len(next) == len(cur) {
		return 0, nil
	}
	return len(cur) - len(next), s.write(next)
}

// write persists next and then publishes it, so a failed write changes
// nothing a reader can see.
func (s *Store) write(next []Device) error {
	raw, err := json.Marshal(file{Devices: next})
	if err != nil {
		return err
	}
	if err := peer.WritePrivateFile(s.path, raw); err != nil {
		return fmt.Errorf("devstore: write: %w", err)
	}
	if st, err := os.Stat(s.path); err == nil {
		s.mtime = st.ModTime()
	}
	s.snap.Store(&next)
	s.pendingReload = true
	return nil
}
