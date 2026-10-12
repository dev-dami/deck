package auth

import (
	"crypto/subtle"
	"strings"
	"time"
)

// Token layout: "<device id>.<secret>"; only sha256 of the secret, bound to
// the server id, is kept.

// Pair consumes the armed code and registers a device. The returned token
// is shown to the caller once: only its hash is kept.
func (s *Store) Pair(code, name string) (token string, dev Device, err error) {
	name = strings.TrimSpace(name)
	if name == "" {
		name = "Phone"
	}
	if r := []rune(name); len(r) > 64 {
		name = string(r[:64])
	}
	id, err := random(9)
	if err != nil {
		return "", Device{}, err
	}
	secret, err := random(32)
	if err != nil {
		return "", Device{}, err
	}
	dev = Device{ID: id, Name: name, Hash: s.digest(secret), CreatedAt: s.now().UTC().Truncate(time.Second)}
	err = s.withLock(func() error {
		if !s.takeCode(code) {
			return ErrInvalidCode
		}
		f, err := s.read()
		if err != nil {
			return err
		}
		f.Devices = append(f.Devices, dev)
		return s.write(DevicesFile, f)
	})
	if err != nil {
		return "", Device{}, err
	}
	return id + "." + secret, dev, nil
}

// Verify checks a token against the file on disk, read-only: a revoke made
// by another process applies on the next connect, and no lock is taken. An
// unknown or foreign token is ErrInvalidToken; a key idle for
// wire.DeviceKeyExpiry is ErrExpired.
func (s *Store) Verify(token string) (Device, error) {
	id, secret, ok := strings.Cut(token, ".")
	if !ok || id == "" || secret == "" {
		return Device{}, ErrInvalidToken
	}
	want := []byte(s.digest(secret))
	f, err := s.read()
	if err != nil {
		return Device{}, err
	}
	var found *Device
	for i := range f.Devices {
		if f.Devices[i].ID == id {
			found = &f.Devices[i]
			break
		}
	}
	// The comparison runs whether or not the id exists, so a probe cannot
	// tell a known id from an unknown one by timing.
	have := []byte(strings.Repeat("-", len(want)))
	if found != nil {
		have = []byte(found.Hash)
	}
	if subtle.ConstantTimeCompare(have, want) != 1 || found == nil {
		return Device{}, ErrInvalidToken
	}
	if s.now().After(found.ExpiresAt()) {
		return Device{}, ErrExpired
	}
	return *found, nil
}
