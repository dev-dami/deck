// Package pubsub provides a lightweight in-process broker for fan-out
// event delivery between services and the UI.
//
// Delivery semantics:
//
//   - [Broker.Publish] is best-effort and lossy under contention. If a
//     subscriber's channel is full, the event is dropped for that
//     subscriber, a counter is incremented, and a warning is logged (at
//     most once a second per broker, with the number dropped).
//     This is the right choice for high-frequency intermediate updates
//     (e.g. streaming token deltas) where only the latest state
//     matters.
//
//   - [Broker.PublishMustDeliver] is bounded-blocking. For each
//     subscriber it first tries a non-blocking send, then falls back to
//     a per-subscriber blocking send with a hard timeout. On timeout the
//     event is dropped for that subscriber, an error is logged, and the
//     must-deliver drop counter is incremented. The publisher never
//     blocks indefinitely. This is the right choice for terminal events
//     (finish, tool result, error, cancel) that must not be silently
//     coalesced away.
//
//   - [Broker.SubscribeReliable] is the subscriber side of lossless
//     delivery. Neither publish method ever drops an event for a reliable
//     subscriber: one that falls more than its buffer behind is cut
//     instead. Its channel closes and [Subscription.Lost] reports true,
//     so it knows it missed something and can resync (a remote feed
//     starts a new epoch and its clients refetch). Whatever feeds clients
//     that build state from events (the desktop, the phone) subscribes
//     this way.
//
// Counters ([Broker.DropCount], [Broker.MustDeliverDropCount],
// [Broker.CutCount]) are exposed so callers can surface saturation in
// telemetry.
package pubsub

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
)

const (
	// bufferSize is the per-subscriber channel capacity for any broker
	// created via NewBroker. Publish is non-blocking, so a full buffer
	// drops events (with a warning log); sized to cover a long
	// streaming assistant turn (~one UpdatedEvent per token) even under
	// TUI render stalls.
	bufferSize = 4096

	// defaultMustDeliverTimeout is the per-subscriber upper bound on how
	// long [Broker.PublishMustDeliver] will block waiting for buffer
	// space before giving up on that subscriber.
	defaultMustDeliverTimeout = 50 * time.Millisecond
)

type Broker[T any] struct {
	subs                 map[chan Event[T]]struct{}
	reliable             map[*reliableSub[T]]struct{}
	mu                   sync.RWMutex
	done                 chan struct{}
	subCount             int
	channelBufferSize    int
	mustDeliverTimeout   time.Duration
	dropCount            atomic.Uint64
	mustDeliverDropCount atomic.Uint64
	cutCount             atomic.Uint64
	// dropLoggedAt (unix nanos) and unloggedDrops rate-limit the drop
	// warning; see warnDropped.
	dropLoggedAt  atomic.Int64
	unloggedDrops atomic.Uint64
}

func NewBroker[T any]() *Broker[T] {
	return NewBrokerWithOptions[T](bufferSize)
}

func NewBrokerWithOptions[T any](channelBufferSize int) *Broker[T] {
	return &Broker[T]{
		subs:               make(map[chan Event[T]]struct{}),
		reliable:           make(map[*reliableSub[T]]struct{}),
		done:               make(chan struct{}),
		channelBufferSize:  channelBufferSize,
		mustDeliverTimeout: defaultMustDeliverTimeout,
	}
}

// SetMustDeliverTimeout overrides the per-subscriber timeout used by
// [Broker.PublishMustDeliver]. A zero or negative value resets to the
// default. Intended primarily for tests.
func (b *Broker[T]) SetMustDeliverTimeout(d time.Duration) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if d <= 0 {
		b.mustDeliverTimeout = defaultMustDeliverTimeout
		return
	}
	b.mustDeliverTimeout = d
}

