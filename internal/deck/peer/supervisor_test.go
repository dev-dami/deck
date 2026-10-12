package peer

import (
	"context"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/dev-dami/deck/internal/deck/config"
)

// proxy forwards a stable address to a target and can be taken down (new
// and existing connections dropped) or frozen (bytes stop flowing).
type proxy struct {
	t      *testing.T
	ln     net.Listener
	target string

	mu     sync.Mutex
	down   bool
	frozen bool
	conns  map[net.Conn]struct{}
	wg     sync.WaitGroup
}

func newProxy(t *testing.T, target string) *proxy {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p := &proxy{t: t, ln: ln, target: target, conns: map[net.Conn]struct{}{}}
	p.wg.Add(1)
	go p.serve()
	t.Cleanup(p.close)
	return p
}

func (p *proxy) addr() string { return p.ln.Addr().String() }

func (p *proxy) serve() {
	defer p.wg.Done()
	for {
		c, err := p.ln.Accept()
		if err != nil {
			return
		}
		p.mu.Lock()
		if p.down {
			p.mu.Unlock()
			c.Close()
			continue
		}
		up, err := net.Dial("tcp", p.target)
		if err != nil {
			p.mu.Unlock()
			c.Close()
			continue
		}
		p.conns[c], p.conns[up] = struct{}{}, struct{}{}
		p.wg.Add(2)
		p.mu.Unlock()
		go p.pipe(c, up)
		go p.pipe(up, c)
	}
}

func (p *proxy) pipe(dst, src net.Conn) {
	defer p.wg.Done()
	buf := make([]byte, 32<<10)
	for {
		n, err := src.Read(buf)
		for {
			p.mu.Lock()
			frozen, down := p.frozen, p.down
			p.mu.Unlock()
			if down || !frozen {
				break
			}
			time.Sleep(5 * time.Millisecond)
		}
		if n > 0 {
			if _, werr := dst.Write(buf[:n]); werr != nil {
				break
			}
		}
		if err != nil {
			break
		}
	}
	dst.Close()
	src.Close()
}

func (p *proxy) setDown(down bool) {
	p.mu.Lock()
	p.down = down
	var cs []net.Conn
	if down {
		for c := range p.conns {
			cs = append(cs, c)
		}
		p.conns = map[net.Conn]struct{}{}
	}
	p.mu.Unlock()
	for _, c := range cs {
		c.Close()
	}
}

func (p *proxy) setFrozen(f bool) {
	p.mu.Lock()
	p.frozen = f
	p.mu.Unlock()
}

func (p *proxy) close() {
	p.ln.Close()
	p.setDown(true)
	p.wg.Wait()
}

// nextState waits for the next event.
func nextState(t *testing.T, ch <-chan Event) Event {
	t.Helper()
	select {
	case ev := <-ch:
		return ev
	case <-time.After(10 * time.Second):
		t.Fatal("no state change")
	}
	return Event{}
}

// waitState reads events until state shows up, returning the ones seen.
func waitState(t *testing.T, ch <-chan Event, want State) []State {
	t.Helper()
	var seen []State
	for {
		ev := nextState(t, ch)
		seen = append(seen, ev.Status.State)
		if ev.Status.State == want {
			return seen
		}
	}
}

func supervised(t *testing.T, opts ServerOptions) (*Supervisor, *proxy, <-chan Event) {
	t.Helper()
	_, b, srv, pinA := pairUp(t, opts)
	px := newProxy(t, srv.Addr().String())
	sup := NewSupervisor(SupervisorOptions{
		Peer: "a",
		Dial: func(ctx context.Context) (*Conn, error) {
			return Dial(ctx, DialOptions{Identity: b.id, Peer: pinA, Addr: px.addr(), Logger: quietLog,
				Keepalive: true, Limits: Limits{KeepaliveInterval: time.Hour}})
		},
		PingInterval: 40 * time.Millisecond,
		PingTimeout:  100 * time.Millisecond,
		MaxPingFails: 3,
		MinBackoff:   10 * time.Millisecond,
		MaxBackoff:   40 * time.Millisecond,
	})
	events, stop := sup.Subscribe()
	t.Cleanup(stop)
	t.Cleanup(sup.Close)
	return sup, px, events
}

