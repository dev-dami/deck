package server

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/dev-dami/deck/pkg/remote"
	"github.com/dev-dami/deck/pkg/remote/auth"
	"github.com/dev-dami/deck/pkg/remote/remotetest"
	"github.com/stretchr/testify/require"
)

type closedEventsBackend struct{ remote.Backend }

func (closedEventsBackend) Events(context.Context) <-chan remote.Event {
	events := make(chan remote.Event)
	close(events)
	return events
}

// Returning from Run hands the data directory back to its owner. The auth
// worker must finish its final Flush before that return, on every exit path.
func TestRunJoinsFinalLastSeenFlush(t *testing.T) {
	for _, reason := range []string{"caller_cancelled", "backend_closed"} {
		t.Run(reason, func(t *testing.T) {
			clk := newClock()
			store, err := auth.Open(filepath.Join(t.TempDir(), "remote"), auth.WithClock(clk.now))
			require.NoError(t, err)
			code, err := store.Arm()
			require.NoError(t, err)
			_, device, err := store.Pair(code.Code, "phone")
			require.NoError(t, err)
			clk.advance(2 * time.Minute)
			store.Touch(device.ID)
			var be remote.Backend = remotetest.New()
			if reason == "backend_closed" {
				be = closedEventsBackend{be}
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if reason == "caller_cancelled" {
				cancel()
			}
			srv := New(be, store, Options{Now: clk.now})
			srv.Run(ctx)
			persisted, err := store.Get(device.ID)
			require.NoError(t, err)
			require.Equal(t, clk.now().UTC().Truncate(time.Second), persisted.LastSeen, "Run returned before its pending auth write finished")
		})
	}
}

func TestStopJoinsFinalLastSeenFlush(t *testing.T) {
	r, err := Start(context.Background(), remotetest.New(), RunOptions{DataDir: t.TempDir()})
	require.NoError(t, err)
	t.Cleanup(r.Stop)
	code, err := r.Store.Arm()
	require.NoError(t, err)
	_, device, err := r.Store.Pair(code.Code, "phone")
	require.NoError(t, err)
	r.Store.Touch(device.ID)
	r.Stop()
	persisted, err := r.Store.Get(device.ID)
	require.NoError(t, err)
	require.False(t, persisted.LastSeen.IsZero(), "Stop returned before auth flush finished")
}
