package auth

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestR1_LimitsArePerSourceAndPerDeviceNeverGlobal(t *testing.T) {
	clk := newClock()
	l := NewLimits(clk.now)
	for i := 0; i < SourceFailures; i++ {
		_, locked := l.Locked("10.0.0.1:1234", "")
		require.False(t, locked)
		l.Fail("10.0.0.1:1234", "")
	}
	wait, locked := l.Locked("10.0.0.1:9999", "")
	require.True(t, locked, "the lockout is per address, not per port")
	require.Equal(t, SourceLockout, wait)
	_, locked = l.Locked("10.0.0.2:1", "")
	require.False(t, locked, "another source is not affected")

	for i := 0; i < DeviceFailures; i++ {
		l.Fail("10.0.0.3:1", "dev1")
	}
	_, locked = l.Locked("10.0.0.4:1", "dev1")
	require.True(t, locked, "the device is locked from any address")
	_, locked = l.Locked("10.0.0.4:1", "dev2")
	require.False(t, locked)

	clk.advance(SourceLockout + time.Second)
	_, locked = l.Locked("10.0.0.1:1", "")
	require.False(t, locked, "a lockout ends")
	l.OK("10.0.0.3:1", "dev1")
	_, locked = l.Locked("10.0.0.3:1", "dev1")
	require.False(t, locked, "success clears the record")
}

func TestR4_LimitsArePruned(t *testing.T) {
	clk := newClock()
	l := NewLimits(clk.now)
	l.Fail("10.0.0.1:1", "d")
	require.Equal(t, 2, l.Tracked())
	l.Prune()
	require.Equal(t, 2, l.Tracked(), "a fresh record stays")
	clk.advance(MaxLockout + time.Minute)
	l.Prune()
	require.Zero(t, l.Tracked())
}

func TestIPv6LocksTheWhole64(t *testing.T) {
	clk := newClock()
	l := NewLimits(clk.now)
	for i := 0; i < SourceFailures; i++ {
		l.Fail("[2001:db8::1]:1", "")
	}
	_, locked := l.Locked("[2001:db8::2]:1", "")
	require.True(t, locked)
	_, locked = l.Locked("[2001:db9::1]:1", "")
	require.False(t, locked)
}