func TestSupervisorReconnects(t *testing.T) {
	leakCheck(t)
	sup, px, events := supervised(t, ServerOptions{})
	if st := sup.Status(); st.State != StateIdle {
		t.Fatalf("initial state %s", st.State)
	}
	sup.Start(context.Background())

	ev := nextState(t, events)
	if ev.From != StateIdle || ev.Status.State != StateDialing {
		t.Fatalf("first event %s -> %s", ev.From, ev.Status.State)
	}
	ev = nextState(t, events)
	if ev.From != StateDialing || ev.Status.State != StateConnected {
		t.Fatalf("second event %s -> %s (%s)", ev.From, ev.Status.State, ev.Status.LastError)
	}
	if sup.Client() == nil || sup.Conn() == nil {
		t.Fatal("no live client while connected")
	}

	// The peer goes away: backoff, then dial attempts that fail, in order.
	px.setDown(true)
	ev = nextState(t, events)
	if ev.From != StateConnected || ev.Status.State != StateBackoff || ev.Status.LastError == "" || ev.Status.NextRetry.IsZero() {
		t.Fatalf("after drop: %s -> %s %+v", ev.From, ev.Status.State, ev.Status)
	}
	for i := 0; i < 3; i++ {
		if ev := nextState(t, events); ev.Status.State != StateDialing {
			t.Fatalf("expected dialing, got %s", ev.Status.State)
		}
		ev := nextState(t, events)
		if ev.Status.State != StateBackoff {
			t.Fatalf("expected backoff, got %s", ev.Status.State)
		}
		if ev.Status.Attempts < 1 {
			t.Fatalf("attempts %d", ev.Status.Attempts)
		}
	}
	if sup.Client() != nil {
		t.Fatal("client kept after the link dropped")
	}

	// Back up: it reconnects and re-initializes on its own.
	px.setDown(false)
	waitState(t, events, StateConnected)
	st := sup.Status()
	if st.State != StateConnected || st.Attempts != 0 || st.LastError != "" {
		t.Fatalf("after reconnect: %+v", st)
	}
	res, err := sup.Client().Execute(ctxT(t), OpPeerStatus, nil)
	if err != nil || res.IsError {
		t.Fatalf("call after reconnect: %+v %v", res, err)
	}

	sup.Close()
	if ev := nextState(t, events); ev.Status.State != StateIdle {
		t.Fatalf("after Close: %s", ev.Status.State)
	}
}

func TestSupervisorDegradesThenDrops(t *testing.T) {
	leakCheck(t)
	sup, px, events := supervised(t, ServerOptions{})
	sup.Start(context.Background())
	waitState(t, events, StateConnected)
	px.setFrozen(true)
	ev := nextState(t, events)
	if ev.From != StateConnected || ev.Status.State != StateDegraded {
		t.Fatalf("frozen link: %s -> %s", ev.From, ev.Status.State)
	}
	ev = nextState(t, events)
	if ev.From != StateDegraded || ev.Status.State != StateBackoff {
		t.Fatalf("still frozen: %s -> %s", ev.From, ev.Status.State)
	}
	px.setFrozen(false)
	waitState(t, events, StateConnected)
}

func TestSupervisorCloseWhileDialing(t *testing.T) {
	leakCheck(t)
	started := make(chan struct{})
	sup := NewSupervisor(SupervisorOptions{
		Peer: "x",
		Dial: func(ctx context.Context) (*Conn, error) {
			close(started)
			<-ctx.Done()
			return nil, ctx.Err()
		},
	})
	sup.Start(context.Background())
	<-started
	done := make(chan struct{})
	go func() {
		sup.Close()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Close hung while dialing")
	}
	sup.Close() // twice is fine
	NewSupervisor(SupervisorOptions{Peer: "never"}).Close()
}

