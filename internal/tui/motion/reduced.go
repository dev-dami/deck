package motion

import (
	"os"
	"sync/atomic"
)

var reduced atomic.Bool

func init() {
	reduced.Store(os.Getenv("DECK_REDUCED_MOTION") == "1")
}

// Reduced reports whether motion is globally reduced. When true every
// animation snaps to its target immediately and no frame ticks are scheduled.
func Reduced() bool { return reduced.Load() }

// SetReduced switches reduced motion on or off for the whole process.
func SetReduced(v bool) { reduced.Store(v) }
