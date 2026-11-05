package cmd

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	deckconfig "github.com/dev-dami/deck/internal/deck/config"
)

func TestServeCommandAndFlags(t *testing.T) {
	t.Parallel()
	// Parent, not rootCmd.Commands: Commands sorts, which races with the
	// other parallel tests.
	require.Same(t, rootCmd, serveCmd.Parent(), "missing deck serve")
	require.Equal(t, "2m0s", serveCmd.Flags().Lookup("drain").DefValue)
	for _, flag := range []string{"remote", "port", "lan", "rpc"} {
		require.NotNil(t, serveCmd.Flags().Lookup(flag), "deck serve --%s", flag)
	}
	require.Error(t, serveCmd.Args(serveCmd, []string{"extra"}))
}

func TestServeNeedsPeersOrRemote(t *testing.T) {
	t.Parallel()
	err := servePeersOn(deckconfig.Config{}, false)
	require.ErrorContains(t, err, "peers.enabled")
	require.ErrorContains(t, servePeersOn(deckconfig.Config{Peers: &deckconfig.Peers{}}, false), "--remote")
	require.NoError(t, servePeersOn(deckconfig.Config{}, true))
	require.NoError(t, servePeersOn(deckconfig.Config{Peers: &deckconfig.Peers{Enabled: true}}, false))
}

// fakeEngine is busy for busyFor calls of Busy (forever when negative).
type fakeEngine struct {
	drained         bool
	busyFor         int
	busyBeforeDrain bool
}

func (f *fakeEngine) Drain() { f.drained = true }

func (f *fakeEngine) Busy() int {
	if !f.drained {
		f.busyBeforeDrain = true
	}
	if f.busyFor == 0 {
		return 0
	}
	f.busyFor--
	return 1
}

func TestDrainWaitsForAgents(t *testing.T) {
	t.Parallel()
	f := &fakeEngine{busyFor: 3}
	start := time.Now()
	drain(context.Background(), f, time.Minute)
	require.True(t, f.drained)
	require.False(t, f.busyBeforeDrain, "new work must be refused before the wait")
	require.Zero(t, f.busyFor)
	require.Less(t, time.Since(start), 10*time.Second)
}

func TestDrainStopsAtTheLimit(t *testing.T) {
	t.Parallel()
	start := time.Now()
	drain(context.Background(), &fakeEngine{busyFor: -1}, 300*time.Millisecond)
	took := time.Since(start)
	require.GreaterOrEqual(t, took, 300*time.Millisecond)
	require.Less(t, took, 5*time.Second)
}

func TestDrainStopsOnSecondSignal(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(100*time.Millisecond, cancel)
	start := time.Now()
	drain(ctx, &fakeEngine{busyFor: -1}, time.Hour)
	require.Less(t, time.Since(start), 5*time.Second)
}
