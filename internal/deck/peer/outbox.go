package peer

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// The outbox (docs/peers-design.md §1): calls to a teammate that could not
// be delivered wait in outbox.json in the peers directory, oldest first, and
// go when its link is back. Each carries an idempotency key the receiver
// drops a repeat by, so a call that arrived but whose answer was lost does
// nothing the second time. Team chat and code contracts go through it.

const (
	// maxOutbox caps the waiting calls; the oldest go first.
	maxOutbox = 200
	// outboxTTL drops a call that waited this long.
	outboxTTL = 7 * 24 * time.Hour
	// outboxRetry is how often the outbox is tried while calls wait, besides
	// every change of a link.
	outboxRetry = 30 * time.Second
	// outboxGap is the least time between two tries.
	outboxGap = 5 * time.Second
)

// outItem is one call waiting for its peer.
type outItem struct {
	ID   string          `json:"id"`
	Peer string          `json:"peer"`
	Op   string          `json:"op"`
	Args json.RawMessage `json:"args"`
	At   time.Time       `json:"at"`
}

type outbox struct {
	path  string // "" keeps it in memory
	mu    sync.Mutex
	items []outItem
}

func newOutbox(dir string) *outbox {
	o := &outbox{}
	if dir != "" {
		o.path = filepath.Join(dir, "outbox.json")
		if b, err := os.ReadFile(o.path); err == nil {
			_ = json.Unmarshal(b, &o.items)
		}
		o.trim(time.Now())
	}
	return o
}

// newCallKey is a fresh idempotency key.
func newCallKey() string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// trim drops expired calls and the oldest past maxOutbox. o.mu is held, or o
// is not shared yet.
func (o *outbox) trim(now time.Time) {
	kept := o.items[:0]
	for _, it := range o.items {
		if now.Sub(it.At) < outboxTTL {
			kept = append(kept, it)
		}
	}
	if len(kept) > maxOutbox {
		kept = kept[len(kept)-maxOutbox:]
	}
	o.items = kept
}

// save writes the outbox. o.mu is held.
func (o *outbox) save() {
	if o.path == "" {
		return
	}
	if len(o.items) == 0 {
		_ = os.Remove(o.path)
		return
	}
	if b, err := json.Marshal(o.items); err == nil {
		_ = writePrivateFile(o.path, b, false)
	}
}

func (o *outbox) add(it outItem) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.items = append(o.items, it)
	o.trim(time.Now())
	o.save()
}

// remove drops the call with key id for peer, and reports whether it was
// still waiting.
func (o *outbox) remove(peer, id string) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	kept := o.items[:0]
	for _, it := range o.items {
		if it.Peer != peer || it.ID != id {
			kept = append(kept, it)
		}
	}
	found := len(kept) < len(o.items)
	o.items = kept
	o.save()
	return found
}

// byPeer is the waiting calls, by peer, oldest first.
func (o *outbox) byPeer() map[string][]outItem {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.trim(time.Now())
	out := map[string][]outItem{}
	for _, it := range o.items {
		out[it.Peer] = append(out[it.Peer], it)
	}
	return out
}

func (o *outbox) len() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return len(o.items)
}

// QueuedError says which teammates a message waits for: they could not be
// reached, and it goes when their link is back.
type QueuedError struct{ Peers []string }

func (e QueuedError) Error() string {
	return "queued for " + strings.Join(e.Peers, ", ") + ": it goes when their link is back"
}

// queue keeps a call for peer in the outbox and makes sure it is tried.
func (s *Service) queue(peer, op, id string, args any) {
	b, err := json.Marshal(args)
	if err != nil {
		return
	}
	s.out.add(outItem{ID: id, Peer: peer, Op: op, Args: b, At: time.Now()})
	s.flushSoon()
}

// flushSoon starts the loop that delivers the outbox, unless it runs.
func (s *Service) flushSoon() {
	if s.out.len() == 0 || !s.flushing.CompareAndSwap(false, true) {
		return
	}
	go s.flushLoop()
}

// flushLoop tries the outbox after every change of the links (a link back up
// is one), and every outboxRetry, until it is empty or the Service closes.
func (s *Service) flushLoop() {
	defer s.flushing.Store(false)
	for s.base.Err() == nil {
		s.mu.Lock()
		v := s.ver
		s.mu.Unlock()
		if s.flush(s.base) == 0 {
			return
		}
		ctx, cancel := context.WithTimeout(s.base, outboxRetry)
		s.Wait(ctx, v)
		cancel()
		select {
		case <-s.base.Done():
		case <-time.After(outboxGap):
		}
	}
}

// flush delivers what waits for each peer, in order, stopping at a peer's
// first failure, and returns how many calls still wait.
func (s *Service) flush(ctx context.Context) int {
	byPeer := s.out.byPeer()
	peers := make([]string, 0, len(byPeer))
	for p := range byPeer {
		peers = append(peers, p)
	}
	sort.Strings(peers)
	for _, p := range peers {
		if _, pinned, err := OpenStore(s.dir).Get(p); err == nil && !pinned {
			for _, it := range byPeer[p] {
				s.out.remove(p, it.ID) // unpaired since: it can never go
			}
			continue
		}
		cctx, cancel := context.WithTimeout(ctx, chatTimeout)
		_ = s.withPeer(cctx, p, func(cl *Client) error {
			for _, it := range byPeer[p] {
				r, err := cl.Execute(cctx, it.Op, it.Args)
				if err != nil {
					return err // the link failed: later
				}
				// Answered, even when refused: trying again would not change it.
				s.out.remove(p, it.ID)
				if r.IsError {
					s.refused(p, it, Sanitize(truncate(r.Text(), 200)))
				}
			}
			return nil
		})
		cancel()
	}
	return s.out.len()
}

// execOK runs op on cl and turns a tool error into an error.
func execOK(ctx context.Context, cl *Client, op string, args any) error {
	r, err := cl.Execute(ctx, op, args)
	if err != nil {
		return err
	}
	if r.IsError {
		return errors.New(Sanitize(truncate(r.Text(), 200)))
	}
	return nil
}
