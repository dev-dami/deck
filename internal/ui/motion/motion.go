// Package motion says whether the UI may animate: off when the user set
// motion to "off" in Deck's settings or DECK_REDUCED_MOTION=1.
package motion

import (
	"os"
	"sync/atomic"
)

var off atomic.Bool

// Set records the user's motion setting ("on" or "off").
func Set(setting string) { off.Store(setting == "off") }

// Off reports whether animations should hold still.
func Off() bool { return off.Load() || os.Getenv("DECK_REDUCED_MOTION") == "1" }
