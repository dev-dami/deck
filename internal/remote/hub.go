package remote

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"sync"

	"github.com/dev-dami/deck/internal/remote/wire"
)

// ringSize is how many events a reconnecting phone can replay.
const ringSize = 2000

// subBuffer is how many frames may wait for one slow phone before it is cut
// loose; blocking the hub would stall every other phone and the agents.
const subBuffer = 256

type stored struct {
	seq   uint64
	frame wire.Frame
	ev    Event
}

// subscriber is one connection's feed.
type subscriber struct {
	ch    chan wire.Frame
	focus string
	// kicked is closed when the hub drops the subscriber for being too slow.
	kicked chan struct{}
	once   sync.Once
}

func (s *subscriber) kick() { s.once.Do(func() { close(s.kicked) }) }

// Hub numbers events, keeps a ring of recent ones for replay, and fans them
// out to subscribers.
type Hub struct {
	epoch string

	mu   sync.Mutex
	seq  uint64
	ring []stored
	subs map[*subscriber]struct{}
}

// NewHub returns an empty hub with a fresh epoch. A phone resuming against a
// different epoch (Deck restarted) must refetch instead of replay.
func NewHub() *Hub {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return &Hub{epoch: hex.EncodeToString(b), subs: map[*subscriber]struct{}{}}
}

// Epoch identifies this hub's run.
func (h *Hub) Epoch() string { return h.epoch }

// Seq is the sequence number of the latest event.
func (h *Hub) Seq() uint64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.seq
}

func (s *subscriber) wants(ev Event) bool {
	return !ev.Scoped || (s.focus != "" && s.focus == ev.SessionID)
}

// Publish numbers ev and delivers it to every subscriber that wants it. A
// subscriber whose buffer is full is kicked, never waited for.
func (h *Hub) Publish(ev Event) {
	payload, err := json.Marshal(ev.Payload)
	if err != nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.seq++
	f := wire.Frame{T: wire.FrameEv, Seq: h.seq, E: ev.Name, P: payload}
	h.ring = append(h.ring, stored{seq: h.seq, frame: f, ev: ev})
	if len(h.ring) > ringSize {
		h.ring = append(h.ring[:0], h.ring[len(h.ring)-ringSize:]...)
	}
	for s := range h.subs {
		if !s.wants(ev) {
			continue
		}
		select {
		case s.ch <- f:
		default:
			s.kick()
		}
	}
}

// Subscribe registers a subscriber and returns the frames to replay before its
// live feed. resync is true when the phone's position cannot be replayed (new
// epoch, or it fell out of the ring) and it must refetch its state.
func (h *Hub) Subscribe(resume *wire.Resume) (sub *subscriber, replay []wire.Frame, resync bool) {
	sub = &subscriber{ch: make(chan wire.Frame, subBuffer), kicked: make(chan struct{})}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.subs[sub] = struct{}{}
	if resume == nil {
		return sub, nil, false
	}
	if resume.Epoch != h.epoch || resume.Seq > h.seq {
		return sub, nil, true
	}
	if resume.Seq == h.seq {
		return sub, nil, false
	}
	if len(h.ring) == 0 || h.ring[0].seq > resume.Seq+1 {
		return sub, nil, true
	}
	for _, s := range h.ring {
		// Scoped rows are skipped: a reconnecting phone refetches the
		// focused session's messages anyway.
		if s.seq > resume.Seq && !s.ev.Scoped {
			replay = append(replay, s.frame)
		}
	}
	return sub, replay, false
}

// Unsubscribe removes sub.
func (h *Hub) Unsubscribe(sub *subscriber) {
	h.mu.Lock()
	delete(h.subs, sub)
	h.mu.Unlock()
}

// Focus sets the session whose message rows sub receives ("" = none).
func (h *Hub) Focus(sub *subscriber, sessionID string) {
	h.mu.Lock()
	sub.focus = sessionID
	h.mu.Unlock()
}
