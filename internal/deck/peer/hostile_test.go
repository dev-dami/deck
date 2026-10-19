package peer

import (
	"bufio"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

// The hostile client suite: a raw x/crypto/ssh client trying everything an
// SSH client can ask for. Every attempt must be refused or closed, without a
// panic, a hang or a leaked goroutine.

// rawDial runs an SSH client handshake against addr with the given auth.
func rawDial(t *testing.T, addr string, auth []ssh.AuthMethod, hostKey ssh.HostKeyCallback) (ssh.Conn, <-chan ssh.NewChannel, <-chan *ssh.Request, error) {
	t.Helper()
	raw, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		return nil, nil, nil, err
	}
	_ = raw.SetDeadline(time.Now().Add(10 * time.Second))
	if hostKey == nil {
		hostKey = ssh.InsecureIgnoreHostKey()
	}
	sc, chans, reqs, err := ssh.NewClientConn(raw, addr, &ssh.ClientConfig{
		User: "root", Auth: auth, HostKeyCallback: hostKey, Timeout: 5 * time.Second,
	})
	if err != nil {
		raw.Close()
		return nil, nil, nil, err
	}
	_ = raw.SetDeadline(time.Time{})
	go ssh.DiscardRequests(reqs)
	go func() {
		for nc := range chans {
			_ = nc.Reject(ssh.Prohibited, "test client")
		}
	}()
	t.Cleanup(func() { sc.Close() })
	return sc, chans, reqs, nil
}

