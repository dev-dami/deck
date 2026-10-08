// Package yolo is the effect that plays when a thread switches to yolo mode:
// a blast wave of colour bursts from the middle of the screen, a big YOLO
// flashes where it started, and a second ring follows the first outwards,
// handing the normal screen back from the centre to the edges.
//
// It is pure: the host keeps rendering its own screen and passes every frame
// through Apply while the effect runs, driving time from its motion clock.
//
//	0ms     the blast leaves the centre: a white-hot front, concentric rainbow behind it
//	60ms    YOLO pops in the middle
//	700ms   the front has passed the corners; the whole screen ripples
//	850ms   the trailing ring leaves the centre, uncovering the screen (and eating YOLO)
//	1500ms  done: Apply returns the screen untouched
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
	Duration = 1500 * time.Millisecond

	blastFor = 700 * time.Millisecond // front: centre to corners
	tailAt   = 850 * time.Millisecond // trailing ring leaves the centre
	wordOn   = 60 * time.Millisecond
	wordOff  = Duration
	popFor   = 140 * time.Millisecond
)

// The rings, in cells of radius: the bright shock front, and the dim edge
// of the trailing ring.
const (
	frontW = 3.0
	tailW  = 5.0
)

// hueStep quantises hues so neighbouring cells share colours and the output
// stays small; hueSpread is degrees per cell of radius, hueSpeed how fast the
// bands ripple outwards (degrees per second).
const (
	hueStep   = 12.0
	hueSpread = 9.0
	hueSpeed  = 520.0
)

// Tiers of the palette: the shock front, the body, the trailing edge.
const (
	tierFront = iota
	tierBody
	tierTail
	tiers
)

// Anim is the effect's state. The zero value is finished.
type Anim struct {
	dark    bool
	start   time.Time
	started bool
	skipped bool

	pal     []string // tiers × hue steps
	palDark bool
	palProf termenv.Profile

	// This frame's geometry.
	cx, cy, rIn, rOut float64
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

// radius is the distance of cell (x, y) from the centre, counting a row as
// two columns so the rings come out round on a terminal.
func (a *Anim) radius(x, y int) float64 {
	return math.Hypot(float64(x)-a.cx, 2*(float64(y)-a.cy))
}

// Apply returns screen as it looks at now: every line exactly w cells. When
// the effect is done, screen comes back unchanged.
func (a *Anim) Apply(screen string, w int, now time.Time) string {
	if a.Done(now) || w <= 0 {
		return screen
	}
	t := now.Sub(a.start)
	lines := strings.Split(screen, "\n")
	h := len(lines)

	a.cx, a.cy = float64(w-1)/2, float64(h-1)/2
	reach := math.Hypot(a.cx+1, 2*(a.cy+1)) + tailW // past the farthest corner
	a.rOut = easeOut(float64(t)/float64(blastFor)) * reach
	a.rIn = -1
	if t >= tailAt {
		a.rIn = easeIn(float64(t-tailAt)/float64(Duration-tailAt)) * reach
	}

	word := a.word(w, h, t)
	in := func(x, y int) bool {
		r := a.radius(x, y)
		return r >= a.rIn && (r < a.rOut || word != nil && word.at(x, y))
	}
	shift := hueSpeed * t.Seconds()
	for y, line := range lines {
		var b strings.Builder
		var cells []string
		for x := 0; x < w; {
			on := in(x, y)
			e := x + 1
			for e < w && in(e, y) == on {
				e++
			}
			if on {
				if cells == nil {
					cells = splitCells(ansi.Strip(line), w)
				}
				a.paint(&b, cells, y, x, e, shift, word)
			} else {
				b.WriteString(pad(ansi.Cut(line, x, e), e-x))
			}
			x = e
		}
		lines[y] = b.String()
	}
	return strings.Join(lines, "\n")
}

// paint writes cells [lo, hi) of a row in colour, grouping runs of one colour.
// Escapes are written directly (precomputed per colour) rather than through
// lipgloss: this runs for every cell of every frame.
func (a *Anim) paint(b *strings.Builder, cells []string, y, lo, hi int, shift float64, word *wordLayer) {
	runKey, open := math.MinInt, false
	pal := a.palette()
	n := len(pal) / tiers
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
			r := a.radius(x, y)
			tier := tierBody
			switch {
			case a.rOut-r < frontW:
				tier = tierFront
			case a.rIn >= 0 && r-a.rIn < tailW:
				tier = tierTail
			}
			hue := math.Mod(r*hueSpread-shift, 360)
			if hue < 0 {
				hue += 360
			}
			key = tier*n + int(hue/hueStep)%n
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

// palette is the rainbow, one escape per tier and hue step, built once per
// theme and colour profile.
func (a *Anim) palette() []string {
	prof := lipgloss.ColorProfile()
	if a.pal == nil || a.palDark != a.dark || a.palProf != prof {
		n := int(360 / hueStep)
		a.pal = make([]string, tiers*n)
		for tier := 0; tier < tiers; tier++ {
			for i := 0; i < n; i++ {
				a.pal[tier*n+i] = a.cellEsc(float64(i)*hueStep, tier)
			}
		}
		a.palDark, a.palProf = a.dark, prof
	}
	return a.pal
}

// cellEsc is one coloured cell: a saturated background with the screen's text
// kept readable on top of it. The front is near white, the tail fades out.
func (a *Anim) cellEsc(hue float64, tier int) string {
	bgS, bgL, fgL := 0.85, 0.62, 0.16
	if a.dark {
		bgL, fgL = 0.42, 0.93
	}
	switch tier {
	case tierFront:
		bgS, bgL, fgL = 1.0, 0.86, 0.20
	case tierTail:
		bgS = 0.45
		if a.dark {
			bgL = 0.24
		} else {
			bgL = 0.80
		}
	}
	bg := colorful.Hsl(hue, bgS, bgL).Clamped().Hex()
	fg := colorful.Hsl(hue, 0.35, fgL).Clamped().Hex()
	return sgr(fg, bg, tier == tierFront)
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
	if n > w { // cutting a wide grapheme can leave it a cell short: fill below
		s = ansi.Truncate(s, w, "")
		n = ansi.StringWidth(s)
	}
	return s + strings.Repeat(" ", w-n)
}

func easeOut(p float64) float64 { p = clamp01(p); return 1 - (1-p)*(1-p)*(1-p) }
func easeIn(p float64) float64  { p = clamp01(p); return p * p * p }

func clamp01(p float64) float64 { return math.Max(0, math.Min(1, p)) }
