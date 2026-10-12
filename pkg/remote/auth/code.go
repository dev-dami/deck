package auth

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
	"time"

	"github.com/dev-dami/deck/internal/deck/peer"
	"github.com/dev-dami/deck/pkg/remote/wire"
)

// Code is an armed pairing code.
type Code struct {
	Code      string // XXXXX-XXXXX, as peer.NewPairCode makes it
	ExpiresAt time.Time
}

// armed is pair.json: the hash of the one armed code (R3: one at a time).
type armed struct {
	Hash      string    `json:"hash"`
	ExpiresAt time.Time `json:"expires_at"`
}

// Arm mints a code valid for wire.PairCodeTTL, replacing any armed before.
func (s *Store) Arm() (Code, error) {
	code, err := peer.NewPairCode()
	if err != nil {
		return Code{}, err
	}
	c := Code{Code: code, ExpiresAt: s.now().Add(wire.PairCodeTTL).UTC().Truncate(time.Second)}
	err = s.withLock(func() error {
		return s.write(PairFile, armed{Hash: s.digest(normalizeCode(code)), ExpiresAt: c.ExpiresAt})
	})
	if err != nil {
		return Code{}, err
	}
	return c, nil
}

// Armed reports whether a code is armed and when it expires.
func (s *Store) Armed() (time.Time, bool) {
	a, err := s.readArmed()
	if err != nil || !s.now().Before(a.ExpiresAt) {
		return time.Time{}, false
	}
	return a.ExpiresAt, true
}

// Disarm forgets the armed code.
func (s *Store) Disarm() error {
	return s.withLock(func() error {
		err := os.Remove(s.path(PairFile))
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	})
}

func (s *Store) readArmed() (armed, error) {
	var a armed
	data, err := os.ReadFile(s.path(PairFile))
	if err != nil {
		return a, err
	}
	if err := json.Unmarshal(data, &a); err != nil {
		return a, err
	}
	return a, nil
}

// takeCode consumes the armed code when code matches it; the caller holds
// the lock. Of two racing callers exactly one succeeds.
func (s *Store) takeCode(code string) bool {
	a, err := s.readArmed()
	if err != nil {
		return false
	}
	if !s.now().Before(a.ExpiresAt) {
		_ = os.Remove(s.path(PairFile))
		return false
	}
	if a.Hash != s.digest(normalizeCode(code)) {
		return false
	}
	return os.Remove(s.path(PairFile)) == nil
}

// normalizeCode upper-cases a typed code and drops dashes and spaces, as
// peer does for its codes.
func normalizeCode(s string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(s) {
		if r == '-' || r == ' ' || r == '\t' {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}
