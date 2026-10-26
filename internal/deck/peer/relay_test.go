package peer

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/dev-dami/deck/internal/deck/config"
)

// setPeers edits s's peers section with fn and puts it in effect.
func setPeers(t *testing.T, s *Service, fn func(p *config.Peers)) {
	t.Helper()
	if _, err := config.UpdatePeers(s.cfgPath, func(p *config.Peers) bool { fn(p); return true }); err != nil {
		t.Fatal(err)
	}
	if err := s.Reload(); err != nil {
		t.Fatal(err)
	}
}

func identityOf(t *testing.T, s *Service) *Identity {
	t.Helper()
	id, err := LoadIdentity(s.dir)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// relayTrio makes three Decks: r listens, with peers.relay set to relay; a
// and b both dial r, and are pinned with each other but have no address for
// each other. a dials b via r, and b lets r relay links to it. It returns
// once r has both links.
func relayTrio(t *testing.T, relay bool) (a, b, r *Service) {
	t.Helper()
	r, a, b = newService(t, "r", ServiceOptions{}), newService(t, "a", ServiceOptions{}), newService(t, "b", ServiceOptions{})
	if err := r.SetListen(true, freeAddr(t)); err != nil {
		t.Fatal(err)
	}
	addr := r.Snapshot().Listening
	pin := func(on, other *Service, name, addr string) {
		if err := OpenStore(on.dir).Pin(NewPinned(name, addr, "", identityOf(t, other).PublicKey())); err != nil {
			t.Fatal(err)
		}
	}
	pin(r, a, "a", "")
	pin(r, b, "b", "")
	pin(a, r, "r", addr)
	pin(b, r, "r", addr)
	pin(a, b, "b", "")
	pin(b, a, "a", "")
	setPeers(t, r, func(p *config.Peers) { p.Relay = relay })
	setPeers(t, b, func(p *config.Peers) {
		p.Remote = append(p.Remote, config.PeerRemote{Name: "r", Addr: addr, Dial: true, Relay: true})
	})
	setPeers(t, a, func(p *config.Peers) {
		p.Remote = append(p.Remote, config.PeerRemote{Name: "r", Addr: addr, Dial: true},
			config.PeerRemote{Name: "b", Dial: true, Via: "r"})
	})
	waitFor(t, 10*time.Second, "r's links from a and b", func() bool {
		return r.Manager().directLink("a") != nil && r.Manager().directLink("b") != nil
	})
	return a, b, r
}

// tapRelay records every byte r's splices copy.
func tapRelay(r *Service) func() []byte {
	var mu sync.Mutex
	var buf bytes.Buffer
	rl := r.Manager().relays
	rl.mu.Lock()
	rl.tap = func(p []byte) {
		mu.Lock()
		buf.Write(p)
		mu.Unlock()
	}
	rl.mu.Unlock()
	return func() []byte {
		mu.Lock()
		defer mu.Unlock()
		return bytes.Clone(buf.Bytes())
	}
}

// relayServerOf is the Server that serves links relayed to s, or nil.
func relayServerOf(s *Service) *Server {
	m := s.Manager()
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.rsrv
}

func relayedUp(a *Service) func() bool {
	return func() bool {
		sup := a.Manager().Supervisor("b")
		return sup != nil && sup.Status().State == StateConnected
	}
}

func TestRelayLinksTwoDecksThatCannotReachEachOther(t *testing.T) {
	leakCheck(t)
	a, b, r := relayTrio(t, true)
	spliced := tapRelay(r)
	waitFor(t, 20*time.Second, "a's relayed link to b", relayedUp(a))
	if st := a.Manager().Supervisor("b").Status(); st.Via != "r" {
		t.Fatalf("a's link to b: %+v, want via r", st)
	}
	ctx := ctxT(t)

	out, err := a.PeerStatus(ctx, "b")
	if err != nil || !strings.Contains(out, `"b"`) {
		t.Fatalf("peer.status over the relay = %q, %v", out, err)
	}
	const marker = "relay-plaintext-marker-5d1c"
	if reached, err := a.PostChat(ctx, "claude1", marker); err != nil || !slices.Contains(reached, "b") {
		t.Fatalf("a posts: %v, %v", reached, err)
	}
	if got := b.Team().Chat(); len(got) != 1 || got[0].Author() != "a/claude1" || got[0].Text != marker {
		t.Fatalf("b's chat = %+v", got)
	}
	// b has no address for a and no supervisor for it: it answers over the
	// relayed link a opened.
	if reached, err := b.SendChat(ctx, "got it"); err != nil || !slices.Contains(reached, "a") {
		t.Fatalf("b answers over the relayed link: %v, %v", reached, err)
	}

	// Both ends show the link as relayed via r.
	find := func(s *Service, name string) PeerState {
		for _, p := range s.Snapshot().Peers {
			if p.Name == name {
				return p
			}
		}
		t.Fatalf("no peer %q", name)
		return PeerState{}
	}
	if ps := find(a, "b"); ps.Link.Via != "r" || ps.Via != "r" {
		t.Fatalf("a's view of b: %+v", ps)
	}
	if ps := find(b, "a"); ps.Link.Via != "r" || ps.Link.Inbound != 1 {
		t.Fatalf("b's view of a: %+v", ps.Link)
	}

	// r copied the inner SSH session, and none of it in the clear.
	got := spliced()
	if !bytes.Contains(got, []byte(sshVersion)) {
		t.Fatalf("r's tap saw no inner handshake (%d bytes)", len(got))
	}
	if bytes.Contains(got, []byte(marker)) || bytes.Contains(got, []byte("console.post")) {
		t.Fatal("r saw the inner traffic in the clear")
	}
	if r.Manager().relays.bytes.Load() == 0 {
		t.Fatal("no bytes counted")
	}

	// r turns relaying off: the splice ends and a cannot dial b again.
	setPeers(t, r, func(p *config.Peers) { p.Relay = false })
	waitFor(t, 10*time.Second, "a's link to b down", func() bool { return !relayedUp(a)() })
	waitFor(t, 20*time.Second, "a's dial refused", func() bool {
		return strings.Contains(a.Manager().Supervisor("b").Status().LastError, "relaying is off")
	})
}

func TestRelayOffByDefault(t *testing.T) {
	leakCheck(t)
	a, b, _ := relayTrio(t, false)
	waitFor(t, 20*time.Second, "a's dial refused", func() bool {
		return strings.Contains(a.Manager().Supervisor("b").Status().LastError, "relaying is off")
	})
	if relayServerOf(b) != nil {
		t.Fatal("b served a relayed link")
	}
}

// openReason opens a channel of typ on c and returns why it was refused
// (0 when it was accepted).
func openReason(t *testing.T, c *Conn, typ string, payload []byte) ssh.RejectionReason {
	t.Helper()
	ch, err := c.openChannel(ctxT(t), typ, payload)
	if err == nil {
		ch.Close()
		return 0
	}
	var oe *ssh.OpenChannelError
	if !errors.As(err, &oe) {
		t.Fatalf("open %s: %v", typ, err)
	}
	return oe.Reason
}

// TestHostileRelayChannels: the relay types are refused unless every rule
// holds, on the relay and on the target, and a relayed link logs in only
// with the key the relay named.
func TestHostileRelayChannels(t *testing.T) {
	leakCheck(t)
	a, b, r := relayTrio(t, true)
	waitFor(t, 20*time.Second, "a's relayed link to b", relayedUp(a))
	aToR := a.Manager().directLink("r")
	rToB := r.Manager().directLink("b")
	ida, idb, idr := identityOf(t, a), identityOf(t, b), identityOf(t, r)
	req := func(fp string) []byte { return ssh.Marshal(relayReq{Target: fp}) }
	from := func(fp string) []byte { return ssh.Marshal(relayedReq{Name: "a", Fingerprint: fp}) }

	// On the relay.
	for name, tc := range map[string]struct {
		payload []byte
		want    ssh.RejectionReason
	}{
		"bad payload":   {[]byte("x"), ssh.Prohibited},
		"unknown peer":  {req("SHA256:nope"), ssh.ConnectionFailed},
		"to itself":     {req(ida.Fingerprint()), ssh.ConnectionFailed},
		"to the relay":  {req(idr.Fingerprint()), ssh.ConnectionFailed},
		"empty payload": {nil, ssh.Prohibited},
	} {
		if got := openReason(t, aToR, RelayChannelType, tc.payload); got != tc.want {
			t.Errorf("relay %s: reason %v, want %v", name, got, tc.want)
		}
	}
	c := newTestPeer(t, "c")
	if err := OpenStore(r.dir).Pin(NewPinned("c", "", "", c.id.PublicKey())); err != nil {
		t.Fatal(err)
	}
	if got := openReason(t, aToR, RelayChannelType, req(c.id.Fingerprint())); got != ssh.ConnectionFailed {
		t.Errorf("relay to a peer with no link: reason %v", got)
	}
	// The relay's per-peer cap: a already has one relayed link.
	rl := r.Manager().relays
	rl.mu.Lock()
	rl.maxPer = 1
	rl.mu.Unlock()
	if got := openReason(t, aToR, RelayChannelType, req(idb.Fingerprint())); got != ssh.ResourceShortage {
		t.Errorf("relay over the cap: reason %v", got)
	}
	rl.mu.Lock()
	rl.maxPer = defaultMaxRelaysPerPeer
	rl.mu.Unlock()
	// a relays nothing: relay@deck to a is refused, and relayed@deck from a
	// peer a has not marked as a relay.
	rToA := r.Manager().directLink("a")
	if got := openReason(t, rToA, RelayChannelType, req(idb.Fingerprint())); got != ssh.Prohibited {
		t.Errorf("relay through a, relay off: reason %v", got)
	}
	if got := openReason(t, rToA, RelayedChannelType, ssh.Marshal(relayedReq{Name: "b", Fingerprint: idb.Fingerprint()})); got != ssh.Prohibited {
		t.Errorf("relayed into a from r, not a relay there: reason %v", got)
	}

	// On the target.
	for name, tc := range map[string]struct {
		payload []byte
		want    ssh.RejectionReason
	}{
		"bad payload":     {[]byte{1, 2}, ssh.Prohibited},
		"unpinned origin": {from("SHA256:nope"), ssh.Prohibited},
		"from the relay":  {from(idr.Fingerprint()), ssh.Prohibited},
	} {
		if got := openReason(t, rToB, RelayedChannelType, tc.payload); got != tc.want {
			t.Errorf("relayed %s: reason %v, want %v", name, got, tc.want)
		}
	}
	// The relay named a's key: no other key may log in, even one pinned on b.
	login := func(signer ssh.Signer) error {
		ch, err := rToB.openChannel(ctxT(t), RelayedChannelType, from(ida.Fingerprint()))
		if err != nil {
			return err
		}
		raw := newChanConn(ch, relayAddr("test"), nil)
		defer raw.Close()
		sc, chans, reqs, err := ssh.NewClientConn(raw, "b", &ssh.ClientConfig{
			Config: baseSSHConfig(), User: "deck", Auth: []ssh.AuthMethod{ssh.PublicKeys(signer)},
			HostKeyCallback: pinnedHostKey("b", idb.PublicKey()), Timeout: 5 * time.Second,
		})
		if err != nil {
			return err
		}
		defer sc.Close()
		go ssh.DiscardRequests(reqs)
		go func() {
			for nc := range chans {
				_ = nc.Reject(ssh.Prohibited, "test")
			}
		}()
		// The inner link takes mcp channels only, never a relay type.
		for _, typ := range []string{RelayChannelType, RelayedChannelType, RelayPairChannelType, RelayedPairChannelType} {
			ch, _, err := sc.OpenChannel(typ, from(ida.Fingerprint()))
			var oe *ssh.OpenChannelError
			if err == nil || !errors.As(err, &oe) || oe.Reason != ssh.UnknownChannelType {
				if ch != nil {
					ch.Close()
				}
				t.Errorf("inner link accepted %s: %v", typ, err)
			}
		}
		return nil
	}
	if err := login(idr.Signer); err == nil {
		t.Error("the relay logged in with its own key on a link it said came from a")
	}
	if err := login(ida.Signer); err != nil {
		t.Errorf("the named key: %v", err)
	}
	// The relayed link the supervisor keeps is inner too: b cannot ask a to
	// relay over it.
	waitFor(t, 5*time.Second, "the test logins gone", func() bool { return len(relayServerOf(b).Conns()) == 1 })
	for _, c := range relayServerOf(b).Conns() {
		if got := openReason(t, c, RelayChannelType, req(idr.Fingerprint())); got != ssh.UnknownChannelType {
			t.Errorf("relay over a relayed link: reason %v", got)
		}
	}

	// b stops letting r relay to it.
	setPeers(t, b, func(p *config.Peers) {
		for i := range p.Remote {
			if p.Remote[i].Name == "r" {
				p.Remote[i].Relay = false
			}
		}
	})
	if got := openReason(t, rToB, RelayedChannelType, from(ida.Fingerprint())); got != ssh.Prohibited {
		t.Errorf("relayed from a peer no longer a relay: reason %v", got)
	}
	waitFor(t, 10*time.Second, "the relayed link closed", func() bool { return len(relayServerOf(b).Conns()) == 0 })

	// r unpins a: its next relay request is refused before the sweep drops
	// the link.
	if _, err := OpenStore(r.dir).Remove("a"); err != nil {
		t.Fatal(err)
	}
	if got := openReason(t, aToR, RelayChannelType, req(idb.Fingerprint())); got != ssh.Prohibited {
		t.Errorf("relay for an unpinned peer: reason %v", got)
	}
}

// TestRemoveRelayRemote: removing the relay leaves a config that loads; a
// remote dialed only through it is no longer dialed.
func TestRemoveRelayRemote(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if _, err := config.UpdatePeers(path, func(p *config.Peers) bool {
		p.Remote = []config.PeerRemote{{Name: "r", Addr: "127.0.0.1:7722", Dial: true},
			{Name: "b", Dial: true, Via: "r"}, {Name: "c", Addr: "127.0.0.1:7723", Dial: true, Via: "r"}}
		return true
	}); err != nil {
		t.Fatal(err)
	}
	if removed, err := RemoveRemote(path, "r"); err != nil || !removed {
		t.Fatalf("RemoveRemote = %v, %v", removed, err)
	}
	p, err := config.LoadPeers(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Remote) != 2 || p.Remote[0].Via != "" || p.Remote[0].Dial || p.Remote[1].Via != "" || !p.Remote[1].Dial {
		t.Fatalf("remotes = %+v", p.Remote)
	}
}

func TestRelayerCaps(t *testing.T) {
	rl := newRelayer(2, 1, time.Minute)
	r1, ok := rl.acquire("x", "y")
	if !ok {
		t.Fatal("first")
	}
	if _, ok := rl.acquire("x", "z"); ok {
		t.Fatal("per-peer cap not kept")
	}
	if _, ok := rl.acquire("z", "w"); !ok {
		t.Fatal("second")
	}
	if _, ok := rl.acquire("u", "v"); ok {
		t.Fatal("total cap not kept")
	}
	r1()
	r1() // once only
	if _, ok := rl.acquire("x", "y"); !ok {
		t.Fatal("slot not given back")
	}
	if rl.total != 2 || len(rl.per) != 4 {
		t.Fatalf("counts: total %d, per %v", rl.total, rl.per)
	}
}

// TestRelaySpliceIdle: a splice with no traffic either way ends after the
// idle time, and closes both channels.
func TestRelaySpliceIdle(t *testing.T) {
	leakCheck(t)
	_, b, _, pinA := pairUp(t, ServerOptions{})
	ca, cb := b.dial(t, pinA, nil), b.dial(t, pinA, nil)
	x, err := ca.openChannel(context.Background(), ChannelType, nil)
	if err != nil {
		t.Fatal(err)
	}
	y, err := cb.openChannel(context.Background(), ChannelType, nil)
	if err != nil {
		t.Fatal(err)
	}
	rl := newRelayer(0, 0, 100*time.Millisecond)
	done := make(chan struct{})
	go func() {
		rl.run(x, y)
		close(done)
	}()
	waitDone(t, done, 5*time.Second, "idle splice")
}
