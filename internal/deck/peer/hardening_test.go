package peer

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sourcegraph/jsonrpc2"
	"golang.org/x/crypto/ssh"
)

// Tests for the security review fixes: revocation of live links, allow
// lists, limits that keep unauthenticated clients from starving pinned
// peers, pairing under load, handler timeouts and peer text on terminals.

func waitDone(t *testing.T, ch <-chan struct{}, d time.Duration, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(d):
		t.Fatalf("%s: not done within %s", what, d)
	}
}

func waitFor(t *testing.T, d time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestHostileCertAndSKKeysRefused(t *testing.T) {
	leakCheck(t)
	_, b, srv, _ := pairUp(t, ServerOptions{LockoutThreshold: 1000})
	addr := srv.Addr().String()

	// A certificate wrapping the pinned key itself, signed by some CA.
	cert := &ssh.Certificate{Key: b.id.PublicKey(), CertType: ssh.UserCert, ValidPrincipals: []string{"root"},
		ValidBefore: ssh.CertTimeInfinity}
	if err := cert.SignCert(rand.Reader, ed25519Signer(t)); err != nil {
		t.Fatal(err)
	}
	certSigner, err := ssh.NewCertSigner(cert, b.id.Signer)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := rawDial(t, addr, []ssh.AuthMethod{ssh.PublicKeys(certSigner)}, nil); err == nil {
		t.Fatal("certificate of the pinned key authenticated")
	}
	if _, err := srv.checkKey(nil, cert); err == nil {
		t.Fatal("checkKey accepted a certificate")
	}

	// sk-ssh-ed25519 (a security key) with the pinned key's bytes.
	edKey := b.id.PublicKey().(ssh.CryptoPublicKey).CryptoPublicKey().(ed25519.PublicKey)
	sk, err := ssh.ParsePublicKey(ssh.Marshal(struct {
		Name        string
		KeyBytes    []byte
		Application string
	}{ssh.KeyAlgoSKED25519, edKey, "ssh:"}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := srv.checkKey(nil, sk); err == nil {
		t.Fatal("checkKey accepted an sk-ssh-ed25519 key")
	}
	// Neither can be pinned (load would refuse the whole store after).
	store := OpenStore(t.TempDir())
	for _, k := range []ssh.PublicKey{cert, sk} {
		if err := store.Pin(NewPinned("x", "", "", k)); err == nil {
			t.Fatalf("pinned a %s key", k.Type())
		}
	}
	// The plain pinned key still gets in.
	if err := dialSoon(t, addr, []ssh.AuthMethod{ssh.PublicKeys(b.id.Signer)}); err != nil {
		t.Fatalf("pinned key refused: %v", err)
	}
}

// managedPair starts a listening Manager for a and a dialing one for b,
// pinned to each other, and waits for the link.
func managedPair(t *testing.T) (a, b *testPeer, ma, mb *Manager) {
	t.Helper()
	a, b = newTestPeer(t, "a"), newTestPeer(t, "b")
	var err error
	ma, err = NewManager(ManagerOptions{Dir: a.dir, Logger: quietLog, Peers: peersCfg("a", true, nil), SweepInterval: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	if err := ma.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(ma.Stop)
	a.pin(t, b, "")
	b.pin(t, a, ma.Addr())
	mb, err = NewManager(ManagerOptions{Dir: b.dir, Logger: quietLog, Peers: peersCfg("b", false, []remoteCfg{{"a", ma.Addr()}}),
		SweepInterval: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	if err := mb.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(mb.Stop)
	waitFor(t, 10*time.Second, "link up", func() bool {
		s := mb.Supervisor("a")
		return s != nil && s.Status().State == StateConnected && len(ma.srv.Conns()) == 1
	})
	return a, b, ma, mb
}

// TestRevokedPeerRefusedOnNextCall: removing a pin on the listening side
// takes effect on the next call over an open link, without a restart or a
// sweep, and the link is dropped.
func TestRevokedPeerRefusedOnNextCall(t *testing.T) {
	leakCheck(t)
	a, _, ma, mb := managedPair(t)
	in := ma.srv.Conns()[0]
	cl := mb.Supervisor("a").Client()
	ctx := ctxT(t)
	if res, err := cl.Execute(ctx, OpPeerStatus, nil); err != nil || res.IsError {
		t.Fatalf("before removal: %+v %v", res, err)
	}
	if _, err := a.store.Remove("b"); err != nil {
		t.Fatal(err)
	}
	res, err := cl.Execute(ctx, OpPeerStatus, nil)
	if err == nil && (!res.IsError || res.Text() != msgRevoked) {
		t.Fatalf("call from a peer removed on the listening side ran: %+v", res)
	}
	waitDone(t, in.Done(), 5*time.Second, "inbound link of a removed peer")
}

// TestRevokedPeerRefusedOnNextCallDialer: the same on the dialing side: the
// listener calling back over the link is refused once the dialer removed it.
func TestRevokedPeerRefusedOnNextCallDialer(t *testing.T) {
	leakCheck(t)
	_, b, ma, _ := managedPair(t)
	in := ma.srv.Conns()[0]
	ctx := ctxT(t)
	rcl := openInit(t, in)
	if res, err := rcl.Execute(ctx, OpPeerStatus, nil); err != nil || res.IsError {
		t.Fatalf("before removal: %+v %v", res, err)
	}
	if _, err := b.store.Remove("a"); err != nil {
		t.Fatal(err)
	}
	res, err := rcl.Execute(ctx, OpPeerStatus, nil)
	if err == nil && (!res.IsError || res.Text() != msgRevoked) {
		t.Fatalf("call from a peer removed on the dialing side ran: %+v", res)
	}
	waitDone(t, in.Done(), 5*time.Second, "link after the dialer removed its peer")
}

// TestRevokedPeerSweep: after Store.Remove, the sweep closes a live inbound
// link that makes no calls, and stops the supervisor dialing a removed peer.
func TestRevokedPeerSweep(t *testing.T) {
	leakCheck(t)
	a, b, ma, mb := managedPair(t)
	in := ma.srv.Conns()[0]

	if _, err := a.store.Remove("b"); err != nil {
		t.Fatal(err)
	}
	ma.sweep()
	waitDone(t, in.Done(), 5*time.Second, "inbound link after sweep")

	if _, err := b.store.Remove("a"); err != nil {
		t.Fatal(err)
	}
	mb.sweep()
	if mb.Supervisor("a") != nil || len(mb.Statuses()) != 0 {
		t.Fatal("supervisor still running for a removed peer")
	}

	// A re-paired key (same name, new key) is treated as removed too.
	other := newTestPeer(t, "a")
	if err := b.store.Replace(NewPinned("a", ma.Addr(), "", other.id.PublicKey())); err != nil {
		t.Fatal(err)
	}
	mb.sweep() // starts a supervisor for the new pin: it can never connect
	sup := mb.Supervisor("a")
	if sup == nil {
		t.Fatal("no supervisor for the newly pinned dial peer")
	}
	waitFor(t, 10*time.Second, "permanent failure", func() bool {
		st := sup.Status()
		return st.State == StateBackoff && st.Permanent
	})
}

// TestHostileServerIntoDialer: a server (here one that holds the pinned host
// key) opening session/direct-tcpip/... channels or sending global requests
// into the dialing Deck is refused; only mcp channels are served, under the
// default allow list.
func TestHostileServerIntoDialer(t *testing.T) {
	leakCheck(t)
	a, b := newTestPeer(t, "a"), newTestPeer(t, "b")
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	type served struct {
		sc  ssh.Conn
		err error
	}
	got := make(chan served, 1)
	go func() {
		raw, err := ln.Accept()
		if err != nil {
			got <- served{err: err}
			return
		}
		cfg := &ssh.ServerConfig{PublicKeyCallback: func(ssh.ConnMetadata, ssh.PublicKey) (*ssh.Permissions, error) { return nil, nil }}
		cfg.AddHostKey(a.id.Signer)
		sc, chans, reqs, err := ssh.NewServerConn(raw, cfg)
		if err != nil {
			raw.Close()
			got <- served{err: err}
			return
		}
		go ssh.DiscardRequests(reqs)
		go func() {
			for nc := range chans {
				_ = nc.Reject(ssh.Prohibited, "test server")
			}
		}()
		got <- served{sc: sc}
	}()
	b.dial(t, NewPinned("a", ln.Addr().String(), "", a.id.PublicKey()), nil)
	s := <-got
	if s.err != nil {
		t.Fatal(s.err)
	}
	sc := s.sc
	defer sc.Close()

	for _, typ := range []string{"session", "direct-tcpip", "forwarded-tcpip", "x11", "auth-agent@openssh.com",
		"direct-streamlocal@openssh.com", "deck-pair", "MCP", ""} {
		ch, _, err := sc.OpenChannel(typ, []byte("payload"))
		var oe *ssh.OpenChannelError
		if err == nil || !errors.As(err, &oe) || oe.Reason != ssh.UnknownChannelType {
			if ch != nil {
				ch.Close()
			}
			t.Fatalf("dialer accepted channel %q: %v", typ, err)
		}
	}
	for _, typ := range []string{"tcpip-forward", "streamlocal-forward@openssh.com", "hostkeys-00@openssh.com", "x"} {
		if ok, _, err := sc.SendRequest(typ, true, []byte{0, 0, 0, 0}); err != nil || ok {
			t.Fatalf("dialer answered global request %q: ok=%v err=%v", typ, ok, err)
		}
	}
	ch, reqs, err := sc.OpenChannel(ChannelType, nil)
	if err != nil {
		t.Fatal(err)
	}
	go ssh.DiscardRequests(reqs)
	defer ch.Close()
	if ok, err := ch.SendRequest("exec", true, ssh.Marshal(struct{ S string }{"id"})); err != nil || ok {
		t.Fatalf("dialer answered exec: ok=%v err=%v", ok, err)
	}
	r := bufio.NewReader(ch)
	sendLine(t, ch, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`)
	if resp := readResp(t, r); resp.Error != nil {
		t.Fatalf("initialize: %+v", resp)
	}
	sendLine(t, ch, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"execute","arguments":{"op":"git.status"}}}`)
	if resp := readResp(t, r); resp.Error != nil || !strings.Contains(string(resp.Result), "not allowed") {
		t.Fatalf("op outside the default allow list: %+v", resp)
	}
}

// TestIdleSocketsDoNotStarvePinnedPeer: unauthenticated sockets have their
// own slots, so 16 idle ones (the old shared limit) do not keep a pinned
// peer out.
func TestIdleSocketsDoNotStarvePinnedPeer(t *testing.T) {
	leakCheck(t)
	// A high per-IP cap stands in for 16 different source addresses.
	_, b, srv, pinA := pairUp(t, ServerOptions{MaxPreAuthPerIP: 64, LockoutThreshold: 1000})
	addr := srv.Addr().String()
	for i := 0; i < 16; i++ {
		c, err := net.Dial("tcp", addr)
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
	}
	time.Sleep(50 * time.Millisecond) // let the server accept them
	cl := openInit(t, b.dial(t, pinA, nil))
	if _, err := cl.Ping(ctxT(t)); err != nil {
		t.Fatalf("pinned peer starved by idle sockets: %v", err)
	}
}

// TestPerKeyLimits: links and calls in flight are capped per pinned key.
func TestPerKeyLimits(t *testing.T) {
	leakCheck(t)
	h := &recHandler{StatusHandler: StatusHandler{Name: "a"}, block: make(chan struct{})}
	defer close(h.block)
	_, b, _, pinA := pairUp(t, ServerOptions{Handler: h, MaxConnsPerKey: 2, MaxInFlightPerKey: 1,
		Caller: allowOps(OpPeerStatus, "block")})
	c1 := b.dial(t, pinA, nil)
	c2 := b.dial(t, pinA, nil)
	// The third link of the same key is closed right after auth (Dial
	// itself may or may not see it finish first).
	if c3, err := Dial(ctxT(t), DialOptions{Identity: b.id, Peer: pinA, Logger: quietLog}); err == nil {
		defer c3.Close()
		waitDone(t, c3.Done(), 5*time.Second, "link over the per-key cap")
	}

	ctx := ctxT(t)
	cl1, cl2 := openInit(t, c1), openInit(t, c2)
	go func() { _, _ = cl1.Execute(ctx, "block", nil) }()
	waitFor(t, 5*time.Second, "blocking call", func() bool { return h.calls() >= 1 })
	// One call in flight for the key: a call over its other link is busy.
	if _, err := cl2.Execute(ctx, OpPeerStatus, nil); err == nil || !strings.Contains(err.Error(), "too many calls") {
		t.Fatalf("second call of the key not refused: %v", err)
	}
}

// otherLoopback dials from another loopback address when the system has one
// (most do; macOS only has 127.0.0.1 by default).
func otherLoopback(t *testing.T, addr string) (net.Conn, bool) {
	t.Helper()
	d := net.Dialer{LocalAddr: &net.TCPAddr{IP: net.ParseIP("127.0.0.2")}, Timeout: time.Second}
	c, err := d.Dial("tcp", addr)
	if err != nil {
		return nil, false
	}
	return c, true
}

// TestPairListenerNotHeldByIdleConnections: idle connections cannot block
// the real dialer (they used to hold the one-at-a-time listener for 30s) and
// use no attempts.
func TestPairListenerNotHeldByIdleConnections(t *testing.T) {
	leakCheck(t)
	fastKDF(t)
	l, d := newTestPeer(t, "vps"), newTestPeer(t, "pc")
	code, _ := NewPairCode()
	addr, out := servePair(t, l, code, PairOptions{MaxAttempts: 1})

	// One idle connection from the dialer's own IP (the per-IP cap is 2),
	// and more from another address when there is one.
	idle, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer idle.Close()
	for i := 0; i < pairMaxConns; i++ {
		c, ok := otherLoopback(t, addr)
		if !ok {
			break
		}
		defer c.Close()
	}
	time.Sleep(50 * time.Millisecond)
	start := time.Now()
	if _, err := DialPair(ctxT(t), addr, PairOptions{Identity: d.id, Name: "pc", Code: code}); err != nil {
		t.Fatalf("pairing next to idle connections: %v", err)
	}
	if el := time.Since(start); el > 5*time.Second {
		t.Fatalf("pairing waited %s behind idle connections", el)
	}
	if o := waitPair(t, out); o.err != nil {
		t.Fatal(o.err)
	}
	if !waitClosed(idle, 2*time.Second) {
		t.Fatal("idle pairing connection not closed")
	}
}

func TestPairIdleConnectionCutAtHelloTimeout(t *testing.T) {
	leakCheck(t)
	fastKDF(t)
	// Long enough for the real dialer's handshake and KDF under -race.
	old := pairHelloTimeout
	pairHelloTimeout = time.Second
	t.Cleanup(func() { pairHelloTimeout = old })
	l, d := newTestPeer(t, "vps"), newTestPeer(t, "pc")
	code, _ := NewPairCode()
	addr, out := servePair(t, l, code, PairOptions{MaxAttempts: 1})
	// Fill this IP's two slots with silent connections: they are cut at the
	// hello timeout and the real dialer then gets in.
	for i := 0; i < pairMaxPerIP; i++ {
		c, err := net.Dial("tcp", addr)
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		if !waitClosed(c, 3*time.Second) {
			t.Fatal("silent pairing connection kept past the hello timeout")
		}
	}
	if _, err := DialPair(ctxT(t), addr, PairOptions{Identity: d.id, Name: "pc", Code: code}); err != nil {
		t.Fatalf("pairing after silent connections: %v", err)
	}
	if o := waitPair(t, out); o.err != nil {
		t.Fatal(o.err)
	}
}

// TestPinRefusesReplaceWithoutFlag: pairing a different key under a name
// already pinned is refused unless --replace, even with --yes.
func TestPinRefusesReplaceWithoutFlag(t *testing.T) {
	var out bytes.Buffer
	c := CLI{Dir: t.TempDir(), Out: &out}
	k1, k2 := newKey(t), newKey(t)
	if err := OpenStore(c.Dir).Pin(NewPinned("vps", "", "", k1)); err != nil {
		t.Fatal(err)
	}
	res := PairResult{PeerName: "vps", PeerKey: k2, PeerFingerprint: Fingerprint(k2), OwnFingerprint: "SHA256:me", RemoteAddr: "x"}
	if err := c.pin("vps", "", "", res, true, false); err == nil || !strings.Contains(err.Error(), "--replace") {
		t.Fatalf("replace without --replace: %v", err)
	}
	if p, _, _ := OpenStore(c.Dir).Get("vps"); p.Fingerprint != Fingerprint(k1) {
		t.Fatal("key replaced without --replace")
	}
	// The same key again is fine (e.g. a new address).
	same := PairResult{PeerName: "vps", PeerKey: k1, PeerFingerprint: Fingerprint(k1), RemoteAddr: "x"}
	if err := c.pin("vps", "vps.example.com:7722", "", same, true, false); err != nil {
		t.Fatalf("same key again: %v", err)
	}
	if err := c.pin("vps", "", "", res, true, true); err != nil {
		t.Fatalf("with --replace: %v", err)
	}
	if p, _, _ := OpenStore(c.Dir).Get("vps"); p.Fingerprint != Fingerprint(k2) {
		t.Fatal("--replace did not replace the key")
	}
}

// switchSigner is a host key that can change between key exchanges.
type switchSigner struct {
	mu  sync.Mutex
	cur ssh.Signer
}

func (s *switchSigner) set(n ssh.Signer) {
	s.mu.Lock()
	s.cur = n
	s.mu.Unlock()
}

func (s *switchSigner) PublicKey() ssh.PublicKey {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cur.PublicKey()
}

func (s *switchSigner) Sign(r io.Reader, data []byte) (*ssh.Signature, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cur.Sign(r, data)
}

// TestPairHostKeyIsPinnedToTheFirstKey: the host key callback runs again on
// every re-key; a second, different key must end the connection, and the same
// key must keep passing.
func TestPairHostKeyIsPinnedToTheFirstKey(t *testing.T) {
	var f firstHostKey
	k1 := ed25519Signer(t).PublicKey()
	k2 := ed25519Signer(t).PublicKey()
	if err := f.check(k1); err != nil {
		t.Fatalf("first key: %v", err)
	}
	if err := f.check(k1); err != nil {
		t.Fatalf("same key again: %v", err)
	}
	if err := f.check(k2); !errors.Is(err, ErrHostKeyMismatch) {
		t.Fatalf("changed key: %v, want ErrHostKeyMismatch", err)
	}
	if got := f.key(); got == nil || !sameKey(got, k1) {
		t.Fatal("the first key was not kept")
	}
}

// opRec records the ops that reach it and offers several in search.
type opRec struct {
	mu  sync.Mutex
	ops []string
}

func (h *opRec) Search(context.Context, Caller, string) ([]OpInfo, error) {
	return []OpInfo{{OpPeerStatus, "{}", "status"}, {"console.read", "{}", "feed"}, {"git.status", "{}", "git"}}, nil
}

func (h *opRec) Execute(_ context.Context, _ Caller, op string, _ json.RawMessage) (string, error) {
	h.mu.Lock()
	h.ops = append(h.ops, op)
	h.mu.Unlock()
	return "ran " + op, nil
}

// TestAllowInboundOpVariants: only an exact allowed op reaches the handler,
// whatever shape the request takes, and search lists allowed ops only.
func TestAllowInboundOpVariants(t *testing.T) {
	leakCheck(t)
	h := &opRec{}
	ch, r := mcpChannel(t, ServerOptions{Handler: h, Caller: allowOps(OpPeerStatus)})
	sendLine(t, ch, `{"jsonrpc":"2.0","id":0,"method":"initialize","params":{}}`)
	if resp := readResp(t, r); resp.Error != nil {
		t.Fatalf("initialize: %+v", resp)
	}
	for i, args := range []string{
		`{"op":"Peer.Status"}`,
		`{"op":"PEER.STATUS"}`,
		`{"op":"peer.status\u0000"}`,
		// JSON escapes, decoded by the server: zero-width space, one dot
		// leader, Cyrillic er, fullwidth full stop.
		"{\"op\":\"peer.status\\u200b\"}",
		"{\"op\":\"peer\\u2024status\"}",
		"{\"op\":\"\\u0440eer.status\"}",
		"{\"op\":\"peer\\uff0estatus\"}",
		`{"op":"console.read","since":0}`,
		`{"op":"console.read","args":{"op":"peer.status"}}`,
		`{"op":"peer.status","op":"console.read"}`,
		`{"op":["peer.status"]}`,
		`{"op":{"op":"peer.status"}}`,
		`{"args":{"op":"peer.status"}}`,
		` {"op":"peer.status"} `,                   // allowed: the same op, spaces around the JSON
		`{"op":" peer.status "}`,                   // allowed: execute trims the op
		`{"op":"console.read","op":"peer.status"}`, // allowed: the last key wins, for the check and the handler alike
	} {
		sendLine(t, ch, fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":"tools/call","params":{"name":"execute","arguments":%s}}`, i+1, args))
		resp := readResp(t, r)
		if resp.Error != nil {
			t.Fatalf("%s: rpc error %+v", args, resp.Error)
		}
	}
	h.mu.Lock()
	ops := append([]string(nil), h.ops...)
	h.mu.Unlock()
	if len(ops) != 3 {
		t.Fatalf("handler ran %d ops, want the 3 exact peer.status: %q", len(ops), ops)
	}
	for _, op := range ops {
		if op != OpPeerStatus {
			t.Fatalf("handler ran %q", op)
		}
	}
	// Search drops the ops the caller may not run.
	sendLine(t, ch, `{"jsonrpc":"2.0","id":100,"method":"tools/call","params":{"name":"search","arguments":{"query":""}}}`)
	resp := readResp(t, r)
	if resp.Error != nil || !strings.Contains(string(resp.Result), OpPeerStatus) ||
		strings.Contains(string(resp.Result), "console.read") || strings.Contains(string(resp.Result), "git.status") {
		t.Fatalf("search: %+v %s", resp.Error, resp.Result)
	}
}

func TestContractorOpsNeedRole(t *testing.T) {
	allow := []string{OpPeerStatus, "contract.propose"}
	if (Caller{Role: "observer", AllowInbound: allow}).Allowed("contract.propose") {
		t.Fatal("observer may run a contractor op")
	}
	if (Caller{AllowInbound: allow}).Allowed("contract.propose") {
		t.Fatal("empty role may run a contractor op")
	}
	if !(Caller{Role: "contractor", AllowInbound: allow}).Allowed("contract.propose") {
		t.Fatal("contractor refused an allowed contractor op")
	}
	if !(Caller{Role: "contractor"}).Allowed("contract.propose") {
		t.Fatal("the default allow list refused a contractor its op")
	}
	if (Caller{Role: "contractor", AllowInbound: []string{OpPeerStatus}}).Allowed("contract.propose") {
		t.Fatal("contractor op allowed by an allow_inbound that leaves it out")
	}
	if !(Caller{}).Allowed(OpPeerStatus) || (Caller{}).Allowed("console.read") || (Caller{}).Allowed("") {
		t.Fatal("default allow list")
	}
}

// stuckHandler ignores ctx and returns only when released.
type stuckHandler struct {
	StatusHandler
	release chan struct{}
	entered chan struct{}
	once    sync.Once
}

func (h *stuckHandler) Execute(ctx context.Context, from Caller, op string, args json.RawMessage) (string, error) {
	if op != "stuck" {
		return h.StatusHandler.Execute(ctx, from, op, args)
	}
	h.once.Do(func() { close(h.entered) })
	<-h.release
	return "", errors.New("/home/me/secret/path: too late")
}

// TestHandlerIgnoringCtxStillTimesOut: the call is answered at the timeout,
// the stuck handler keeps its slot, and Conn.Close and Server.Close return.
func TestHandlerIgnoringCtxStillTimesOut(t *testing.T) {
	leakCheck(t)
	h := &stuckHandler{StatusHandler: StatusHandler{Name: "a"}, release: make(chan struct{}), entered: make(chan struct{})}
	defer close(h.release) // before leakCheck's cleanup
	_, b, srv, pinA := pairUp(t, ServerOptions{Handler: h, Caller: allowOps(OpPeerStatus, "stuck"),
		Limits: Limits{CallTimeout: 100 * time.Millisecond, MaxInFlight: 1}})
	conn := b.dial(t, pinA, nil)
	cl := openInit(t, conn)
	ctx := ctxT(t)
	res, err := cl.Execute(ctx, "stuck", nil)
	if err != nil || !res.IsError || res.Text() != msgTimeout {
		t.Fatalf("stuck call: %+v %v", res, err)
	}
	// Still running, it holds the channel's only slot.
	if _, err := cl.Execute(ctx, OpPeerStatus, nil); err == nil || !strings.Contains(err.Error(), "too many calls") {
		t.Fatalf("slot of a stuck handler was freed: %v", err)
	}
	done := make(chan struct{})
	go func() {
		conn.Close()
		srv.Close()
		close(done)
	}()
	waitDone(t, done, 3*time.Second, "Conn.Close and Server.Close with a stuck handler")
}

// TestHandlerErrorsNotSentVerbatim: a handler's error text stays local.
func TestHandlerErrorsNotSentVerbatim(t *testing.T) {
	leakCheck(t)
	h := &stuckHandler{StatusHandler: StatusHandler{Name: "a"}, release: make(chan struct{}), entered: make(chan struct{})}
	close(h.release) // returns its error at once
	_, b, _, pinA := pairUp(t, ServerOptions{Handler: h, Caller: allowOps("stuck", "nope")})
	cl := openInit(t, b.dial(t, pinA, nil))
	res, err := cl.Execute(ctxT(t), "stuck", nil)
	if err != nil || !res.IsError || res.Text() != msgFailed || strings.Contains(res.Text(), "secret") {
		t.Fatalf("handler error: %+v %v", res, err)
	}
	res, err = cl.Execute(ctxT(t), "nope", nil)
	if err != nil || res.Text() != msgUnknownOp {
		t.Fatalf("unknown op: %+v %v", res, err)
	}
}

func TestDuplicateRequestIDRefused(t *testing.T) {
	leakCheck(t)
	h := &recHandler{StatusHandler: StatusHandler{Name: "a"}, block: make(chan struct{})}
	ch, r := mcpChannel(t, ServerOptions{Handler: h, Caller: allowOps("block")})
	sendLine(t, ch, `{"jsonrpc":"2.0","id":0,"method":"initialize","params":{}}`)
	if resp := readResp(t, r); resp.Error != nil {
		t.Fatalf("initialize: %+v", resp)
	}
	call := `{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"execute","arguments":{"op":"block"}}}`
	sendLine(t, ch, call)
	waitFor(t, 5*time.Second, "first call", func() bool { return h.calls() == 1 })
	sendLine(t, ch, call)
	resp := readResp(t, r)
	if string(resp.ID) != "7" || resp.Error == nil || resp.Error.Code != codeInvalidRequest || !strings.Contains(resp.Error.Message, "duplicate") {
		t.Fatalf("duplicate id: %+v", resp)
	}
	close(h.block)
	resp = readResp(t, r)
	if string(resp.ID) != "7" || resp.Error != nil || !strings.Contains(string(resp.Result), "unblocked") {
		t.Fatalf("first call: %+v", resp)
	}
	if h.calls() != 1 {
		t.Fatalf("handler ran %d times", h.calls())
	}
	// Once answered, the id may be used again.
	sendLine(t, ch, strings.Replace(call, `"block"`, `"peer.status"`, 1))
	if resp := readResp(t, r); resp.Error != nil {
		t.Fatalf("reused id after its answer: %+v", resp)
	}
}

// escHandler answers peer.status with terminal escapes.
type escHandler struct{ StatusHandler }

func (escHandler) Execute(context.Context, Caller, string, json.RawMessage) (string, error) {
	// \xc2\x9b is U+009B (CSI), \xe2\x80\xae U+202E (right-to-left override).
	return "\x1b[2J\x1b]0;owned\x07status\xc2\x9b31m\xe2\x80\xaeok\r\nline", nil
}

func TestPingStripsEscapes(t *testing.T) {
	leakCheck(t)
	a, b := newTestPeer(t, "vps"), newTestPeer(t, "pc")
	srv := a.listen(t, ServerOptions{Handler: &escHandler{}})
	a.pin(t, b, "")
	b.pin(t, a, srv.Addr().String())
	var out bytes.Buffer
	c := CLI{Dir: b.dir, Out: &out}
	if err := c.Ping(ctxT(t), "vps"); err != nil {
		t.Fatal(err)
	}
	// "\xe2\x80\xae" is U+202E, right-to-left override.
	for _, bad := range []string{"\x1b", "\x07", "\xc2\x9b", "\xe2\x80\xae", "\r"} {
		if strings.Contains(out.String(), bad) {
			t.Fatalf("ping output has %q: %q", bad, out.String())
		}
	}
	if !strings.Contains(out.String(), "?[2J?]0;owned?status?31m?ok? line") {
		t.Fatalf("ping output: %q", out.String())
	}
}

func TestSanitize(t *testing.T) {
	for in, want := range map[string]string{
		"plain text":              "plain text",
		"a\x1b[31mb":              "a?[31mb",
		"bell\x07":                "bell?",
		"c1\u009bx":               "c1?x",
		"bidi\xe2\x80\xaegnp.exe": "bidi?gnp.exe", // U+202E
		"zero\xe2\x80\x8bwidth":   "zero?width",   // U+200B
		"tab\tnl\ncr\r":           "tab nl cr?",
		"del\x7f":                 "del?",
		"bad utf8 \xff":           "bad utf8 \xef\xbf\xbd", // U+FFFD
		"unicode ünïcödé 日本":      "unicode ünïcödé 日本",
	} {
		if got := Sanitize(in); got != want {
			t.Errorf("Sanitize(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSupervisorPermanentErrorWaitsMax(t *testing.T) {
	leakCheck(t)
	fired := make(chan time.Duration, 100)
	sup := NewSupervisor(SupervisorOptions{
		Peer: "x",
		Dial: func(context.Context) (*Conn, error) {
			return nil, fmt.Errorf("peer: connect x: %w", ErrHostKeyMismatch)
		},
		After: func(d time.Duration) <-chan time.Time {
			select {
			case fired <- d:
			default:
			}
			c := make(chan time.Time, 1)
			c <- time.Now()
			return c
		},
		Jitter: func() float64 { return 0.5 },
	})
	sup.Start(context.Background())
	for i := 0; i < 3; i++ {
		select {
		case d := <-fired:
			if d != 60*time.Second {
				t.Fatalf("permanent failure retried after %s, want the 60s maximum", d)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("no retry")
		}
	}
	waitFor(t, 5*time.Second, "permanent status", func() bool { return sup.Status().Permanent })
	sup.Close()
}

// TestClientLateReplyAfterClose: a reply that arrives after the client was
// closed (or a repeated one) is ignored. With jsonrpc2's own Conn this sent on
// a closed channel and crashed the process.
func TestClientLateReplyAfterClose(t *testing.T) {
	cc, sc := net.Pipe()
	defer sc.Close()
	c := newClient(context.Background(), cc, 0, slog.New(slog.NewTextHandler(io.Discard, nil)))
	errc := make(chan error, 1)
	go func() { errc <- c.Call(ctxT(t), "ping", nil, nil) }()
	line, err := bufio.NewReader(sc).ReadBytes('\n')
	if err != nil {
		t.Fatal(err)
	}
	var req struct {
		ID uint64 `json:"id"`
	}
	if err := json.Unmarshal(line, &req); err != nil {
		t.Fatal(err)
	}
	_ = c.Close()
	if err := <-errc; !errors.Is(err, jsonrpc2.ErrClosed) {
		t.Fatalf("pending call: %v, want ErrClosed", err)
	}
	reply := fmt.Sprintf("{\"jsonrpc\":\"2.0\",\"id\":%d,\"result\":{}}\n", req.ID)
	for i := 0; i < 3; i++ {
		_, _ = sc.Write([]byte(reply))
	}
	select {
	case <-c.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("client did not finish")
	}
}
