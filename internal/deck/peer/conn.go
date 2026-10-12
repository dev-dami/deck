package peer

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/crypto/ssh"
)

// ChannelType is the only SSH channel type a peer accepts.
const ChannelType = "mcp"

// keepaliveRequest is the only global request a peer answers.
const keepaliveRequest = "keepalive@openssh.com"

// Limits bound one peer connection. Zero fields take the defaults.
type Limits struct {
	MaxChannels      int           // concurrent mcp channels the other side may open (4)
	MaxInFlight      int           // concurrent tools/call per channel (8)
	MaxMessage       int           // bytes per JSON-RPC message (DefaultMaxMessage)
	CallTimeout      time.Duration // per tools/call (60s)
	IdleTimeout      time.Duration // no traffic at all for this long closes the link (90s)
	HandshakeTimeout time.Duration // SSH handshake including auth (10s)
	// KeepaliveInterval is how often the dialing side sends
	// keepalive@openssh.com (15s); KeepaliveMisses unanswered in a row (3)
	// close the link.
	KeepaliveInterval time.Duration
	KeepaliveMisses   int
}

func (l Limits) withDefaults() Limits {
	if l.MaxChannels <= 0 {
		l.MaxChannels = 4
	}
	if l.MaxInFlight <= 0 {
		l.MaxInFlight = 8
	}
	if l.MaxMessage <= 0 {
		l.MaxMessage = DefaultMaxMessage
	}
	if l.CallTimeout <= 0 {
		l.CallTimeout = 60 * time.Second
	}
	if l.IdleTimeout <= 0 {
		l.IdleTimeout = 90 * time.Second
	}
	if l.HandshakeTimeout <= 0 {
		l.HandshakeTimeout = 10 * time.Second
	}
	if l.KeepaliveInterval <= 0 {
		l.KeepaliveInterval = 15 * time.Second
	}
	if l.KeepaliveMisses <= 0 {
		l.KeepaliveMisses = 3
	}
	return l
}

// Conn is one authenticated SSH link to a pinned peer. It is symmetric: both
// ends serve MCP on every mcp channel the other side opens, and either end
// can open its own channels with OpenMCP to call the other's tools. The
// dialing side gets one from Dial, the listening side from Server.
type Conn struct {
	sc     ssh.Conn
	caller Caller
	h      Handler
	self   string // this Deck's name, in serverInfo
	ver    string
	lim    Limits
	log    *slog.Logger

	recheck func(Caller) (Caller, error)
	perKey  *keyLimiter

	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}
	wg     sync.WaitGroup
	chans  chan struct{} // open incoming channels
	misses atomic.Int32
	closed sync.Once
}

// connParams is what newConn needs besides the SSH connection.
type connParams struct {
	caller    Caller
	handler   Handler
	self, ver string
	lim       Limits
	log       *slog.Logger
	keepalive bool
	// recheck re-validates the caller before every call it makes here (see
	// mcpServer.recheck); nil skips it, for short-lived links like Probe.
	recheck func(Caller) (Caller, error)
	// perKey, when set, caps calls in flight per pinned key.
	perKey *keyLimiter
}

func newConn(sc ssh.Conn, chans <-chan ssh.NewChannel, reqs <-chan *ssh.Request, p connParams) *Conn {
	ctx, cancel := context.WithCancel(context.Background())
	c := &Conn{
		sc: sc, caller: p.caller, h: p.handler, self: p.self, ver: p.ver,
		lim: p.lim.withDefaults(), log: p.log, recheck: p.recheck, perKey: p.perKey,
		ctx: ctx, cancel: cancel, done: make(chan struct{}),
	}
	if c.log == nil {
		c.log = slog.Default()
	}
	if c.h == nil {
		c.h = &StatusHandler{Name: p.self, Version: p.ver, Started: time.Now()}
	}
	c.chans = make(chan struct{}, c.lim.MaxChannels)
	c.wg.Add(3)
	go func() {
		defer c.wg.Done()
		_ = sc.Wait()
		cancel()
		close(c.done)
	}()
	go func() {
		defer c.wg.Done()
		c.globalRequests(reqs)
	}()
	go func() {
		defer c.wg.Done()
		c.newChannels(chans)
	}()
	if p.keepalive {
		c.wg.Add(1)
		go func() {
			defer c.wg.Done()
			c.keepalive()
		}()
	}
	return c
}

// Peer is the pinned peer on the other end.
func (c *Conn) Peer() Caller { return c.caller }

// RemoteAddr is the other end's network address.
func (c *Conn) RemoteAddr() net.Addr { return c.sc.RemoteAddr() }

// Done is closed when the link is down.
func (c *Conn) Done() <-chan struct{} { return c.done }

// KeepaliveMisses is the number of keepalives in a row left unanswered.
func (c *Conn) KeepaliveMisses() int { return int(c.misses.Load()) }

// Close closes the link and waits for everything it started.
func (c *Conn) Close() error {
	var err error
	c.closed.Do(func() {
		err = c.sc.Close()
		c.cancel()
	})
	c.wg.Wait()
	if errors.Is(err, net.ErrClosed) {
		err = nil
	}
	return err
}

