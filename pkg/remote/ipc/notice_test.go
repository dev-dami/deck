package ipc

import (
	"sync"
	"testing"
	"time"

	"github.com/dev-dami/deck/pkg/remote"
	"github.com/dev-dami/deck/pkg/remote/remotetest"
)

// screenFake is a provider with a screen.
type screenFake struct {
	*remotetest.Fake
	mu    sync.Mutex
	texts []string
}

func (s *screenFake) Notice(text string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.texts = append(s.texts, text)
}

func (s *screenFake) got() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.texts...)
}

func linkWith(t *testing.T, be remote.Backend) *Proxy {
	t.Helper()
	ln, err := Listen()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	e := Endpoint{Addr: ln.Addr().String(), Secret: "s"}
	ctx := t.Context()
	provider, err := Dial(ctx, e, "prj_a1")
	if err != nil {
		t.Fatal(err)
	}
	go ServeConn(ctx, provider, be)
	_, conn, err := Accept(ln, e.Secret)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	p, err := NewProxy(conn)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestNoticesReachAProviderWithAScreen(t *testing.T) {
	be := &screenFake{Fake: remotetest.New()}
	p := linkWith(t, be)
	if !p.HasUI() {
		t.Fatal("a provider with a screen was not recognised")
	}
	if err := p.Notify("3 actions run in 30s"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for len(be.got()) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("notice never arrived")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got := be.got(); len(got) != 1 || got[0] != "3 actions run in 30s" {
		t.Fatalf("got %v", got)
	}
}

func TestNoticesToAHeadlessProviderAreHarmless(t *testing.T) {
	p := linkWith(t, remotetest.New())
	if p.HasUI() {
		t.Fatal("a headless provider claims a screen")
	}
	if err := p.Notify("anyone there?"); err != nil {
		t.Fatalf("a notice to a headless provider should be a no-op, got %v", err)
	}
}

func TestNotifyAfterTheProviderLeftFails(t *testing.T) {
	p := linkWith(t, &screenFake{Fake: remotetest.New()})
	if _, ok := p.w.w.(interface{ Close() error }); ok {
		_ = p.w.w.(interface{ Close() error }).Close()
	}
	select {
	case <-p.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("proxy did not notice")
	}
	if err := p.Notify("hello"); err != ErrOffline {
		t.Fatalf("got %v", err)
	}
}
