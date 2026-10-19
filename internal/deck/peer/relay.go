package peer

import (
	"context"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/dev-dami/deck/internal/version"
)

// Relaying lets two Decks that cannot reach each other link through a third
// Deck they are both paired with (docs/multiplayer-design.md section 8.6).
//
// A, the origin, opens a relay@deck channel on its link to R, the relay,
// naming the target B by its key fingerprint. R accepts it only when
// peers.relay is on, A and B are both pinned on R, and B has a direct link
// to R. R then opens a relayed@deck channel on its link to B, saying which
// pinned key the link comes from, and copies bytes between the two channels.
// B accepts that only from a peer whose peers.remote row has relay set, and
// only for a key pinned on B. A and B then run a full SSH handshake inside
// the spliced channel, A as client and B as server, each checking the
// other's pinned key as on a direct link. R sees only ciphertext: it never
// holds a key of the inner link and never ends the inner SSH session.
//
// The inner link is a normal peer link: mcp channels only, and B's usual
// Caller and allow list for A. It takes no relay channel itself.

// Relay caps when ManagerOptions leaves them at zero.
const (
	defaultMaxRelays        = 16
	defaultMaxRelaysPerPeer = 4
)

// relayReq is the relay@deck payload: the target's key fingerprint, so
// the origin and the relay need not agree on names.
type relayReq struct {
	Target string
}

// relayedReq is the relayed@deck payload: the relay's name for the origin,
// and the origin's key fingerprint as pinned on the relay.
type relayedReq struct {
	Name        string
	Fingerprint string
}

// relayFrom marks a connection the Server got through a relay: via is the
// relay's name, fp the only key that may log in on it.
type relayFrom struct {
	via, fp string
}

// relayer counts relayed channels against the caps and keeps the splices
// that run, so they can be ended together.
type relayer struct {
	idle   time.Duration
	max    int // relayed channels on this Deck at once
	maxPer int // per pinned key taking part
	bytes  atomic.Int64
	wg     sync.WaitGroup // running splices

	mu    sync.Mutex
	total int
	per   map[string]int
	live  map[*splice]struct{}
	// tap, when set, sees every chunk a splice copies (tests).
	tap func([]byte)
}

func newRelayer(max, maxPer int, idle time.Duration) *relayer {
	if max <= 0 {
		max = defaultMaxRelays
	}
	if maxPer <= 0 {
		maxPer = defaultMaxRelaysPerPeer
	}
	return &relayer{idle: idle, max: max, maxPer: maxPer, per: map[string]int{}, live: map[*splice]struct{}{}}
}

// acquire takes one relay slot for each key, within the caps. release
// gives them back; call it once.
func (r *relayer) acquire(keys ...string) (release func(), ok bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.total >= r.max {
		return nil, false
	}
	for _, k := range keys {
		if r.per[k] >= r.maxPer {
			return nil, false
		}
	}
	r.total++
	for _, k := range keys {
		r.per[k]++
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			r.mu.Lock()
			defer r.mu.Unlock()
			r.total--
			for _, k := range keys {
				if r.per[k]--; r.per[k] <= 0 {
					delete(r.per, k)
				}
			}
		})
	}, true
}

// splice is one relayed link on the relay: two channels and a stop.
type splice struct {
	stop func()
}

// run copies bytes both ways between a and b until either side ends or
// nothing moves for r.idle, then closes both.
func (r *relayer) run(a, b ssh.Channel) int64 {
	var once sync.Once
	sp := &splice{stop: func() {
		once.Do(func() {
			a.Close()
			b.Close()
		})
	}}
	r.mu.Lock()
	r.live[sp] = struct{}{}
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		delete(r.live, sp)
		r.mu.Unlock()
	}()
	var tmu sync.Mutex
	t := time.AfterFunc(r.idle, sp.stop)
	defer t.Stop()
	touch := func() {
		tmu.Lock()
		t.Reset(r.idle)
		tmu.Unlock()
	}
	var n atomic.Int64
	var wg sync.WaitGroup
	wg.Add(2)
	for _, dir := range [][2]ssh.Channel{{a, b}, {b, a}} {
		go func() {
			defer wg.Done()
			defer sp.stop()
			n.Add(r.pipe(dir[0], dir[1], touch))
		}()
	}
	wg.Wait()
	return n.Load()
}

// pipe copies dst <- src, calling touch for every chunk.
func (r *relayer) pipe(dst io.Writer, src io.Reader, touch func()) int64 {
	buf := make([]byte, 32<<10)
	var n int64
	for {
		k, err := src.Read(buf)
		if k > 0 {
			touch()
			r.mu.Lock()
			tap := r.tap
			r.mu.Unlock()
			if tap != nil {
				tap(buf[:k])
			}
			if _, werr := dst.Write(buf[:k]); werr != nil {
				return n
			}
			n += int64(k)
			r.bytes.Add(int64(k))
		}
		if err != nil {
			return n
		}
	}
}

