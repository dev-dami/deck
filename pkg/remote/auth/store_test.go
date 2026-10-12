package auth

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dev-dami/deck/internal/deck/peer"
	"github.com/dev-dami/deck/pkg/remote/wire"
)

type clock struct{ t atomic.Pointer[time.Time] }

func newClock() *clock {
	c := &clock{}
	c.set(time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC))
	return c
}

func (c *clock) set(t time.Time)         { c.t.Store(&t) }
func (c *clock) now() time.Time          { return *c.t.Load() }
func (c *clock) advance(d time.Duration) { c.set(c.now().Add(d)) }
func openStore(t *testing.T, clk *clock) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "remote"), WithClock(clk.now))
	require.NoError(t, err)
	return s
}

func pair(t *testing.T, s *Store, name string) (string, Device) {
	t.Helper()
	c, err := s.Arm()
	require.NoError(t, err)
	tok, dev, err := s.Pair(c.Code, name)
	require.NoError(t, err)
	return tok, dev
}

func TestPairVerifyRevoke(t *testing.T) {
	clk := newClock()
	s := openStore(t, clk)
	require.NotEmpty(t, s.ServerID())

	tok, dev := pair(t, s, "  Pixel ")
	require.Equal(t, "Pixel", dev.Name)
	require.True(t, strings.HasPrefix(tok, dev.ID+"."))

	got, err := s.Verify(tok)
	require.NoError(t, err)
	require.Equal(t, dev.ID, got.ID)
	require.False(t, got.AllowYolo, "yolo is off for a new device")

	_, err = s.Verify(dev.ID + ".wrong")
	require.ErrorIs(t, err, ErrInvalidToken)
	_, err = s.Verify("nope")
	require.ErrorIs(t, err, ErrInvalidToken)

	require.NoError(t, s.Revoke(dev.ID))
	_, err = s.Verify(tok)
	require.ErrorIs(t, err, ErrInvalidToken)
	require.ErrorIs(t, s.Revoke(dev.ID), ErrNotFound)

	data, err := os.ReadFile(s.path(DevicesFile))
	require.NoError(t, err)
	require.NotContains(t, string(data), tok[len(dev.ID)+1:], "the secret is never stored")
}

func TestR3_OneCodeAtATimeAndExpiry(t *testing.T) {
	clk := newClock()
	s := openStore(t, clk)
	first, err := s.Arm()
	require.NoError(t, err)
	second, err := s.Arm()
	require.NoError(t, err)
	_, _, err = s.Pair(first.Code, "a")
	require.ErrorIs(t, err, ErrInvalidCode, "arming again replaces the code")

	// A code is typed with any case and spacing.
	typed := strings.ToLower(strings.ReplaceAll(second.Code, "-", " "))
	_, _, err = s.Pair(typed, "a")
	require.NoError(t, err)
	_, _, err = s.Pair(second.Code, "b")
	require.ErrorIs(t, err, ErrInvalidCode, "a code is used once")

	third, err := s.Arm()
	require.NoError(t, err)
	exp, ok := s.Armed()
	require.True(t, ok)
	require.Equal(t, clk.now().Add(wire.PairCodeTTL).Truncate(time.Second), exp)
	clk.advance(wire.PairCodeTTL + time.Second)
	_, ok = s.Armed()
	require.False(t, ok)
	_, _, err = s.Pair(third.Code, "c")
	require.ErrorIs(t, err, ErrInvalidCode, "an expired code is refused")
}

func TestR3_TokenIsScopedToTheDataDir(t *testing.T) {
	clk := newClock()
	a := openStore(t, clk)
	b := openStore(t, clk)
	tok, _ := pair(t, a, "Pixel")
	_, err := b.Verify(tok)
	require.ErrorIs(t, err, ErrInvalidToken, "another project does not know the token")

	// Even the file itself, copied over, is bound to the server id.
	data, err := os.ReadFile(a.path(DevicesFile))
	require.NoError(t, err)
	fixed := strings.Replace(string(data), a.ServerID(), b.ServerID(), 1)
	require.NoError(t, os.WriteFile(b.path(DevicesFile), []byte(fixed), 0o600))
	_, err = b.Verify(tok)
	require.ErrorIs(t, err, ErrInvalidToken)
}

