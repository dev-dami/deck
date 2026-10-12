package peer

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net"
	"regexp"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

type pairOutcome struct {
	res PairResult
	err error
}

// servePair starts ServePair for p on a loopback port.
func servePair(t *testing.T, p *testPeer, code string, o PairOptions) (string, <-chan pairOutcome) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	o.Identity, o.Name, o.Code, o.Logger = p.id, p.name, code, quietLog
	out := make(chan pairOutcome, 1)
	go func() {
		res, err := ServePair(context.Background(), ln, o)
		out <- pairOutcome{res, err}
	}()
	return ln.Addr().String(), out
}

func waitPair(t *testing.T, ch <-chan pairOutcome) pairOutcome {
	t.Helper()
	select {
	case o := <-ch:
		return o
	case <-time.After(10 * time.Second):
		t.Fatal("ServePair did not return")
	}
	return pairOutcome{}
}

func TestPairCodeFormat(t *testing.T) {
	re := regexp.MustCompile(`^[23456789ABCDEFGHJKMNPQRSTVWXYZ]{5}-[23456789ABCDEFGHJKMNPQRSTVWXYZ]{5}$`)
	seen := map[string]bool{}
	for i := 0; i < 200; i++ {
		c, err := NewPairCode()
		if err != nil || !re.MatchString(c) {
			t.Fatalf("code %q: %v", c, err)
		}
		if seen[c] {
			t.Fatalf("repeated code %q", c)
		}
		seen[c] = true
		if !validCode(normalizeCode(strings.ToLower(c))) || !validCode(normalizeCode(strings.ReplaceAll(c, "-", " "))) {
			t.Fatalf("normalize failed for %q", c)
		}
	}
	for _, bad := range []string{"", "ABCDE", "ABCDE-2345O", "ABCDE-234561", "ABCDE-2345I"} {
		if validCode(normalizeCode(bad)) {
			t.Fatalf("%q accepted", bad)
		}
	}
}

func TestPairHappyPathRealKDF(t *testing.T) {
	leakCheck(t)
	l, d := newTestPeer(t, "vps"), newTestPeer(t, "pc")
	code, _ := NewPairCode()
	addr, out := servePair(t, l, code, PairOptions{})
	res, err := DialPair(ctxT(t), addr, PairOptions{Identity: d.id, Name: "pc", Code: strings.ToLower(code)})
	if err != nil {
		t.Fatal(err)
	}
	srv := waitPair(t, out)
	if srv.err != nil {
		t.Fatal(srv.err)
	}
	// Both sides see the same two fingerprints.
	if res.PeerFingerprint != l.id.Fingerprint() || res.OwnFingerprint != d.id.Fingerprint() {
		t.Fatalf("dialer saw %s / %s", res.PeerFingerprint, res.OwnFingerprint)
	}
	if srv.res.PeerFingerprint != d.id.Fingerprint() || srv.res.OwnFingerprint != l.id.Fingerprint() {
		t.Fatalf("listener saw %s / %s", srv.res.PeerFingerprint, srv.res.OwnFingerprint)
	}
	if res.PeerName != "vps" || srv.res.PeerName != "pc" {
		t.Fatalf("names: %q %q", res.PeerName, srv.res.PeerName)
	}
	if !sameKey(res.PeerKey, l.id.PublicKey()) || !sameKey(srv.res.PeerKey, d.id.PublicKey()) {
		t.Fatal("keys differ from identities")
	}
	// Single use: the listener is gone.
	if _, err := DialPair(ctxT(t), addr, PairOptions{Identity: d.id, Name: "pc", Code: code}); err == nil {
		t.Fatal("code worked twice")
	}

	// The pins made from the results connect, both ways.
	l.store.Pin(NewPinned("pc", "", "", srv.res.PeerKey))
	srvConn := l.listen(t, ServerOptions{})
	pin := NewPinned("vps", srvConn.Addr().String(), "", res.PeerKey)
	cl := openInit(t, d.dial(t, pin, nil))
	if _, err := cl.Ping(ctxT(t)); err != nil {
		t.Fatal(err)
	}
}

func TestPairWrongCodeThenRightCode(t *testing.T) {
	leakCheck(t)
	fastKDF(t)
	l, d := newTestPeer(t, "vps"), newTestPeer(t, "pc")
	code, _ := NewPairCode()
	other, _ := NewPairCode()
	addr, out := servePair(t, l, code, PairOptions{MaxAttempts: 3})
	if _, err := DialPair(ctxT(t), addr, PairOptions{Identity: d.id, Name: "pc", Code: other}); !errors.Is(err, ErrPairWrong) {
		t.Fatalf("wrong code: %v", err)
	}
	if _, err := DialPair(ctxT(t), addr, PairOptions{Identity: d.id, Name: "pc", Code: code}); err != nil {
		t.Fatalf("right code after a wrong one: %v", err)
	}
	if o := waitPair(t, out); o.err != nil {
		t.Fatal(o.err)
	}
}

