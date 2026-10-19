package mobile

import (
	"context"
	"testing"
	"time"

	"github.com/dev-dami/deck/pkg/remote/projects"
	"github.com/dev-dami/deck/pkg/remote/remotetest"
)

func TestProviderCancellationClosesIdleConnection(t *testing.T) {
	e := start(t)
	reg, err := projects.Open(e.dir)
	if err != nil {
		t.Fatal(err)
	}
	p, err := reg.Add(t.TempDir(), "cancel test")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(e.ctx)
	defer cancel()
	done := make(chan struct{})
	go func() { defer close(done); Provide(ctx, e.dir, p.ID, remotetest.New()) }()
	e.waitFor("idle provider attachment", func() bool {
		for _, ref := range e.status().Projects {
			if ref.ID == p.ID {
				return ref.Online
			}
		}
		return false
	})
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("provider cancellation left idle connection open")
	}
	e.waitFor("provider detached while daemon stays alive", func() bool {
		for _, ref := range e.status().Projects {
			if ref.ID == p.ID {
				return !ref.Online
			}
		}
		return false
	})
}
