package peer

import (
	"context"
	"errors"
	"math/rand/v2"
	"sync"
	"time"
)

// State is a supervised link's state.
type State string

// Link states: idle -> dialing -> connected <-> degraded, and backoff after
// any failure, then dialing again.
const (
	StateIdle      State = "idle"
	StateDialing   State = "dialing"
	StateConnected State = "connected"
	StateDegraded  State = "degraded"
	StateBackoff   State = "backoff"
)

// Status is a snapshot of one supervised peer.
type Status struct {
	Peer      string
	State     State
	Since     time.Time     // when State began
	LastError string        // the last failure, if any
	NextRetry time.Time     // in backoff: when the next dial starts
	Latency   time.Duration // the last MCP ping's round trip
	Attempts  int           // failed dials since the last stable connection
	// Permanent is set when the last failure needs a person, not a retry:
	// the peer's host key changed, this Deck is not pinned there, or the peer
	// is no longer pinned here. Retries then wait MaxBackoff; nothing is
	// ever re-pinned on its own.
	Permanent bool
}

// Event is one state change.
type Event struct {
	From   State
	Status Status // the new status; Status.State is the new state
}

// SupervisorOptions configures a Supervisor. Zero durations take defaults.
type SupervisorOptions struct {
	Peer string
	// Dial connects to the peer. The supervisor owns the returned Conn.
	Dial func(ctx context.Context) (*Conn, error)
	// PingInterval is how often the link is checked with an MCP ping (15s);
	// PingTimeout bounds one ping (10s); MaxPingFails failures in a row (3)
	// drop the link. One failure marks it degraded.
	PingInterval time.Duration
	PingTimeout  time.Duration
	MaxPingFails int
	// MinBackoff and MaxBackoff bound the retry delay (1s, 60s). It doubles
	// per failed attempt, with +-20% jitter; a permanent failure (see
	// Status.Permanent) waits MaxBackoff.
	MinBackoff, MaxBackoff time.Duration
	// StableAfter is how long a link must stay up before the backoff starts
	// over from MinBackoff (30s), so a peer that accepts and then drops
	// every link at once is not redialed every second.
	StableAfter time.Duration
	// Jitter returns a number in [0, 1); nil means math/rand.
	Jitter func() float64
	// After returns a channel that fires after d; nil means time.After.
	// Tests use it to skip real waiting.
	After func(d time.Duration) <-chan time.Time
	// Now is the clock; nil means time.Now.
	Now func() time.Time
}

// Supervisor keeps one peer connected: it dials, re-runs MCP initialize on
// every connection, pings, and retries with exponential backoff. Start it
// once; Close stops it and waits for it.
type Supervisor struct {
	o SupervisorOptions

	mu     sync.Mutex
	st     Status
	conn   *Conn
	client *Client
	subs   map[chan Event]struct{}

	cancel context.CancelFunc
	done   chan struct{}
	once   sync.Once
}

// NewSupervisor builds a Supervisor in state idle.
func NewSupervisor(o SupervisorOptions) *Supervisor {
	if o.PingInterval <= 0 {
		o.PingInterval = 15 * time.Second
	}
	if o.PingTimeout <= 0 {
		o.PingTimeout = 10 * time.Second
	}
	if o.MaxPingFails <= 0 {
		o.MaxPingFails = 3
	}
	if o.MinBackoff <= 0 {
		o.MinBackoff = time.Second
	}
	if o.MaxBackoff < o.MinBackoff {
		o.MaxBackoff = 60 * time.Second
	}
	if o.StableAfter <= 0 {
		o.StableAfter = 30 * time.Second
	}
	if o.Jitter == nil {
		o.Jitter = rand.Float64
	}
	if o.After == nil {
		o.After = time.After
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	s := &Supervisor{o: o, subs: map[chan Event]struct{}{}, done: make(chan struct{})}
	s.st = Status{Peer: o.Peer, State: StateIdle, Since: o.Now()}
	return s
}

// Start runs the supervisor until ctx ends or Close.
func (s *Supervisor) Start(ctx context.Context) {
	s.once.Do(func() {
		ctx, s.cancel = context.WithCancel(ctx)
		go func() {
			defer close(s.done)
			s.run(ctx)
		}()
	})
}

// Close stops the supervisor, closes its connection and waits.
func (s *Supervisor) Close() {
	started := true
	s.once.Do(func() { started = false })
	if !started {
		return
	}
	s.cancel()
	<-s.done
}

// Status returns the current snapshot.
func (s *Supervisor) Status() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.st
}

// Client returns the live MCP client, or nil when not connected.
func (s *Supervisor) Client() *Client {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.client
}