// stopAll ends every splice that runs.
func (r *relayer) stopAll() {
	r.mu.Lock()
	stops := make([]func(), 0, len(r.live))
	for sp := range r.live {
		stops = append(stops, sp.stop)
	}
	r.mu.Unlock()
	for _, stop := range stops {
		stop()
	}
}

// openRelay asks the Deck at the other end of c to relay a link to its peer
// with key fingerprint fp, and returns the channel as a net.Conn for the SSH
// handshake inside it.
func (c *Conn) openRelay(ctx context.Context, fp string, timeout time.Duration) (net.Conn, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	ch, err := c.openChannel(ctx, RelayChannelType, ssh.Marshal(relayReq{Target: fp}))
	if err != nil {
		return nil, fmt.Errorf("peer: relay through %s: %w", c.caller.Name, err)
	}
	return newChanConn(ch, relayAddr("relay:"+c.caller.Name), nil), nil
}

// relayChannel is the relay hook of the Manager's direct links: it takes
// relay@deck (this Deck relays) and relayed@deck (this Deck is the target).
// It runs on the link's channel loop, so it never waits on the network.
func (m *Manager) relayChannel(c *Conn, nc ssh.NewChannel) {
	if nc.ChannelType() == RelayChannelType {
		m.relayFor(c, nc)
		return
	}
	m.acceptRelayed(c, nc)
}

// relayFor relays a link from the peer on c to the peer the request names,
// when relaying is on, both are pinned here, and the target has a direct
// link to this Deck.
func (m *Manager) relayFor(c *Conn, nc ssh.NewChannel) {
	from := c.Peer()
	if p := m.peers.Load(); p == nil || !p.Relay {
		_ = nc.Reject(ssh.Prohibited, "relaying is off")
		return
	}
	var req relayReq
	if ssh.Unmarshal(nc.ExtraData(), &req) != nil {
		_ = nc.Reject(ssh.Prohibited, "bad relay request")
		return
	}
	if _, err := m.recheck(from); err != nil {
		_ = nc.Reject(ssh.Prohibited, "not pinned")
		return
	}
	to, ok := m.pinnedByFingerprint(req.Target)
	if !ok || to.Fingerprint == from.Fingerprint {
		_ = nc.Reject(ssh.ConnectionFailed, "no such peer")
		return
	}
	link := m.directLink(to.Name)
	if link == nil || link.Peer().Fingerprint != to.Fingerprint {
		_ = nc.Reject(ssh.ConnectionFailed, "the peer has no link to this Deck")
		return
	}
	release, ok := m.relays.acquire(from.Fingerprint, to.Fingerprint)
	if !ok {
		_ = nc.Reject(ssh.ResourceShortage, "too many relays")
		return
	}
	m.relays.wg.Add(1)
	go func() {
		defer m.relays.wg.Done()
		defer release()
		ctx, cancel := context.WithTimeout(c.ctx, link.lim.HandshakeTimeout)
		out, err := link.openChannel(ctx, RelayedChannelType, ssh.Marshal(relayedReq{Name: from.Name, Fingerprint: from.Fingerprint}))
		cancel()
		if err != nil {
			_ = nc.Reject(ssh.ConnectionFailed, "the peer refused the relay")
			return
		}
		in, reqs, err := nc.Accept()
		if err != nil {
			out.Close()
			return
		}
		go refuseRequests(reqs)
		m.o.Logger.Info("peer: relaying", "from", from.Name, "to", to.Name)
		n := m.relays.run(in, out)
		m.o.Logger.Info("peer: relay ended", "from", from.Name, "to", to.Name, "bytes", n)
	}()
}

// acceptRelayed serves a link that the peer on c relays from another
// peer, when this Deck's row for c's peer has relay set and the origin's
// key is pinned here. The link then goes through the relay Server's
// handshake, limits and lockout like an accepted TCP connection, and may
// only log in with the key the relay named.
func (m *Manager) acceptRelayed(c *Conn, nc ssh.NewChannel) {
	via := c.Peer()
	if r, ok := m.peers.Load().Find(via.Name); !ok || !r.Relay {
		_ = nc.Reject(ssh.Prohibited, "relayed links from this peer are off")
		return
	}
	if _, err := m.recheck(via); err != nil {
		_ = nc.Reject(ssh.Prohibited, "not pinned")
		return
	}
	var req relayedReq
	if ssh.Unmarshal(nc.ExtraData(), &req) != nil {
		_ = nc.Reject(ssh.Prohibited, "bad relayed request")
		return
	}
	origin, ok := m.pinnedByFingerprint(req.Fingerprint)
	if !ok || origin.Fingerprint == via.Fingerprint {
		_ = nc.Reject(ssh.Prohibited, "not pinned")
		return
	}
	srv := m.relayServer()
	if srv == nil {
		_ = nc.Reject(ssh.ConnectionFailed, "not running")
		return
	}
	release, ok := m.relays.acquire(via.Fingerprint)
	if !ok {
		_ = nc.Reject(ssh.ResourceShortage, "too many relays")
		return
	}
	ch, reqs, err := nc.Accept()
	if err != nil {
		release()
		return
	}
	go refuseRequests(reqs)
	raw := newChanConn(ch, relayAddr("relay:"+via.Name), release)
	srv.accept(raw, "relay:"+via.Name+" "+origin.Fingerprint, &relayFrom{via: via.Name, fp: origin.Fingerprint})
}

