package peer

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
)

// sshVersion is the SSH identification both ends send.
const sshVersion = "SSH-2.0-DeckPeer_1"

// extPeer and extFingerprint carry the pinned name and the key's fingerprint
// from auth to the connection.
const (
	extPeer        = "deck-peer"
	extFingerprint = "deck-peer-fp"
)

var errNotPinned = errors.New("peer: key not pinned")

// Dial errors that retrying will not fix: a supervisor backs off at its
// maximum interval on them (IsPermanent) and never re-pins.
var (
	// ErrHostKeyMismatch: the peer answered with another host key than the
	// pinned one.
	ErrHostKeyMismatch = errors.New("host key mismatch")
	// ErrNotAccepted: the peer refused this Deck's key (it is not pinned
	// there, or this key is locked out).
	ErrNotAccepted = errors.New("peer: the other Deck refused this Deck's key (not pinned there?)")
)

// IsPermanent reports whether a dial error needs a person rather than a
// retry: a changed host key, this Deck not pinned there, or the peer no
// longer pinned here.
func IsPermanent(err error) bool {
	return errors.Is(err, ErrHostKeyMismatch) || errors.Is(err, ErrNotAccepted) || errors.Is(err, ErrRevoked)
}

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
	// config such as allow_inbound; nil uses the pin alone. It is called again
	// before every call, so config it reads applies to open links.
	Caller func(Pinned) Caller
	Limits Limits
	// Connections that have not authenticated yet and authenticated links
	// are counted apart, so a flood of the first never takes a slot of the
	// second.
	//
	// MaxPreAuth caps connections still in the handshake (32), and
	// MaxPreAuthPerIP those from one IP or IPv6 /64 (2). PreAuthTimeout
	// bounds the handshake on this side (5s, or Limits.HandshakeTimeout when
	// that is shorter).
	MaxPreAuth, MaxPreAuthPerIP int
	PreAuthTimeout              time.Duration
	// MaxConns caps authenticated links (16) and MaxConnsPerKey those of
	// one pinned key (4).
	MaxConns, MaxConnsPerKey int
	// MaxInFlightPerKey caps tools/call in flight per pinned key over all of
	// its links and channels (16), including handlers still running after
	// their call timed out.
	MaxInFlightPerKey int
	// Lockout: after LockoutThreshold failed handshakes (5) a source is
	// refused for LockoutBase (30s), doubling per lockout up to LockoutMax
	// (15m). A failure that offered a key counts against that IP and key
	// only, so a removed peer retrying does not lock out others behind the
	// same NAT; the IP alone is locked after 4x the threshold of those, so
	// rotating keys does not dodge it.
	LockoutThreshold        int
	LockoutBase, LockoutMax time.Duration
	// OnConn, when set, is called for every authenticated connection.
	OnConn func(*Conn)
	Logger *slog.Logger

	perKey *keyLimiter      // shared with the Manager's dialers; nil makes one
	now    func() time.Time // tests
}

