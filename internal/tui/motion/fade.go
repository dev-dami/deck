package motion

import (
	"math"
	"time"
)

// Easing maps linear progress 0..1 to eased progress 0..1.
type Easing = func(float64) float64

// Linear is the identity easing.
func Linear(t float64) float64 { return t }

// EaseOutCubic starts fast and settles gently.
func EaseOutCubic(t float64) float64 {
	t = clamp01(t)
	u := 1 - t
	return 1 - u*u*u
}

// EaseInOutSine accelerates and decelerates smoothly.
func EaseInOutSine(t float64) float64 {
	t = clamp01(t)
	return -(math.Cos(math.Pi*t) - 1) / 2
}

func clamp01(v float64) float64 {
	switch {
	case v < 0 || math.IsNaN(v):
		return 0
	case v > 1:
		return 1
	}
	return v
}

// Fade is a time-based 0..1 progress with easing. Before Start it reports 0.
// In reduced motion it jumps to 1 as soon as it is started.
type Fade struct {
	d       time.Duration
	ease    Easing
	start   time.Time
	started bool
}

// NewFade returns a Fade lasting d. A nil ease means linear.
func NewFade(d time.Duration, ease func(float64) float64) Fade {
	if ease == nil {
		ease = Linear
	}
	return Fade{d: d, ease: ease}
}

// Start (re)starts the fade at now.
func (f *Fade) Start(now time.Time) {
	f.start = now
	f.started = true
}

// Reset returns the fade to its un-started state (progress 0).
func (f *Fade) Reset() { f.started = false }

// Started reports whether Start has been called.
func (f *Fade) Started() bool { return f.started }

// Progress returns the eased progress at now, in 0..1.
func (f *Fade) Progress(now time.Time) float64 {
	if !f.started {
		return 0
	}
	if Reduced() || f.d <= 0 {
		return 1
	}
	ease := f.ease
	if ease == nil {
		ease = Linear
	}
	return clamp01(ease(clamp01(float64(now.Sub(f.start)) / float64(f.d))))
}

// Done reports whether the fade has been started and has finished.
func (f *Fade) Done(now time.Time) bool {
	if !f.started {
		return false
	}
	return Reduced() || f.d <= 0 || now.Sub(f.start) >= f.d
}

// Moving reports whether the fade is started and not yet done.
func (f *Fade) Moving(now time.Time) bool { return f.started && !f.Done(now) }