func ed25519Signer(t *testing.T) ssh.Signer {
	t.Helper()
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	s, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestHostileAuth(t *testing.T) {
	leakCheck(t)
	_, b, srv, _ := pairUp(t, ServerOptions{LockoutThreshold: 1000})
	addr := srv.Addr().String()

	cases := map[string][]ssh.AuthMethod{
		"unpinned key": {ssh.PublicKeys(ed25519Signer(t))},
		"password":     {ssh.Password("hunter2")},
		"keyboard-interactive": {ssh.KeyboardInteractive(func(string, string, []string, []bool) ([]string, error) {
			return []string{"x"}, nil
		})},
		"none":                         nil,
		"password then unpinned key":   {ssh.Password("x"), ssh.PublicKeys(ed25519Signer(t))},
		"many unpinned keys":           {ssh.PublicKeys(ed25519Signer(t), ed25519Signer(t), ed25519Signer(t), ed25519Signer(t), ed25519Signer(t))},
		"ecdsa key":                    {ssh.PublicKeys(ecdsaSigner(t))},
		"pinned key behind 3 unpinned": {ssh.PublicKeys(ed25519Signer(t), ed25519Signer(t), ed25519Signer(t), b.id.Signer)},
	}
	for name, auth := range cases {
		t.Run(name, func(t *testing.T) {
			_, _, _, err := rawDial(t, addr, auth, nil)
			if err == nil {
				t.Fatal("authenticated")
			}
		})
	}
	// The pinned key alone gets in.
	if err := dialSoon(t, addr, []ssh.AuthMethod{ssh.PublicKeys(b.id.Signer)}); err != nil {
		t.Fatalf("pinned key refused: %v", err)
	}
}

func ecdsaSigner(t *testing.T) ssh.Signer {
	t.Helper()
	k, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	s, err := ssh.NewSignerFromKey(k)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestHostileChannelsAndRequests(t *testing.T) {
	leakCheck(t)
	_, b, srv, _ := pairUp(t, ServerOptions{})
	sc, _, _, err := rawDial(t, srv.Addr().String(), []ssh.AuthMethod{ssh.PublicKeys(b.id.Signer)}, nil)
	if err != nil {
		t.Fatal(err)
	}

	for _, typ := range []string{"session", "direct-tcpip", "forwarded-tcpip", "x11", "auth-agent@openssh.com",
		"direct-streamlocal@openssh.com", "tun@openssh.com", "deck-pair", "MCP", "mcp ", "", "unknown-xyz", strings.Repeat("m", 5000),
		RelayChannelType, RelayedChannelType} {
		ch, _, err := sc.OpenChannel(typ, []byte("payload"))
		var oe *ssh.OpenChannelError
		if err == nil || !errors.As(err, &oe) || oe.Reason != ssh.UnknownChannelType {
			if ch != nil {
				ch.Close()
			}
			t.Fatalf("channel %q: %v, want UnknownChannelType", truncate(typ, 20), err)
		}
	}

	for _, typ := range []string{"tcpip-forward", "cancel-tcpip-forward", "streamlocal-forward@openssh.com",
		"cancel-streamlocal-forward@openssh.com", "no-more-sessions@openssh.com", "hostkeys-prove-00@openssh.com", "x"} {
		ok, _, err := sc.SendRequest(typ, true, []byte{0, 0, 0, 0})
		if err != nil || ok {
			t.Fatalf("global request %q: ok=%v err=%v", typ, ok, err)
		}
	}
	if ok, _, err := sc.SendRequest(keepaliveRequest, true, nil); err != nil || !ok {
		t.Fatalf("keepalive: ok=%v err=%v", ok, err)
	}

	ch, _, err := sc.OpenChannel(ChannelType, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ch.Close()
	for _, typ := range []string{"shell", "exec", "pty-req", "subsystem", "env", "window-change", "x11-req",
		"auth-agent-req@openssh.com", "signal", "break", "xon-xoff", "eow@openssh.com"} {
		ok, err := ch.SendRequest(typ, true, ssh.Marshal(struct{ S string }{"sh -c id"}))
		if err != nil || ok {
			t.Fatalf("channel request %q: ok=%v err=%v", typ, ok, err)
		}
	}
	// The mcp channel still works after all of that.
	r := bufio.NewReader(ch)
	sendLine(t, ch, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`)
	if resp := readResp(t, r); resp.Error != nil {
		t.Fatalf("initialize after refusals: %+v", resp)
	}
}

type rpcResp struct {
	ID     json.RawMessage `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func sendLine(t *testing.T, ch ssh.Channel, s string) {
	t.Helper()
	if _, err := ch.Write([]byte(s + "\n")); err != nil {
		t.Fatal(err)
	}
}

func readResp(t *testing.T, r *bufio.Reader) rpcResp {
	t.Helper()
	type res struct {
		line []byte
		err  error
	}
	got := make(chan res, 1)
	go func() {
		line, err := r.ReadBytes('\n')
		got <- res{line, err}
	}()
	select {
	case g := <-got:
		if g.err != nil {
			t.Fatalf("read: %v", g.err)
		}
		var resp rpcResp
		if err := json.Unmarshal(g.line, &resp); err != nil {
			t.Fatalf("bad response %q: %v", g.line, err)
		}
		return resp
	case <-time.After(5 * time.Second):
		t.Fatal("no response")
	}
	return rpcResp{}
}

// expectEOF waits for the server to close the channel.
func expectEOF(t *testing.T, r *bufio.Reader) {
	t.Helper()
	done := make(chan error, 1)
	go func() {
		for {
			if _, err := r.ReadBytes('\n'); err != nil {
				done <- err
				return
			}
		}
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("channel not closed")
	}
}

func mcpChannel(t *testing.T, opts ServerOptions) (ssh.Channel, *bufio.Reader) {
	t.Helper()
	_, b, srv, _ := pairUp(t, opts)
	sc, _, _, err := rawDial(t, srv.Addr().String(), []ssh.AuthMethod{ssh.PublicKeys(b.id.Signer)}, nil)
	if err != nil {
		t.Fatal(err)
	}
	ch, reqs, err := sc.OpenChannel(ChannelType, nil)
	if err != nil {
		t.Fatal(err)
	}
	go ssh.DiscardRequests(reqs)
	t.Cleanup(func() { ch.Close() })
	return ch, bufio.NewReader(ch)
}

func TestHostileJSONRPC(t *testing.T) {
	leakCheck(t)
	ch, r := mcpChannel(t, ServerOptions{})

	// Before initialize, tools are refused; ping is fine.
	sendLine(t, ch, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	if resp := readResp(t, r); resp.Error == nil || resp.Error.Code != codeNotInitialized {
		t.Fatalf("tools/list before initialize: %+v", resp)
	}
	sendLine(t, ch, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"execute","arguments":{"op":"peer.status"}}}`)
	if resp := readResp(t, r); resp.Error == nil || resp.Error.Code != codeNotInitialized {
		t.Fatalf("tools/call before initialize: %+v", resp)
	}
	sendLine(t, ch, `{"jsonrpc":"2.0","id":3,"method":"ping"}`)
	if resp := readResp(t, r); resp.Error != nil {
		t.Fatalf("ping: %+v", resp)
	}
	sendLine(t, ch, `{"jsonrpc":"2.0","id":4,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`)
	if resp := readResp(t, r); resp.Error != nil || !strings.Contains(string(resp.Result), "2025-06-18") {
		t.Fatalf("initialize: %+v", resp)
	}
	sendLine(t, ch, `{"jsonrpc":"2.0","method":"notifications/initialized"}`)

	for i, req := range []struct {
		line string
		code int
	}{
		{`{"jsonrpc":"2.0","id":10,"method":"shell/exec"}`, codeMethodNotFound},
		{`{"jsonrpc":"2.0","id":11,"method":"resources/list"}`, codeMethodNotFound},
		{`{"jsonrpc":"2.0","id":12,"method":"sampling/createMessage"}`, codeMethodNotFound},
		{`{"jsonrpc":"2.0","id":13,"method":"tools/call","params":{"name":"approve","arguments":{}}}`, codeInvalidParams},
		{`{"jsonrpc":"2.0","id":14,"method":"tools/call","params":{"name":"bash","arguments":{"cmd":"id"}}}`, codeInvalidParams},
		{`{"jsonrpc":"2.0","id":15,"method":"tools/call","params":{"name":"","arguments":{}}}`, codeInvalidParams},
		{`{"jsonrpc":"2.0","id":16,"method":"tools/call","params":"not an object"}`, codeInvalidParams},
		{`{"jsonrpc":"2.0","id":17,"method":"tools/call","params":{"name":"Execute"}}`, codeInvalidParams},
	} {
		sendLine(t, ch, req.line)
		resp := readResp(t, r)
		if resp.Error == nil || resp.Error.Code != req.code {
			t.Fatalf("case %d %s: %+v, want code %d", i, req.line, resp, req.code)
		}
	}
	// Notifications for unknown methods get no reply; the next call still works.
	sendLine(t, ch, `{"jsonrpc":"2.0","method":"evil/notify","params":{}}`)
	sendLine(t, ch, `{"jsonrpc":"2.0","id":"s1","method":"tools/call","params":{"name":"execute","arguments":{"op":"nope"}}}`)
	resp := readResp(t, r)
	if string(resp.ID) != `"s1"` || resp.Error != nil || !strings.Contains(string(resp.Result), "not allowed") {
		t.Fatalf("op outside the default allow list: %+v", resp)
	}

	// Malformed JSON: a parse error, then the channel closes.
	sendLine(t, ch, `{"jsonrpc":"2.0","id":`)
	if resp := readResp(t, r); resp.Error == nil || resp.Error.Code != codeParse {
		t.Fatalf("malformed: %+v", resp)
	}
	expectEOF(t, r)
}

func TestHostileBatchAndJunkClose(t *testing.T) {
	leakCheck(t)
	for _, line := range []string{
		`[{"jsonrpc":"2.0","id":1,"method":"ping"}]`,
		`[]`,
		`42`,
		`{}`,
		`{"jsonrpc":"2.0","id":{"x":1},"method":"ping"}`,
		"\x00\x01\x02",
	} {
		t.Run(line, func(t *testing.T) {
			ch, r := mcpChannel(t, ServerOptions{})
			sendLine(t, ch, line)
			expectEOF(t, r)
		})
	}
}

func TestHostileOversizeMessage(t *testing.T) {
	leakCheck(t)
	ch, r := mcpChannel(t, ServerOptions{Limits: Limits{MaxMessage: 4096}})
	// One long line with no newline: refused once it passes the cap, without
	// waiting for the end of it.
	big := `{"jsonrpc":"2.0","id":1,"method":"ping","params":{"x":"` + strings.Repeat("A", 64<<10)
	go func() { _, _ = ch.Write([]byte(big)) }()
	resp := readResp(t, r)
	if resp.Error == nil || resp.Error.Code != codeInvalidRequest || !strings.Contains(resp.Error.Message, "too large") {
		t.Fatalf("oversize: %+v", resp)
	}
	expectEOF(t, r)
}

func TestHostileTooManyChannels(t *testing.T) {
	leakCheck(t)
	_, b, srv, _ := pairUp(t, ServerOptions{Limits: Limits{MaxChannels: 2}})
	sc, _, _, err := rawDial(t, srv.Addr().String(), []ssh.AuthMethod{ssh.PublicKeys(b.id.Signer)}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var open []ssh.Channel
	for i := 0; i < 2; i++ {
		ch, reqs, err := sc.OpenChannel(ChannelType, nil)
		if err != nil {
			t.Fatal(err)
		}
		go ssh.DiscardRequests(reqs)
		open = append(open, ch)
	}
	_, _, err = sc.OpenChannel(ChannelType, nil)
	var oe *ssh.OpenChannelError
	if !errors.As(err, &oe) || oe.Reason != ssh.ResourceShortage {
		t.Fatalf("third channel: %v, want ResourceShortage", err)
	}
	for _, ch := range open {
		ch.Close()
	}
}

func TestHostileWrongHostKey(t *testing.T) {
	leakCheck(t)
	_, b, srv, pinA := pairUp(t, ServerOptions{})
	// A pin whose key is someone else's: the real server must be refused.
	wrong := NewPinned("a", srv.Addr().String(), "", newKey(t))
	_, err := Dial(ctxT(t), DialOptions{Identity: b.id, Peer: wrong, Logger: quietLog})
	if err == nil || !strings.Contains(err.Error(), "host key mismatch") {
		t.Fatalf("wrong host key accepted: %v", err)
	}
	// No pinned key at all.
	_, err = Dial(ctxT(t), DialOptions{Identity: b.id, Peer: Pinned{Name: "a", Addr: srv.Addr().String()}})
	if err == nil {
		t.Fatal("dialed without a pinned key")
	}
	// The right pin works.
	c, err := Dial(ctxT(t), DialOptions{Identity: b.id, Peer: pinA, Logger: quietLog})
	if err != nil {
		t.Fatal(err)
	}
	c.Close()
}

func TestHostileConnectionLimitAndStalledHandshake(t *testing.T) {
	leakCheck(t)
	_, b, srv, _ := pairUp(t, ServerOptions{MaxPreAuthPerIP: 2, Limits: Limits{HandshakeTimeout: 300 * time.Millisecond}, LockoutThreshold: 1000})
	addr := srv.Addr().String()

	// Two connections that never speak take both of this IP's handshake
	// slots.
	var holds []net.Conn
	for i := 0; i < 2; i++ {
		c, err := net.Dial("tcp", addr)
		if err != nil {
			t.Fatal(err)
		}
		holds = append(holds, c)
	}
	time.Sleep(50 * time.Millisecond) // let the server accept them
	third, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	if !waitClosed(third, 200*time.Millisecond) {
		t.Fatal("connection over the limit was not closed at once")
	}
	third.Close()
	// The stalled handshakes are cut off by the handshake timeout.
	for _, c := range holds {
		if !waitClosed(c, 3*time.Second) {
			t.Fatal("stalled handshake not closed")
		}
		c.Close()
	}
	// Slots are free again.
	time.Sleep(50 * time.Millisecond)
	if err := dialSoon(t, addr, []ssh.AuthMethod{ssh.PublicKeys(b.id.Signer)}); err != nil {
		t.Fatalf("after limit: %v", err)
	}
}

// waitLocked waits for the server to record the failures it is still
// processing: a handshake failure is counted after the client has given up,
// so the lockout can trail the last dial by a moment.
func waitLocked(t *testing.T, l *lockout, key string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !l.locked(key) {
		if time.Now().After(deadline) {
			t.Fatalf("%s was never locked out", key)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// dialSoon dials until it gets in or a few seconds pass. The server frees the
// pre-auth slot of a failed handshake a moment after the client has given up,
// so a dial right behind a failure can be refused for that reason alone.
func dialSoon(t *testing.T, addr string, auth []ssh.AuthMethod) error {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		c, _, _, err := rawDial(t, addr, auth, nil)
		if err == nil {
			c.Close()
			return nil
		}
		if time.Now().After(deadline) {
			return err
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestHostileLockout(t *testing.T) {
	leakCheck(t)
	var mu sync.Mutex
	now := time.Now()
	clock := func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		return now
	}
	advance := func(d time.Duration) {
		mu.Lock()
		now = now.Add(d)
		mu.Unlock()
	}
	_, b, srv, _ := pairUp(t, ServerOptions{LockoutThreshold: 3, LockoutBase: time.Minute, now: clock})
	addr := srv.Addr().String()
	noKey := []ssh.AuthMethod{ssh.Password("hunter2")}
	good := []ssh.AuthMethod{ssh.PublicKeys(b.id.Signer)}

	// Failures that offer no key lock the whole IP: even the pinned key is
	// cut off before the handshake.
	for i := 0; i < 40 && !srv.lock.locked("127.0.0.1"); i++ {
		if _, _, _, err := rawDial(t, addr, noKey, nil); err == nil {
			t.Fatal("password authenticated")
		}
		time.Sleep(10 * time.Millisecond)
	}
	waitLocked(t, srv.lock, "127.0.0.1")
	if _, _, _, err := rawDial(t, addr, good, nil); err == nil {
		t.Fatal("locked-out IP got in")
	}
	advance(61 * time.Second)
	if err := dialSoon(t, addr, good); err != nil {
		t.Fatalf("after lockout expired: %v", err)
	}

	// A removed (unpinned) key retrying locks out that key only: another
	// peer behind the same IP (NAT) still gets in.
	removed := []ssh.AuthMethod{ssh.PublicKeys(ed25519Signer(t))}
	for i := 0; i < 5; i++ {
		if _, _, _, err := rawDial(t, addr, removed, nil); err == nil {
			t.Fatal("unpinned key authenticated")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := dialSoon(t, addr, good); err != nil {
		t.Fatalf("one key's failures locked out the IP: %v", err)
	}

	// Rotating keys does not dodge the IP lock: 4x the threshold of keyed
	// failures locks the IP too.
	for i := 0; i < 80 && !srv.spray.locked("127.0.0.1"); i++ {
		_, _, _, _ = rawDial(t, addr, []ssh.AuthMethod{ssh.PublicKeys(ed25519Signer(t))}, nil)
		time.Sleep(10 * time.Millisecond)
	}
	waitLocked(t, srv.spray, "127.0.0.1")
	if _, _, _, err := rawDial(t, addr, good, nil); err == nil {
		t.Fatal("key spraying did not lock the IP")
	}
}

// TestLockoutTableAtCapacity: with the table full of live lockouts a new
// source is still tracked and locked (the oldest record makes room).
func TestLockoutTableAtCapacity(t *testing.T) {
	now := time.Unix(0, 0)
	l := newLockout(1, time.Hour, time.Hour, func() time.Time { return now })
	for i := 0; i < maxTracked; i++ {
		l.fail(fmt.Sprintf("10.%d.%d.%d", i>>16&255, i>>8&255, i&255))
		now = now.Add(time.Millisecond)
	}
	if len(l.m) != maxTracked || !l.locked("10.0.0.0") {
		t.Fatalf("table %d, first locked %v", len(l.m), l.locked("10.0.0.0"))
	}
	l.fail("192.0.2.1")
	if !l.locked("192.0.2.1") {
		t.Fatal("new source ignored when the table is full")
	}
	if len(l.m) > maxTracked {
		t.Fatalf("table grew to %d", len(l.m))
	}
	if l.locked("10.0.0.0") {
		t.Fatal("the oldest record was not the one evicted")
	}
	if !l.locked("10.0.0.1") {
		t.Fatal("a newer record was evicted")
	}
}

func TestLockoutBackoffGrows(t *testing.T) {
	now := time.Unix(0, 0)
	l := newLockout(2, time.Second, 4*time.Second, func() time.Time { return now })
	ip := "192.0.2.1"
	for strike, want := range []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 4 * time.Second} {
		l.fail(ip)
		l.fail(ip)
		if !l.locked(ip) {
			t.Fatalf("strike %d: not locked", strike)
		}
		now = now.Add(want - time.Millisecond)
		if !l.locked(ip) {
			t.Fatalf("strike %d: unlocked before %s", strike, want)
		}
		now = now.Add(2 * time.Millisecond)
		if l.locked(ip) {
			t.Fatalf("strike %d: still locked after %s", strike, want)
		}
	}
	l.ok(ip)
	if l.locked(ip) {
		t.Fatal("locked after ok")
	}
	if got := lockKey(&net.TCPAddr{IP: net.ParseIP("2001:db8:1:2:3:4:5:6"), Port: 1}); got != "2001:db8:1:2::/64" {
		t.Fatalf("lockKey v6 = %q", got)
	}
	if got := lockKey(&net.TCPAddr{IP: net.ParseIP("192.0.2.7"), Port: 1}); got != "192.0.2.7" {
		t.Fatalf("lockKey v4 = %q", got)
	}
}