// Server is the embedded SSH listener. It authenticates pinned peers by key,
// accepts only mcp channels and answers only keepalives; everything else an
// SSH client can ask for is refused.
type Server struct {
	opts  ServerOptions
	cfg   *ssh.ServerConfig
	lock  *lockout // failures with no key offered, per IP
	keyed *lockout // failures with a key offered, per IP and key
	spray *lockout // failures with a key offered, per IP, at 4x the threshold
	log   *slog.Logger

	mu       sync.Mutex
	ln       net.Listener
	raw      map[net.Conn]struct{}
	conns    map[*Conn]struct{}
	preAuth  map[string]int // handshakes in progress per lockKey
	preAuthN int
	authN    int
	perKey   map[string]int // authenticated links per fingerprint
	closed   bool
	wg       sync.WaitGroup
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
	if opts.MaxConnsPerKey <= 0 {
		opts.MaxConnsPerKey = 4
	}
	if opts.MaxPreAuth <= 0 {
		opts.MaxPreAuth = 32
	}
	if opts.MaxPreAuthPerIP <= 0 {
		opts.MaxPreAuthPerIP = 2
	}
	if opts.PreAuthTimeout <= 0 {
		opts.PreAuthTimeout = 5 * time.Second
	}
	if opts.MaxInFlightPerKey <= 0 {
		opts.MaxInFlightPerKey = 16
	}
	if opts.perKey == nil {
		opts.perKey = newKeyLimiter(opts.MaxInFlightPerKey)
	}
	if opts.LockoutThreshold <= 0 {
		opts.LockoutThreshold = 5
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	s := &Server{
		opts:    opts,
		lock:    newLockout(opts.LockoutThreshold, opts.LockoutBase, opts.LockoutMax, opts.now),
		keyed:   newLockout(opts.LockoutThreshold, opts.LockoutBase, opts.LockoutMax, opts.now),
		spray:   newLockout(4*opts.LockoutThreshold, opts.LockoutBase, opts.LockoutMax, opts.now),
		log:     opts.Logger,
		raw:     map[net.Conn]struct{}{},
		conns:   map[*Conn]struct{}{},
		preAuth: map[string]int{},
		perKey:  map[string]int{},
	}
	s.cfg = &ssh.ServerConfig{
		Config:                  baseSSHConfig(),
		PublicKeyAuthAlgorithms: []string{ssh.KeyAlgoED25519},
		MaxAuthTries:            3,
		ServerVersion:           sshVersion,
		// No PasswordCallback, KeyboardInteractiveCallback or NoClientAuth:
		// public key is the only method offered. handle sets
		// PublicKeyCallback per connection.
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

// checkKey lets in only a plain ed25519 key (no certificate, no
// sk-ssh-ed25519) from the pinned store. The fingerprint travels with the
// name, so handle can insist the pin it looks up is still this key.
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
	return &ssh.Permissions{Extensions: map[string]string{extPeer: p.Name, extFingerprint: p.Fingerprint}}, nil
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
		if s.lock.locked(key) || s.spray.locked(key) || !s.admit(raw, key) {
			raw.Close()
			continue
		}
		go func() {
			defer s.wg.Done()
			defer s.untrack(raw)
			s.handle(raw, key)
		}()
	}
}

// admit registers raw as a handshake in progress, within MaxPreAuth and
// MaxPreAuthPerIP, and adds its goroutine to wg, under the lock Close takes,
// so Close never waits while a connection is still being added.
func (s *Server) admit(raw net.Conn, key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.preAuthN >= s.opts.MaxPreAuth || s.preAuth[key] >= s.opts.MaxPreAuthPerIP {
		return false
	}
	s.preAuthN++
	s.preAuth[key]++
	s.raw[raw] = struct{}{}
	s.wg.Add(1)
	return true
}

// authenticated moves a connection from the handshake count to the link
// counts, within MaxConns and MaxConnsPerKey; ok is false when there is no
// room. Every admit is followed by exactly one authenticated or preAuthDone.
func (s *Server) authenticated(key, fp string) (ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.endPreAuth(key)
	if s.authN >= s.opts.MaxConns || s.perKey[fp] >= s.opts.MaxConnsPerKey {
		return false
	}
	s.authN++
	s.perKey[fp]++
	return true
}

func (s *Server) preAuthDone(key string) {
	s.mu.Lock()
	s.endPreAuth(key)
	s.mu.Unlock()
}

func (s *Server) endPreAuth(key string) {
	s.preAuthN--
	if s.preAuth[key]--; s.preAuth[key] <= 0 {
		delete(s.preAuth, key)
	}
}

func (s *Server) linkDone(fp string) {
	s.mu.Lock()
	s.authN--
	if s.perKey[fp]--; s.perKey[fp] <= 0 {
		delete(s.perKey, fp)
	}
	s.mu.Unlock()
}

func (s *Server) untrack(raw net.Conn) {
	s.mu.Lock()
	delete(s.raw, raw)
	s.mu.Unlock()
	raw.Close()
}

// handle runs one connection: the handshake under PreAuthTimeout, then the
// link until it ends.
func (s *Server) handle(raw net.Conn, key string) {
	lim := s.opts.Limits
	// The callbacks run on this goroutine, inside NewServerConn.
	var (
		offered   ssh.PublicKey
		keyLocked bool
	)
	cfg := *s.cfg
	cfg.PublicKeyCallback = func(meta ssh.ConnMetadata, k ssh.PublicKey) (*ssh.Permissions, error) {
		offered = k
		if s.keyed.locked(key + " " + Fingerprint(k)) {
			keyLocked = true
			return nil, errNotPinned
		}
		return s.checkKey(meta, k)
	}
	timeout := s.opts.PreAuthTimeout
	if lim.HandshakeTimeout < timeout {
		timeout = lim.HandshakeTimeout
	}
	timer := time.AfterFunc(timeout, func() { raw.Close() })
	sc, chans, reqs, err := ssh.NewServerConn(&idleConn{Conn: raw, idle: lim.IdleTimeout}, &cfg)
	timer.Stop()
	if err != nil {
		s.preAuthDone(key)
		switch {
		case keyLocked:
			// Already locked out for this key; not a new strike.
		case offered != nil:
			s.keyed.fail(key + " " + Fingerprint(offered))
			s.spray.fail(key)
		default:
			s.lock.fail(key)
		}
		s.log.Debug("peer: handshake failed", "remote", key, "error", err)
		return
	}
	name := sc.Permissions.Extensions[extPeer]
	fp := sc.Permissions.Extensions[extFingerprint]
	p, ok, err := s.opts.Store.Get(name)
	if err != nil || !ok || p.Fingerprint != fp {
		s.preAuthDone(key)
		closeUnserved(sc, chans, reqs) // unpinned or re-paired between auth and now
		return
	}
	s.lock.ok(key)
	s.spray.ok(key)
	s.keyed.ok(key + " " + fp)
	if !s.authenticated(key, fp) {
		s.log.Warn("peer: connection limit reached; refusing a link", "peer", name, "remote", key)
		closeUnserved(sc, chans, reqs)
		return
	}
	defer s.linkDone(fp)
	caller := s.callerFor(p)
	c := newConn(sc, chans, reqs, connParams{
		caller: caller, handler: s.opts.Handler, self: s.opts.Name, ver: s.opts.Version,
		lim: lim, log: s.log, recheck: s.recheck, perKey: s.opts.perKey,
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

// callerFor is the Caller a pin gets: Name and Fingerprint always from the
// pin, the rest from opts.Caller.
func (s *Server) callerFor(p Pinned) Caller {
	c := Caller{Role: p.Role}
	if s.opts.Caller != nil {
		c = s.opts.Caller(p)
	}
	c.Name, c.Fingerprint = p.Name, p.Fingerprint
	return c
}

// recheck re-reads the pin behind a link before each call: a peer removed or
// re-paired since it connected is refused (and its link dropped).
func (s *Server) recheck(c Caller) (Caller, error) {
	p, ok, err := s.opts.Store.Get(c.Name)
	if err != nil {
		return Caller{}, err
	}
	if !ok || p.Fingerprint != c.Fingerprint {
		return Caller{}, ErrRevoked
	}
	return s.callerFor(p), nil
}

// closeUnserved closes an SSH connection nobody will serve, draining its
// channels so x/crypto's reader is never stuck delivering to them.
func closeUnserved(sc ssh.Conn, chans <-chan ssh.NewChannel, reqs <-chan *ssh.Request) {
	sc.Close()
	go ssh.DiscardRequests(reqs)
	go func() {
		for nc := range chans {
			_ = nc.Reject(ssh.Prohibited, "closing")
		}
	}()
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

// keyLimiter counts something per pinned key (calls in flight) against a cap.
type keyLimiter struct {
	mu  sync.Mutex
	n   map[string]int
	max int
}

func newKeyLimiter(max int) *keyLimiter { return &keyLimiter{n: map[string]int{}, max: max} }

func (k *keyLimiter) acquire(key string) bool {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.n[key] >= k.max {
		return false
	}
	k.n[key]++
	return true
}

func (k *keyLimiter) release(key string) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.n[key]--; k.n[key] <= 0 {
		delete(k.n, key)
	}
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
	Caller Caller
	// Recheck, when set, re-validates the peer before each of its calls
	// back over this link and returns its current Caller; an error refuses
	// the call and drops the link (see Manager).
	Recheck       func(Caller) (Caller, error)
	Name, Version string
	Limits        Limits
	// Keepalive turns on keepalive@openssh.com (the supervisor does).
	Keepalive   bool
	DialTimeout time.Duration // TCP connect (10s)
	Logger      *slog.Logger

	perKey *keyLimiter // the Manager's, shared with its Server
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
		} else if strings.Contains(err.Error(), "unable to authenticate") {
			err = fmt.Errorf("%w (%v)", ErrNotAccepted, err)
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
		lim: lim, log: o.Logger, keepalive: o.Keepalive, recheck: o.Recheck, perKey: o.perKey,
	}), nil
}

// pinnedHostKey accepts exactly the pinned key, on the first key exchange and
// on every re-key.
func pinnedHostKey(name string, pinned ssh.PublicKey) ssh.HostKeyCallback {
	return func(_ string, _ net.Addr, key ssh.PublicKey) error {
		if !sameKey(key, pinned) {
			return fmt.Errorf("%w for peer %q: got %s, pinned %s (re-pair if it really changed)",
				ErrHostKeyMismatch, name, Fingerprint(key), Fingerprint(pinned))
		}
		return nil
	}
}
