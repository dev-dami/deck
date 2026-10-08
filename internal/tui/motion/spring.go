package motion

import (
	"math"

	"github.com/charmbracelet/harmonica"
)

// settleEps is the position and velocity threshold below which a spring is at rest.
const settleEps = 0.01

// Preset is a spring tuning (angular frequency and damping ratio).
type Preset struct {
	Freq    float64
	Damping float64
}

// Spring presets. All are critically damped or very nearly, so cell positions
// never visibly overshoot.
var (
	// Snappy drives expand/collapse (~180ms).
	Snappy = Preset{Freq: 40, Damping: 1.0}
	// Glide drives the sidebar selection bar and scrolling.
	Glide = Preset{Freq: 16, Damping: 0.95}
	// Soft drives the @ and / menus (~100ms).
	Soft = Preset{Freq: 60, Damping: 1.0}
)

// New builds a Spring from the preset at the given frame rate.
func (p Preset) New(fps int) Spring { return NewSpring(fps, p.Freq, p.Damping) }

// Spring animates a float towards a target. Pos is the value to render.
type Spring struct {
	Pos, Vel, Target float64

	s     harmonica.Spring
	ready bool
}

// NewSpring returns a spring stepping at fps with the given angular frequency
// and damping ratio.
func NewSpring(fps int, freq, damping float64) Spring {
	if fps < 1 {
		fps = 60
	}
	return Spring{s: harmonica.NewSpring(harmonica.FPS(fps), freq, damping), ready: true}
}

// Set changes the target; call Update each frame afterwards. In reduced
// motion the spring snaps immediately.
func (s *Spring) Set(target float64) {
	s.Target = target
	if Reduced() {
		s.Snap()
	}
}

// Snap jumps to the target with no velocity.
func (s *Spring) Snap() {
	s.Pos = s.Target
	s.Vel = 0
}

// Update advances one frame and reports whether the spring is still moving.
func (s *Spring) Update() (moving bool) {
	if Reduced() || !s.ready {
		s.Snap()
		return false
	}
	if s.atRest() {
		s.Snap()
		return false
	}
	s.Pos, s.Vel = s.s.Update(s.Pos, s.Vel, s.Target)
	if s.atRest() {
		s.Snap()
		return false
	}
	return true
}

func (s *Spring) atRest() bool {
	return math.Abs(s.Pos-s.Target) < settleEps && math.Abs(s.Vel) < settleEps
}

// Moving reports whether the spring has not yet settled.
func (s *Spring) Moving() bool { return !s.atRest() }
