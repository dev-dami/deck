package peer

import (
	"context"
	"errors"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/dev-dami/deck/internal/deck/config"
)

// newTestService makes a Service with its own peers directory and config
// file.
func newTestService(t *testing.T, name string) *Service {
	t.Helper()
	dir := t.TempDir()
	s, err := NewService(ServiceOptions{Dir: filepath.Join(dir, "peers"), ConfigPath: filepath.Join(dir, "config.json"), Logger: quietLog})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	if err := s.Start(nil); err != nil {
		t.Fatal(err)
	}
	if err := s.SetName(name); err != nil {
		t.Fatal(err)
	}
	return s
}

// freeAddr is a loopback address with a port nothing listens on just now.
func freeAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()
	return addr
}

func TestServiceHotAppliesEnableAndListen(t *testing.T) {
	leakCheck(t)
	s := newTestService(t, "a")
	if s.Manager() != nil {
		t.Fatal("a Manager was made with peers off")
	}
	if err := s.SetEnabled(true); err != nil {
		t.Fatal(err)
	}
	snap := s.Snapshot()
	if !snap.Enabled || snap.Fingerprint == "" || snap.Running {
		t.Fatalf("on, nothing to run: %+v", snap)
	}
	addr := freeAddr(t)
	if err := s.SetListen(true, addr); err != nil {
		t.Fatal(err)
	}
	if snap = s.Snapshot(); snap.Listening != addr || !snap.Running || snap.Pending {
		t.Fatalf("listening without a restart: %+v", snap)
	}
	moved := freeAddr(t)
	if err := s.SetListen(true, moved); err != nil {
		t.Fatal(err)
	}
	if snap = s.Snapshot(); snap.Listening != moved {
		t.Fatalf("listener not moved: %+v", snap)
	}
	if err := s.SetEnabled(false); err != nil {
		t.Fatal(err)
	}
	if snap = s.Snapshot(); snap.Listening != "" || snap.Running {
		t.Fatalf("still running after off: %+v", snap)
	}
}

func TestServiceWaitWakesOnChange(t *testing.T) {
	s := newTestService(t, "a")
	v := s.Snapshot().Version
	done := make(chan uint64, 1)
	go func() { done <- s.Wait(ctxT(t), v) }()
	if err := s.SetEnabled(true); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-done:
		if got == v {
			t.Fatal("Wait returned the version it waited past")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Wait did not wake")
	}
}

func TestServicePairWaitDialAndPin(t *testing.T) {
	leakCheck(t)
	fastKDF(t)
	a, b := newTestService(t, "a"), newTestService(t, "b")
	w, err := a.StartPairing("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	if !a.Snapshot().Pairing {
		t.Fatal("pairing not shown")
	}
	res, err := b.PairDial(ctxT(t), w.Addr, w.Code, "")
	if err != nil {
		t.Fatal(err)
	}
	out := <-w.Done
	if out.Err != nil {
		t.Fatal(out.Err)
	}
	if res.PeerFingerprint != w.Fingerprint || out.Result.PeerFingerprint != res.OwnFingerprint {
		t.Fatal("the two sides do not show the same fingerprints")
	}
	if err := a.Pin(PinRequest{Name: out.Result.PeerName, Result: out.Result}); err != nil {
		t.Fatal(err)
	}
	if err := b.Pin(PinRequest{Name: "a", Addr: w.Addr, Dial: true, Result: res}); err != nil {
		t.Fatal(err)
	}
	snap := b.Snapshot()
	if len(snap.Peers) != 1 || !snap.Peers[0].Pinned || !snap.Peers[0].Dial || snap.Peers[0].Fingerprint != w.Fingerprint {
		t.Fatalf("b's peers: %+v", snap.Peers)
	}

	// Another key under the same name needs Replace.
	other := newTestPeer(t, "x")
	res.PeerKey, res.PeerFingerprint = other.id.PublicKey(), other.id.Fingerprint()
	if err := b.Pin(PinRequest{Name: "a", Addr: w.Addr, Result: res}); !errors.Is(err, ErrPinTaken) {
		t.Fatalf("pin over another key: %v", err)
	}
	if err := b.Pin(PinRequest{Name: "a", Addr: w.Addr, Replace: true, Result: res}); err != nil {
		t.Fatal(err)
	}

	if err := b.Remove("a"); err != nil {
		t.Fatal(err)
	}
	if snap := b.Snapshot(); len(snap.Peers) != 0 {
		t.Fatalf("removed peer still listed: %+v", snap.Peers)
	}
}

func TestServicePairingCancel(t *testing.T) {
	leakCheck(t)
	s := newTestService(t, "a")
	w, err := s.StartPairing("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	w.Cancel()
	out := <-w.Done
	if !errors.Is(out.Err, context.Canceled) {
		t.Fatalf("cancelled pairing ended with %v", out.Err)
	}
}

func TestServicePinRefusesOwnName(t *testing.T) {
	s := newTestService(t, "a")
	other := newTestPeer(t, "x")
	res := PairResult{PeerKey: other.id.PublicKey(), PeerFingerprint: other.id.Fingerprint()}
	if err := s.Pin(PinRequest{Name: "a", Result: res}); err == nil {
		t.Fatal("pinned a peer under this Deck's own name")
	}
}

func TestServicePendingAfterOutsideEdit(t *testing.T) {
	s := newTestService(t, "a")
	if _, err := config.UpdatePeers(s.cfgPath, func(p *config.Peers) bool { p.Enabled = true; return true }); err != nil {
		t.Fatal(err)
	}
	if !s.Snapshot().Pending {
		t.Fatal("an outside edit is not pending")
	}
	if err := s.Reload(); err != nil {
		t.Fatal(err)
	}
	if s.Snapshot().Pending {
		t.Fatal("still pending after Reload")
	}
}

func TestManagerApplyBeforeAndAfterStart(t *testing.T) {
	leakCheck(t)
	a := newTestPeer(t, "a")
	m, err := NewManager(ManagerOptions{Dir: a.dir, Logger: quietLog, SweepInterval: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Stop()
	if err := m.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if m.Running() {
		t.Fatal("running with no config")
	}
	if err := m.Apply(peersCfg("a", true, nil)); err != nil {
		t.Fatal(err)
	}
	if m.Addr() == "" || !m.Running() {
		t.Fatal("Apply did not start the listener")
	}
	if err := m.Apply(&config.Peers{Enabled: true, Name: "renamed"}); err != nil {
		t.Fatal(err)
	}
	if m.Addr() != "" || m.Running() || m.Name() != "renamed" {
		t.Fatalf("after Apply without a listener: addr %q running %v name %q", m.Addr(), m.Running(), m.Name())
	}
}
