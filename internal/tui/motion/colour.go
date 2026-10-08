package motion

import (
	"math"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/lucasb-eyer/go-colorful"
)

// PulsePeriod is the period of the "working" breathing dot.
const PulsePeriod = 1600 * time.Millisecond

// Blend mixes a and b by t (0 gives a, 1 gives b), picking each colour's dark
// or light value, in HCL space (Lab for greys where hue is undefined).
func Blend(a, b lipgloss.AdaptiveColor, t float64, dark bool) lipgloss.Color {
	pick := func(c lipgloss.AdaptiveColor) string {
		if dark {
			return c.Dark
		}
		return c.Light
	}
	as, bs := pick(a), pick(b)
	if t <= 0 || math.IsNaN(t) {
		return lipgloss.Color(as)
	}
	if t >= 1 {
		return lipgloss.Color(bs)
	}
	ca, errA := colorful.Hex(as)
	cb, errB := colorful.Hex(bs)
	if errA != nil || errB != nil {
		if t < 0.5 {
			return lipgloss.Color(as)
		}
		return lipgloss.Color(bs)
	}
	m := ca.BlendHcl(cb, t)
	if math.IsNaN(m.R) || math.IsNaN(m.G) || math.IsNaN(m.B) {
		m = ca.BlendLab(cb, t).Clamped()
	}
	return lipgloss.Color(m.Hex())
}

// Ramp returns a style whose foreground is a blended with b at t.
func Ramp(a, b lipgloss.AdaptiveColor, t float64, dark bool) lipgloss.Style {
	return lipgloss.NewStyle().Foreground(Blend(a, b, t, dark))
}

// Pulse returns a sine breathing value in 0..1 with the given period, derived
// from now (no state, no ticker). It starts at 0 and peaks at half period. In
// reduced motion, or with a non-positive period, it holds at 1.
func Pulse(now time.Time, period time.Duration) float64 {
	if Reduced() || period <= 0 {
		return 1
	}
	phase := float64(now.UnixNano()%int64(period)) / float64(period)
	return clamp01(0.5 - 0.5*math.Cos(2*math.Pi*phase))
}
