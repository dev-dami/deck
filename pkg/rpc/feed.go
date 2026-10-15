package rpc

import (
	"crypto/rand"
	"encoding/hex"
	"sync"

	"github.com/dev-dami/deck/pkg/rpc/deckv1"
)

// RingSize is how many events a reconnecting client can replay.
const RingSize = 4000

// QueueSize is how many events may wait for one subscriber before it is
// cut loose; it then reconnects and resumes from its last seq.
const QueueSize = 2048

// feed numbers events, keeps a ring of recent ones for replay and fans them
// out. It is pkg/remote/hub for typed events: a desktop on a local socket
// wants every event, so nothing is scoped to a focused session.
type feed struct {
	epoch string

	mu   sync.Mutex
	seq  uint64
	ring []*deckv1.Event
	subs map[*sub]struct{}
}

type sub struct {
	ch     chan *deckv1.Event
	kicked chan struct{}
	once   sync.Once
}

func (s *sub) kick() { s.once.Do(func() { close(s.kicked) }) }

func newFeed() *feed {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return &feed{epoch: hex.EncodeToString(b), subs: map[*sub]struct{}{}}
}

// publish numbers ev and hands it to every subscriber, kicking one whose
// queue is full rather than waiting for it.
func (f *feed) publish(ev *deckv1.Event) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.seq++
	ev.Seq = f.seq
	f.ring = append(f.ring, ev)
	if len(f.ring) > RingSize {
		f.ring = append(f.ring[:0], f.ring[len(f.ring)-RingSize:]...)
	}
	for s := range f.subs {
		select {
		case s.ch <- ev:
		default:
			s.kick()
		}
	}
}

// subscribe registers a subscriber and queues what it missed since
// (epoch, seq). ready opens the stream; its Resync says the position could
// not be replayed. An empty epoch is a fresh client: nothing to replay and
// nothing to resync.
func (f *feed) subscribe(epoch string, seq uint64) (*sub, *deckv1.Ready) {
	s := &sub{ch: make(chan *deckv1.Event, QueueSize), kicked: make(chan struct{})}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.subs[s] = struct{}{}
	ready := &deckv1.Ready{Epoch: f.epoch, Seq: f.seq}
	switch {
	case epoch == "", epoch == f.epoch && seq == f.seq:
		return s, ready
	case epoch != f.epoch, seq > f.seq:
		ready.Resync = true
		return s, ready
	}
	first := f.seq - uint64(len(f.ring)) + 1
	if len(f.ring) == 0 || first > seq+1 || f.seq-seq > QueueSize {
		ready.Resync = true
		return s, ready
	}
	for _, ev := range f.ring[seq+1-first:] {
		s.ch <- ev // fits: the queue is new and holds QueueSize
	}
	return s, ready
}

func (f *feed) unsubscribe(s *sub) {
	f.mu.Lock()
	delete(f.subs, s)
	f.mu.Unlock()
}
