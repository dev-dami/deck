package auth

import (
	"fmt"
	"testing"
	"time"
)

// TestLockoutTableAtCapacity: with the table full of live lockouts a new
// source is still tracked and locked (the oldest record makes room).
func TestLockoutTableAtCapacity(t *testing.T) {
	now := time.Unix(0, 0)
	l := NewLockout(1, time.Hour, time.Hour, func() time.Time { return now })
	for i := 0; i < maxTracked; i++ {
		l.Fail(fmt.Sprintf("10.%d.%d.%d", i>>16&255, i>>8&255, i&255))
		now = now.Add(time.Millisecond)
	}
	if l.Len() != maxTracked || !l.Locked("10.0.0.0") {
		t.Fatalf("table %d, first locked %v", l.Len(), l.Locked("10.0.0.0"))
	}
	l.Fail("192.0.2.1")
	if !l.Locked("192.0.2.1") {
		t.Fatal("new source ignored when the table is full")
	}
	if l.Len() > maxTracked {
		t.Fatalf("table grew to %d", l.Len())
	}
	if l.Locked("10.0.0.0") {
		t.Fatal("the oldest record was not the one evicted")
	}
	if !l.Locked("10.0.0.1") {
		t.Fatal("a newer record was evicted")
	}
}

func TestLockoutBackoffGrows(t *testing.T) {
	now := time.Unix(0, 0)
	l := NewLockout(2, time.Second, 4*time.Second, func() time.Time { return now })
	ip := "192.0.2.1"
	for strike, want := range []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 4 * time.Second} {
		l.Fail(ip)
		l.Fail(ip)
		if !l.Locked(ip) {
			t.Fatalf("strike %d: not locked", strike)
		}
		now = now.Add(want - time.Millisecond)
		if !l.Locked(ip) {
			t.Fatalf("strike %d: unlocked before %s", strike, want)
		}
		now = now.Add(2 * time.Millisecond)
		if l.Locked(ip) {
			t.Fatalf("strike %d: still locked after %s", strike, want)
		}
	}
	l.OK(ip)
	if l.Locked(ip) {
		t.Fatal("locked after OK")
	}
}

func TestLockKey(t *testing.T) {
	for addr, want := range map[string]string{
		"[2001:db8:1:2:3:4:5:6]:1": "2001:db8:1:2::/64",
		"192.0.2.7:1":              "192.0.2.7",
		"192.0.2.7":                "192.0.2.7",
		"pipe":                     "pipe",
	} {
		if got := LockKey(addr); got != want {
			t.Errorf("LockKey(%q) = %q, want %q", addr, got, want)
		}
	}
}