func (b *Broker[T]) Shutdown() {
	select {
	case <-b.done: // Already closed
		return
	default:
		close(b.done)
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	for ch := range b.subs {
		delete(b.subs, ch)
		close(ch)
	}
	// Shutting down ends the stream; it is not a loss.
	for rs := range b.reliable {
		delete(b.reliable, rs)
		rs.dead.Store(true)
		close(rs.ch)
	}

	b.subCount = 0
}

func (b *Broker[T]) Subscribe(ctx context.Context) <-chan Event[T] {
	b.mu.Lock()
	defer b.mu.Unlock()

	select {
	case <-b.done:
		ch := make(chan Event[T])
		close(ch)
		return ch
	default:
	}

	sub := make(chan Event[T], b.channelBufferSize)
	b.subs[sub] = struct{}{}
	b.subCount++

	go func() {
		<-ctx.Done()

		b.mu.Lock()
		defer b.mu.Unlock()

		select {
		case <-b.done:
			return
		default:
		}

		delete(b.subs, sub)
		close(sub)
		b.subCount--
	}()

	return sub
}

// Subscription is a reliable subscription ([Broker.SubscribeReliable]).
// Events arrive on C in the order they were published (as seen by any one
// publishing goroutine) and none is ever skipped. C closes when the
// subscriber's context ends, when the broker shuts down, or when the
// subscriber fell behind and was cut; only the last makes Lost true.
type Subscription[T any] struct {
	C    <-chan Event[T]
	lost func() bool
}

// NewSubscription wraps a channel the caller feeds as a Subscription,
// with lost reporting whether it ended because events were missed. It is
// for code that merges several reliable subscriptions into one.
func NewSubscription[T any](c <-chan Event[T], lost func() bool) *Subscription[T] {
	return &Subscription[T]{C: c, lost: lost}
}

// Lost reports whether the subscription was cut for falling behind (or an
// upstream it depends on was), so events are missing: the subscriber must
// rebuild its state from a snapshot before trusting events again. It is
// meaningful once C is closed.
func (s *Subscription[T]) Lost() bool { return s.lost != nil && s.lost() }

type reliableSub[T any] struct {
	ch chan Event[T]
	// dead is set once nothing more may be sent: cut, cancelled or shut
	// down. Publishers skip a dead subscriber; the channel itself is closed
	// under the broker's write lock, never while a publisher holds the read
	// lock and may be sending.
	dead atomic.Bool
	lost atomic.Bool
}

// SubscribeReliable subscribes without ever losing an event silently: see
// [Subscription]. Its buffer is the broker's channel size.
func (b *Broker[T]) SubscribeReliable(ctx context.Context) *Subscription[T] {
	b.mu.Lock()
	defer b.mu.Unlock()

	rs := &reliableSub[T]{ch: make(chan Event[T], b.channelBufferSize)}
	sub := &Subscription[T]{C: rs.ch, lost: rs.lost.Load}
	select {
	case <-b.done:
		rs.dead.Store(true)
		close(rs.ch)
		return sub
	default:
	}
	b.reliable[rs] = struct{}{}

	go func() {
		<-ctx.Done()
		b.mu.Lock()
		defer b.mu.Unlock()
		if _, ok := b.reliable[rs]; !ok {
			return // cut or shut down already
		}
		delete(b.reliable, rs)
		rs.dead.Store(true)
		close(rs.ch)
	}()
	return sub
}

// CutReliable cuts every reliable subscriber as if it had fallen behind.
// Code that re-publishes another broker's events calls it when its own
// upstream subscription was cut, so the loss reaches the subscribers that
// depend on it instead of stopping at the hop.
func (b *Broker[T]) CutReliable() {
	b.mu.RLock()
	var cut []*reliableSub[T]
	for rs := range b.reliable {
		if rs.dead.CompareAndSwap(false, true) {
			cut = append(cut, rs)
		}
	}
	b.mu.RUnlock()
	b.cut(cut, "upstream")
}

// cut ends reliable subscribers a publisher has already marked dead: they
// are marked lost, then their channels close.
func (b *Broker[T]) cut(subs []*reliableSub[T], t EventType) {
	if len(subs) == 0 {
		return
	}
	b.mu.Lock()
	n := 0
	for _, rs := range subs {
		if _, ok := b.reliable[rs]; !ok {
			continue
		}
		delete(b.reliable, rs)
		rs.lost.Store(true)
		close(rs.ch)
		n++
	}
	b.mu.Unlock()
	if n > 0 {
		b.cutCount.Add(uint64(n))
		slog.Warn("Pubsub reliable subscriber fell behind; cut so it resyncs", "type", t, "cut", n)
	}
}

func (b *Broker[T]) GetSubscriberCount() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.subCount
}

// DropCount returns the cumulative number of events dropped by
// [Broker.Publish] because a subscriber's channel was full.
func (b *Broker[T]) DropCount() uint64 {
	return b.dropCount.Load()
}

// MustDeliverDropCount returns the cumulative number of events dropped
// by [Broker.PublishMustDeliver] after the per-subscriber timeout
// expired.
func (b *Broker[T]) MustDeliverDropCount() uint64 {
	return b.mustDeliverDropCount.Load()
}

// CutCount returns how many reliable subscribers were cut, for falling
// behind or because their upstream was.
func (b *Broker[T]) CutCount() uint64 {
	return b.cutCount.Load()
}

