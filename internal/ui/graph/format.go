package graph

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

// Compact is a number in at most four or five cells: 980, 9.8k, 12k, 1.2m.
func Compact(v float64) string {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return "—"
	}
	if v < 0 {
		return "-" + Compact(-v)
	}
	units := []struct {
		div    float64
		suffix string
	}{{1, ""}, {1e3, "k"}, {1e6, "m"}, {1e9, "b"}, {1e12, "t"}}
	for i, u := range units {
		n := v / u.div
		s := trimZero(n)
		// Promote 999.95k to 1m rather than print "1000k".
		if i < len(units)-1 && n >= 999.5 {
			continue
		}
		return s + u.suffix
	}
	return trimZero(v/1e12) + "t"
}

// trimZero is n with one decimal below ten and none above, without ".0".
func trimZero(n float64) string {
	if n < 10 {
		s := strconv.FormatFloat(n, 'f', 1, 64)
		return strings.TrimSuffix(s, ".0")
	}
	return strconv.FormatFloat(n, 'f', 0, 64)
}

// NiceCeil is the smallest 1, 2 or 5 × 10ⁿ at or above v; 1 for v <= 0.
func NiceCeil(v float64) float64 {
	if v <= 0 || math.IsNaN(v) || math.IsInf(v, 0) {
		return 1
	}
	exp := math.Pow(10, math.Floor(math.Log10(v)))
	for _, m := range []float64{1, 2, 5, 10} {
		// The epsilon keeps 3000 from becoming 5000 on rounding error.
		if m*exp >= v*(1-1e-9) {
			return m * exp
		}
	}
	return 10 * exp
}

// Countdown is d as "3d 4h", "2h 13m", "13m" or "<1m".
func Countdown(d time.Duration) string {
	if d < time.Minute {
		return "<1m"
	}
	d = d.Round(time.Minute)
	days, hours, mins := int(d/(24*time.Hour)), int(d/time.Hour)%24, int(d/time.Minute)%60
	switch {
	case days > 0:
		return fmt.Sprintf("%dd %dh", days, hours)
	case hours > 0:
		return fmt.Sprintf("%dh %02dm", hours, mins)
	}
	return fmt.Sprintf("%dm", mins)
}

// Millis is a span length: 820ms, 1.2s, 14s, 2m 05s.
func Millis(ms int64) string {
	switch {
	case ms < 1000:
		return fmt.Sprintf("%dms", max(ms, 0))
	case ms < 10_000:
		return trimZero(float64(ms)/1000) + "s"
	case ms < 60_000:
		return fmt.Sprintf("%ds", (ms+500)/1000)
	}
	s := (ms + 500) / 1000
	return fmt.Sprintf("%dm %02ds", s/60, s%60)
}

// timeFormats picks axis and tooltip time formats for instants spaced
// step apart over span: clock times when hourly, weekdays across a week,
// dates beyond that.
func timeFormats(step, span time.Duration) (axis, tip func(time.Time) string) {
	switch {
	case step < 24*time.Hour:
		return func(t time.Time) string { return t.Format("15:04") },
			func(t time.Time) string { return t.Format("Mon 15:04") }
	case span <= 7*24*time.Hour:
		return func(t time.Time) string { return t.Format("Mon") },
			func(t time.Time) string { return t.Format("Mon Jan 2") }
	}
	return func(t time.Time) string { return t.Format("Jan 2") },
		func(t time.Time) string { return t.Format("Mon Jan 2") }
}
