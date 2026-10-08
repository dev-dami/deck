// Package yolo is the effect that plays when a thread switches to yolo mode:
// a rainbow wave sweeps across the whole screen, a big YOLO flashes in the
// middle, and the wave sweeps out again, leaving the normal screen.
//
// It is pure: the host keeps rendering its own screen and passes every frame
// through Apply while the effect runs, driving time from its motion clock.
//
//	0ms     wave enters from the top-left corner
//	450ms   the whole screen is rainbow; YOLO pops in
//	1150ms  YOLO goes; the wave leaves, uncovering the screen from the top-left
//	1600ms  done: Apply returns the screen untouched
package yolo

import (
	"math"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/lucasb-eyer/go-colorful"
	"github.com/muesli/termenv"
	"github.com/rivo/uniseg"

	"github.com/dev-dami/deck/internal/tui/motion"
)

// Timeline.
const (
	Duration = 1600 * time.Millisecond

	sweepIn  = 450 * time.Millisecond
	wordOn   = 380 * time.Millisecond
	wordOff  = 1150 * time.Millisecond
	sweepOut = 1150 * time.Millisecond
	popFor   = 120 * time.Millisecond
)

// hueStep quantises hues so neighbouring cells share colours and the output
// stays small; hueSpeed is how fast the rainbow flows (degrees per second).
const (
	hueStep   = 12.0
	hueSpread = 7.0 // degrees per diagonal cell
	hueSpeed  = 420.0
)

// Anim is the effect's state. The zero value is finished.
type Anim struct {
	dark    bool
	start   time.Time
	started bool
	skipped bool

	pal     []string
	palDark bool
	palProf termenv.Profile
}

// New returns a finished effect; call Start to play it.
func New(dark bool) *Anim { return &Anim{dark: dark} }

// SetDark switches the palette for dark or light terminals.
func (a *Anim) SetDark(dark bool) { a.dark = dark }

// Start plays the effect from now. Under reduced motion it is done at once.
func (a *Anim) Start(now time.Time) {
	a.start, a.started, a.skipped = now, true, false
}

// Skip ends the effect immediately.
func (a *Anim) Skip() { a.skipped = true }

// Done reports whether the effect is over (or never started).
func (a *Anim) Done(now time.Time) bool {
	return !a.started || a.skipped || motion.Reduced() || now.Sub(a.start) >= Duration
}

// Advance reports whether the effect still needs frames.
func (a *Anim) Advance(now time.Time) bool { return !a.Done(now) }

// Apply returns screen as it looks at now: every line exactly w cells. When
// the effect is done, screen comes back unchanged.
func (a *Anim) Apply(screen string, w int, now time.Time) string {
	if a.Done(now) || w <= 0 {
		return screen
	}
	t := now.Sub(a.start)
	lines := strings.Split(screen, "\n")
	h := len(lines)
	span := float64(w + 2*h) // the diagonal coordinate d = x + 2y runs over [0, span)

	// The wave front, as a diagonal coordinate: rainbow where in(d) is true.
	var in func(d int) bool
	switch {
	case t < sweepIn:
		front := easeOut(float64(t)/float64(sweepIn)) * span
		in = func(d int) bool { return float64(d) < front }
	case t < sweepOut:
		in = func(int) bool { return true }
	default:
		front := easeIn(float64(t-sweepOut)/float64(Duration-sweepOut)) * span
		in = func(d int) bool { return float64(d) >= front }
	}

	word := a.word(w, h, t)
	shift := hueSpeed * t.Seconds()
	for y, line := range lines {
		// Find the rainbow span on this row: [lo, hi) in cells.
		lo, hi := -1, -1
		for x := 0; x < w; x++ {
			if in(x + 2*y) {
				if lo < 0 {
					lo = x
				}
				hi = x + 1
			}
		}
		if lo < 0 {
			lines[y] = pad(line, w)
			continue
		}
		cells := splitCells(ansi.Strip(line), w)
		var b strings.Builder
		if lo > 0 {
			b.WriteString(pad(ansi.Truncate(line, lo, ""), lo))
		}
		a.paint(&b, cells, y, lo, hi, shift, word)
		if hi < w {
			b.WriteString(pad(ansi.TruncateLeft(line, hi, ""), w-hi))
		}
		lines[y] = b.String()
	}
	return strings.Join(lines, "\n")
}

