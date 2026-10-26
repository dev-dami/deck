package peer

import (
	"bytes"
	"context"
	"net"
	"strings"
	"testing"
	"time"
)

// TestControlPairWithRunningDeck: deck peer pair, with a Deck running,
// has that Deck wait for the code on its own listener and pin the other.
func TestControlPairWithRunningDeck(t *testing.T) {
	leakCheck(t)
	a, b := newService(t, "a", ServiceOptions{}), newService(t, "b", ServiceOptions{})
	if err := a.SetListen(true, freeAddr(t)); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	cli := CLI{Dir: a.dir, ConfigPath: a.cfgPath, Out: &out}
	dialed := make(chan error, 1)
	err := cli.Pair(ctxT(t), PairArgs{Yes: true, Ready: func(addr, code string) {
		go func() {
			res, err := b.PairDial(context.Background(), addr, code, "")
			if err == nil {
				err = b.Pin(PinRequest{Name: "a", Addr: addr, Dial: true, Result: res})
			}
			dialed <- err
		}()
	}})
	if err != nil {
		t.Fatalf("pair: %v\n%s", err, out.String())
	}
	if err := <-dialed; err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "The Deck running here waits") || !strings.Contains(out.String(), "the running Deck has it in effect") {
		t.Fatalf("output:\n%s", out.String())
	}
	if _, ok, _ := OpenStore(a.dir).Get("b"); !ok {
		t.Fatal("b is not pinned on a")
	}
	waitFor(t, 10*time.Second, "b's link to a", func() bool {
		sup := b.Manager().Supervisor("a")
		return sup != nil && sup.Status().State == StateConnected
	})
}

// TestControlAddThroughRelay: deck peer add, with a Deck running, pairs
// through that Deck's relay when the invite's address does not answer.
func TestControlAddThroughRelay(t *testing.T) {
	leakCheck(t)
	a, b, _ := pairTrio(t, true)
	w, err := b.StartPairListen(ctxT(t))
	if err != nil {
		t.Fatal(err)
	}
	defer w.Cancel()
	host, port, _ := net.SplitHostPort(unreachable(t))
	inv := Invite{Host: host, Port: port, Code: w.Code, Fingerprint: w.Fingerprint, Name: "b"}
	var out bytes.Buffer
	cli := CLI{Dir: a.dir, ConfigPath: a.cfgPath, Out: &out}
	if err := cli.Add(ctxT(t), AddArgs{Invite: inv.Encode(), Yes: true}); err != nil {
		t.Fatalf("add: %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "Code accepted through r") || !strings.Contains(out.String(), "reach it through r") {
		t.Fatalf("output:\n%s", out.String())
	}
	if row := findPeer(t, a, "b"); row.Via != "r" || !row.Dial {
		t.Fatalf("a's row for b: %+v", row)
	}
}

// TestControlRelayApplies: deck peer relay changes the running Deck at once.
func TestControlRelayApplies(t *testing.T) {
	leakCheck(t)
	r := newService(t, "r", ServiceOptions{})
	var out bytes.Buffer
	cli := CLI{Dir: r.dir, ConfigPath: r.cfgPath, Out: &out}
	if err := cli.Relay([]string{"on"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "The Deck running here applied it") {
		t.Fatalf("output:\n%s", out.String())
	}
	if snap := r.Snapshot(); !snap.Relay || snap.Pending {
		t.Fatalf("relay %v, pending %v", snap.Relay, snap.Pending)
	}
	if p := r.Manager().peers.Load(); p == nil || !p.Relay {
		t.Fatal("the manager does not relay")
	}
}

// TestControlOneDeckServes: a second Deck on the same directory leaves the
// socket to the first, and a socket left behind is taken over.
func TestControlOneDeckServes(t *testing.T) {
	leakCheck(t)
	a := newService(t, "a", ServiceOptions{})
	b, err := NewService(ServiceOptions{Dir: a.dir, ConfigPath: a.cfgPath, Logger: quietLog})
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Start(nil); err != nil {
		t.Fatal(err)
	}
	b.Close()
	if ran, err := ReloadRunning(a.dir); !ran || err != nil {
		t.Fatalf("a's socket after b stopped: %v, %v", ran, err)
	}
	a.Close()
	if ran, _ := ReloadRunning(a.dir); ran {
		t.Fatal("a socket answers after a stopped")
	}
}