// pinnedByFingerprint returns the pin with key fingerprint fp.
func (m *Manager) pinnedByFingerprint(fp string) (Pinned, bool) {
	pins, err := m.store.List()
	if err != nil {
		return Pinned{}, false
	}
	for _, p := range pins {
		if p.Fingerprint == fp {
			return p, true
		}
	}
	return Pinned{}, false
}

// directLink is a live link to name that is not itself relayed: the one its
// supervisor keeps, else one it opened to the listener.
func (m *Manager) directLink(name string) *Conn {
	if sup := m.Supervisor(name); sup != nil {
		if c := sup.Conn(); c != nil && c.Via() == "" && !isDone(c) {
			return c
		}
	}
	m.mu.Lock()
	srv := m.srv
	m.mu.Unlock()
	if srv == nil {
		return nil
	}
	for _, c := range srv.Conns() {
		if c.Peer().Name == name && !isDone(c) {
			return c
		}
	}
	return nil
}

func isDone(c *Conn) bool {
	select {
	case <-c.Done():
		return true
	default:
		return false
	}
}

// relayServer is the Server that serves links relayed to this Deck, made
// the first time one comes. It has no listener of its own, and its own
// connection caps, so relayed links never take the slots of direct ones.
func (m *Manager) relayServer() *Server {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.live {
		return nil
	}
	if m.rsrv == nil {
		s, err := NewServer(ServerOptions{
			Identity: m.id, Store: m.store, Handler: m.o.Handler,
			Name: m.Name(), Version: version.Version,
			Caller: m.caller, Limits: m.o.Limits, Logger: m.o.Logger,
			OnConn: m.inbound, OnDisconnect: m.inbound, perKey: m.perKey,
		})
		if err != nil {
			return nil
		}
		m.rsrv = s
	}
	return m.rsrv
}

// relayAddr is the address of a relayed connection: "relay:" and the relay.
type relayAddr string

func (a relayAddr) Network() string { return "relay" }
func (a relayAddr) String() string  { return string(a) }

// chanConn is an SSH channel as a net.Conn, for the SSH handshake that runs
// inside a relayed channel.
type chanConn struct {
	ssh.Channel
	addr    net.Addr
	onClose func()

	mu     sync.Mutex
	closed bool
	rd, wd *time.Timer
}

func newChanConn(ch ssh.Channel, addr net.Addr, onClose func()) *chanConn {
	return &chanConn{Channel: ch, addr: addr, onClose: onClose}
}

func (c *chanConn) LocalAddr() net.Addr  { return c.addr }
func (c *chanConn) RemoteAddr() net.Addr { return c.addr }

// Close closes the channel and, once, runs onClose.
func (c *chanConn) Close() error {
	err := c.Channel.Close()
	c.mu.Lock()
	first := !c.closed
	c.closed = true
	for _, t := range []*time.Timer{c.rd, c.wd} {
		if t != nil {
			t.Stop()
		}
	}
	c.mu.Unlock()
	if first && c.onClose != nil {
		c.onClose()
	}
	return err
}

func (c *chanConn) SetDeadline(t time.Time) error {
	_ = c.SetReadDeadline(t)
	return c.SetWriteDeadline(t)
}

func (c *chanConn) SetReadDeadline(t time.Time) error  { c.deadline(&c.rd, t); return nil }
func (c *chanConn) SetWriteDeadline(t time.Time) error { c.deadline(&c.wd, t); return nil }

// deadline arms *tm to close the channel at t; a zero t disarms it. A
// channel has no deadlines of its own, so a deadline that passes closes the
// link. That is what idleConn and the handshake timeout want of it.
func (c *chanConn) deadline(tm **time.Timer, t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return
	}
	if t.IsZero() {
		if *tm != nil {
			(*tm).Stop()
		}
		return
	}
	if *tm == nil {
		*tm = time.AfterFunc(time.Until(t), func() { _ = c.Close() })
		return
	}
	(*tm).Reset(time.Until(t))
}
