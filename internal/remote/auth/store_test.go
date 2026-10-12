package auth

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func newStore(t *testing.T) (*Store, *clock) {
	t.Helper()
	c := &clock{t: time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC)}
	s, err := New(t.TempDir(), WithClock(c.now))
	if err != nil {
		t.Fatal(err)
	}
	return s, c
}

func pair(t *testing.T, s *Store) (string, Device) {
	t.Helper()
	code, err := s.NewCode()
	if err != nil {
		t.Fatal(err)
	}
	tok, dev, err := s.Pair(code, "Pixel", "10.0.0.2")
	if err != nil {
		t.Error(err)
	}
	return tok, dev
}

func TestPairThenVerify(t *testing.T) {
	s, _ := newStore(t)
	tok, dev := pair(t, s)
	got, err := s.Verify(tok, "10.0.0.2")
	if err != nil || got.ID != dev.ID || got.Name != "Pixel" {
		t.Fatalf("Verify = %+v, %v", got, err)
	}
}

func TestVerifyRejects(t *testing.T) {
	s, _ := newStore(t)
	tok, dev := pair(t, s)
	for name, bad := range map[string]string{
		"wrong secret": dev.ID + ".nope",
		"unknown id":   "zzz." + strings.SplitN(tok, ".", 2)[1],
		"no dot":       "justonestring",
		"empty":        "",
		"empty secret": dev.ID + ".",
	} {
		if _, err := s.Verify(bad, "x"); !errors.Is(err, ErrInvalidToken) {
			t.Errorf("%s: err = %v, want ErrInvalidToken", name, err)
		}
	}
}

func TestSecretIsNotStored(t *testing.T) {
	s, _ := newStore(t)
	tok, _ := pair(t, s)
	data, err := os.ReadFile(s.path())
	if err != nil {
		t.Fatal(err)
	}
	secret := strings.SplitN(tok, ".", 2)[1]
	if strings.Contains(string(data), secret) {
		t.Fatal("devices.json contains the raw secret")
	}
}

func TestCodeSingleUse(t *testing.T) {
	s, _ := newStore(t)
	code, _ := s.NewCode()
	if _, _, err := s.Pair(code, "a", ""); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Pair(code, "b", ""); !errors.Is(err, ErrInvalidCode) {
		t.Fatalf("second use err = %v", err)
	}
}

func TestCodeExpires(t *testing.T) {
	s, c := newStore(t)
	code, _ := s.NewCode()
	c.add(CodeTTL + time.Second)
	if _, _, err := s.Pair(code, "a", ""); !errors.Is(err, ErrInvalidCode) {
		t.Fatalf("expired err = %v", err)
	}
}

func TestUnknownCode(t *testing.T) {
	s, _ := newStore(t)
	if _, _, err := s.Pair("bogus", "a", ""); !errors.Is(err, ErrInvalidCode) {
		t.Fatalf("err = %v", err)
	}
}

func TestCodeRaceHasOneWinner(t *testing.T) {
	s, _ := newStore(t)
	code, _ := s.NewCode()
	var wins atomic.Int32
	var wg sync.WaitGroup
	for range 50 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, _, err := s.Pair(code, "p", ""); err == nil {
				wins.Add(1)
			}
		}()
	}
	wg.Wait()
	if wins.Load() != 1 {
		t.Fatalf("%d pairings succeeded, want 1", wins.Load())
	}
}

func TestRevoke(t *testing.T) {
	s, _ := newStore(t)
	tok, dev := pair(t, s)
	if err := s.Revoke(dev.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Verify(tok, "x"); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("after revoke err = %v", err)
	}
	if err := s.Revoke(dev.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second revoke err = %v", err)
	}
}

func TestRevokeAll(t *testing.T) {
	s, _ := newStore(t)
	pair(t, s)
	pair(t, s)
	n, err := s.RevokeAll()
	if err != nil || n != 2 {
		t.Fatalf("RevokeAll = %d, %v", n, err)
	}
	if ds, _ := s.List(); len(ds) != 0 {
		t.Fatalf("%d devices left", len(ds))
	}
}

func TestSecondStoreSeesRevokeAndDoesNotClobber(t *testing.T) {
	dir := t.TempDir()
	a, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	b, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	tokA, devA := pair(t, a)
	tokB, _ := pair(t, b)
	if _, err := b.Verify(tokA, "x"); err != nil {
		t.Fatalf("store b cannot see a's device: %v", err)
	}
	if err := b.Revoke(devA.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Verify(tokA, "x"); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("a still accepts revoked token: %v", err)
	}
	if _, err := a.Verify(tokB, "x"); err != nil {
		t.Fatalf("a lost b's device: %v", err)
	}
}

func TestConcurrentPairingsAllPersist(t *testing.T) {
	s, _ := newStore(t)
	var wg sync.WaitGroup
	for range 10 {
		wg.Add(1)
		go func() { defer wg.Done(); pair(t, s) }()
	}
	wg.Wait()
	if ds, _ := s.List(); len(ds) != 10 {
		t.Fatalf("%d devices persisted, want 10", len(ds))
	}
}

func TestLastSeenIsThrottled(t *testing.T) {
	s, c := newStore(t)
	paired := c.now()
	tok, _ := pair(t, s)
	c.add(10 * time.Second)
	d, _ := s.Verify(tok, "10.0.0.2")
	if !d.LastSeen.Equal(paired) {
		t.Fatalf("last_seen rewritten within granularity: %v", d.LastSeen)
	}
	c.add(2 * time.Minute)
	d, _ = s.Verify(tok, "10.0.0.2")
	if !d.LastSeen.Equal(c.now()) {
		t.Fatalf("last_seen not refreshed: %v", d.LastSeen)
	}
}

func TestFileMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX modes")
	}
	s, _ := newStore(t)
	pair(t, s)
	fi, err := os.Stat(s.path())
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v", fi.Mode().Perm())
	}
}

func TestCorruptFileIsAnError(t *testing.T) {
	s, _ := newStore(t)
	if err := os.WriteFile(s.path(), []byte("{nope"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.List(); err == nil {
		t.Fatal("corrupt devices.json accepted")
	}
}

func TestLimiterPerAddr(t *testing.T) {
	c := &clock{t: time.Unix(1000, 0)}
	l := NewLimiter(c.now)
	for range 5 {
		if !l.Allowed("a") {
			t.Fatal("blocked too early")
		}
		l.Fail("a")
	}
	if l.Allowed("a") {
		t.Fatal("not blocked after 5 failures")
	}
	if !l.Allowed("b") {
		t.Fatal("other address blocked")
	}
	c.add(2 * time.Minute)
	if !l.Allowed("a") {
		t.Fatal("block did not expire")
	}
}

func TestLimiterGlobalDisable(t *testing.T) {
	c := &clock{t: time.Unix(1000, 0)}
	l := NewLimiter(c.now)
	for i := range 20 {
		l.Fail(fmt.Sprintf("addr-%d", i))
	}
	if !l.Disabled() || l.Allowed("fresh") {
		t.Fatal("pairing not disabled after 20 failures")
	}
	c.add(time.Hour)
	if l.Allowed("fresh") {
		t.Fatal("global lockout must last until restart")
	}
}