// paint writes cells [lo, hi) of a row in rainbow, grouping runs of one colour.
// Escapes are written directly (precomputed per colour) rather than through
// lipgloss: this runs for every cell of every frame.
func (a *Anim) paint(b *strings.Builder, cells []string, y, lo, hi int, shift float64, word *wordLayer) {
	runKey, open := math.MinInt, false
	pal := a.palette()
	owe := false // the last cell was a wide head drawn one cell wide
	for x := lo; x < hi; x++ {
		key, esc, text := 0, "", cells[x]
		letter := word != nil && word.at(x, y)
		// Keep the row exactly hi-lo cells wide. A wide grapheme's tail ("")
		// becomes a space when its head was not drawn whole: cut by the left
		// edge, covered by a letter, or squeezed by the right edge or a letter.
		switch {
		case text == "":
			if owe || x == lo {
				text = " "
			}
			owe = false
		case uniseg.StringWidth(text) > 1:
			owe = letter || x+1 >= hi || word != nil && word.at(x+1, y)
			if owe && !letter {
				text = " "
			}
		default:
			owe = false
		}
		if letter {
			key, esc, text = -1, word.esc, "█"
		} else {
			hue := math.Mod(float64(x+2*y)*hueSpread+shift, 360)
			key = int(hue/hueStep) % len(pal)
			esc = pal[key]
		}
		if key != runKey {
			if esc != "" {
				b.WriteString(esc) // a new SGR replaces the previous colours
				open = true
			}
			runKey = key
		}
		b.WriteString(text)
	}
	if open {
		b.WriteString(reset)
	}
}

const reset = "\x1b[0m"

// palette is the rainbow, one escape per hue step, built once per theme and
// colour profile.
func (a *Anim) palette() []string {
	prof := lipgloss.ColorProfile()
	if a.pal == nil || a.palDark != a.dark || a.palProf != prof {
		n := int(360 / hueStep)
		a.pal = make([]string, n)
		for i := range a.pal {
			a.pal[i] = a.cellEsc(float64(i) * hueStep)
		}
		a.palDark, a.palProf = a.dark, prof
	}
	return a.pal
}

// cellEsc is one rainbow cell: a saturated background with the screen's text
// kept readable on top of it.
func (a *Anim) cellEsc(hue float64) string {
	bgL, fgL := 0.62, 0.16
	if a.dark {
		bgL, fgL = 0.40, 0.92
	}
	bg := colorful.Hsl(hue, 0.80, bgL).Clamped().Hex()
	fg := colorful.Hsl(hue, 0.35, fgL).Clamped().Hex()
	return sgr(fg, bg, false)
}

// sgr is the escape setting fg and bg (hex) in the current colour profile,
// degraded like lipgloss does; "" when the profile has no colour.
func sgr(fg, bg string, bold bool) string {
	prof := lipgloss.ColorProfile()
	if prof == termenv.Ascii {
		return ""
	}
	seq := "0" // reset first so nothing from the screen underneath leaks in
	if bold {
		seq += ";1"
	}
	if s := prof.Color(fg).Sequence(false); s != "" {
		seq += ";" + s
	}
	if s := prof.Color(bg).Sequence(true); s != "" {
		seq += ";" + s
	}
	return "\x1b[" + seq + "m"
}

// splitCells breaks a plain line into one string per cell, w cells long. A
// wide grapheme takes its first cell and leaves "" in the next, so painting a
// run never splits it; a wide grapheme cut by w becomes a space.
func splitCells(s string, w int) []string {
	cells := make([]string, 0, w)
	g := uniseg.NewGraphemes(s)
	for g.Next() && len(cells) < w {
		cl := g.Str()
		cw := uniseg.StringWidth(cl)
		switch {
		case cw <= 0:
			continue
		case cw == 1:
			cells = append(cells, cl)
		case len(cells)+cw <= w:
			cells = append(cells, cl)
			for i := 1; i < cw; i++ {
				cells = append(cells, "")
			}
		default:
			cells = append(cells, " ")
		}
	}
	for len(cells) < w {
		cells = append(cells, " ")
	}
	return cells
}

// pad makes an ANSI line exactly w cells.
func pad(s string, w int) string {
	n := ansi.StringWidth(s)
	if n > w {
		return ansi.Truncate(s, w, "")
	}
	return s + strings.Repeat(" ", w-n)
}

func easeOut(p float64) float64 { p = clamp01(p); return 1 - (1-p)*(1-p)*(1-p) }
func easeIn(p float64) float64  { p = clamp01(p); return p * p * p }

func clamp01(p float64) float64 { return math.Max(0, math.Min(1, p)) }
