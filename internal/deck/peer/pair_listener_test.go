package peer

import (
	"context"
	"errors"
	"net"
	"runtime"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/dev-dami/deck/internal/deck/config"
)

// Pairing served by the link's own listener (Server.ArmPair, Manager.ArmPair,
// Service.StartPairListen).

// pairLogin logs in to addr as PairUser with signer, as a hostile client
// would, without the pairing exchange.
func pairLogin(t *testing.T, addr string, signer ssh.Signer) (ssh.Conn, error) {
	t.Helper()
	raw, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		return nil, err
	}
	_ = raw.SetDeadline(time.Now().Add(10 * time.Second))
	sc, chans, reqs, err := ssh.NewClientConn(raw, addr, &ssh.ClientConfig{
		User: PairUser, Auth: []ssh.AuthMethod{ssh.PublicKeys(signer)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), Timeout: 5 * time.Second,
	})
	if err != nil {
		raw.Close()
		return nil, err
	}
	go ssh.DiscardRequests(reqs)
	go func() {
		for nc := range chans {
			_ = nc.Reject(ssh.Prohibited, "test client")
		}
	}()
	t.Cleanup(func() { sc.Close() })
	return sc, nil
}

// waitOutcome waits for a PairWait's outcome.
func waitOutcome(t *testing.T, w *PairWait) PairOutcome {
	t.Helper()
	select {
	case o := <-w.Done:
		return o
	case <-time.After(10 * time.Second):
		t.Fatal("pairing did not end")
	}
	return PairOutcome{}
}

