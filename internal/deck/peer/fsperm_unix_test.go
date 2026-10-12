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

func TestCheckTrustedModeBits(t *testing.T) {
	path := t.TempDir() + "/f"
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	for mode, want := range map[os.FileMode][2]bool{ // {ok as a pin list, ok as a secret}
		0o600: {true, true},
		0o644: {true, false},
		0o640: {true, false},
		0o620: {false, false},
		0o602: {false, false},
		0o666: {false, false},
	} {
		if err := os.Chmod(path, mode); err != nil {
			t.Fatal(err)
		}
		if got := checkTrusted(path, false) == nil; got != want[0] {
			t.Errorf("mode %04o: trusted = %v, want %v", mode, got, want[0])
		}
		if got := checkTrusted(path, true) == nil; got != want[1] {
			t.Errorf("mode %04o: trusted as secret = %v, want %v", mode, got, want[1])
		}
	}
}
