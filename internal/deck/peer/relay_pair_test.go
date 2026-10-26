package peer

import (
	"errors"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/dev-dami/deck/internal/deck/config"
)

// pairTrio makes three Decks: r listens with relaying set to relay; a and b
// are paired with r and dial it, but are not paired with each other. b
// waits for codes on a loopback address of its own (it has no listener).
func pairTrio(t *testing.T, relay bool) (a, b, r *Service) {
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
	setPeers(t, r, func(p *config.Peers) {
		p.Relay = relay
		p.Remote = append(p.Remote, config.PeerRemote{Name: "a"}, config.PeerRemote{Name: "b"})
	})
	pairAddr := freeAddr(t)
	setPeers(t, b, func(p *config.Peers) {
		p.Listen.Addr = pairAddr
		p.Remote = append(p.Remote, config.PeerRemote{Name: "r", Addr: addr, Dial: true})
	})
	setPeers(t, a, func(p *config.Peers) {
		p.Remote = append(p.Remote, config.PeerRemote{Name: "r", Addr: addr, Dial: true})
	})
	waitFor(t, 10*time.Second, "r's links from a and b", func() bool {
		return r.Manager().directLink("a") != nil && r.Manager().directLink("b") != nil
	})
	return a, b, r
}

// unreachable is an address nothing listens on.
func unreachable(t *testing.T) string { return freeAddr(t) }

func findPeer(t *testing.T, s *Service, name string) config.PeerRemote {
	t.Helper()
	p, err := config.LoadPeers(s.cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	r, ok := p.Find(name)
	if !ok {
		t.Fatalf("no row for %q in %+v", name, p.Remote)
	}
	return r
}

// TestPairThroughRelay: two Decks that cannot reach each other pair through
// a Deck both are paired with, and are then linked through it with no
// setting changed by hand.
func TestPairThroughRelay(t *testing.T) {
	leakCheck(t)
	a, b, r := pairTrio(t, true)
	w, err := b.StartPairListen(ctxT(t))
	if err != nil {
		t.Fatal(err)
	}
	defer w.Cancel()
	if w.Shared {
		t.Fatal("a wait on a listener of its own is not Shared")
	}
	res, err := a.PairDial(ctxT(t), unreachable(t), w.Code, w.Fingerprint)
	if err != nil {
		t.Fatalf("pair through r: %v", err)
	}
	if res.Via != "r" || !res.Dialed || res.PeerFingerprint != identityOf(t, b).Fingerprint() {
		t.Fatalf("a's result: %+v", res)
	}
	var out PairOutcome
	select {
	case out = <-w.Done:
	case <-time.After(10 * time.Second):
		t.Fatal("b got no outcome")
	}
	if out.Err != nil || out.Result.Via != "r" || out.Result.Dialed || out.Result.PeerFingerprint != identityOf(t, a).Fingerprint() {
		t.Fatalf("b's outcome: %+v", out)
	}

	if err := a.Pin(PinRequest{Name: "b", Addr: unreachable(t), Dial: true, Result: res}); err != nil {
		t.Fatal(err)
	}
	if err := b.Pin(PinRequest{Name: "a", Result: out.Result}); err != nil {
		t.Fatal(err)
	}
	if row := findPeer(t, a, "b"); row.Via != "r" || row.Addr != "" || !row.Dial {
		t.Fatalf("a's row for b: %+v", row)
	}
	if row := findPeer(t, b, "r"); !row.Relay {
		t.Fatalf("b's row for r: %+v, want relay on", row)
	}
	waitFor(t, 20*time.Second, "a's relayed link to b", relayedUp(a))
	if r.Snapshot().Relaying != 1 {
		t.Fatalf("r relays %d links, want 1", r.Snapshot().Relaying)
	}
}

// TestPairThroughRelayRefusals: each Deck that stands in the way is named.
func TestPairThroughRelayRefusals(t *testing.T) {
	leakCheck(t)
	a, b, r := pairTrio(t, false)
	fpB := identityOf(t, b).Fingerprint()
	const code = "ABCDE-23456"

	_, err := a.PairDial(ctxT(t), unreachable(t), code, fpB)
	var miss *RelayMissError
	if !errors.As(err, &miss) || !errors.Is(err, ErrPairNoAnswer) || !errors.Is(err, ErrPairRelay) {
		t.Fatalf("relaying off: %v", err)
	}
	if txt := PairErrText(err, "x:1"); !strings.Contains(txt, "r does not relay") {
		t.Fatalf("relaying off reads %q", txt)
	}

	if err := r.SetRelay(true); err != nil {
		t.Fatal(err)
	}
	_, err = a.PairDial(ctxT(t), unreachable(t), code, fpB)
	if txt := PairErrText(err, "x:1"); !strings.Contains(txt, "not waiting for a code") {
		t.Fatalf("b not waiting reads %q (%v)", txt, err)
	}

	// A wrong code reaches b: it is b's answer, not the relay's.
	w, err := b.StartPairListen(ctxT(t))
	if err != nil {
		t.Fatal(err)
	}
	defer w.Cancel()
	if _, err := a.PairDial(ctxT(t), unreachable(t), code, fpB); !errors.Is(err, ErrPairWrong) {
		t.Fatalf("wrong code through r: %v", err)
	}
	// Without the invite's fingerprint there is no relay to try.
	if _, err := a.PairDial(ctxT(t), unreachable(t), w.Code, ""); errors.Is(err, ErrPairRelay) || !errors.Is(err, ErrPairNoAnswer) {
		t.Fatalf("no fingerprint: %v", err)
	}
}

// TestRelaySettings: the three relay settings each change one row, and the
// refusals a link meets read as what to fix.
func TestRelaySettings(t *testing.T) {
	leakCheck(t)
	a, b, r := pairTrio(t, false)
	pin := func(on, other *Service, name string) {
		if err := OpenStore(on.dir).Pin(NewPinned(name, "", "", identityOf(t, other).PublicKey())); err != nil {
			t.Fatal(err)
		}
	}
	pin(a, b, "b")
	pin(b, a, "a")

	if err := a.SetVia("b", "b"); err == nil {
		t.Fatal("b relays its own link")
	}
	if err := a.SetVia("b", "nobody"); err == nil {
		t.Fatal("via an unpaired Deck")
	}
	if err := a.SetVia("b", "r"); err != nil {
		t.Fatal(err)
	}
	if row := findPeer(t, a, "b"); row.Via != "r" || !row.Dial {
		t.Fatalf("a's row for b: %+v", row)
	}
	waitFor(t, 20*time.Second, "relaying off named", func() bool {
		st := a.Manager().Supervisor("b").Status()
		return strings.Contains(RelayErrText("r", "b", st.LastError), "r does not relay")
	})
	if err := r.SetRelay(true); err != nil {
		t.Fatal(err)
	}
	if !r.Snapshot().Relay {
		t.Fatal("r's snapshot: relay off")
	}
	waitFor(t, 20*time.Second, "b's refusal named", func() bool {
		st := a.Manager().Supervisor("b").Status()
		return strings.Contains(RelayErrText("r", "b", st.LastError), "on b, let r relay to it")
	})
	if err := b.SetMayRelay("r", true); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 20*time.Second, "a's relayed link to b", relayedUp(a))

	if err := a.SetVia("b", ""); err != nil {
		t.Fatal(err)
	}
	if row := findPeer(t, a, "b"); row.Via != "" || row.Dial {
		t.Fatalf("a's row for b, direct with no address: %+v", row)
	}
}

