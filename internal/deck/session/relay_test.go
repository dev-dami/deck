package session

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dev-dami/deck/internal/deck/agent"
	"github.com/dev-dami/deck/internal/deck/agent/fake"
	"github.com/dev-dami/deck/internal/deck/store"
)

// gateAdapter's runs hold Send for the member ids in blocked until gate closes.
type gateAdapter struct {
	agent.Adapter
	gate    chan struct{}
	mu      sync.Mutex
	blocked map[string]bool
}

type gateRun struct {
	agent.Run
	id string
	a  *gateAdapter
}

func (g *gateAdapter) Start(ctx context.Context, sp agent.Spec) (agent.Run, error) {
	run, err := g.Adapter.Start(ctx, sp)
	if err != nil {
		return nil, err
	}
	return &gateRun{Run: run, id: sp.ID, a: g}, nil
}

func (r *gateRun) Send(p string) error {
	r.a.mu.Lock()
	b := r.a.blocked[r.id]
	r.a.mu.Unlock()
	if b {
		<-r.a.gate
	}
	return r.Run.Send(p)
}

// A thread.send whose delivery blocks holds up only its own thread.
func TestBlockedRelayDoesNotStallOtherThreads(t *testing.T) {
	dir := t.TempDir()
	st, err := store.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	hub := fake.NewHub()
	codex := &gateAdapter{Adapter: fake.NewTurns(0, hub, turn(say("ok"), finish()), turn(say("ok"), finish())),
		gate: make(chan struct{}), blocked: map[string]bool{}}
	m := NewManager(map[string]agent.Adapter{"claude": fake.NewTurns(0, hub), "codex": codex}, st, testCfg(),
		Options{Hub: hub, MCPFor: fake.MCPFor})
	t.Cleanup(m.Close)
	collect(t, m)

	newPair := func() (string, string) {
		tid, err := m.NewThread(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		a, err := m.AddMember(tid, "claude", "")
		if err != nil {
			t.Fatal(err)
		}
		c, err := m.AddMember(tid, "codex", "")
		if err != nil {
			t.Fatal(err)
		}
		return a, c
	}
	aA, cA := newPair()
	aB, _ := newPair()
	codex.mu.Lock()
	codex.blocked[cA] = true
	codex.mu.Unlock()

	stuck := make(chan error, 1)
	go func() {
		_, err := hub.Send(nil, aA, "codex", "first thread")
		stuck <- err
	}()
	time.Sleep(50 * time.Millisecond) // let the first call reach the blocked Send
	done := make(chan string, 1)
	go func() {
		res, _ := hub.Send(nil, aB, "codex", "second thread")
		done <- res
	}()
	select {
	case res := <-done:
		if !strings.Contains(res, "delivered to codex") {
			t.Fatalf("second thread's send = %q", res)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("a blocked delivery in one thread stalled another thread's thread.send")
	}
	select {
	case <-stuck:
		t.Fatal("the blocked send returned early")
	default:
	}
	close(codex.gate)
	select {
	case err := <-stuck:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the blocked send never finished")
	}
}

// The end of a turn is settled on the thread's relay worker: the pump of the
// member whose turn ended goes on while its relays are delivered, and each
// turn's relays follow that turn's turn_done, in the order the turns ended.
func TestAfterTurnRunsOffThePumpInTurnOrder(t *testing.T) {
	hub := fake.NewHub()
	claude := fake.NewTurns(0, hub, turn(say("$codex first look"), finish()), turn(say("$codex second look"), finish()))
	codex := &gateAdapter{Adapter: fake.NewTurns(0, hub), gate: make(chan struct{}), blocked: map[string]bool{"codex": true}}
	m := NewManager(map[string]agent.Adapter{"claude": claude, "codex": codex}, nil, testCfg(), Options{Hub: hub, MCPFor: fake.MCPFor})
	t.Cleanup(m.Close)
	c := collect(t, m)
	tid, _ := m.NewThread(t.TempDir())
	if _, err := m.AddMember(tid, "codex", ""); err != nil {
		t.Fatal(err)
	}
	if err := m.Post(tid, "$claude look at this"); err != nil {
		t.Fatal(err)
	}
	// The first relay's delivery now blocks in codex's Send, and with it the
	// thread's relay worker; claude's next turn must still come through.
	waitFor(t, "the first relay", func() bool { return len(c.relays()) == 1 })
	if err := m.Post(tid, "$claude again"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "claude's second turn", func() bool { return c.turnDones("claude") == 2 })
	if n := len(c.relays()); n != 1 {
		t.Fatalf("%d relays while the worker was blocked", n)
	}
	close(codex.gate)
	waitFor(t, "the second relay", func() bool { return len(c.relays()) == 2 })

	th := m.thread(tid)
	th.mu.Lock()
	var order []string
	for _, e := range th.log {
		switch {
		case e.Kind == agent.KindTurnDone && e.AgentID == "claude":
			order = append(order, "turn_done")
		case e.Type == store.EntryRelay:
			order = append(order, e.Text)
		}
	}
	th.mu.Unlock()
	want := []string{"turn_done", "first look", "turn_done", "second look"}
	if strings.Join(order, "|") != strings.Join(want, "|") {
		t.Fatalf("log order = %q, want %q", order, want)
	}
}