// Publish delivers an event to every active subscriber.
//
// Delivery is non-blocking and lossy for ordinary subscribers: if a
// subscriber's channel is full the event is dropped for that subscriber,
// a warning is logged, and [Broker.DropCount] is incremented. A reliable
// subscriber whose channel is full is cut instead. Use
// [Broker.PublishMustDeliver] for events that must not be silently
// dropped.
func (b *Broker[T]) Publish(t EventType, payload T) {
	b.mu.RLock()

	select {
	case <-b.done:
		b.mu.RUnlock()
		return
	default:
	}

	event := Event[T]{Type: t, Payload: payload}

	for sub := range b.subs {
		select {
		case sub <- event:
		default:
			// Channel is full, subscriber is slow — skip this event.
			// Lossy by design; counted and logged so saturation is
			// observable.
			b.dropCount.Add(1)
			b.warnDropped(t)
		}
	}
	var cut []*reliableSub[T]
	for rs := range b.reliable {
		if rs.dead.Load() {
			continue
		}
		select {
		case rs.ch <- event:
		default:
			if rs.dead.CompareAndSwap(false, true) {
				cut = append(cut, rs)
			}
		}
	}
	b.mu.RUnlock()
	b.cut(cut, t)
}

// dropLogInterval is the least time between two "buffer full" warnings of
// one broker. A stalled subscriber can drop thousands of events a second;
// one log line (and JSON encode and file write) each would make the stall
// worse, so drops in between are counted into the next warning.
const dropLogInterval = time.Second

// warnDropped logs a dropped event, at most once per dropLogInterval, with
// how many were dropped since the last warning.
func (b *Broker[T]) warnDropped(t EventType) {
	b.unloggedDrops.Add(1)
	now := time.Now().UnixNano()
	last := b.dropLoggedAt.Load()
	if last != 0 && now-last < int64(dropLogInterval) {
		return
	}
	if !b.dropLoggedAt.CompareAndSwap(last, now) {
		return // another publisher is logging this window
	}
	slog.Warn("Pubsub buffer full; dropping event", "type", t, "dropped", b.unloggedDrops.Swap(0))
}

// PublishMustDeliver delivers an event with bounded-blocking semantics.
// For each subscriber it first attempts a non-blocking send, then falls
// back to a blocking send bounded by a per-subscriber timeout (default
// [defaultMustDeliverTimeout]). On timeout the event is dropped for
// that subscriber, [Broker.MustDeliverDropCount] is incremented, and an
// error is logged; a reliable subscriber is cut instead. The publisher
// never blocks indefinitely.
//
// Use this for terminal events that must reach subscribers (finish,
// tool result, error, cancel). Ordinary subscribers must still tolerate
// rare drops after timeout — recovery is the subscriber's responsibility
// (e.g. a re-fetch on the next session-visible event).
func (b *Broker[T]) PublishMustDeliver(ctx context.Context, t EventType, payload T) {
	b.mu.RLock()

	select {
	case <-b.done:
		b.mu.RUnlock()
		return
	default:
	}

	event := Event[T]{Type: t, Payload: payload}
	timeout := b.mustDeliverTimeout
	var cut []*reliableSub[T]

	for sub := range b.subs {
		// Fast path: non-blocking send.
		select {
		case sub <- event:
			continue
		default:
		}

		// Slow path: bounded blocking send.
		timer := time.NewTimer(timeout)
		select {
		case sub <- event:
			timer.Stop()
		case <-timer.C:
			b.mustDeliverDropCount.Add(1)
			slog.Error("PublishMustDeliver timed out delivering event",
				"type", t, "timeout", timeout)
		case <-ctx.Done():
			timer.Stop()
			// The event reaches no reliable subscriber either: each of
			// them has now missed it.
			for rs := range b.reliable {
				if rs.dead.CompareAndSwap(false, true) {
					cut = append(cut, rs)
				}
			}
			b.mu.RUnlock()
			b.cut(cut, t)
			return
		}
	}
	// A reliable subscriber gets the same grace, then is cut rather than
	// fed a gap.
	for rs := range b.reliable {
		if rs.dead.Load() {
			continue
		}
		select {
		case rs.ch <- event:
			continue
		default:
		}
		timer := time.NewTimer(timeout)
		select {
		case rs.ch <- event:
			timer.Stop()
		case <-timer.C:
			if rs.dead.CompareAndSwap(false, true) {
				cut = append(cut, rs)
			}
		case <-ctx.Done():
			timer.Stop()
			if rs.dead.CompareAndSwap(false, true) {
				cut = append(cut, rs)
			}
		}
	}
	b.mu.RUnlock()
	b.cut(cut, t)
}
