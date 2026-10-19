package mobile

import (
	"context"
	"log/slog"
	"time"

	"github.com/dev-dami/deck/pkg/remote"
	"github.com/dev-dami/deck/pkg/remote/ipc"
)

// Provide offers be to the daemon as the Deck of project pj, and keeps
// offering it: if there is no daemon yet, or it restarts, it tries again.
// It blocks until ctx ends, so run it in a goroutine.
func Provide(ctx context.Context, dir, pj string, be remote.Backend) {
	for ctx.Err() == nil {
		if ep, err := ipc.ReadEndpoint(dir); err == nil {
			if conn, err := ipc.Dial(ctx, ep, pj); err == nil {
				// ServeConn may be blocked reading an idle daemon. Cancellation
				// must close the socket so disabling sharing detaches promptly.
				stopClose := context.AfterFunc(ctx, func() { _ = conn.Close() })
				err := ipc.ServeConn(ctx, conn, be)
				stopClose()
				conn.Close()
				if err != nil && ctx.Err() == nil {
					slog.Debug("remote: daemon connection ended", "error", err)
				}
			}
		}
		select {
		case <-time.After(providerRetry):
		case <-ctx.Done():
			return
		}
	}
}
