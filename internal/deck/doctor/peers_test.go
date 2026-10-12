package doctor

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/dev-dami/deck/internal/deck/config"
	"github.com/dev-dami/deck/internal/deck/peer"
)

type fakePeers struct {
	fp, path string
	idErr    error
	privErr  error
	pinned   map[string]string
	probe    map[string]error
	probed   []string
}

func (f *fakePeers) Identity() (string, string, error) { return f.fp, f.path, f.idErr }
func (f *fakePeers) Private(string) error              { return f.privErr }
func (f *fakePeers) Pinned(name string) (string, bool, error) {
	fp, ok := f.pinned[name]
	return fp, ok, nil
}

func (f *fakePeers) Probe(_ context.Context, name string, _ time.Duration) (time.Duration, error) {
	f.probed = append(f.probed, name)
	return 12 * time.Millisecond, f.probe[name]
}

func TestNoPeersSectionWithoutConfig(t *testing.T) {
	r := &fakeRunner{}
	opts := baseOptions(t, r)
	opts.PeerChecker = &fakePeers{}
	out := runReport(t, opts)
	if strings.Contains(out, "peers") {
		t.Fatalf("peers section without peers config:\n%s", out)
	}
}

func TestPeersSection(t *testing.T) {
	r := &fakeRunner{}
	opts := baseOptions(t, r)
	fp := "SHA256:abcdefghijklmnopqrstuvwxyz0123456789ABCDEFG"
	f := &fakePeers{
		fp: fp, path: "/x/id_ed25519",
		pinned: map[string]string{"vps": fp, "laptop": fp, "down": fp},
		probe:  map[string]error{"down": errors.New("connection refused")},
	}
	opts.PeerChecker = f
	opts.Peers = &config.Peers{
		Enabled: true, Name: "pc",
		Listen: config.PeerListen{Enabled: true, Addr: "127.0.0.1:7722"},
		Remote: []config.PeerRemote{
			{Name: "vps", Addr: "vps.example.com:7722", Dial: true},
			{Name: "laptop"},
			{Name: "down", Addr: "down.example.com:7722", Dial: true},
			{Name: "new", Addr: "new.example.com:7722", Dial: true},
		},
	}
	out := runReport(t, opts)
	for _, want := range []string{
		`peers (on), this Deck is "pc"`,
		"✓ identity  " + fp + "  (private)",
		"listen    127.0.0.1:7722",
		"✓ vps       pinned SHA256:abcdefghijkl…, reachable at vps.example.com:7722 (12ms)",
		"✓ laptop    pinned SHA256:abcdefghijkl…, dials in",
		"✗ down      pinned SHA256:abcdefghijkl…, unreachable at down.example.com:7722: connection refused",
		"✗ new       not paired — run: deck peer add new new.example.com:7722\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if got := strings.Join(f.probed, ","); got != "vps,down" {
		t.Errorf("probed %q, want only pinned dialable peers", got)
	}
}

// TestPeersSectionStripsEscapes: a probe error can carry the peer's own
// text, which must not reach the terminal as escape sequences.
func TestPeersSectionStripsEscapes(t *testing.T) {
	r := &fakeRunner{}
	opts := baseOptions(t, r)
	fp := "SHA256:abcdefghijklmnopqrstuvwxyz0123456789ABCDEFG"
	opts.PeerChecker = &fakePeers{
		fp: fp, path: "/x/id_ed25519",
		pinned: map[string]string{"vps": fp},
		probe:  map[string]error{"vps": errors.New("initialize: \x1b[2J\x1b]0;pwned\x07evil\u202etxt.exe\r\nnext")},
	}
	opts.Peers = &config.Peers{
		Listen: config.PeerListen{Enabled: true, Addr: "0.0.0.0:7722"},
		Remote: []config.PeerRemote{{Name: "vps", Addr: "vps.example.com:7722", Dial: true}},
	}
	out := runReport(t, opts)
	for _, bad := range []string{"\x1b", "\x07", "\u202e", "\r"} {
		if strings.Contains(out, bad) {
			t.Fatalf("output has %q:\n%q", bad, out)
		}
	}
	if !strings.Contains(out, "?[2J?]0;pwned?evil?txt.exe") {
		t.Fatalf("sanitized error missing:\n%s", out)
	}
	if !strings.Contains(out, "! listen    0.0.0.0:7722  every interface") {
		t.Fatalf("no wildcard warning:\n%s", out)
	}
}

func TestPeersSectionIdentityProblems(t *testing.T) {
	r := &fakeRunner{}
	opts := baseOptions(t, r)
	opts.Peers = &config.Peers{Remote: []config.PeerRemote{{Name: "vps", Addr: "vps.example.com:7722"}}}

	f := &fakePeers{idErr: peer.ErrNoIdentity, pinned: map[string]string{"vps": "SHA256:x"}}
	opts.PeerChecker = f
	out := runReport(t, opts)
	if !strings.Contains(out, "peers (off)") || !strings.Contains(out, "✗ identity  not created — run: deck peer fingerprint") {
		t.Fatalf("missing identity line:\n%s", out)
	}
	if !strings.Contains(out, "not checked (no identity)") || len(f.probed) != 0 {
		t.Fatalf("probed without an identity:\n%s", out)
	}
	if !strings.Contains(out, "listen    off") {
		t.Fatalf("listen should be off:\n%s", out)
	}

	f = &fakePeers{fp: "SHA256:y", path: "p", privErr: errors.New("also readable by Everyone")}
	opts.PeerChecker = f
	out = runReport(t, opts)
	if !strings.Contains(out, "! identity  SHA256:y  not private: also readable by Everyone") {
		t.Fatalf("missing permission warning:\n%s", out)
	}
}
