package store

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestLockFailsFastWhileHeld(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "threads")
	release, err := Lock(dir)
	if err != nil {
		t.Fatalf("first Lock: %v", err)
	}
	_, err = Lock(dir)
	if err == nil {
		t.Fatal("a second Lock got the store too")
	}
	want := filepath.Join(dir, lockFile)
	if !strings.Contains(err.Error(), want) || !strings.Contains(err.Error(), "pid ") {
		t.Fatalf("the error must name the lock file and the owner: %v", err)
	}
	release()
	again, err := Lock(dir)
	if err != nil {
		t.Fatalf("Lock after release: %v", err)
	}
	again()
}
