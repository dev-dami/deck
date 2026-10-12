package peer

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
)

// sshVersion is the SSH identification both ends send.
const sshVersion = "SSH-2.0-DeckPeer_1"

// extPeer carries the pinned name from auth to the connection.
const extPeer = "deck-peer"

var errNotPinned = errors.New("peer: key not pinned")

// baseSSHConfig limits both ends to modern algorithms: ML-KEM/X25519 or
// X25519 key exchange and AEAD ciphers only.
func baseSSHConfig() ssh.Config {
	return ssh.Config{
		KeyExchanges: []string{ssh.KeyExchangeMLKEM768X25519, ssh.KeyExchangeCurve25519},
		Ciphers:      []string{ssh.CipherChaCha20Poly1305, ssh.CipherAES256GCM, ssh.CipherAES128GCM},
	}
}

// ServerOptions configures a Server.
type ServerOptions struct {
	Identity *Identity
	Store    *Store
	// Handler answers the peers' tool calls; nil means a StatusHandler.
	Handler Handler
	// Name and Version are this Deck's, for serverInfo.
	Name, Version string
	// Caller turns a pinned peer into the Caller its handler sees, adding
	// config such as allow_inbound; nil uses the pin alone.
	Caller func(Pinned) Caller
	Limits Limits
	// MaxConns caps connections at once, handshakes included (16).
	MaxConns int
	// Lockout: after LockoutThreshold failed handshakes from one IP (5) it is
	// refused for LockoutBase (30s), doubling per lockout up to LockoutMax (15m).
	LockoutThreshold        int
	LockoutBase, LockoutMax time.Duration
	// OnConn, when set, is called for every authenticated connection.
	OnConn func(*Conn)
	Logger *slog.Logger

	now func() time.Time // tests
}

// Server is the embedded SSH listener. It authenticates pinned peers by key,
// accepts only mcp channels and answers only keepalives; everything else an
// SSH client can ask for is refused.
type Server struct {
	opts ServerOptions
	cfg  *ssh.ServerConfig
	lock *lockout
	sem  chan struct{}
	log  *slog.Logger

	mu     sync.Mutex
	ln     net.Listener
	raw    map[net.Conn]struct{}
	conns  map[*Conn]struct{}
	closed bool
	wg     sync.WaitGroup
}

// NewServer checks opts and builds a Server; Serve or Listen starts it.
func NewServer(opts ServerOptions) (*Server, error) {
	if opts.Identity == nil || opts.Store == nil {
		return nil, errors.New("peer: server needs an identity and a store")
	}
	opts.Limits = opts.Limits.withDefaults()
	if opts.MaxConns <= 0 {
		opts.MaxConns = 16
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	s := &Server{
		opts:  opts,
		lock:  newLockout(opts.LockoutThreshold, opts.LockoutBase, opts.LockoutMax, opts.now),
		sem:   make(chan struct{}, opts.MaxConns),
		log:   opts.Logger,
		raw:   map[net.Conn]struct{}{},
		conns: map[*Conn]struct{}{},
	}
	s.cfg = &ssh.ServerConfig{
		Config:                  baseSSHConfig(),
		PublicKeyAuthAlgorithms: []string{ssh.KeyAlgoED25519},
		MaxAuthTries:            3,
		ServerVersion:           sshVersion,
		// No PasswordCallback, KeyboardInteractiveCallback or NoClientAuth:
		// public key is the only method offered.
		PublicKeyCallback: s.checkKey,
	}
	s.cfg.AddHostKey(opts.Identity.Signer)
	return s, nil
}

// Listen starts a Server on addr.
func Listen(addr string, opts ServerOptions) (*Server, error) {
	s, err := NewServer(opts)
	if err != nil {
		return nil, err
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("peer: listen %s: %w", addr, err)
	}
	s.mu.Lock()
	s.ln = ln // so Addr works at once
	s.mu.Unlock()
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		_ = s.Serve(ln)
	}()
	return s, nil
}

