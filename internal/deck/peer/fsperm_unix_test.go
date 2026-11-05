//go:build !windows

package peer

import (
	"os"
	"testing"
)

// shareWithOthers makes path readable and writable by every user.
func shareWithOthers(t *testing.T, path string) {
	t.Helper()
	if err := os.Chmod(path, 0o666); err != nil {
		t.Fatal(err)
	}
}
