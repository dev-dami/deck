package engine

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dev-dami/deck/internal/deck/agent"
	"github.com/dev-dami/deck/internal/deck/agent/fake"
	"github.com/dev-dami/deck/internal/deck/config"
	"github.com/dev-dami/deck/internal/deck/peer"
	"github.com/dev-dami/deck/internal/deck/session"
	"github.com/dev-dami/deck/internal/deck/store"
	"github.com/dev-dami/deck/internal/deck/sub"
)

// deck is one Deck in a test: its threads, its peer link and the glue.
type deck struct {
	mgr   *session.Manager
	svc   *peer.Service
	deals *contracts
	dir   string
	// onApproval answers each approval its agents raise.
	onApproval func(session.ApprovalMsg)
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
	d := &deck{mgr: mgr, dir: dir, deals: newContracts(mgr)}
	d.deals.waits = nil
	svc, err := peer.NewService(peer.ServiceOptions{Dir: filepath.Join(dir, "peers"), ConfigPath: filepath.Join(dir, "config.json"),
		Logger: slog.New(slog.DiscardHandler), SubStore: mgr.SubStore, Contracts: d.deals.hooks(), Shared: sharing{mgr},
		Local: peer.Local{Presence: (&localWork{mgr: mgr}).Presence}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(svc.Close)
	d.svc = svc
	d.deals.svc.Store(svc)
	mgr.SetContractor(d.deals)
	mgr.SetTaskEnd(d.deals.taskEnd)
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
			case msg := <-mgr.Updates():
				if ap, ok := msg.(session.ApprovalMsg); ok && d.onApproval != nil {
					go d.onApproval(ap)
				}
			case <-stop:
				return
			}
		}
	}()
	return d
}

