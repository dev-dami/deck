package server

import (
	"sync"
	"time"

	"github.com/dev-dami/deck/pkg/remote/wire"
)

// idem remembers the message ids of recent chat.send calls by idempotency
// key, so a retry after a dropped connection does not post twice. A key is
// recorded only after the send succeeded (R11); the map holds at most
// wire.IdempotencyKeys entries for wire.IdempotencyTTL.
type idem struct {
	now func() time.Time

	mu    sync.Mutex
	keys  map[string]idemEntry
	order []string // insertion order, for the cap
}

type idemEntry struct {
	messageID string
	exp       time.Time
}

func newIdem(now func() time.Time) *idem {
	return &idem{now: now, keys: map[string]idemEntry{}}
}

// lookup returns the message id recorded for key, if any.
func (m *idem) lookup(key string) (string, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.prune()
	e, ok := m.keys[key]
	if !ok {
		return "", false
	}
	return e.messageID, true
}

// record remembers key; the oldest entry goes when the map is full.
func (m *idem) record(key, messageID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.prune()
	if _, ok := m.keys[key]; !ok {
		m.order = append(m.order, key)
	}
	m.keys[key] = idemEntry{messageID: messageID, exp: m.now().Add(wire.IdempotencyTTL)}
	for len(m.keys) > wire.IdempotencyKeys {
		oldest := m.order[0]
		m.order = m.order[1:]
		delete(m.keys, oldest)
	}
}

// prune drops expired keys; the caller holds the lock.
func (m *idem) prune() {
	now := m.now()
	kept := m.order[:0]
	for _, k := range m.order {
		if e, ok := m.keys[k]; ok && now.Before(e.exp) {
			kept = append(kept, k)
		} else {
			delete(m.keys, k)
		}
	}
	m.order = kept
}

func (m *idem) size() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.keys)
}
