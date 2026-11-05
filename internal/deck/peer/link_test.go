package peer

import (
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dev-dami/deck/internal/deck/config"
)

// newService makes a running Service named name with peers on, in a temp
// dir.
func newService(t *testing.T, name string, o ServiceOptions) *Service {
	t.Helper()
	dir := t.TempDir()
	o.Dir, o.ConfigPath, o.Logger = filepath.Join(dir, "peers"), filepath.Join(dir, "config.json"), quietLog
	s, err := NewService(o)
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
	if err := s.SetEnabled(true); err != nil {
		t.Fatal(err)
	}
	return s
}

// linkedServices makes Services a (listening) and b (dialing a), pinned to
// each other, and waits until b's link to a is up. local is a's own work.
func linkedServices(t *testing.T, aLocal, bLocal Local) (a, b *Service) {
	t.Helper()
	return linkedWith(t, ServiceOptions{Local: aLocal}, ServiceOptions{Local: bLocal}, "")
}

// linkedWith is linkedServices with more options for each Service, and the
// role a gives b.
func linkedWith(t *testing.T, ao, bo ServiceOptions, role string) (a, b *Service) {
	t.Helper()
	a, b = newService(t, "a", ao), newService(t, "b", bo)
	if err := a.SetListen(true, freeAddr(t)); err != nil {
		t.Fatal(err)
	}
	addr := a.Snapshot().Listening
	ida, _ := LoadIdentity(a.dir)
	idb, _ := LoadIdentity(b.dir)
	if err := OpenStore(a.dir).Pin(NewPinned("b", "", role, idb.PublicKey())); err != nil {
		t.Fatal(err)
	}
	if err := OpenStore(b.dir).Pin(NewPinned("a", addr, "", ida.PublicKey())); err != nil {
		t.Fatal(err)
	}
	if _, err := SetRemote(b.cfgPath, config.PeerRemote{Name: "a", Addr: addr, Dial: true}); err != nil {
		t.Fatal(err)
	}
	if err := b.Reload(); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 10*time.Second, "b's link to a", func() bool { return b.Manager().Supervisor("a").Client() != nil })
	return a, b
}

func TestPresenceComesWithPeerStatus(t *testing.T) {
	var mu sync.Mutex
	var asked []string
	var limits []bool
	aLocal := Local{Presence: func(_ string, repos []string, lim bool) Presence {
		mu.Lock()
		asked, limits = repos, append(limits, lim)
		mu.Unlock()
		return Presence{
			Agents: []AgentPresence{{Name: "claude2", Kind: "claude", Status: "working", Task: "fix the \x1b[2Jflaky test", ContextPct: 40}},
			Leases: []LeasePresence{{Repo: "r1", Agent: "claude2", Paths: []string{"a.go"}, Exclusive: true}},
			Limits: []LimitPresence{{Kind: "claude", Window: "5h", UsedPct: 80}},
		}
	}}
	_, b := linkedServices(t, aLocal, Local{Repos: func() []string { return []string{"r1"} }})

	b.pollPresence(ctxT(t))
	var ps PeerState
	for _, p := range b.Snapshot().Peers {
		if p.Name == "a" {
			ps = p
		}
	}
	if ps.Presence == nil || len(ps.Presence.Agents) != 1 || ps.Presence.Agents[0].Task != "fix the ?[2Jflaky test" || len(ps.Presence.Leases) != 1 {
		t.Fatalf("a's presence = %+v", ps.Presence)
	}
	mu.Lock()
	gotRepos, gotLimits := asked, limits
	mu.Unlock()
	if !slices.Equal(gotRepos, []string{"r1"}) || len(gotLimits) == 0 || gotLimits[0] {
		t.Fatalf("a was asked about %q, limits %v", gotRepos, gotLimits)
	}

	// Agents ask through peers.status: the same answer, as text.
	out, err := b.PeerStatus(ctxT(t), "a")
	if err != nil || !strings.Contains(out, `"claude2"`) {
		t.Fatalf("PeerStatus = %q, %v", out, err)
	}
}
