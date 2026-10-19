package store

import (
	"path/filepath"
	"strings"
	"testing"
)

// Terminal Decks share the store; deck serve has it alone.
func TestLockSharedAndExclusive(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "threads")
	a, err := Lock(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Lock(dir, false)
	if err != nil {
		t.Fatalf("a second terminal Deck: %v", err)
	}
	if _, err := Lock(dir, true); err == nil || !strings.Contains(err.Error(), filepath.Join(dir, lockFile)) {
		t.Fatalf("serve while terminal Decks run: %v", err)
	}
	a()
	b()
	serve, err := Lock(dir, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Lock(dir, false); err == nil || !strings.Contains(err.Error(), "deck serve (pid ") {
		t.Fatalf("a terminal Deck while serve runs: %v", err)
	}
	serve()
	again, err := Lock(dir, false)
	if err != nil {
		t.Fatalf("Lock after release: %v", err)
	}
	again()
}