func TestR3_KeysExpireAfterIdle(t *testing.T) {
	clk := newClock()
	s := openStore(t, clk)
	tok, dev := pair(t, s, "Pixel")
	require.Equal(t, dev.CreatedAt.Add(wire.DeviceKeyExpiry), dev.ExpiresAt())

	clk.advance(wire.DeviceKeyExpiry - time.Hour)
	_, err := s.Verify(tok)
	require.NoError(t, err)
	s.Touch(dev.ID)
	require.NoError(t, s.Flush())

	clk.advance(wire.DeviceKeyExpiry - time.Hour)
	got, err := s.Verify(tok)
	require.NoError(t, err, "use pushes the expiry out")
	require.Equal(t, got.LastSeen.Add(wire.DeviceKeyExpiry), got.ExpiresAt())

	clk.advance(2 * time.Hour)
	_, err = s.Verify(tok)
	require.ErrorIs(t, err, ErrExpired)
}

func TestR16_LastSeenIsDebouncedAndOffPath(t *testing.T) {
	clk := newClock()
	s := openStore(t, clk)
	_, dev := pair(t, s, "Pixel")
	before, err := os.Stat(s.path(DevicesFile))
	require.NoError(t, err)

	s.Touch(dev.ID)
	after, err := os.Stat(s.path(DevicesFile))
	require.NoError(t, err)
	require.Equal(t, before.ModTime(), after.ModTime(), "Touch writes nothing")

	require.NoError(t, s.Flush())
	got, err := s.Get(dev.ID)
	require.NoError(t, err)
	require.Equal(t, clk.now().Truncate(time.Second), got.LastSeen)

	clk.advance(10 * time.Second)
	s.Touch(dev.ID)
	s.mu.Lock()
	n := len(s.pending)
	s.mu.Unlock()
	require.Zero(t, n, "a second touch inside a minute is dropped")

	clk.advance(time.Minute)
	s.Touch(dev.ID)
	require.NoError(t, s.Flush())
	got, err = s.Get(dev.ID)
	require.NoError(t, err)
	require.Equal(t, clk.now().Truncate(time.Second), got.LastSeen)
}

func TestR16_WatcherSeesRevocation(t *testing.T) {
	clk := newClock()
	s := openStore(t, clk)
	_, dev := pair(t, s, "Pixel")

	changes := make(chan struct{}, 8)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.Watch(ctx, func() { changes <- struct{}{} })

	// A second store on the same directory stands in for `deck remote
	// revoke` in another process.
	other, err := Open(s.Dir(), WithClock(clk.now))
	require.NoError(t, err)
	require.NoError(t, other.Revoke(dev.ID))
	select {
	case <-changes:
	case <-time.After(2 * PollInterval):
		t.Fatal("the watcher did not report the change")
	}
}

func TestR15_AllowYoloPerDevice(t *testing.T) {
	clk := newClock()
	s := openStore(t, clk)
	_, a := pair(t, s, "a")
	_, b := pair(t, s, "b")
	require.NoError(t, s.SetAllowYolo(a.ID, true))
	require.ErrorIs(t, s.SetAllowYolo("ghost", true), ErrNotFound)
	devs, err := s.List()
	require.NoError(t, err)
	require.Len(t, devs, 2)
	for _, d := range devs {
		require.Equal(t, d.ID == a.ID, d.AllowYolo)
	}
	w := devs[1].Wire(true)
	require.Equal(t, b.ID, w.ID)
	require.True(t, w.Self)
	require.True(t, w.LastSeen.IsZero())
}

func TestFilesArePrivate(t *testing.T) {
	clk := newClock()
	s := openStore(t, clk)
	pair(t, s, "a")
	require.NoError(t, peer.CheckPrivate(s.Dir()))
	require.NoError(t, peer.CheckPrivate(s.path(DevicesFile)))
}

func TestAdvert(t *testing.T) {
	clk := newClock()
	s := openStore(t, clk)
	_, err := s.ReadAdvert()
	require.ErrorIs(t, err, ErrNoServer)
	a := Advert{URL: "http://127.0.0.1:7878", Hosts: []string{"127.0.0.1"}, Port: 7878, PID: os.Getpid(), StartedAt: clk.now()}
	require.NoError(t, s.WriteAdvert(a))
	got, err := s.ReadAdvert()
	require.NoError(t, err)
	require.Equal(t, a.URL, got.URL)
	require.NoError(t, s.ClearAdvert())
	_, err = s.ReadAdvert()
	require.ErrorIs(t, err, ErrNoServer)
}