func TestPairOnMainListener(t *testing.T) {
	leakCheck(t)
	fastKDF(t)
	a, b, srv, _ := pairUp(t, ServerOptions{})
	pc := newTestPeer(t, "pc")
	before, _ := a.store.List()

	w, err := srv.ArmPair(context.Background(), PairOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if w.Addr != srv.Addr().String() || !w.Shared || w.Fingerprint != a.id.Fingerprint() {
		t.Fatalf("armed wait: %+v", w)
	}
	res, err := DialPair(ctxT(t), srv.Addr().String(), PairOptions{Identity: pc.id, Name: "pc", Code: w.Code})
	if err != nil {
		t.Fatal(err)
	}
	o := waitOutcome(t, w)
	if o.Err != nil {
		t.Fatal(o.Err)
	}
	if res.PeerFingerprint != a.id.Fingerprint() || o.Result.PeerFingerprint != pc.id.Fingerprint() || o.Result.PeerName != "pc" {
		t.Fatalf("fingerprints: dialer saw %s, listener saw %s", res.PeerFingerprint, o.Result.PeerFingerprint)
	}
	// Pairing pins nothing by itself.
	after, _ := a.store.List()
	if len(after) != len(before) {
		t.Fatalf("pairing changed the store: %v -> %v", before, after)
	}
	// Single use: the code is disarmed once Done has the outcome.
	if _, err := DialPair(ctxT(t), srv.Addr().String(), PairOptions{Identity: pc.id, Name: "pc", Code: w.Code}); !errors.Is(err, ErrPairNotWaiting) {
		t.Fatalf("second use of the code: %v, want ErrPairNotWaiting", err)
	}
	// The pinned peer was never affected.
	pin, _, _ := b.store.Get("a")
	c := b.dial(t, pin, nil)
	c.Close()
}

func TestPairUserRefusedWhenNotArmed(t *testing.T) {
	leakCheck(t)
	fastKDF(t)
	_, _, srv, _ := pairUp(t, ServerOptions{LockoutThreshold: 2, LockoutBase: time.Minute})
	addr := srv.Addr().String()
	stranger := ed25519Signer(t)
	code, _ := NewPairCode()
	pc := newTestPeer(t, "pc")
	if _, err := DialPair(ctxT(t), addr, PairOptions{Identity: pc.id, Name: "pc", Code: code}); !errors.Is(err, ErrPairNotWaiting) {
		t.Fatalf("pairing with nothing armed: %v, want ErrPairNotWaiting", err)
	}
	// A refused pairing login is an auth failure like any other.
	for i := 0; i < 10 && !srv.keyed.locked("127.0.0.1 "+Fingerprint(stranger.PublicKey())); i++ {
		if _, err := pairLogin(t, addr, stranger); err == nil {
			t.Fatal("deck-pair let in with nothing armed")
		}
		time.Sleep(10 * time.Millisecond)
	}
	waitLocked(t, srv.keyed, "127.0.0.1 "+Fingerprint(stranger.PublicKey()))
}

func TestPairConnectionGetsOnlyThePairChannel(t *testing.T) {
	leakCheck(t)
	_, b, srv, _ := pairUp(t, ServerOptions{LockoutThreshold: 1000})
	w, err := srv.ArmPair(context.Background(), PairOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		w.Cancel()
		waitOutcome(t, w)
	}()
	// An unpinned key, and even a pinned one, logged in as PairUser gets
	// no mcp channel and nothing else but the pairing channel.
	for name, signer := range map[string]ssh.Signer{"unpinned": ed25519Signer(t), "pinned": b.id.Signer} {
		sc, err := pairLogin(t, srv.Addr().String(), signer)
		if err != nil {
			t.Fatalf("%s: pairing login refused while armed: %v", name, err)
		}
		for _, typ := range []string{ChannelType, "session", "direct-tcpip", "MCP"} {
			if ch, _, err := sc.OpenChannel(typ, nil); err == nil {
				ch.Close()
				t.Fatalf("%s: pairing connection opened a %q channel", name, typ)
			}
		}
		if ok, _, _ := sc.SendRequest("tcpip-forward", true, nil); ok {
			t.Fatalf("%s: pairing connection got a global request", name)
		}
		if ok, _, err := sc.SendRequest(keepaliveRequest, true, nil); err != nil || !ok {
			t.Fatalf("%s: keepalive refused: %v", name, err)
		}
		sc.Close()
	}
	// None of that was an attempt at the code: it is still armed.
	if srv.pairs.get() == nil {
		t.Fatal("junk on pairing connections ended the code")
	}
}

func TestPinnedPeersConnectWhileArmed(t *testing.T) {
	leakCheck(t)
	_, b, srv, pinA := pairUp(t, ServerOptions{})
	w, err := srv.ArmPair(context.Background(), PairOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		w.Cancel()
		waitOutcome(t, w)
	}()
	c := b.dial(t, pinA, nil)
	cl, err := c.OpenMCP(ctxT(t))
	if err != nil {
		t.Fatalf("pinned peer's mcp channel while a code is armed: %v", err)
	}
	cl.Close()
	// An unpinned key under the normal user is still refused.
	if _, _, _, err := rawDial(t, srv.Addr().String(), []ssh.AuthMethod{ssh.PublicKeys(ed25519Signer(t))}, nil); err == nil {
		t.Fatal("an unpinned key got in while a code is armed")
	}
}

func TestPairExpiryDisarms(t *testing.T) {
	leakCheck(t)
	fastKDF(t)
	_, _, srv, _ := pairUp(t, ServerOptions{})
	w, err := srv.ArmPair(context.Background(), PairOptions{Expiry: 300 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	if o := waitOutcome(t, w); !errors.Is(o.Err, ErrPairExpired) {
		t.Fatalf("expired code ended with %v", o.Err)
	}
	if srv.pairs.get() != nil {
		t.Fatal("expired code still armed")
	}
	pc := newTestPeer(t, "pc")
	if _, err := DialPair(ctxT(t), srv.Addr().String(), PairOptions{Identity: pc.id, Name: "pc", Code: w.Code}); !errors.Is(err, ErrPairNotWaiting) {
		t.Fatalf("expired code: %v, want ErrPairNotWaiting", err)
	}
}

func TestPairWrongCodeOnMainListenerChangesNothing(t *testing.T) {
	leakCheck(t)
	fastKDF(t)
	a, _, srv, _ := pairUp(t, ServerOptions{})
	before, _ := a.store.List()
	w, err := srv.ArmPair(context.Background(), PairOptions{MaxAttempts: 2})
	if err != nil {
		t.Fatal(err)
	}
	pc := newTestPeer(t, "pc")
	wrong, _ := NewPairCode()
	for wrong == w.Code {
		wrong, _ = NewPairCode()
	}
	if _, err := DialPair(ctxT(t), srv.Addr().String(), PairOptions{Identity: pc.id, Name: "pc", Code: wrong}); !errors.Is(err, ErrPairWrong) {
		t.Fatalf("wrong code: %v", err)
	}
	after, _ := a.store.List()
	if len(after) != len(before) || after[0].Fingerprint != before[0].Fingerprint {
		t.Fatal("a wrong code changed the store")
	}
	// The second wrong code burns it.
	if _, err := DialPair(ctxT(t), srv.Addr().String(), PairOptions{Identity: pc.id, Name: "pc", Code: wrong}); err == nil {
		t.Fatal("wrong code accepted")
	}
	if o := waitOutcome(t, w); !errors.Is(o.Err, ErrPairAttempts) {
		t.Fatalf("burned code ended with %v", o.Err)
	}
}

func TestPairInviteFingerprintMismatchAborts(t *testing.T) {
	leakCheck(t)
	fastKDF(t)
	_, _, srv, _ := pairUp(t, ServerOptions{})
	w, err := srv.ArmPair(context.Background(), PairOptions{MaxAttempts: 1})
	if err != nil {
		t.Fatal(err)
	}
	pc := newTestPeer(t, "pc")
	other := newTestPeer(t, "other")
	_, err = DialPair(ctxT(t), srv.Addr().String(), PairOptions{Identity: pc.id, Name: "pc", Code: w.Code, ExpectFingerprint: other.id.Fingerprint()})
	if !errors.Is(err, ErrPairFingerprint) {
		t.Fatalf("mismatching fingerprint: %v, want ErrPairFingerprint", err)
	}
	if !strings.Contains(PairErrText(err, "x:7722"), "intercepting") {
		t.Fatalf("message: %s", PairErrText(err, "x:7722"))
	}
	// The code was never used: the one attempt is still there.
	res, err := DialPair(ctxT(t), srv.Addr().String(), PairOptions{Identity: pc.id, Name: "pc", Code: w.Code, ExpectFingerprint: w.Fingerprint})
	if err != nil {
		t.Fatalf("matching fingerprint: %v", err)
	}
	if res.PeerFingerprint != w.Fingerprint {
		t.Fatal("pinned another key than the invite's")
	}
	if o := waitOutcome(t, w); o.Err != nil {
		t.Fatal(o.Err)
	}
}

func TestCLIAddInviteMismatchPinsNothing(t *testing.T) {
	leakCheck(t)
	fastKDF(t)
	_, _, srv, _ := pairUp(t, ServerOptions{})
	w, err := srv.ArmPair(context.Background(), PairOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		w.Cancel()
		waitOutcome(t, w)
	}()
	host, port, _ := net.SplitHostPort(w.Addr)
	other := newTestPeer(t, "other")
	inv := Invite{Host: host, Port: port, Code: w.Code, Fingerprint: other.id.Fingerprint(), Name: "vps"}
	var out syncBuf
	c := CLI{Dir: t.TempDir(), Out: &out}
	err = c.Add(ctxT(t), AddArgs{Invite: inv.Encode(), Yes: true})
	if !errors.Is(err, ErrPairFingerprint) {
		t.Fatalf("add with a mismatching invite: %v", err)
	}
	if pins, _ := OpenStore(c.Dir).List(); len(pins) != 0 {
		t.Fatalf("pinned after a mismatch: %v", pins)
	}
	if strings.Contains(err.Error(), w.Code) || strings.Contains(out.String(), w.Code) {
		t.Fatal("the code leaked into the output")
	}
	if err := c.Add(ctxT(t), AddArgs{Invite: "deck://pair?v=9"}); !errors.Is(err, ErrNotInvite) {
		t.Fatalf("bad invite: %v", err)
	}
}

func TestPairDialNoAnswerAndCancel(t *testing.T) {
	leakCheck(t)
	pc := newTestPeer(t, "pc")
	code, _ := NewPairCode()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close() // nothing listens there now: connection refused
	_, err = DialPair(ctxT(t), addr, PairOptions{Identity: pc.id, Code: code})
	if !errors.Is(err, ErrPairNoAnswer) {
		t.Fatalf("refused: %v, want ErrPairNoAnswer", err)
	}
	if msg := PairErrText(err, addr); !strings.Contains(msg, "No answer from "+addr) || !strings.Contains(msg, "firewall") {
		t.Fatalf("message: %s", msg)
	}

	// A listener that never speaks: cancelling returns at once.
	silent, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer silent.Close()
	go func() {
		c, err := silent.Accept()
		if err == nil {
			defer c.Close()
			time.Sleep(3 * time.Second)
		}
	}()
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(200*time.Millisecond, cancel)
	start := time.Now()
	if _, err := DialPair(ctx, silent.Addr().String(), PairOptions{Identity: pc.id, Code: code}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled dial: %v", err)
	}
	if el := time.Since(start); el > 2*time.Second {
		t.Fatalf("cancel took %s", el)
	}
}

func TestPairErrTextWording(t *testing.T) {
	addr := "203.0.113.5:7722"
	cases := map[error]string{
		ErrPairNotWaiting: "Wrong or expired code",
		ErrPairUnproven:   "Wrong or expired code",
		ErrPairWrong:      "Wrong code",
		ErrPairHandshake:  "Host key / handshake failed",
		ErrPairNoAnswer:   "No answer from " + addr + ". Is Deck running there, is Accept connections on, and is port 7722 open in its firewall?",
	}
	for err, want := range cases {
		if got := PairErrText(err, addr); !strings.Contains(got, want) {
			t.Fatalf("%v: %q lacks %q", err, got, want)
		}
	}
}

func TestReachable(t *testing.T) {
	for addr, want := range map[string]bool{
		"127.0.0.1:7722": false, "[::1]:7722": false, "localhost:7722": false,
		"0.0.0.0:7722": true, ":7722": true, "10.0.0.5:7722": true, "junk": false,
	} {
		if got := Reachable(addr); got != want {
			t.Fatalf("Reachable(%q) = %v", addr, got)
		}
	}
}

func TestManagerArmsPairOnItsListener(t *testing.T) {
	leakCheck(t)
	fastKDF(t)
	a := newTestPeer(t, "a")
	m, err := NewManager(ManagerOptions{Dir: a.dir, Logger: quietLog, Peers: peersCfg("a", true, nil), SweepInterval: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer m.Stop()
	w, err := m.ArmPair(context.Background(), PairOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if w.Addr != m.Addr() || w.Reachable || !w.Shared {
		t.Fatalf("armed on %s (listener %s), reachable %v, shared %v", w.Addr, m.Addr(), w.Reachable, w.Shared)
	}
	// Renaming moves the listener (a new Server on a new port): the code
	// stays armed and is served on the new one.
	if err := m.Apply(peersCfg("a2", true, nil)); err != nil {
		t.Fatal(err)
	}
	pc := newTestPeer(t, "pc")
	if _, err := DialPair(ctxT(t), m.Addr(), PairOptions{Identity: pc.id, Name: "pc", Code: w.Code}); err != nil {
		t.Fatalf("pairing after the listener moved: %v", err)
	}
	if o := waitOutcome(t, w); o.Err != nil {
		t.Fatal(o.Err)
	}

	// Turning the listener off ends a code armed on it.
	w, err = m.ArmPair(context.Background(), PairOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Apply(&config.Peers{Enabled: true, Name: "a2", Remote: []config.PeerRemote{{Name: "x", Addr: "127.0.0.1:1", Dial: true}}}); err != nil {
		t.Fatal(err)
	}
	if o := waitOutcome(t, w); !errors.Is(o.Err, context.Canceled) {
		t.Fatalf("code outlived its listener: %v", o.Err)
	}
	if _, err := m.ArmPair(context.Background(), PairOptions{}); !errors.Is(err, errNoListener) {
		t.Fatalf("armed without a listener: %v", err)
	}
}

// listeningService is a Service with the team link on and its listener on
// this machine only.
func listeningService(t *testing.T, name string) *Service {
	t.Helper()
	s := newTestService(t, name)
	if err := s.SetEnabled(true); err != nil {
		t.Fatal(err)
	}
	if err := s.SetListen(true, freeAddr(t)); err != nil {
		t.Fatal(err)
	}
	if s.Manager() == nil || s.Manager().Addr() == "" {
		t.Fatal("not listening")
	}
	return s
}

func TestServiceStartPairListenUsesTheLinkListener(t *testing.T) {
	leakCheck(t)
	fastKDF(t)
	a, b := listeningService(t, "a"), newTestService(t, "b")
	w, err := a.StartPairListen(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if w.Addr != a.Manager().Addr() || !w.Shared || w.Reachable {
		t.Fatalf("wait %+v on listener %s", w, a.Manager().Addr())
	}
	if !a.Snapshot().Pairing {
		t.Fatal("pairing not shown")
	}
	res, err := b.PairDial(ctxT(t), w.Addr, w.Code, w.Fingerprint)
	if err != nil {
		t.Fatal(err)
	}
	if o := waitOutcome(t, w); o.Err != nil || res.PeerFingerprint != w.Fingerprint {
		t.Fatalf("outcome %v, fingerprint %s", o.Err, res.PeerFingerprint)
	}
}

func TestServiceStartPairListenOwnPortAndBusyPort(t *testing.T) {
	leakCheck(t)
	s := newTestService(t, "a") // team link off: no listener of the link's
	addr := freeAddr(t)
	if err := s.SetListen(false, addr); err != nil {
		t.Fatal(err)
	}
	w, err := s.StartPairListen(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if w.Shared || w.Addr != addr || w.Reachable {
		t.Fatalf("own listener: %+v", w)
	}
	// A second start replaces the first and gets the same port back.
	w2, err := s.StartPairListen(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if o := waitOutcome(t, w); !errors.Is(o.Err, context.Canceled) {
		t.Fatalf("replaced wait ended with %v", o.Err)
	}
	w2.Cancel()
	waitOutcome(t, w2)

	// A port taken by something else is an error, not another port.
	busy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer busy.Close()
	if err := s.SetListen(false, busy.Addr().String()); err != nil {
		t.Fatal(err)
	}
	if w, err := s.StartPairListen(context.Background()); err == nil {
		w.Cancel()
		t.Fatalf("waited on %s, a busy port", w.Addr)
	} else if !strings.Contains(err.Error(), "in use") {
		t.Fatalf("busy port: %v", err)
	}
}

func TestServicePairListenAllKeepsTheCode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("binding every interface asks the Windows firewall")
	}
	leakCheck(t)
	fastKDF(t)
	a, b := listeningService(t, "a"), newTestService(t, "b")
	w, err := a.StartPairListen(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	moved, err := a.PairListenAll(w)
	if err != nil {
		t.Fatal(err)
	}
	if moved.Code != w.Code || moved.Done != w.Done || !moved.Reachable || !strings.HasPrefix(moved.Addr, "0.0.0.0:") {
		t.Fatalf("moved wait: %+v", moved)
	}
	if portOfAddr(moved.Addr) != portOfAddr(w.Addr) {
		t.Fatal("the port changed")
	}
	if _, err := b.PairDial(ctxT(t), net.JoinHostPort("127.0.0.1", portOfAddr(moved.Addr)), moved.Code, ""); err != nil {
		t.Fatalf("pairing after moving to every interface: %v", err)
	}
	if o := waitOutcome(t, moved); o.Err != nil {
		t.Fatal(o.Err)
	}
}
