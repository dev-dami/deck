package peer

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/dev-dami/deck/internal/deck/config"
)

// DefaultDir returns ~/.deck/peers, under the same ~/.deck as Deck's config.
func DefaultDir() (string, error) {
	d, err := config.Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "peers"), nil
}

// ensurePrivateDir creates dir when missing and limits it to the current user.
func ensurePrivateDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	return restrictPath(dir, true)
}

// checkLoad runs before a file from the peers directory is trusted: the
// directory and the file must be the current user's alone (checkTrusted), or
// someone else could have pinned their own key or swapped the identity. It
// fails closed with a message that says what to fix.
func checkLoad(path string, secret bool) error {
	if err := checkTrusted(filepath.Dir(path), false); err != nil {
		return fmt.Errorf("peer: refusing to use %s: %v", path, err)
	}
	if err := checkTrusted(path, secret); err != nil {
		return fmt.Errorf("peer: refusing to use %s: %v (fix its permissions, or remove it and pair again)", path, err)
	}
	return nil
}

// writePrivateFile writes data to path atomically: a temp file in the same
// directory is restricted to the current user before anything is written to
// it, then renamed over path. With exclusive set the rename is replaced by a
// hard link, which fails with fs.ErrExist when path already exists, so two
// processes creating the same file never overwrite each other.
func writePrivateFile(path string, data []byte, exclusive bool) error {
	dir := filepath.Dir(path)
	if err := ensurePrivateDir(dir); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp) // no-op after a successful rename
	if err := restrictPath(tmp, false); err != nil {
		f.Close()
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if exclusive {
		err := os.Link(tmp, path)
		if err == nil || errors.Is(err, fs.ErrExist) {
			return err
		}
		// No hard links on this file system: fall back to a plain create.
		if _, serr := os.Stat(path); serr == nil {
			return fs.ErrExist
		}
	}
	return renameRetry(tmp, path)
}

// renameRetry renames tmp over path, retrying briefly: on Windows a reader or
// an antivirus scanner holding the destination open makes rename fail for a
// moment.
func renameRetry(tmp, path string) error {
	var err error
	for i := 0; i < 10; i++ {
		if err = os.Rename(tmp, path); err == nil {
			return nil
		}
		time.Sleep(time.Duration(i+1) * 10 * time.Millisecond)
	}
	return err
}