func TestPairAttemptsBurnCode(t *testing.T) {
	leakCheck(t)
	fastKDF(t)
	l, d := newTestPeer(t, "vps"), newTestPeer(t, "pc")
	code, _ := NewPairCode()
	addr, out := servePair(t, l, code, PairOptions{MaxAttempts: 3})
	for i := 0; i < 3; i++ {
		wrong, _ := NewPairCode()
		if _, err := DialPair(ctxT(t), addr, PairOptions{Identity: d.id, Name: "pc", Code: wrong}); err == nil {
			t.Fatal("wrong code accepted")
		}
	}
	if o := waitPair(t, out); !errors.Is(o.err, ErrPairAttempts) {
		t.Fatalf("ServePair = %v, want ErrPairAttempts", o.err)
	}
	if _, err := DialPair(ctxT(t), addr, PairOptions{Identity: d.id, Name: "pc", Code: code}); err == nil {
		t.Fatal("burned code still works")
	}
}

func TestPairExpiry(t *testing.T) {
	leakCheck(t)
	fastKDF(t)
	l, d := newTestPeer(t, "vps"), newTestPeer(t, "pc")
	code, _ := NewPairCode()
	addr, out := servePair(t, l, code, PairOptions{Expiry: 150 * time.Millisecond})
	if o := waitPair(t, out); !errors.Is(o.err, ErrPairExpired) {
		t.Fatalf("ServePair = %v, want ErrPairExpired", o.err)
	}
	if _, err := DialPair(ctxT(t), addr, PairOptions{Identity: d.id, Name: "pc", Code: code}); err == nil {
		t.Fatal("expired code works")
	}
}

func TestPairExpiryCutsStalledConnection(t *testing.T) {
	leakCheck(t)
	fastKDF(t)
	l := newTestPeer(t, "vps")
	code, _ := NewPairCode()
	addr, out := servePair(t, l, code, PairOptions{Expiry: 300 * time.Millisecond})
	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if o := waitPair(t, out); !errors.Is(o.err, ErrPairExpired) {
		t.Fatalf("ServePair = %v, want ErrPairExpired", o.err)
	}
}

func TestPairJunkDoesNotBurnAttempts(t *testing.T) {
	leakCheck(t)
	fastKDF(t)
	l, d := newTestPeer(t, "vps"), newTestPeer(t, "pc")
	code, _ := NewPairCode()
	addr, out := servePair(t, l, code, PairOptions{MaxAttempts: 1})
	// A port scan and a plain ssh client that opens the wrong channel.
	c, _ := net.Dial("tcp", addr)
	c.Write([]byte("GET / HTTP/1.0\r\n\r\n"))
	waitClosed(c, 2*time.Second)
	c.Close()
	sc, _, _, err := rawDial(t, addr, []ssh.AuthMethod{ssh.PublicKeys(ed25519Signer(t))}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := sc.OpenChannel("session", nil); err == nil {
		t.Fatal("pairing listener accepted a session channel")
	}
	if _, _, err := sc.OpenChannel(ChannelType, nil); err == nil {
		t.Fatal("pairing listener accepted an mcp channel")
	}
	sc.Close()
	if _, err := DialPair(ctxT(t), addr, PairOptions{Identity: d.id, Name: "pc", Code: code}); err != nil {
		t.Fatalf("pairing after junk: %v", err)
	}
	if o := waitPair(t, out); o.err != nil {
		t.Fatal(o.err)
	}
}

// mitm sits between a dialer and a listener with its own key: it accepts the
// dialer's SSH as a server and dials the listener as a client, relaying the
// pairing messages untouched. Without the code it cannot fix the macs.
func mitm(t *testing.T, target string) string {
	t.Helper()
	m := newTestPeer(t, "mallory")
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		raw, err := ln.Accept()
		if err != nil {
			return
		}
		defer raw.Close()
		cfg := &ssh.ServerConfig{PublicKeyCallback: func(ssh.ConnMetadata, ssh.PublicKey) (*ssh.Permissions, error) { return nil, nil }}
		cfg.AddHostKey(m.id.Signer)
		sc, chans, reqs, err := ssh.NewServerConn(raw, cfg)
		if err != nil {
			return
		}
		defer sc.Close()
		go ssh.DiscardRequests(reqs)
		nc, ok := <-chans
		if !ok {
			return
		}
		down, dreqs, err := nc.Accept()
		if err != nil {
			return
		}
		go ssh.DiscardRequests(dreqs)
		up, err := net.Dial("tcp", target)
		if err != nil {
			return
		}
		defer up.Close()
		usc, uchans, ureqs, err := ssh.NewClientConn(up, target, &ssh.ClientConfig{
			User: "x", Auth: []ssh.AuthMethod{ssh.PublicKeys(m.id.Signer)}, HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		})
		if err != nil {
			return
		}
		defer usc.Close()
		go ssh.DiscardRequests(ureqs)
		go func() {
			for nc := range uchans {
				nc.Reject(ssh.Prohibited, "")
			}
		}()
		upch, upreqs, err := usc.OpenChannel(PairChannelType, nil)
		if err != nil {
			return
		}
		go ssh.DiscardRequests(upreqs)
		// Relay one line each way.
		line, err := bufio.NewReader(down).ReadBytes('\n')
		if err != nil {
			return
		}
		upch.Write(line)
		reply, err := bufio.NewReader(upch).ReadBytes('\n')
		if err != nil {
			return
		}
		down.Write(reply)
		down.Close()
		upch.Close()
	}()
	return ln.Addr().String()
}

