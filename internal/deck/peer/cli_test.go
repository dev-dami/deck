package peer

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dev-dami/deck/internal/deck/config"
)

// syncBuf is a goroutine-safe output buffer.
type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func TestCLIPairAddPingRemove(t *testing.T) {
	leakCheck(t)
	fastKDF(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	vpsCfg := filepath.Join(t.TempDir(), "config.json")
	pcCfg := filepath.Join(t.TempDir(), "config.json")
	for path, name := range map[string]string{vpsCfg: "vps", pcCfg: "pc"} {
		cfg := config.Defaults()
		cfg.Peers = &config.Peers{Name: name}
		if err := config.SaveTo(path, cfg); err != nil {
			t.Fatal(err)
		}
	}
	vpsOut, pcOut := &syncBuf{}, &syncBuf{}
	vps := CLI{Dir: t.TempDir(), ConfigPath: vpsCfg, Out: vpsOut, In: strings.NewReader("yes\n")}
	pc := CLI{Dir: t.TempDir(), ConfigPath: pcCfg, Out: pcOut}

	ready := make(chan [2]string, 1)
	pairErr := make(chan error, 1)
	go func() {
		pairErr <- vps.Pair(context.Background(), PairArgs{Addr: "127.0.0.1:0", Expiry: 10 * time.Second,
			Ready: func(addr, code string) { ready <- [2]string{addr, code} }})
	}()
	var addr, code string
	select {
	case r := <-ready:
		addr, code = r[0], r[1]
	case err := <-pairErr:
		t.Fatalf("pair: %v", err)
	}
	if !strings.Contains(vpsOut.String(), code) || !strings.Contains(vpsOut.String(), "deck peer add vps") {
		t.Fatalf("pair output:\n%s", vpsOut)
	}

	// Without --yes and no answer, nothing is pinned on the dialing side.
	if err := pc.Add(ctxT(t), AddArgs{Name: "vps", Addr: addr, Code: code}); err == nil || !strings.Contains(err.Error(), "not pinned") {
		t.Fatalf("add without confirmation: %v", err)
	}
	if err := <-pairErr; err != nil {
		t.Fatalf("pair side: %v", err)
	}
	// The code was single use; the listener pinned pc after its "yes".
	if p, ok, _ := OpenStore(vps.Dir).Get("pc"); !ok || p.Addr != "" {
		t.Fatalf("listener pin: %+v %v", p, ok)
	}
	if _, ok, _ := OpenStore(pc.Dir).Get("vps"); ok {
		t.Fatal("dialer pinned without confirmation")
	}

	// Pair again, confirming with --yes.
	go func() {
		pairErr <- vps.Pair(context.Background(), PairArgs{Addr: "127.0.0.1:0", Yes: true, Expiry: 10 * time.Second,
			Ready: func(addr, code string) { ready <- [2]string{addr, code} }})
	}()
	r := <-ready
	addr, code = r[0], r[1]
	if err := pc.Add(ctxT(t), AddArgs{Name: "vps", Addr: addr, Code: code, Yes: true}); err != nil {
		t.Fatalf("add: %v\n%s", err, pcOut)
	}
	if err := <-pairErr; err != nil {
		t.Fatal(err)
	}
	vpsID, _ := LoadIdentity(vps.Dir)
	pcID, _ := LoadIdentity(pc.Dir)
	out := pcOut.String()
	if !strings.Contains(out, "their fingerprint:  "+vpsID.Fingerprint()) || !strings.Contains(out, "this Deck:          "+pcID.Fingerprint()) {
		t.Fatalf("add output lacks fingerprints:\n%s", out)
	}
	cfg, err := config.LoadFrom(pcCfg)
	if err != nil {
		t.Fatal(err)
	}
	if rem, ok := cfg.Peers.Find("vps"); !ok || rem.Addr != addr || !rem.Dial || cfg.Peers.Enabled {
		t.Fatalf("config remote: %+v %v enabled=%v", rem, ok, cfg.Peers.Enabled)
	}

	// Serve the vps side for ping and list.
	srv, err := Listen("127.0.0.1:0", ServerOptions{Identity: vpsID, Store: OpenStore(vps.Dir), Name: "vps", Logger: quietLog})
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	pin, _, _ := OpenStore(pc.Dir).Get("vps")
	pin.Addr = srv.Addr().String()
	if err := OpenStore(pc.Dir).Pin(pin); err != nil {
		t.Fatal(err)
	}
	pcOut.mu.Lock()
	pcOut.b.Reset()
	pcOut.mu.Unlock()
	if err := pc.Ping(ctxT(t), "vps"); err != nil {
		t.Fatal(err)
	}
	if out := pcOut.String(); !strings.Contains(out, "pong from vps") || !strings.Contains(out, "search, execute") || !strings.Contains(out, `"name":"vps"`) {
		t.Fatalf("ping output:\n%s", out)
	}
	if err := pc.List(ctxT(t), true); err != nil {
		t.Fatal(err)
	}
	if out := pcOut.String(); !strings.Contains(out, "reachable") || !strings.Contains(out, vpsID.Fingerprint()) {
		t.Fatalf("list output:\n%s", out)
	}
	if err := pc.Fingerprint(); err != nil || !strings.Contains(pcOut.String(), "pc  "+pcID.Fingerprint()) {
		t.Fatalf("fingerprint: %v\n%s", err, pcOut)
	}
	if err := pc.Ping(ctxT(t), "nobody"); err == nil {
		t.Fatal("ping of an unknown peer")
	}

	if err := pc.Remove("vps"); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := OpenStore(pc.Dir).Get("vps"); ok {
		t.Fatal("still pinned")
	}
	cfg, _ = config.LoadFrom(pcCfg)
	if _, ok := cfg.Peers.Find("vps"); ok {
		t.Fatal("still in config")
	}
	if err := pc.Remove("vps"); err == nil {
		t.Fatal("removed twice")
	}
	// The removed peer's key no longer gets in on the vps side either once
	// it is removed there.
	if err := vps.Remove("pc"); err != nil {
		t.Fatal(err)
	}
	if _, err := Dial(ctxT(t), DialOptions{Identity: pcID, Peer: pin, Logger: quietLog}); err == nil {
		t.Fatal("removed peer could still connect")
	}
}

func TestCLIValidation(t *testing.T) {
	c := CLI{Dir: t.TempDir(), Out: &bytes.Buffer{}}
	for _, a := range []AddArgs{
		{Name: "Bad Name", Addr: "h:1", Code: "x"},
		{Name: "ok", Addr: "nohost", Code: "x"},
		{Name: "ok", Addr: "h:1", Code: "x", Role: "root"},
	} {
		if err := c.Add(context.Background(), a); err == nil {
			t.Fatalf("Add(%+v) accepted", a)
		}
	}
	if err := c.Pair(context.Background(), PairArgs{Addr: "127.0.0.1:0", Role: "root"}); err == nil {
		t.Fatal("bad role accepted")
	}
	if err := c.Pair(context.Background(), PairArgs{Addr: "127.0.0.1:0", Name: "../x"}); err == nil {
		t.Fatal("bad name accepted")
	}
	if err := c.List(context.Background(), false); err != nil || !strings.Contains(c.Out.(*bytes.Buffer).String(), "No peers") {
		t.Fatalf("empty list: %v", err)
	}
}
