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

// newEpoch is a fresh random epoch. The epoch is per process on purpose:
// the ring lives in memory, so after a restart no seq of the old process
// can be replayed, and a stable epoch would let a client take the new
// process's renumbered events for the ones it missed. A stable epoch
// needs a durable, gap-free event log to replay from (the per-host store
// planned for deck.v2).
func newEpoch() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func newFeed() *feed {
	return &feed{epoch: newEpoch(), subs: map[*sub]struct{}{}}
}

// position is the epoch and the seq of the latest event. A snapshot read
// after taking it reflects at least every event up to that seq (each
// event's change is stored before the event is published), so a client
// that applies the snapshot and then every event after seq, as upserts by
// id, ends at the true state.
func (f *feed) position() (string, uint64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.epoch, f.seq
}

// reset starts a new epoch and cuts every subscriber: something upstream
// lost events, so no position of the old epoch can be resumed. Each
// client reconnects, is told to resync, and refetches its state.
func (f *feed) reset() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.epoch = newEpoch()
	f.ring = nil
	for s := range f.subs {
		s.kick()
	}
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