func TestPairMITMWithoutCodeCannotPin(t *testing.T) {
	leakCheck(t)
	fastKDF(t)
	l, d := newTestPeer(t, "vps"), newTestPeer(t, "pc")
	code, _ := NewPairCode()
	addr, out := servePair(t, l, code, PairOptions{MaxAttempts: 1})
	via := mitm(t, addr)
	_, err := DialPair(ctxT(t), via, PairOptions{Identity: d.id, Name: "pc", Code: code})
	if err == nil {
		t.Fatal("dialer accepted a pairing through a man in the middle")
	}
	// The listener refused the relayed proof (different session, different
	// keys): the one attempt is used up and nothing was paired.
	if o := waitPair(t, out); !errors.Is(o.err, ErrPairAttempts) {
		t.Fatalf("listener: %v, want ErrPairAttempts", o.err)
	}
}

// fakePairServer answers a pairing hello with whatever reply returns.
func fakePairServer(t *testing.T, reply func(pairHello) pairHello) string {
	t.Helper()
	m := newTestPeer(t, "fake")
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		raw, err := ln.Accept()
		if err != nil {
			return
		}
		defer raw.Close()
		cfg := &ssh.ServerConfig{PublicKeyCallback: func(ssh.ConnMetadata, ssh.PublicKey) (*ssh.Permissions, error) { return nil, nil }}
		cfg.AddHostKey(m.id.Signer)
		sc, chans, reqs, err := ssh.NewServerConn(raw, cfg)
		if err != nil {
			return
		}
		defer sc.Close()
		go ssh.DiscardRequests(reqs)
		nc, ok := <-chans
		if !ok {
			return
		}
		ch, creqs, err := nc.Accept()
		if err != nil {
			return
		}
		go ssh.DiscardRequests(creqs)
		var h pairHello
		line, err := bufio.NewReader(ch).ReadBytes('\n')
		if err != nil {
			return
		}
		_ = json.Unmarshal(line, &h)
		_ = writeJSONLine(ch, reply(h))
		ch.Close()
	}()
	return ln.Addr().String()
}

func TestPairDialerRejectsUnprovenListener(t *testing.T) {
	leakCheck(t)
	fastKDF(t)
	d := newTestPeer(t, "pc")
	code, _ := NewPairCode()
	for name, reply := range map[string]func(pairHello) pairHello{
		"ok without mac":   func(pairHello) pairHello { return pairHello{V: 1, OK: true, Name: "vps"} },
		"echoed mac":       func(h pairHello) pairHello { return pairHello{V: 1, OK: true, Name: "vps", MAC: h.MAC} },
		"random mac":       func(pairHello) pairHello { return pairHello{V: 1, OK: true, Name: "vps", MAC: make([]byte, 32)} },
		"wrong version":    func(h pairHello) pairHello { return pairHello{V: 2, OK: true} },
		"not ok, no error": func(pairHello) pairHello { return pairHello{V: 1} },
	} {
		t.Run(name, func(t *testing.T) {
			addr := fakePairServer(t, reply)
			_, err := DialPair(ctxT(t), addr, PairOptions{Identity: d.id, Name: "pc", Code: code})
			if !errors.Is(err, ErrPairUnproven) {
				t.Fatalf("DialPair = %v, want ErrPairUnproven", err)
			}
		})
	}
	if _, err := DialPair(ctxT(t), "127.0.0.1:1", PairOptions{Identity: d.id, Code: "nope"}); err == nil || !strings.Contains(err.Error(), "not a pairing code") {
		t.Fatalf("bad code format: %v", err)
	}
}
