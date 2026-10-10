//go:build !darwin

package notification

import (
	_ "embed"
)

//go:embed deck-icon-solo.png
var Icon []byte
