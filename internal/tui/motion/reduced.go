package motion

import (
	"os"
	"sync/atomic"
)

// reduced is the user's choice (DECK_REDUCED_MOTION, config, SetReduced);
// degraded is the frame budget giving up on a slow terminal.
var reduced, degraded atomic.Bool

func init() {
	reduced.Store(os.Getenv("DECK_REDUCED_MOTION") == "1")
}

// Reduced reports whether motion is globally reduced. When true every
// animation snaps to its target immediately and no frame ticks are scheduled.
func Reduced() bool { return reduced.Load() || degraded.Load() }

// UserReduced reports whether the user asked for reduced motion, ignoring the
// frame budget. One-off effects the user triggers (the yolo blast) play
// unless the user turned motion off.
func UserReduced() bool { return reduced.Load() }

// SetReduced switches reduced motion on or off for the whole process.
// Switching it off also clears a budget degrade.
func SetReduced(v bool) {
	reduced.Store(v)
	if !v {
		degraded.Store(false)
	}
}

func setDegraded() { degraded.Store(true) }