// TestHostileRelayedPairing: a relayed pairing is taken only while b waits
// for a code, and only as PairUser with the key the relay named.
func TestHostileRelayedPairing(t *testing.T) {
	leakCheck(t)
	a, b, r := pairTrio(t, true)
	rToB := r.Manager().directLink("b")
	ida, idb, idr := identityOf(t, a), identityOf(t, b), identityOf(t, r)
	from := func(fp string) []byte { return ssh.Marshal(relayedReq{Name: "a", Fingerprint: fp}) }
	if got := openReason(t, rToB, RelayedPairChannelType, from(ida.Fingerprint())); got != ssh.Prohibited {
		t.Fatalf("relayed pairing while b does not wait: reason %v", got)
	}
	w, err := b.StartPairListen(ctxT(t))
	if err != nil {
		t.Fatal(err)
	}
	defer w.Cancel()
	for name, payload := range map[string][]byte{
		"bad payload":    {1, 2},
		"no key":         from(""),
		"from the relay": from(idr.Fingerprint()),
	} {
		if got := openReason(t, rToB, RelayedPairChannelType, payload); got != ssh.Prohibited {
			t.Errorf("relayed pairing %s: reason %v", name, got)
		}
	}
	login := func(user string, signer ssh.Signer) error {
		ch, err := rToB.openChannel(ctxT(t), RelayedPairChannelType, from(ida.Fingerprint()))
		if err != nil {
			return err
		}
		raw := newChanConn(ch, relayAddr("test"), nil)
		defer raw.Close()
		sc, chans, reqs, err := ssh.NewClientConn(raw, "b", &ssh.ClientConfig{
			Config: baseSSHConfig(), User: user, Auth: []ssh.AuthMethod{ssh.PublicKeys(signer)},
			HostKeyCallback: pinnedHostKey("b", idb.PublicKey()), Timeout: 5 * time.Second,
		})
		if err != nil {
			return err
		}
		go ssh.DiscardRequests(reqs)
		go func() {
			for nc := range chans {
				_ = nc.Reject(ssh.Prohibited, "test")
			}
		}()
		return sc.Close()
	}
	if err := login("deck", ida.Signer); err == nil {
		t.Error("a relayed pairing logged in as a peer link")
	}
	if err := login(PairUser, idr.Signer); err == nil {
		t.Error("the relay paired with its own key on a pairing it said came from a")
	}
	if err := login(PairUser, ida.Signer); err != nil {
		t.Errorf("the named key as PairUser: %v", err)
	}
}
