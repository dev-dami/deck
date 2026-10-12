// Package hub numbers the server's events, keeps a ring of recent ones for
// replay and fans them out to connected phones through bounded queues
// (docs/remote-contract.md §3.3, R11, R12). Nothing here is persisted: a
// restart is a new epoch and every phone refetches.
package hub

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"sync"

	"github.com/dev-dami/deck/pkg/remote"
	"github.com/dev-dami/deck/pkg/remote/wire"
)

// RingSize is how many events a reconnecting phone can replay.
const RingSize = 2000

// QueueSize is how many frames may wait for one phone before it is cut
// loose with wire.CloseSlowConsumer: blocking the hub would stall every
// other phone and the agents.
const QueueSize = wire.OutboundQueue

type stored struct {
	frame  wire.Frame
	scoped bool
}

// Client is one connection's feed.
type Client struct {
	hub   *Hub
	ch    chan wire.Frame
	focus string
	// kicked is closed when the hub drops the client for being too slow.
	kicked chan struct{}
	once   sync.Once
}

// Frames is the client's outbound queue, in order.
func (c *Client) Frames() <-chan wire.Frame { return c.ch }

// Kicked is closed once the client fell too far behind.
func (c *Client) Kicked() <-chan struct{} { return c.kicked }

// Focus is the session whose scoped events the client receives.
func (c *Client) Focus() string {
	c.hub.mu.Lock()
	defer c.hub.mu.Unlock()
	return c.focus
}

func (c *Client) kick() { c.once.Do(func() { close(c.kicked) }) }

// offer queues f, kicking the client when its queue is full.
func (c *Client) offer(f wire.Frame) bool {
	select {
	case c.ch <- f:
		return true
	default:
		c.kick()
		return false
	}
}

// Hub numbers events, keeps a ring of recent ones for replay, and fans them
// out to subscribers.
type Hub struct {
	epoch string

	mu      sync.Mutex
	seq     uint64
	ring    []stored
	clients map[*Client]struct{}
}

// New returns an empty hub with a fresh epoch.
func New() *Hub {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return &Hub{epoch: hex.EncodeToString(b), clients: map[*Client]struct{}{}}
}

// Epoch identifies this hub's run.
func (h *Hub) Epoch() string { return h.epoch }

// Seq is the sequence number of the latest event.
func (h *Hub) Seq() uint64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.seq
}

// Publish numbers ev and delivers it to every client that wants it. A
// client whose queue is full is kicked, never waited for. An event whose
// payload cannot be encoded is dropped.
func (h *Hub) Publish(ev remote.Event) {
	payload, err := json.Marshal(ev.Payload)
	if err != nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.seq++
	f := wire.Frame{T: wire.FrameEv, Ev: &wire.Ev{Seq: h.seq, E: ev.Name}, P: payload}
	h.ring = append(h.ring, stored{frame: f, scoped: ev.Scoped})
	if len(h.ring) > RingSize {
		h.ring = append(h.ring[:0], h.ring[len(h.ring)-RingSize:]...)
	}
	for c := range h.clients {
		if ev.Scoped && (c.focus == "" || c.focus != ev.SessionID) {
			continue
		}
		c.offer(f)
	}
}

// Emit publishes an unscoped event by name.
func (h *Hub) Emit(name string, payload any) {
	h.Publish(remote.Event{Name: name, Payload: payload})
}

// Subscribe registers a client whose focus is resume's, and queues the
// events missed since resume.Seq when they can be replayed. replay says
// some were queued; resync says the phone's position cannot be replayed
// (new epoch, ahead of the hub, fallen out of the ring, or more frames than
// the queue holds) and it must refetch its state. Scoped events are never
// replayed: the phone refetches the focused session anyway.
func (h *Hub) Subscribe(resume *wire.Resume) (c *Client, replay, resync bool) {
	c = &Client{hub: h, ch: make(chan wire.Frame, QueueSize), kicked: make(chan struct{})}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.clients[c] = struct{}{}
	if resume == nil {
		return c, false, false
	}
	c.focus = resume.Focus
	switch {
	case resume.Epoch != h.epoch, resume.Seq > h.seq:
		return c, false, true
	case resume.Seq == h.seq:
		return c, false, false
	}
	// The ring holds seqs (h.seq-len+1 .. h.seq); resume.Seq+1 must be in it.
	first := h.seq - uint64(len(h.ring)) + 1
	if len(h.ring) == 0 || first > resume.Seq+1 {
		return c, false, true
	}
	var frames []wire.Frame
	for _, s := range h.ring[resume.Seq+1-first:] {
		if !s.scoped {
			frames = append(frames, s.frame)
		}
	}
	if len(frames) > QueueSize {
		return c, false, true
	}
	for _, f := range frames {
		c.ch <- f // fits: the queue is new and frames is at most its size
	}
	return c, len(frames) > 0, false
}

// Unsubscribe removes c.
func (h *Hub) Unsubscribe(c *Client) {
	h.mu.Lock()
	delete(h.clients, c)
	h.mu.Unlock()
}

// Focus sets the session whose scoped events c receives ("" = none).
func (h *Hub) Focus(c *Client, sessionID string) {
	h.mu.Lock()
	c.focus = sessionID
	h.mu.Unlock()
}

// Clients is how many are subscribed.
func (h *Hub) Clients() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.clients)
}