// checkKey lets in only an ed25519 key from the pinned store.
func (s *Server) checkKey(_ ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
	if key.Type() != ssh.KeyAlgoED25519 {
		return nil, errNotPinned
	}
	p, ok, err := s.opts.Store.ByKey(key)
	if err != nil {
		s.log.Warn("peer: pinned store unreadable", "error", err)
		return nil, errNotPinned
	}
	if !ok {
		return nil, errNotPinned
	}
	return &ssh.Permissions{Extensions: map[string]string{extPeer: p.Name}}, nil
}

// Serve accepts connections on ln until Close.
func (s *Server) Serve(ln net.Listener) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		ln.Close()
		return net.ErrClosed
	}
	s.ln = ln
	s.mu.Unlock()
	for {
		raw, err := ln.Accept()
		if err != nil {
			s.mu.Lock()
			closed := s.closed
			s.mu.Unlock()
			if closed || errors.Is(err, net.ErrClosed) {
				return net.ErrClosed
			}
			time.Sleep(50 * time.Millisecond) // e.g. too many open files
			continue
		}
		key := lockKey(raw.RemoteAddr())
		if s.lock.locked(key) {
			raw.Close()
			continue
		}
		select {
		case s.sem <- struct{}{}:
		default:
			raw.Close()
			continue
		}
		if !s.track(raw) {
			<-s.sem
			raw.Close()
			continue
		}
		go func() {
			defer s.wg.Done()
			defer func() { <-s.sem }()
			defer s.untrack(raw)
			s.handle(raw, key)
		}()
	}
}

// track registers raw and adds its goroutine to wg, under the lock Close
// takes, so Close never waits while a connection is still being added.
func (s *Server) track(raw net.Conn) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return false
	}
	s.raw[raw] = struct{}{}
	s.wg.Add(1)
	return true
}

func (s *Server) untrack(raw net.Conn) {
	s.mu.Lock()
	delete(s.raw, raw)
	s.mu.Unlock()
	raw.Close()
}

// handle runs one connection: the handshake under HandshakeTimeout, then the
// link until it ends.
func (s *Server) handle(raw net.Conn, key string) {
	lim := s.opts.Limits
	timer := time.AfterFunc(lim.HandshakeTimeout, func() { raw.Close() })
	sc, chans, reqs, err := ssh.NewServerConn(&idleConn{Conn: raw, idle: lim.IdleTimeout}, s.cfg)
	timer.Stop()
	if err != nil {
		s.lock.fail(key)
		s.log.Debug("peer: handshake failed", "remote", key, "error", err)
		return
	}
	name := sc.Permissions.Extensions[extPeer]
	p, ok, err := s.opts.Store.Get(name)
	if err != nil || !ok {
		sc.Close() // unpinned between auth and now
		return
	}
	s.lock.ok(key)
	caller := Caller{Name: p.Name, Fingerprint: p.Fingerprint, Role: p.Role}
	if s.opts.Caller != nil {
		caller = s.opts.Caller(p)
	}
	c := newConn(sc, chans, reqs, connParams{
		caller: caller, handler: s.opts.Handler, self: s.opts.Name, ver: s.opts.Version,
		lim: lim, log: s.log,
	})
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		c.Close()
		return
	}
	s.conns[c] = struct{}{}
	s.mu.Unlock()
	s.log.Info("peer: connected", "peer", caller.Name, "remote", key)
	if s.opts.OnConn != nil {
		s.opts.OnConn(c)
	}
	<-c.Done()
	c.Close()
	s.mu.Lock()
	delete(s.conns, c)
	s.mu.Unlock()
	s.log.Info("peer: disconnected", "peer", caller.Name)
}

// Addr is the listening address (nil before Serve).
func (s *Server) Addr() net.Addr {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ln == nil {
		return nil
	}
	return s.ln.Addr()
}

// Conns returns the authenticated connections open now.
func (s *Server) Conns() []*Conn {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*Conn, 0, len(s.conns))
	for c := range s.conns {
		out = append(out, c)
	}
	return out
}