func TestSupervisorBackoff(t *testing.T) {
	jitter := 0.5 // the middle: no change
	s := NewSupervisor(SupervisorOptions{Jitter: func() float64 { return jitter }})
	want := []time.Duration{1, 2, 4, 8, 16, 32, 60, 60, 60}
	for i, w := range want {
		if got := s.backoff(i + 1); got != w*time.Second {
			t.Fatalf("backoff(%d) = %s, want %ss", i+1, got, w)
		}
	}
	jitter = 0
	if got := s.backoff(1); got != time.Second {
		t.Fatalf("low jitter at attempt 1 = %s, want the 1s floor", got)
	}
	if got := s.backoff(3); got != 3200*time.Millisecond {
		t.Fatalf("low jitter at attempt 3 = %s", got)
	}
	jitter = 0.999
	if got := s.backoff(20); got != 60*time.Second {
		t.Fatalf("high jitter cap = %s", got)
	}
	if got := s.backoff(2); got <= 2*time.Second || got > 2400*time.Millisecond {
		t.Fatalf("high jitter at attempt 2 = %s", got)
	}
}

func TestSupervisorInjectedClock(t *testing.T) {
	leakCheck(t)
	// With an After that fires at once, failures cycle without real waiting
	// and Status reports the planned delay.
	fired := make(chan time.Duration, 100)
	now := time.Unix(1000, 0)
	sup := NewSupervisor(SupervisorOptions{
		Peer: "x",
		Dial: func(context.Context) (*Conn, error) { return nil, io.ErrUnexpectedEOF },
		After: func(d time.Duration) <-chan time.Time {
			select {
			case fired <- d:
			default:
			}
			c := make(chan time.Time, 1)
			c <- now
			return c
		},
		Now:    func() time.Time { return now },
		Jitter: func() float64 { return 0.5 },
	})
	events, stop := sup.Subscribe()
	defer stop()
	sup.Start(context.Background())
	var delays []time.Duration
	for len(delays) < 7 {
		select {
		case d := <-fired:
			delays = append(delays, d)
		case <-time.After(5 * time.Second):
			t.Fatal("backoff did not run")
		}
	}
	sup.Close()
	want := []time.Duration{1, 2, 4, 8, 16, 32, 60}
	for i := range want {
		if delays[i] != want[i]*time.Second {
			t.Fatalf("delays %v", delays)
		}
	}
	ev := <-events
	if ev.Status.State != StateDialing {
		t.Fatalf("first event %s", ev.Status.State)
	}
	ev = <-events
	if ev.Status.State != StateBackoff || ev.Status.LastError != io.ErrUnexpectedEOF.Error() || !ev.Status.NextRetry.Equal(now.Add(time.Second)) {
		t.Fatalf("backoff event %+v", ev.Status)
	}
}

type remoteCfg struct{ name, addr string }

func peersCfg(name string, listen bool, remotes []remoteCfg) *config.Peers {
	p := &config.Peers{Enabled: true, Name: name, Listen: config.PeerListen{Enabled: listen, Addr: "127.0.0.1:0"}}
	for _, r := range remotes {
		p.Remote = append(p.Remote, config.PeerRemote{Name: r.name, Addr: r.addr, Dial: true})
	}
	return p
}

func TestManagerStartsListenerAndSupervisors(t *testing.T) {
	leakCheck(t)
	a, b := newTestPeer(t, "a"), newTestPeer(t, "b")
	ma, err := NewManager(ManagerOptions{Dir: a.dir, Logger: quietLog, Peers: peersCfg("a", true, nil)})
	if err != nil {
		t.Fatal(err)
	}
	if err := ma.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer ma.Stop()
	addr := ma.Addr()
	if addr == "" {
		t.Fatal("not listening")
	}
	a.pin(t, b, "")
	b.pin(t, a, addr)
	mb, err := NewManager(ManagerOptions{Dir: b.dir, Logger: quietLog, Peers: peersCfg("b", false, []remoteCfg{{"a", addr}, {"ghost", "127.0.0.1:1"}})})
	if err != nil {
		t.Fatal(err)
	}
	if err := mb.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	sup := mb.Supervisor("a")
	if sup == nil || mb.Supervisor("ghost") != nil {
		t.Fatal("supervisors: want one for the pinned peer only")
	}
	deadline := time.Now().Add(10 * time.Second)
	for sup.Status().State != StateConnected {
		if time.Now().After(deadline) {
			t.Fatalf("never connected: %+v", sup.Status())
		}
		time.Sleep(10 * time.Millisecond)
	}
	if st := mb.Statuses(); len(st) != 1 || st[0].Peer != "a" {
		t.Fatalf("Statuses = %+v", st)
	}
	mb.Stop()
	ma.Stop()
}
