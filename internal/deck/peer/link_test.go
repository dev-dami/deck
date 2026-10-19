package peer

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dev-dami/deck/internal/deck/config"
)

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
	mk := func(name string, o ServiceOptions) *Service {
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
	a, b = mk("a", ao), mk("b", bo)
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

func TestChatGoesOverTheLinkAndWaitsInTheOutbox(t *testing.T) {
	a, b := linkedServices(t, Local{}, Local{})
	ctx := ctxT(t)

	// b's agent posts over b's own link; a answers over the link b opened,
	// though it has no address to dial b at.
	if reached, err := b.PostChat(ctx, "claude1", "build is green"); err != nil || !slices.Equal(reached, []string{"a"}) {
		t.Fatalf("b posts: %v, %v", reached, err)
	}
	if reached, err := a.SendChat(ctx, "thanks"); err != nil || !slices.Equal(reached, []string{"b"}) {
		t.Fatalf("a answers over the inbound link: %v, %v", reached, err)
	}
	got := a.Team().Chat()
	if len(got) != 2 || got[0].Author() != "b/claude1" || got[0].Text != "build is green" || !got[1].Mine {
		t.Fatalf("a's chat = %+v", got)
	}
	if mine := b.Team().Chat()[0]; !mine.Mine || mine.Agent != "claude1" {
		t.Fatalf("b's own record = %+v", mine)
	}

	// a goes away: the message waits in b's outbox, and arrives once.
	if err := a.SetEnabled(false); err != nil {
		t.Fatal(err)
	}
	reached, err := b.SendChat(ctx, "are you there?")
	var queued QueuedError
	if len(reached) != 0 || !errors.As(err, &queued) || !slices.Equal(queued.Peers, []string{"a"}) || b.out.len() != 1 {
		t.Fatalf("to an absent teammate: %v, %v (outbox %d)", reached, err, b.out.len())
	}
	if err := a.SetEnabled(true); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 20*time.Second, "the queued message", func() bool { return b.out.len() == 0 })
	n := 0
	for _, c := range a.Team().Chat() {
		if c.Text == "are you there?" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("delivered %d times: %+v", n, a.Team().Chat())
	}
}

func TestChatRepeatIsDropped(t *testing.T) {
	team := NewTeam("", nil)
	h := &TeamHandler{Base: &StatusHandler{Name: "me"}, Team: team}
	post := func(from, args string) {
		if _, err := h.Execute(context.Background(), Caller{Name: from}, OpConsolePost, json.RawMessage(args)); err != nil {
			t.Fatal(err)
		}
	}
	post("vps", `{"text":"hi","id":"k1","agent":"claude2"}`)
	post("vps", `{"text":"hi","id":"k1","agent":"claude2"}`) // the outbox tries again
	post("pc", `{"text":"hi","id":"k1"}`)                    // another Deck's key space
	post("vps", `{"text":"x","agent":"Bad Name!"}`)          // a label that is no name goes
	chat := team.Chat()
	if len(chat) != 3 || chat[0].Author() != "vps/claude2" || chat[1].Author() != "pc" || chat[2].Agent != "" {
		t.Fatalf("chat = %+v", chat)
	}
}

func TestPresenceComesWithPeerStatus(t *testing.T) {
	var mu sync.Mutex
	var asked []string
	var limits []bool
	aLocal := Local{Presence: func(repos []string, lim bool) Presence {
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
