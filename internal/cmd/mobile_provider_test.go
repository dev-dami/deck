package cmd

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dev-dami/deck/pkg/remote/projects"
)

func TestOpenTerminalFollowsProjectSharing(t *testing.T) {
	dir, root := t.TempDir(), t.TempDir()
	reg, err := projects.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	writer, err := projects.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	started, stopped := make(chan struct{}, 4), make(chan struct{}, 4)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		provideSharedProject(ctx, reg, projects.IDOf(root), 10*time.Millisecond, func(active context.Context) {
			started <- struct{}{}
			<-active.Done()
			stopped <- struct{}{}
		})
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Error("watcher did not stop")
		}
	})
	wait := func(ch <-chan struct{}, what string) {
		t.Helper()
		select {
		case <-ch:
		case <-time.After(3 * time.Second):
			t.Fatalf("timed out: %s", what)
		}
	}
	select {
	case <-started:
		t.Fatal("unshared project was exposed")
	case <-time.After(40 * time.Millisecond):
	}
	p, err := writer.Add(root, "project")
	if err != nil {
		t.Fatal(err)
	}
	wait(started, "share after terminal opens")
	if err := writer.SetEnabled(p.ID, false); err != nil {
		t.Fatal(err)
	}
	wait(stopped, "disable detaches")
	if err := writer.SetEnabled(p.ID, true); err != nil {
		t.Fatal(err)
	}
	wait(started, "re-enable reattaches")
	if err := os.WriteFile(filepath.Join(dir, "projects.json"), []byte("invalid"), 0600); err != nil {
		t.Fatal(err)
	}
	wait(stopped, "invalid registry fails closed")
}
