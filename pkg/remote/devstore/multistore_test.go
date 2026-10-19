package devstore

import (
	"errors"
	"os"
	"sync"
	"testing"
)

func TestStaleTouchCannotResurrectRevokedDevice(t *testing.T) {
	for _, throttled := range []bool{false, true} {
		t.Run(map[bool]string{false: "first_touch", true: "throttled_touch"}[throttled], func(t *testing.T) {
			a, c, dir := newStore(t)
			d, _, err := a.Add("phone", key(1))
			if err != nil {
				t.Fatal(err)
			}
			if throttled {
				if err := a.Touch(d.ID); err != nil {
					t.Fatal(err)
				}
			}
			_, _ = a.Reload() // consume the local add notification
			b, err := open(dir, c.now)
			if err != nil {
				t.Fatal(err)
			}
			if err := b.Revoke(d.ID); err != nil {
				t.Fatal(err)
			}
			if err := a.Touch(d.ID); !errors.Is(err, ErrNotFound) {
				t.Fatalf("stale touch: %v", err)
			}
			latest, err := open(dir, c.now)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := latest.Get(d.ID); !errors.Is(err, ErrNotFound) {
				t.Fatal("touch resurrected phone on disk")
			}
			if changed, err := a.Reload(); err != nil || !changed {
				t.Fatalf("mutation swallowed watcher notification: %t %v", changed, err)
			}
			if changed, err := a.Reload(); err != nil || changed {
				t.Fatalf("notification was not consumed: %t %v", changed, err)
			}
		})
	}
}

func TestExternalRevocationStillNotifiesAfterSuccessfulMutation(t *testing.T) {
	a, c, dir := newStore(t)
	revoked, _, _ := a.Add("revoked", key(1))
	kept, _, _ := a.Add("kept", key(2))
	_, _ = a.Reload()
	b, err := open(dir, c.now)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Revoke(revoked.ID); err != nil {
		t.Fatal(err)
	}
	if err := a.SetYolo(kept.ID, true); err != nil {
		t.Fatal(err)
	}
	if changed, err := a.Reload(); err != nil || !changed {
		t.Fatalf("revocation watcher notification lost: %t %v", changed, err)
	}
	if len(a.TokenHashes()) != 1 {
		t.Fatal("revoked relay token remains")
	}
}

func TestConcurrentStoresPreserveIndependentPreferences(t *testing.T) {
	a, c, dir := newStore(t)
	d, _, _ := a.Add("phone", key(1))
	b, err := open(dir, c.now)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 20; i++ {
			if err := a.SetYolo(d.ID, true); err != nil {
				errs <- err
				return
			}
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 20; i++ {
			if err := b.SetPush(d.ID, &PushPrefs{P256dh: "public", Auth: "auth", Approvals: true}); err != nil {
				errs <- err
				return
			}
		}
	}()
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	latest, err := open(dir, c.now)
	if err != nil {
		t.Fatal(err)
	}
	got, err := latest.Get(d.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !got.AllowYolo || got.Push == nil || !got.Push.Approvals {
		t.Fatalf("lost independent preference update: %+v", got)
	}
}

func TestReloadMissingFileClearsTrustedSnapshot(t *testing.T) {
	s, _, _ := newStore(t)
	d, _, _ := s.Add("phone", key(1))
	_, _ = s.Reload()
	if err := os.Remove(s.path); err != nil {
		t.Fatal(err)
	}
	if changed, err := s.Reload(); err != nil || !changed {
		t.Fatalf("missing file reload: %t %v", changed, err)
	}
	if _, err := s.Get(d.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("deleted store still trusts phone")
	}
	if len(s.TokenHashes()) != 0 {
		t.Fatal("deleted store retained relay token")
	}
}
