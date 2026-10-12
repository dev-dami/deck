package config

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func validPeers() *Peers {
	return &Peers{
		Enabled: true,
		Name:    "pc",
		Listen:  PeerListen{Enabled: true, Addr: "0.0.0.0:7722"},
		Remote: []PeerRemote{{
			Name: "vps", Addr: "vps.example.com:7722", Dial: true, Role: "contractor",
			AllowInbound: []string{"peer.status", "console.read"},
			MaxScope:     []string{"podman-read"},
		}},
	}
}

func TestPeersDefaultOff(t *testing.T) {
	isolateHome(t)
	stubIdentity(t, "Ada", "hp")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Peers != nil || cfg.PeersEnabled() {
		t.Fatalf("peers must be off by default, got %+v", cfg.Peers)
	}
	// A default config writes no peers key at all.
	data, _ := json.Marshal(cfg)
	if strings.Contains(string(data), "peers") {
		t.Fatalf("default config mentions peers: %s", data)
	}
}

func TestPeersValidate(t *testing.T) {
	stubIdentity(t, "Ada", "hp")
	cases := []struct {
		name   string
		mutate func(p *Peers)
		want   string // "" means valid
	}{
		{"valid", func(*Peers) {}, ""},
		{"empty listen addr ok", func(p *Peers) { p.Listen.Addr = "" }, ""},
		{"ipv6 listen", func(p *Peers) { p.Listen.Addr = "[::1]:7722" }, ""},
		{"bad listen addr", func(p *Peers) { p.Listen.Addr = "nohost" }, "peers.listen.addr"},
		{"bad listen port", func(p *Peers) { p.Listen.Addr = "0.0.0.0:99999" }, "bad port"},
		{"zero port", func(p *Peers) { p.Listen.Addr = "0.0.0.0:0" }, "bad port"},
		{"bad own name", func(p *Peers) { p.Name = "Has Space" }, "peers.name"},
		{"remote name empty", func(p *Peers) { p.Remote[0].Name = "" }, "remote[0].name"},
		{"remote name upper", func(p *Peers) { p.Remote[0].Name = "VPS" }, "remote[0].name"},
		{"remote name slash", func(p *Peers) { p.Remote[0].Name = "a/b" }, "remote[0].name"},
		{"remote name dotdot", func(p *Peers) { p.Remote[0].Name = ".." }, "remote[0].name"},
		{"remote name too long", func(p *Peers) { p.Remote[0].Name = strings.Repeat("a", 33) }, "remote[0].name"},
		{"duplicate", func(p *Peers) { p.Remote = append(p.Remote, PeerRemote{Name: "vps"}) }, "duplicate"},
		{"own name", func(p *Peers) { p.Remote[0].Name = "pc" }, "own name"},
		{"bad remote addr", func(p *Peers) { p.Remote[0].Addr = "vps.example.com" }, "remote[0].addr"},
		{"dial without addr", func(p *Peers) { p.Remote[0].Addr = "" }, "dial needs an addr"},
		{"inbound only without addr", func(p *Peers) { p.Remote[0].Addr = ""; p.Remote[0].Dial = false }, ""},
		{"bad role", func(p *Peers) { p.Remote[0].Role = "admin" }, "role"},
		{"empty role ok", func(p *Peers) { p.Remote[0].Role = "" }, ""},
		{"observer role", func(p *Peers) { p.Remote[0].Role = "observer" }, ""},
		{"empty allow entry", func(p *Peers) { p.Remote[0].AllowInbound = []string{"peer.status", " "} }, "allow_inbound[1]"},
		{"empty scope entry", func(p *Peers) { p.Remote[0].MaxScope = []string{""} }, "max_scope[0]"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := Defaults()
			cfg.Peers = validPeers()
			tc.mutate(cfg.Peers)
			err := cfg.Validate()
			if tc.want == "" {
				if err != nil {
					t.Fatalf("want valid, got %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want error containing %q, got %v", tc.want, err)
			}
		})
	}
}

func TestPeersRoundTripAndDefaults(t *testing.T) {
	isolateHome(t)
	stubIdentity(t, "Ada", "hp")
	path := filepath.Join(t.TempDir(), "config.json")
	writeFile(t, path, `{"peers": {"name": "pc", "listen": {"enabled": false},
		"remote": [{"name": "vps", "addr": "vps.example.com:7722", "dial": true}],
		"sandbox": {"ttl": "2h", "presets": {"podman-read": ["ps"]}}}}`)
	cfg, err := LoadFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.PeersEnabled() {
		t.Fatal("peers.enabled defaults to false")
	}
	if got := cfg.Peers.Listen.ListenAddr(); got != ":7722" {
		t.Fatalf("ListenAddr = %q", got)
	}
	r, ok := cfg.Peers.Find("vps")
	if !ok || r.RoleOrDefault() != PeerRoleObserver || !r.Dial {
		t.Fatalf("remote = %+v ok=%v", r, ok)
	}
	if cfg.Peers.Sandbox == nil || cfg.Peers.Sandbox.TTL != "2h" {
		t.Fatalf("sandbox not parsed: %+v", cfg.Peers.Sandbox)
	}
	if err := SaveTo(path, cfg); err != nil {
		t.Fatal(err)
	}
	again, err := LoadFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	a, _ := json.Marshal(cfg.Peers)
	b, _ := json.Marshal(again.Peers)
	if string(a) != string(b) {
		t.Fatalf("round trip changed peers:\n%s\n%s", a, b)
	}
	var nilPeers *Peers
	if _, ok := nilPeers.Find("vps"); ok {
		t.Fatal("nil peers has no remotes")
	}
}

func TestLoadRejectsInvalidPeers(t *testing.T) {
	isolateHome(t)
	stubIdentity(t, "Ada", "hp")
	path := filepath.Join(t.TempDir(), "config.json")
	writeFile(t, path, `{"peers": {"remote": [{"name": "vps", "role": "root"}]}}`)
	if _, err := LoadFrom(path); err == nil || !strings.Contains(err.Error(), "role") {
		t.Fatalf("want role error, got %v", err)
	}
}
