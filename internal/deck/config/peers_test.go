package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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
		{"dial via a relay", func(p *Peers) {
			p.Remote = append(p.Remote, PeerRemote{Name: "pc2", Dial: true, Via: "vps"})
		}, ""},
		{"via itself", func(p *Peers) { p.Remote[0].Via = "vps" }, "remote[0].via"},
		{"via unknown", func(p *Peers) { p.Remote[0].Via = "nas" }, "not in peers.remote"},
		{"via bad name", func(p *Peers) { p.Remote[0].Via = "No Name" }, "remote[0].via"},
		{"inbound only without addr", func(p *Peers) { p.Remote[0].Addr = ""; p.Remote[0].Dial = false }, ""},
		{"bad role", func(p *Peers) { p.Remote[0].Role = "admin" }, "role"},
		{"empty role ok", func(p *Peers) { p.Remote[0].Role = "" }, ""},
		{"observer role", func(p *Peers) { p.Remote[0].Role = "observer" }, ""},
		{"empty allow entry", func(p *Peers) { p.Remote[0].AllowInbound = []string{"peer.status", " "} }, "allow_inbound[1]"},
		{"empty scope entry", func(p *Peers) { p.Remote[0].MaxScope = []string{""} }, "max_scope[0]"},
		{"allow entry with spaces", func(p *Peers) { p.Remote[0].AllowInbound = []string{" peer.status"} }, "spaces around"},
		{"allow entry with tab", func(p *Peers) { p.Remote[0].AllowInbound = []string{"peer.status\t"} }, "spaces around"},
		{"scope entry with spaces", func(p *Peers) { p.Remote[0].MaxScope = []string{"podman-read "} }, "spaces around"},
		{"unknown preset", func(p *Peers) { p.Remote[0].MaxScope = []string{"podman-write"} }, "is not a preset"},
		{"both presets", func(p *Peers) { p.Remote[0].MaxScope = []string{"podman-read", "systemd-read"} }, ""},
		{"bad ttl", func(p *Peers) { p.Sandbox = &PeerSandbox{TTL: "soon"} }, "peers.sandbox.ttl"},
		{"zero ttl", func(p *Peers) { p.Sandbox = &PeerSandbox{TTL: "0s"} }, "peers.sandbox.ttl"},
		{"negative scratch", func(p *Peers) { p.Sandbox = &PeerSandbox{ScratchMB: -1} }, "negative"},
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

func TestUpdatePeersKeepsOtherKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	writeFile(t, path, `{"motion": "on", "some_future_key": {"x": 42}, "peers": {"name": "pc"}}`)
	t.Setenv(EnvReducedMotion, "1")
	changed, err := UpdatePeers(path, func(p *Peers) bool {
		p.Remote = append(p.Remote, PeerRemote{Name: "vps", Role: PeerRoleObserver})
		return true
	})
	if err != nil || !changed {
		t.Fatalf("UpdatePeers = %v %v", changed, err)
	}
	var raw map[string]json.RawMessage
	data, _ := os.ReadFile(path)
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	if string(raw["motion"]) != `"on"` {
		t.Fatalf("motion = %s, the environment override was persisted", raw["motion"])
	}
	if !strings.Contains(string(raw["some_future_key"]), "42") {
		t.Fatalf("unknown key dropped: %s", data)
	}
	p, err := LoadPeers(path)
	if err != nil || p == nil || p.Name != "pc" {
		t.Fatalf("LoadPeers = %+v %v", p, err)
	}
	if r, ok := p.Find("vps"); !ok || r.Dial || r.RoleOrDefault() != PeerRoleObserver {
		t.Fatalf("remote = %+v %v", r, ok)
	}
	// No change, no write; an invalid change is refused.
	if changed, err := UpdatePeers(path, func(*Peers) bool { return false }); changed || err != nil {
		t.Fatalf("no-op update: %v %v", changed, err)
	}
	if _, err := UpdatePeers(path, func(p *Peers) bool {
		p.Remote = append(p.Remote, PeerRemote{Name: "Bad Name"})
		return true
	}); err == nil {
		t.Fatal("invalid peers saved")
	}
	// A missing file gets just the peers section.
	fresh := filepath.Join(t.TempDir(), "config.json")
	if _, err := UpdatePeers(fresh, func(p *Peers) bool { p.Name = "x"; return true }); err != nil {
		t.Fatal(err)
	}
	if p, err := LoadPeers(fresh); err != nil || p == nil || p.Name != "x" {
		t.Fatalf("fresh: %+v %v", p, err)
	}
}

func TestListenAllInterfaces(t *testing.T) {
	for addr, want := range map[string]bool{
		"": true, ":7722": true, "0.0.0.0:7722": true, "[::]:7722": true,
		"127.0.0.1:7722": false, "10.0.0.2:7722": false, "[::1]:7722": false, "vps.example.com:7722": false,
	} {
		if got := (PeerListen{Addr: addr}).AllInterfaces(); got != want {
			t.Errorf("AllInterfaces(%q) = %v, want %v", addr, got, want)
		}
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

func TestSandboxLimits(t *testing.T) {
	var none *Peers
	if ttl, mb, kb := none.SandboxLimits(); ttl != 2*time.Hour || mb != 64 || kb != 64 {
		t.Fatalf("defaults = %v %d %d", ttl, mb, kb)
	}
	p := &Peers{Sandbox: &PeerSandbox{TTL: "30m", OutputKB: 16}}
	if ttl, mb, kb := p.SandboxLimits(); ttl != 30*time.Minute || mb != 64 || kb != 16 {
		t.Fatalf("limits = %v %d %d", ttl, mb, kb)
	}
}
