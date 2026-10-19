package daemon

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"sync"

	"github.com/dev-dami/deck/pkg/remote/wire"
)

// Hub tuning, as in the v2 hub.
const (
	ringSize  = 512
	queueSize = 256
)

// event is what the daemon publishes: a project's event, or a computer-level
// one when pj is empty.
type event struct {
	pj        string
	name      string
	sessionID string
	scoped    bool
	payload   any
}

type stored struct {
	frame  wire.Frame3
	scoped bool
}

// client is one phone's subscription.
type client struct {
	hub    *hub
	ch     chan wire.Frame3
	kicked chan struct{}
	once   sync.Once
	focus  wire.FocusRef
}

func (c *client) kick() { c.once.Do(func() { close(c.kicked) }) }

func (c *client) offer(f wire.Frame3) {
	select {
	case c.ch <- f:
	default:
		c.kick()
	}
}

// hub numbers events, keeps a ring for replay and fans them out.
type hub struct {
	epoch string

	mu      sync.Mutex
	seq     uint64
	ring    []stored
	clients map[*client]struct{}
}

func newHub() *hub {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return &hub{epoch: hex.EncodeToString(b), clients: map[*client]struct{}{}}
}

func (h *hub) publish(ev event) {
	payload, err := json.Marshal(ev.payload)
	if err != nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.seq++
	f := wire.Frame3{T: wire.FrameEv, PJ: ev.pj, Ev: &wire.Ev{Seq: h.seq, E: ev.name}, P: payload}
	h.ring = append(h.ring, stored{frame: f, scoped: ev.scoped})
	if len(h.ring) > ringSize {
		h.ring = append(h.ring[:0], h.ring[len(h.ring)-ringSize:]...)
	}
	for c := range h.clients {
		if ev.scoped && (c.focus.SessionID == "" || c.focus.PJ != ev.pj || c.focus.SessionID != ev.sessionID) {
			continue
		}
		c.offer(f)
	}
}

// subscribe registers a client. resync says the phone's position cannot be
// replayed and it must refetch; replay says missed events were queued.
func (h *hub) subscribe(resume *wire.Resume3) (c *client, replay, resync bool) {
	c = &client{hub: h, ch: make(chan wire.Frame3, queueSize), kicked: make(chan struct{})}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.clients[c] = struct{}{}
	if resume == nil {
		return c, false, false
	}
	if resume.Focus != nil {
		c.focus = *resume.Focus
	}
	switch {
	case resume.Epoch != h.epoch, resume.Seq > h.seq:
		return c, false, true
	case resume.Seq == h.seq:
		return c, false, false
	}
	first := h.seq - uint64(len(h.ring)) + 1
	if len(h.ring) == 0 || first > resume.Seq+1 {
		return c, false, true
	}
	var frames []wire.Frame3
	for _, s := range h.ring[resume.Seq+1-first:] {
		if !s.scoped {
			frames = append(frames, s.frame)
		}
	}
	if len(frames) > queueSize {
		return c, false, true
	}
	for _, f := range frames {
		c.ch <- f
	}
	return c, len(frames) > 0, false
}

func (h *hub) unsubscribe(c *client) {
	h.mu.Lock()
	delete(h.clients, c)
	h.mu.Unlock()
}

func (h *hub) setFocus(c *client, f wire.FocusRef) {
	h.mu.Lock()
	c.focus = f
	h.mu.Unlock()
}
