package motion

import (
	"strings"
	"time"

	"github.com/rivo/uniseg"
)

// Typewriter reveals a string over a duration, one user-perceived character
// (grapheme cluster) at a time, so multi-rune emoji are never split.
type Typewriter struct {
	clusters []string
	d        time.Duration
	start    time.Time
	started  bool
}

// NewTypewriter returns a Typewriter revealing s over d.
func NewTypewriter(s string, d time.Duration) Typewriter {
	var cl []string
	g := uniseg.NewGraphemes(s)
	for g.Next() {
		cl = append(cl, g.Str())
	}
	return Typewriter{clusters: cl, d: d}
}

// Start begins the reveal at now.
func (t *Typewriter) Start(now time.Time) {
	t.start = now
	t.started = true
}

// Started reports whether Start has been called.
func (t *Typewriter) Started() bool { return t.started }

// Visible returns the revealed prefix at now. Before Start it is empty, except
// in reduced motion where the full text is always shown.
func (t *Typewriter) Visible(now time.Time) string {
	return strings.Join(t.clusters[:t.count(now)], "")
}

func (t *Typewriter) count(now time.Time) int {
	n := len(t.clusters)
	if Reduced() {
		return n
	}
	if !t.started {
		return 0
	}
	if t.d <= 0 || now.Sub(t.start) >= t.d {
		return n
	}
	if now.Before(t.start) {
		return 0
	}
	return int(float64(n) * float64(now.Sub(t.start)) / float64(t.d))
}

// Done reports whether the whole text is visible.
func (t *Typewriter) Done(now time.Time) bool { return t.count(now) >= len(t.clusters) }

// Moving reports whether the reveal has started and is not finished.
func (t *Typewriter) Moving(now time.Time) bool {
	return t.started && !Reduced() && !t.Done(now)
}