// Conn returns the live connection, or nil.
func (s *Supervisor) Conn() *Conn {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.conn
}

// Subscribe returns a channel of state changes, in order, and a func to stop.
// The channel is buffered; a subscriber that falls more than 32 events behind
// misses events (Status always has the latest).
func (s *Supervisor) Subscribe() (<-chan Event, func()) {
	ch := make(chan Event, 32)
	s.mu.Lock()
	s.subs[ch] = struct{}{}
	s.mu.Unlock()
	var once sync.Once
	return ch, func() {
		once.Do(func() {
			s.mu.Lock()
			delete(s.subs, ch)
			s.mu.Unlock()
		})
	}
}

// set moves to state and tells subscribers when the state changed.
func (s *Supervisor) set(state State, mutate func(*Status)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	from := s.st.State
	if mutate != nil {
		mutate(&s.st)
	}
	if state == from {
		return
	}
	s.st.State = state
	s.st.Since = s.o.Now()
	if state != StateBackoff {
		s.st.NextRetry = time.Time{}
	}
	ev := Event{From: from, Status: s.st}
	for ch := range s.subs {
		select {
		case ch <- ev:
		default:
		}
	}
}

// backoff is the delay before retry n (1-based).
func (s *Supervisor) backoff(n int) time.Duration {
	d := s.o.MinBackoff
	for i := 1; i < n && d < s.o.MaxBackoff; i++ {
		d *= 2
	}
	d = time.Duration(float64(d) * (0.8 + 0.4*s.o.Jitter()))
	if d < s.o.MinBackoff {
		d = s.o.MinBackoff
	}
	if d > s.o.MaxBackoff {
		d = s.o.MaxBackoff
	}
	return d
}

func (s *Supervisor) run(ctx context.Context) {
	defer s.set(StateIdle, nil)
	attempts := 0
	for ctx.Err() == nil {
		s.set(StateDialing, nil)
		up, err := s.session(ctx)
		if ctx.Err() != nil {
			return
		}
		if up >= s.o.StableAfter {
			attempts = 0
		}
		attempts++
		d := s.backoff(attempts)
		permanent := IsPermanent(err)
		if permanent {
			d = s.o.MaxBackoff
		}
		s.set(StateBackoff, func(st *Status) {
			st.LastError = errText(err)
			st.Attempts = attempts
			st.NextRetry = s.o.Now().Add(d)
			st.Permanent = permanent
		})
		select {
		case <-ctx.Done():
			return
		case <-s.o.After(d):
		}
	}
}

// session dials, initializes and pings until the link fails. up is how long
// the link was connected (0 when it never got there).
func (s *Supervisor) session(ctx context.Context) (up time.Duration, err error) {
	conn, err := s.o.Dial(ctx)
	if err != nil {
		return 0, err
	}
	defer conn.Close()
	cl, err := conn.OpenMCP(ctx)
	if err != nil {
		return 0, err
	}
	defer cl.Close()
	ictx, cancel := context.WithTimeout(ctx, s.o.PingTimeout)
	_, err = cl.Initialize(ictx)
	cancel()
	if err != nil {
		return 0, err
	}
	lat, err := s.ping(ctx, cl)
	if err != nil {
		return 0, err
	}
	since := s.o.Now()
	defer func() { up = s.o.Now().Sub(since) }()
	s.mu.Lock()
	s.conn, s.client = conn, cl
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.conn, s.client = nil, nil
		s.mu.Unlock()
	}()
	s.set(StateConnected, func(st *Status) {
		st.Latency = lat
		st.Attempts = 0
		st.LastError = ""
		st.Permanent = false
	})

	fails := 0
	for {
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		case <-conn.Done():
			return 0, errors.New("connection lost")
		case <-cl.Done():
			return 0, errors.New("mcp channel closed")
		case <-s.o.After(s.o.PingInterval):
		}
		lat, err := s.ping(ctx, cl)
		if ctx.Err() != nil {
			return 0, ctx.Err()
		}
		if err == nil && conn.KeepaliveMisses() == 0 {
			fails = 0
			s.set(StateConnected, func(st *Status) { st.Latency = lat })
			continue
		}
		if err == nil {
			err = errors.New("keepalive unanswered")
		} else {
			fails++
		}
		if fails >= s.o.MaxPingFails {
			return 0, err
		}
		s.set(StateDegraded, func(st *Status) { st.LastError = errText(err) })
	}
}

func (s *Supervisor) ping(ctx context.Context, cl *Client) (time.Duration, error) {
	pctx, cancel := context.WithTimeout(ctx, s.o.PingTimeout)
	defer cancel()
	return cl.Ping(pctx)
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
