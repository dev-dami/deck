package engine

import (
	"log/slog"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/dev-dami/deck/internal/deck/agent"
	"github.com/dev-dami/deck/internal/deck/agent/fake"
	"github.com/dev-dami/deck/internal/deck/config"
	"github.com/dev-dami/deck/internal/deck/peer"
	"github.com/dev-dami/deck/internal/deck/session"
	"github.com/dev-dami/deck/internal/deck/store"
)

// deck is one Deck in a test: its threads and its peer link.
type deck struct {
	mgr *session.Manager
	svc *peer.Service
	dir string
}

func say(text string) fake.Step {
	return fake.Step{Event: agent.Event{Kind: agent.KindAssistantText, Text: text}}
}
func finish() fake.Step { return fake.Step{Event: agent.Event{Kind: agent.KindTurnDone}} }
func turn(steps ...fake.Step) fake.Script {
	return fake.Script(steps)
}
func asks(id string) fake.Step {
	return fake.Step{
		Event:    agent.Event{Kind: agent.KindToolCall, Tool: &agent.Tool{ID: id, Name: "Bash", Title: "make", Status: agent.ToolRunning}},
		Approval: &agent.ApprovalRequest{ID: id, Kind: agent.ApproveCommand, ToolName: "Bash", Summary: "make"},
	}
}

func newDeck(t *testing.T, name string, claude, codex []fake.Script) *deck {
	t.Helper()
	dir := t.TempDir()
	st, err := store.New(filepath.Join(dir, "threads"))
	if err != nil {
		t.Fatal(err)
	}
	hub := fake.NewHub()
	cfg := config.Config{UserName: "tester", DefaultAgent: "claude", Motion: "off"}
	mgr := session.NewManager(map[string]agent.Adapter{"claude": fake.NewTurns(0, hub, claude...), "codex": fake.NewTurns(0, hub, codex...)},
		st, cfg, session.Options{Hub: hub, MCPFor: fake.MCPFor})
	t.Cleanup(mgr.Close)
	d := &deck{mgr: mgr, dir: dir}
	svc, err := peer.NewService(peer.ServiceOptions{Dir: filepath.Join(dir, "peers"), ConfigPath: filepath.Join(dir, "config.json"),
		Logger: slog.New(slog.DiscardHandler), SubStore: mgr.SubStore, Shared: sharing{mgr},
		Local: peer.Local{Presence: (&localWork{mgr: mgr}).Presence}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(svc.Close)
	d.svc = svc
	for _, err := range []error{svc.Start(nil), svc.SetName(name), svc.SetEnabled(true)} {
		if err != nil {
			t.Fatal(err)
		}
	}
	stop := make(chan struct{})
	t.Cleanup(func() { close(stop) })
	go func() {
		for {
			select {
			case <-mgr.Updates():
			case <-stop:
				return
			}
		}
	}()
	return d
}

// link has b dial a, each pinned on the other.
func link(t *testing.T, a, b *deck) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()
	if err := a.svc.SetListen(true, addr); err != nil {
		t.Fatal(err)
	}
	addr = a.svc.Snapshot().Listening
	ida, _ := peer.LoadIdentity(filepath.Join(a.dir, "peers"))
	idb, _ := peer.LoadIdentity(filepath.Join(b.dir, "peers"))
	if err := peer.OpenStore(filepath.Join(a.dir, "peers")).Pin(peer.NewPinned("b", "", "", idb.PublicKey())); err != nil {
		t.Fatal(err)
	}
	if err := peer.OpenStore(filepath.Join(b.dir, "peers")).Pin(peer.NewPinned("a", addr, "", ida.PublicKey())); err != nil {
		t.Fatal(err)
	}
	if _, err := peer.SetRemote(filepath.Join(b.dir, "config.json"), config.PeerRemote{Name: "a", Addr: addr, Dial: true}); err != nil {
		t.Fatal(err)
	}
	if err := b.svc.Reload(); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "b's link to a", func() bool { return b.svc.Manager().Supervisor("a").Client() != nil })
}

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	for end := time.Now().Add(20 * time.Second); time.Now().Before(end); time.Sleep(20 * time.Millisecond) {
		if ok() {
			return
		}
	}
	t.Fatalf("timed out waiting for %s", what)
}
