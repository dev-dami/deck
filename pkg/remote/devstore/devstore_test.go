package devstore

import (
	"bytes"
	"errors"
	"os"
	"sync"
	"testing"
	"time"
)

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func newStore(t *testing.T) (*Store, *clock, string) {
	t.Helper()
	dir := t.TempDir()
	c := &clock{t: time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC)}
	s, err := open(dir, c.now)
	if err != nil {
		t.Fatal(err)
	}
	return s, c, dir
}

func key(b byte) []byte { return bytes.Repeat([]byte{b}, 32) }

func TestAddLookupAndPersist(t *testing.T) {
	s, c, dir := newStore(t)
	d, token, err := s.Add("Pixel 9", key(1))
	if err != nil {
		t.Fatal(err)
	}
	if d.RelayTokenHash != HashToken(token) || d.RelayTokenHash == token {
		t.Fatal("only the token's hash may be stored")
	}
	got, err := s.ByStatic(key(1))
	if err != nil || got.ID != d.ID {
		t.Fatalf("ByStatic: %v %v", got, err)
	}
	if _, err := s.ByStatic(key(2)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown key: %v", err)
	}
	raw, _ := os.ReadFile(s.path)
	if bytes.Contains(raw, []byte(token)) {
		t.Fatal("the relay token reached disk")
	}
	again, err := open(dir, c.now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := again.ByStatic(key(1)); err != nil {
		t.Fatalf("device lost across reopen: %v", err)
	}
}

func TestAddRejectsDuplicatesAndBadKeys(t *testing.T) {
	s, _, _ := newStore(t)
	if _, _, err := s.Add("a", key(1)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Add("again", key(1)); err == nil {
		t.Fatal("the same phone paired twice")
	}
	if _, _, err := s.Add("short", []byte{1}); err == nil {
		t.Fatal("a short key was accepted")
	}
}

func TestRevokeIsImmediate(t *testing.T) {
	s, _, _ := newStore(t)
	d, _, _ := s.Add("a", key(1))
	if err := s.Revoke(d.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ByStatic(key(1)); !errors.Is(err, ErrNotFound) {
		t.Fatal("a revoked device is still accepted")
	}
	if len(s.TokenHashes()) != 0 {
		t.Fatal("a revoked device's token hash is still listed")
	}
	if err := s.Revoke(d.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second revoke: %v", err)
	}
}

func TestExpiryAndTouch(t *testing.T) {
	s, c, _ := newStore(t)
	d, _, _ := s.Add("a", key(1))
	c.advance(89 * 24 * time.Hour)
	if _, err := s.ByStatic(key(1)); err != nil {
		t.Fatalf("expired early: %v", err)
	}
	if err := s.Touch(d.ID); err != nil {
		t.Fatal(err)
	}
	c.advance(89 * 24 * time.Hour)
	if _, err := s.ByStatic(key(1)); err != nil {
		t.Fatal("use did not extend the expiry")
	}
	c.advance(91 * 24 * time.Hour)
	if _, err := s.ByStatic(key(1)); !errors.Is(err, ErrNotFound) {
		t.Fatal("an unused device did not expire")
	}
	if n, err := s.Prune(); err != nil || n != 1 {
		t.Fatalf("prune: %d %v", n, err)
	}
}

func TestTouchWritesAtMostOncePerMinute(t *testing.T) {
	s, c, _ := newStore(t)
	d, _, _ := s.Add("a", key(1))
	if err := s.Touch(d.ID); err != nil {
		t.Fatal(err)
	}
	before, _ := os.Stat(s.path)
	c.advance(10 * time.Second)
	if err := s.Touch(d.ID); err != nil {
		t.Fatal(err)
	}
	after, _ := os.Stat(s.path)
	if !after.ModTime().Equal(before.ModTime()) {
		t.Fatal("a second touch within a minute rewrote the file")
	}
	c.advance(time.Minute)
	if got, _ := s.Get(d.ID); !got.LastSeen.Equal(time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC)) {
		t.Fatalf("last seen %v", got.LastSeen)
	}
}

func TestYoloIsOffByDefaultAndPerDevice(t *testing.T) {
	s, _, _ := newStore(t)
	a, _, _ := s.Add("a", key(1))
	b, _, _ := s.Add("b", key(2))
	if a.AllowYolo || b.AllowYolo {
		t.Fatal("a new device has yolo")
	}
	if err := s.SetYolo(a.ID, true); err != nil {
		t.Fatal(err)
	}
	ga, _ := s.Get(a.ID)
	gb, _ := s.Get(b.ID)
	if !ga.AllowYolo || gb.AllowYolo {
		t.Fatal("the grant leaked to another device")
	}
}

func TestPasskeysDoNotMutateAPublishedSnapshot(t *testing.T) {
	s, _, _ := newStore(t)
	d, _, _ := s.Add("a", key(1))
	if err := s.AddPasskey(d.ID, Passkey{ID: "p1", PublicKey: []byte{1}}); err != nil {
		t.Fatal(err)
	}
	held, _ := s.Get(d.ID) // a reader holding the old snapshot
	if err := s.AddPasskey(d.ID, Passkey{ID: "p2"}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetSignCount(d.ID, "p1", 9); err != nil {
		t.Fatal(err)
	}
	if len(held.Passkeys) != 1 || held.Passkeys[0].SignCount != 0 {
		t.Fatal("a held device changed under its reader")
	}
	now, _ := s.Get(d.ID)
	if len(now.Passkeys) != 2 || now.Passkeys[0].SignCount != 9 {
		t.Fatalf("passkeys %+v", now.Passkeys)
	}
	if err := s.RemovePasskey(d.ID, "p1"); err != nil {
		t.Fatal(err)
	}
	if now, _ = s.Get(d.ID); len(now.Passkeys) != 1 {
		t.Fatal("passkey not removed")
	}
}

func TestReloadSeesAnotherWriter(t *testing.T) {
	s, c, dir := newStore(t)
	other, err := open(dir, c.now)
	if err != nil {
		t.Fatal(err)
	}
	d, _, _ := other.Add("a", key(1))
	// Make the mtime differ even on coarse filesystems.
	future := time.Now().Add(time.Hour)
	if err := os.Chtimes(s.path, future, future); err != nil {
		t.Fatal(err)
	}
	changed, err := s.Reload()
	if err != nil || !changed {
		t.Fatalf("reload: %v %v", changed, err)
	}
	if _, err := s.Get(d.ID); err != nil {
		t.Fatalf("reloaded store misses the device: %v", err)
	}
	if changed, _ := s.Reload(); changed {
		t.Fatal("reload reported a change twice")
	}
}

func TestCorruptFileIsAnError(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(dir+"/"+fileName, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(dir); err == nil {
		t.Fatal("a corrupt device file was accepted")
	}
}

func TestConcurrentReadsDuringWrites(t *testing.T) {
	s, _, _ := newStore(t)
	d, _, _ := s.Add("a", key(1))
	var wg sync.WaitGroup
	stop := make(chan struct{})
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					if _, err := s.ByStatic(key(1)); err != nil {
						t.Error(err)
						return
					}
				}
			}
		}()
	}
	for i := range 20 {
		if err := s.SetYolo(d.ID, i%2 == 0); err != nil {
			t.Fatal(err)
		}
	}
	close(stop)
	wg.Wait()
}

func TestPendingDeviceIsInvisibleUntilConfirmed(t *testing.T) {
	s, _, _ := newStore(t)
	d, _, err := s.AddPending("Pixel 9", key(1))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ByStatic(key(1)); !errors.Is(err, ErrNotFound) {
		t.Fatal("a pending device was accepted by static key")
	}
	if _, err := s.Get(d.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("a pending device was returned by Get")
	}
	if len(s.List()) != 0 || len(s.TokenHashes()) != 0 {
		t.Fatal("a pending device is listed or its token hash is sent to the relay")
	}
	if _, _, err := s.Add("again", key(1)); err == nil {
		t.Fatal("the same phone paired twice while one pairing was pending")
	}
	if err := s.Confirm(d.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ByStatic(key(1)); err != nil {
		t.Fatalf("confirmed device not accepted: %v", err)
	}
	if err := s.Confirm(d.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("a confirmed device was confirmed twice")
	}
}

func TestPendingDeviceCanBeRevoked(t *testing.T) {
	s, _, _ := newStore(t)
	d, _, _ := s.AddPending("Pixel 9", key(1))
	if err := s.Revoke(d.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Add("retry", key(1)); err != nil {
		t.Fatalf("the same phone could not retry after a failed pairing: %v", err)
	}
}
