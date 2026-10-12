//go:build !windows

package peer

import (
	"fmt"
	"os"
)

// restrictPath limits path to the current user: 0700 for a directory, 0600
// for a file.
func restrictPath(path string, isDir bool) error {
	mode := os.FileMode(0o600)
	if isDir {
		mode = 0o700
	}
	return os.Chmod(path, mode)
}

// CheckPrivate reports an error when anyone but the current user can read or
// write path (any group or other permission bit set).
func CheckPrivate(path string) error {
	fi, err := os.Stat(path)
	if err != nil {
		return err
	}
	if perm := fi.Mode().Perm(); perm&0o077 != 0 {
		return fmt.Errorf("%s is accessible by other users (mode %04o); run chmod go-rwx on it", path, perm)
	}
	return nil
}