// drop closes the link without waiting for what it started, so it is safe
// from inside a call and from the Manager's sweep; Close (or the Server)
// does the waiting.
func (c *Conn) drop() {
	c.closed.Do(func() {
		_ = c.sc.Close()
		c.cancel()
	})
}

// OpenMCP opens an mcp channel to the other side, for calling its tools.
// Close the Client when done; closing the Conn closes it too.
func (c *Conn) OpenMCP(ctx context.Context) (*Client, error) {
	type opened struct {
		ch   ssh.Channel
		reqs <-chan *ssh.Request
		err  error
	}
	res := make(chan opened, 1)
	go func() {
		ch, reqs, err := c.sc.OpenChannel(ChannelType, nil)
		res <- opened{ch, reqs, err}
	}()
	var o opened
	select {
	case o = <-res:
	case <-ctx.Done():
		// The open finishes or fails on its own; close what it returns.
		go func() {
			if o := <-res; o.err == nil {
				o.ch.Close()
				go ssh.DiscardRequests(o.reqs)
			}
		}()
		return nil, ctx.Err()
	}
	if o.err != nil {
		return nil, o.err
	}
	go refuseRequests(o.reqs)
	cl := newClient(c.ctx, o.ch, c.lim.MaxMessage, c.log)
	return cl, nil
}

// globalRequests answers keepalives and refuses every other global request
// (tcpip-forward, streamlocal-forward, no-more-sessions, hostkeys...).
func (c *Conn) globalRequests(reqs <-chan *ssh.Request) {
	for r := range reqs {
		if r.Type == keepaliveRequest {
			_ = r.Reply(true, nil)
			continue
		}
		c.log.Debug("peer: refused global request", "peer", c.caller.Name, "type", truncate(r.Type, 64))
		_ = r.Reply(false, nil)
	}
}

// newChannels accepts mcp channels, up to MaxChannels at once, and rejects
// every other type (session, direct-tcpip, forwarded-tcpip, x11,
// auth-agent@openssh.com, ...).
func (c *Conn) newChannels(chans <-chan ssh.NewChannel) {
	for nc := range chans {
		if nc.ChannelType() != ChannelType {
			c.log.Debug("peer: rejected channel", "peer", c.caller.Name, "type", truncate(nc.ChannelType(), 64))
			_ = nc.Reject(ssh.UnknownChannelType, "only mcp channels are accepted")
			continue
		}
		select {
		case c.chans <- struct{}{}:
		default:
			_ = nc.Reject(ssh.ResourceShortage, "too many channels")
			continue
		}
		ch, reqs, err := nc.Accept()
		if err != nil {
			<-c.chans
			continue
		}
		c.wg.Add(2)
		go func() {
			defer c.wg.Done()
			refuseRequests(reqs)
		}()
		go func() {
			defer c.wg.Done()
			defer func() { <-c.chans }()
			srv := newMCPServer(c.h, c.caller, c.self, c.ver,
				serverLimits{maxInFlight: c.lim.MaxInFlight, callTimeout: c.lim.CallTimeout, perKey: c.perKey}, c.log)
			srv.recheck, srv.revoked = c.recheck, c.drop
			serveMCP(c.ctx, ch, srv, c.lim.MaxMessage)
			ch.Close()
		}()
	}
}

// refuseRequests refuses every channel request: shell, exec, pty-req,
// subsystem, env, window-change, x11-req, auth-agent-req@openssh.com,
// signal... An mcp channel is a byte pipe and nothing else.
func refuseRequests(reqs <-chan *ssh.Request) {
	for r := range reqs {
		_ = r.Reply(false, nil)
	}
}

// keepalive sends keepalive@openssh.com every KeepaliveInterval; after
// KeepaliveMisses unanswered in a row it closes the link. Any reply counts:
// the request only proves the other side is alive.
func (c *Conn) keepalive() {
	t := time.NewTicker(c.lim.KeepaliveInterval)
	defer t.Stop()
	pending := make(chan error, 1)
	inFlight := false
	for {
		select {
		case <-c.done:
			return
		case err := <-pending:
			inFlight = false
			if err != nil {
				c.sc.Close()
				return
			}
			c.misses.Store(0)
			continue
		case <-t.C:
		}
		if inFlight {
			if int(c.misses.Add(1)) >= c.lim.KeepaliveMisses {
				c.log.Info("peer: keepalive timed out", "peer", c.caller.Name)
				c.sc.Close()
				return
			}
			continue
		}
		inFlight = true
		c.wg.Add(1)
		go func() {
			defer c.wg.Done()
			_, _, err := c.sc.SendRequest(keepaliveRequest, true, nil)
			pending <- err
		}()
	}
}

// truncate keeps logs of attacker-chosen strings short.
func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

// idleConn closes the link when nothing is read or written for idle: every
// Read and Write pushes its deadline forward.
type idleConn struct {
	net.Conn
	idle time.Duration
}

func (c *idleConn) Read(p []byte) (int, error) {
	_ = c.Conn.SetReadDeadline(time.Now().Add(c.idle))
	return c.Conn.Read(p)
}

func (c *idleConn) Write(p []byte) (int, error) {
	_ = c.Conn.SetWriteDeadline(time.Now().Add(c.idle))
	return c.Conn.Write(p)
}
