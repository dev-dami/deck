package cmd

import (
	"context"
	"time"

	"github.com/dev-dami/deck/pkg/remote/projects"
)

// Sharing may change while the TUI is open (for example through /mobile).
// Keep the adapter dormant until opted in, and close it when sharing ends.
func provideSharedProject(ctx context.Context, reg *projects.Registry, pj string, interval time.Duration, provide func(context.Context)) {
	tick := time.NewTicker(interval)
	defer tick.Stop()
	var cancel context.CancelFunc
	var done chan struct{}
	stop := func() {
		if cancel != nil {
			cancel()
			<-done
			cancel, done = nil, nil
		}
	}
	defer stop()
	for {
		_, err := reg.Reload()
		shared := err == nil
		if shared {
			_, err = reg.Get(pj)
			shared = err == nil
		}
		if shared && cancel == nil {
			active, stopProvide := context.WithCancel(ctx)
			cancel = stopProvide
			done = make(chan struct{})
			finished := done
			go func() { defer close(finished); defer stopProvide(); provide(active) }()
		} else if !shared {
			stop()
		}
		select {
		case <-ctx.Done():
			return
		case <-done:
			// An unexpected disconnect retries only on the next poll.
			cancel()
			cancel, done = nil, nil
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
			}
		case <-tick.C:
		}
	}
}
