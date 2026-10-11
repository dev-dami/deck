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