// link has b dial a, each pinned on the other; a makes b a contractor.
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
	if err := peer.OpenStore(filepath.Join(a.dir, "peers")).Pin(peer.NewPinned("b", "", config.PeerRoleContractor, idb.PublicKey())); err != nil {
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

func gitRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	for _, args := range [][]string{{"init", "-q"}, {"add", "-A"}, {"-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-q", "--allow-empty", "-m", "init"}} {
		if args[0] == "add" {
			if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("old\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return dir
}

func taskOf(t *testing.T, mgr *session.Manager, tid, id string) store.Task {
	t.Helper()
	tk, ok := mgr.Task(tid, id)
	if !ok {
		t.Fatalf("no task %s", id)
	}
	return tk
}

// The whole contract between two Decks: b's agent asks a for a task, b's
// person allows it, a's person accepts it, a's agent changes a copy of b's
// tree, and the capsule lands on b's task, which tells b's agent. Then b's
// person asks for a second one and calls it off while a works on it.
func TestContractBetweenTwoDecks(t *testing.T) {
	repo := gitRepo(t)
	a := newDeck(t, "a", []fake.Script{turn(asks("w"), say("made a.txt new"), finish())}, nil)
	b := newDeck(t, "b", nil, []fake.Script{
		turn(fake.Step{Exec: &fake.Exec{Op: "task.contract", Args: []byte(`{"peer": "a", "brief": "make a.txt new"}`)}}, say("asked"), finish()),
		turn(say("noted"), finish()),
	})
	link(t, a, b)

	var mu sync.Mutex
	held := map[string]session.ApprovalMsg{}
	b.onApproval = func(ap session.ApprovalMsg) {
		if ap.Kind == agent.ApproveShare {
			ap.Respond(agent.Decision{Allow: true})
		}
	}
	a.onApproval = func(ap session.ApprovalMsg) {
		mu.Lock()
		held[ap.AgentID] = ap
		mu.Unlock()
	}
	// answer waits for the agent working on a's n-th contract to ask.
	answer := func(n int) (peer.Contract, session.ApprovalMsg) {
		var ap session.ApprovalMsg
		var c peer.Contract
		waitFor(t, "a's agent asks", func() bool {
			if cs := a.svc.Contracts(); len(cs) > n && cs[n].Task != "" {
				c = cs[n]
			} else {
				return false
			}
			owner := taskOf(t, a.mgr, c.Thread, c.Task).Owner
			mu.Lock()
			defer mu.Unlock()
			var ok bool
			ap, ok = held[owner]
			return ok
		})
		return c, ap
	}

	tid, err := b.mgr.NewThread(repo)
	if err != nil {
		t.Fatal(err)
	}
	lead, err := b.mgr.AddMember(tid, "codex", "")
	if err != nil {
		t.Fatal(err)
	}
	_ = b.mgr.Post(tid, "$codex go")
	waitFor(t, "a hears the proposal", func() bool { return len(a.svc.Contracts()) == 1 })
	c := a.svc.Contracts()[0]
	if c.Title != "make a.txt new" || c.Files != 1 || c.Repo == "" {
		t.Fatalf("proposal = %+v", c)
	}
	if err := a.svc.AnswerContract("b", c.ID, true, "claude"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "b's task taken", func() bool { return taskOf(t, b.mgr, tid, "t1").Owner == "a/claude" })

	c, ap := answer(0)
	info, _ := a.mgr.Thread(c.Thread)
	if err := a.mgr.SetMode(c.Thread, agent.ModeYolo); err == nil {
		t.Fatal("a contract thread went to yolo")
	}
	if err := os.WriteFile(filepath.Join(info.Dir, "a.txt"), []byte("new\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ap.Respond(agent.Decision{Allow: true})

	waitFor(t, "b's task done", func() bool { return taskOf(t, b.mgr, tid, "t1").Status == store.TaskDone })
	tk := taskOf(t, b.mgr, tid, "t1")
	if len(tk.Capsules) != 1 || !strings.Contains(tk.Result, "made a.txt new") {
		t.Fatalf("t1 = %+v", tk)
	}
	if err := b.mgr.SubStore().Apply(context.Background(), repo, sub.ID(tk.Capsules[0])); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(filepath.Join(repo, "a.txt")); string(got) != "new\n" {
		t.Fatalf("a.txt = %q", got)
	}
	_ = lead

	// b's person asks for another and calls it off while a's agent works.
	tk2, queued, err := b.mgr.Contract(tid, "", session.ContractSpec{Peer: "a", Brief: "and again"})
	if err != nil || queued {
		t.Fatalf("second contract: %v queued %v", err, queued)
	}
	waitFor(t, "a hears the second", func() bool { return len(a.svc.Contracts()) == 2 })
	c2 := a.svc.Contracts()[1]
	if err := a.svc.AnswerContract("b", c2.ID, true, "claude"); err != nil {
		t.Fatal(err)
	}
	answer(1)
	if _, err := b.mgr.CancelTask(tid, tk2.ID, ""); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "a's work stops", func() bool {
		k, ok := a.mgr.Task(a.svc.Contracts()[1].Thread, a.svc.Contracts()[1].Task)
		return ok && k.Status == store.TaskCancelled
	})
	if st := a.svc.Contracts()[1].State; st != peer.ContractCancelled {
		t.Fatalf("a's record is %s", st)
	}
}

// A result that cannot be fetched at once is tried again.
func TestResultFetchIsRetried(t *testing.T) {
	d := newDeck(t, "x", nil, nil)
	tries := 0
	d.deals.waits = []time.Duration{time.Millisecond, time.Millisecond, time.Millisecond}
	d.deals.fetch = func(context.Context, string, sub.ID) error {
		if tries++; tries < 3 {
			return errors.New("link down")
		}
		return nil
	}
	if err := d.deals.fetchResult(peer.Contract{Peer: "a"}); err != nil || tries != 3 {
		t.Fatalf("fetch: %v after %d tries", err, tries)
	}
	tries = -10
	if err := d.deals.fetchResult(peer.Contract{Peer: "a"}); err == nil || tries != -6 {
		t.Fatalf("gave up after %d tries: %v", tries+10, err)
	}
}
