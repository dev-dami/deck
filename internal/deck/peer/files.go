package peer

import (
	"fmt"
	"path/filepath"

	"github.com/dev-dami/deck/internal/deck/config"
	"github.com/dev-dami/deck/internal/privfile"
)

// DefaultDir returns ~/.deck/peers, under the same ~/.deck as Deck's config.
func DefaultDir() (string, error) {
	d, err := config.Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "peers"), nil
}

// checkLoad runs before a file from the peers directory is trusted: the
// directory and the file must be the current user's alone
// (privfile.CheckTrusted), or someone else could have pinned their own key or
// swapped the identity. It fails closed with a message that says what to fix.
func checkLoad(path string, secret bool) error {
	if err := privfile.CheckTrusted(filepath.Dir(path), false); err != nil {
		return fmt.Errorf("peer: refusing to use %s: %v", path, err)
	}
	if err := privfile.CheckTrusted(path, secret); err != nil {
		return fmt.Errorf("peer: refusing to use %s: %v (fix its permissions, or remove it and pair again)", path, err)
	}
	return nil
}
