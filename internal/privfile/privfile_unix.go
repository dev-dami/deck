//go:build !windows

package privfile

import (
	"fmt"
	"os"
	"syscall"
)

// Restrict limits path to the current user: 0700 for a directory, 0600
// for a file.
func Restrict(path string, isDir bool) error {
	mode := os.FileMode(0o600)
	if isDir {
		mode = 0o700
	}
	return os.Chmod(path, mode)
}

// Check reports an error when anyone but the current user can read or
// write path (any group or other permission bit set).
func Check(path string) error {
	fi, err := os.Stat(path)
	if err != nil {
		return err
	}
	if perm := fi.Mode().Perm(); perm&0o077 != 0 {
		return fmt.Errorf("%s is accessible by other users (mode %04o); run chmod go-rwx on it", path, perm)
	}
	return nil
}

// CheckTrusted is OpenSSH's StrictModes for a private directory: path must
// be owned by the user Deck runs as and not writable by group or others; with
// secret set it must not be readable by them either.
func CheckTrusted(path string, secret bool) error {
	fi, err := os.Stat(path)
	if err != nil {
		return err
	}
	if st, ok := fi.Sys().(*syscall.Stat_t); ok && int(st.Uid) != os.Geteuid() {
		return fmt.Errorf("%s is owned by uid %d, not by this user (uid %d)", path, st.Uid, os.Geteuid())
	}
	mask := os.FileMode(0o022)
	if secret {
		mask = 0o077
	}
	if perm := fi.Mode().Perm(); perm&mask != 0 {
		return fmt.Errorf("%s is accessible by other users (mode %04o); run chmod go-rwx on it", path, perm)
	}
	return nil
}