// Close stops listening, closes every connection and waits for them.
func (s *Server) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		s.wg.Wait()
		return nil
	}
	s.closed = true
	ln := s.ln
	raws := make([]net.Conn, 0, len(s.raw))
	for r := range s.raw {
		raws = append(raws, r)
	}
	s.mu.Unlock()
	if ln != nil {
		ln.Close()
	}
	for _, r := range raws {
		r.Close()
	}
	s.wg.Wait()
	return nil
}

// DialOptions configures Dial.
type DialOptions struct {
	Identity *Identity
	// Peer is the pin to dial: its Addr, and the only host key accepted.
	Peer Pinned
	// Addr overrides Peer.Addr when set.
	Addr string
	// Handler answers the peer's calls back over this link; nil means a
	// StatusHandler.
	Handler Handler
	// Caller is how the peer appears to Handler; zero means the pin alone.
	Caller        Caller
	Name, Version string
	Limits        Limits
	// Keepalive turns on keepalive@openssh.com (the supervisor does).
	Keepalive   bool
	DialTimeout time.Duration // TCP connect (10s)
	Logger      *slog.Logger
}

// Dial connects to a pinned peer. The peer's host key must be exactly the
// pinned key; there is no trust on first use.
func Dial(ctx context.Context, o DialOptions) (*Conn, error) {
	if o.Identity == nil {
		return nil, errors.New("peer: dial needs an identity")
	}
	pinned := o.Peer.Key()
	if pinned == nil {
		return nil, fmt.Errorf("peer: %q has no pinned key", o.Peer.Name)
	}
	addr := o.Addr
	if addr == "" {
		addr = o.Peer.Addr
	}
	if addr == "" {
		return nil, fmt.Errorf("peer: %q has no address", o.Peer.Name)
	}
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	lim := o.Limits.withDefaults()
	if o.DialTimeout <= 0 {
		o.DialTimeout = 10 * time.Second
	}
	d := net.Dialer{Timeout: o.DialTimeout}
	raw, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, err
	}
	stop := context.AfterFunc(ctx, func() { raw.Close() })
	timer := time.AfterFunc(lim.HandshakeTimeout, func() { raw.Close() })
	cfg := &ssh.ClientConfig{
		Config:            baseSSHConfig(),
		User:              "deck",
		Auth:              []ssh.AuthMethod{ssh.PublicKeys(o.Identity.Signer)},
		HostKeyCallback:   pinnedHostKey(o.Peer.Name, pinned),
		HostKeyAlgorithms: []string{ssh.KeyAlgoED25519},
		ClientVersion:     sshVersion,
	}
	sc, chans, reqs, err := ssh.NewClientConn(&idleConn{Conn: raw, idle: lim.IdleTimeout}, addr, cfg)
	timer.Stop()
	stopped := stop()
	if err != nil || !stopped {
		raw.Close()
		if err == nil {
			sc.Close()
			err = ctx.Err()
		}
		return nil, fmt.Errorf("peer: connect %s: %w", o.Peer.Name, err)
	}
	caller := o.Caller
	if caller.Name == "" {
		caller = Caller{Name: o.Peer.Name, Role: o.Peer.Role}
	}
	caller.Fingerprint = o.Peer.Fingerprint
	return newConn(sc, chans, reqs, connParams{
		caller: caller, handler: o.Handler, self: o.Name, ver: o.Version,
		lim: lim, log: o.Logger, keepalive: o.Keepalive,
	}), nil
}

// pinnedHostKey accepts exactly the pinned key.
func pinnedHostKey(name string, pinned ssh.PublicKey) ssh.HostKeyCallback {
	return func(_ string, _ net.Addr, key ssh.PublicKey) error {
		if !sameKey(key, pinned) {
			return fmt.Errorf("host key mismatch for peer %q: got %s, pinned %s (re-pair if it really changed)",
				name, Fingerprint(key), Fingerprint(pinned))
		}
		return nil
	}
}
