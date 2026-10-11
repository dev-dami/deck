// Package yolo is the effect that plays when a thread switches to yolo mode: a
// shockwave leaves the middle of the screen and the screen itself reacts. Every
// component the wave crosses is pushed outwards, rings on a fading ripple and
// settles back where it was, while a band of colour rides the wave and fades
// back to each component's own colours. A big YOLO pops where it started.
//
// Each cell is on its own spring: when the front reaches it, it is kicked
// outwards along the radius, swings back past its place and settles.
//
// It is pure: the host keeps rendering its own screen and passes every frame
// through Apply while the effect runs, driving time from its motion clock.
//
//	0ms     the wave leaves the centre with a white-hot front
//	80ms    YOLO pops in the middle
//	900ms   the front reaches the corners
//	1150ms  YOLO goes
//	1450ms  everything is back in place: Apply returns the screen untouched
package yolo

import (
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/lucasb-eyer/go-colorful"
	"github.com/rivo/uniseg"

	"github.com/dev-dami/deck/internal/ui/motion"
)

// reducedMotion is the user's request for less motion (motion off in the
// settings, or DECK_REDUCED_MOTION=1): the effect then does not play.
func reducedMotion() bool { return motion.Off() }

// Timeline.
const (
	Duration = 1450 * time.Millisecond

	travel   = 900 * time.Millisecond  // front: centre to corners
	settleAt = 1150 * time.Millisecond // whatever still moves calms to nothing by Duration
	wordOn   = 80 * time.Millisecond
	wordOff  = 1150 * time.Millisecond
	popFor   = 140 * time.Millisecond
)

// Each cell's spring, timed from the moment the front reaches it: a kick of
// push cells outwards, swinging with period swing and dying away over damp.
// Its colour flares and fades over glow; the crest lasts crest.
const (
	push  = 4.0
	swing = 0.34 // seconds
	damp  = 0.20
	glow  = 0.17
	crest = 0.04
)

// hueStep quantises hues so neighbouring cells share colours and the output
// stays small; hueSpread is degrees per cell of radius, hueSpeed how fast the
// colours run outwards (degrees per second).
const (
	hueStep   = 12.0
	hueSpread = 9.0
	hueSpeed  = 520.0
)

// Tiers of the colour band, from the front backwards.
const (
	tierFront  = iota // white-hot crest
	tierStrong        // saturated background
	tierMedium        // deep background
	tierGlow          // coloured text only
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

	// This frame.
	cx, cy, reach, rOut, t, env float64
}

// New returns a finished effect; call Start to play it.
func New(dark bool) *Anim { return &Anim{dark: dark} }

// SetDark switches the palette for dark or light terminals.
func (a *Anim) SetDark(dark bool) {
	if a != nil {
		a.dark = dark
	}
}

// Start plays the effect from now. Like every Anim method it is safe on a nil
// *Anim, which behaves as an effect that never plays.
func (a *Anim) Start(now time.Time) {
	if a == nil {
		return
	}
	a.start, a.started, a.skipped = now, true, false
}

// Skip ends the effect immediately.
func (a *Anim) Skip() {
	if a != nil {
		a.skipped = true
	}
}

// Done reports whether the effect is over (or never started, or the user
// turned motion off).
func (a *Anim) Done(now time.Time) bool {
	return a == nil || !a.started || a.skipped || reducedMotion() || now.Sub(a.start) >= Duration
}

// Advance reports whether the effect still needs frames.
func (a *Anim) Advance(now time.Time) bool { return !a.Done(now) }

// Apply returns screen as it looks at now: the same number of lines, every
// line exactly w cells. When the effect is done, screen comes back unchanged.
func (a *Anim) Apply(screen string, w int, now time.Time) string {
	if a.Done(now) || w <= 0 {
		return screen
	}
	t := now.Sub(a.start)
	lines := strings.Split(screen, "\n")
	h := len(lines)
	grid := make([][]cell, h)
	for y, l := range lines {
		grid[y] = parseLine(l, w)
	}

	a.cx, a.cy = float64(w-1)/2, float64(h-1)/2
	a.reach = math.Hypot(a.cx+1, 2*(a.cy+1))
	a.t = t.Seconds()
	a.rOut = easeOut(float64(t)/float64(travel)) * a.reach
	a.env = 1
	if t > settleAt {
		a.env = 1 - smooth(float64(t-settleAt)/float64(Duration-settleAt))
	}
	word := a.word(w, h, t)
	pal := a.palette()
	n := len(pal) / tiers
	shift := hueSpeed * t.Seconds()

	var b strings.Builder
	for y := 0; y < h; y++ {
		if y > 0 {
			b.WriteByte('\n')
		}
		cur := ""
		for x := 0; x < w; x++ {
			sx, sy, behind := a.source(x, y, w, h)
			text, style := " ", ""
			switch {
			case word != nil && word.at(sx, sy):
				text, style = "█", word.esc
			case sx < 0:
				// pulled in from off the screen: empty
			default:
				c := grid[sy][sx]
				style = c.style
				switch {
				case c.tail:
				case c.wide:
					// Draw a wide grapheme only when its tail lands in the next cell.
					if x+1 < w {
						tx, ty, _ := a.source(x+1, y, w, h)
						if tx == sx+1 && ty == sy && (word == nil || !word.at(tx, ty)) {
							text = c.text
						}
					}
				default:
					text = c.text
				}
				if tier := a.tier(behind); tier >= 0 {
					hue := math.Mod(a.radius(x, y)*hueSpread-shift, 360)
					if hue < 0 {
						hue += 360
					}
					style = pal[tier*n+int(hue/hueStep)%n]
				}
			}
			if style != cur {
				b.WriteString(reset)
				b.WriteString(style)
				cur = style
			}
			b.WriteString(text)
			if uniseg.StringWidth(text) == 2 {
				x++ // the tail cell is drawn
			}
		}
		if cur != "" {
			b.WriteString(reset)
		}
	}
	return b.String()
}

// radius is the distance of cell (x, y) from the centre, counting a row as two
// columns so the wave comes out round on a terminal.
func (a *Anim) radius(x, y int) float64 {
	return math.Hypot(float64(x)-a.cx, 2*(float64(y)-a.cy))
}

// source is the screen cell shown at (x, y) this frame: once the front has
// reached it, the screen there is on a spring pushed outwards along the
// radius. since is how long ago the front reached (x, y) in seconds, negative
// before; sx is -1 when the cell is pulled in from off the screen.
func (a *Anim) source(x, y, w, h int) (sx, sy int, since float64) {
	dx, dy := float64(x)-a.cx, 2*(float64(y)-a.cy)
	r := math.Hypot(dx, dy)
	if r >= a.rOut {
		return x, y, -1
	}
	// When the front passed r: invert easeOut.
	since = a.t - (1-math.Cbrt(1-math.Min(1, r/a.reach)))*travel.Seconds()
	if r < 0.5 || a.env <= 0 {
		return x, y, since
	}
	d := push * a.env * math.Sin(since*2*math.Pi/swing) * math.Exp(-since/damp)
	// Never pull from across the centre: the middle compresses instead.
	d = math.Min(d, r)
	sx = int(math.Round(float64(x) - dx/r*d))
	sy = int(math.Round(float64(y) - dy/r*d/2))
	if sx < 0 || sx >= w || sy < 0 || sy >= h {
		return -1, -1, since
	}
	return sx, sy, since
}

// tier is the colour tier for a cell the front reached since seconds ago, or
// -1 for the screen's own colours.
func (a *Anim) tier(since float64) int {
	if since < 0 {
		return -1
	}
	if since < crest && a.env > 0.5 {
		return tierFront
	}
	switch i := a.env * math.Exp(-since/glow); {
	case i > 0.62:
		return tierStrong
	case i > 0.32:
		return tierMedium
	case i > 0.12:
		return tierGlow
	}
	return -1
}

// cell is one screen cell: its text, and the SGR escapes in force there.
type cell struct {
	style, text string
	wide, tail  bool
}

// parseLine breaks an ANSI line into exactly w cells. Non-SGR escapes are
// dropped; a wide grapheme cut by w becomes a space.
func parseLine(s string, w int) []cell {
	cells := make([]cell, 0, w)
	style := ""
	for i := 0; i < len(s) && len(cells) < w; {
		if s[i] == 0x1b {
			n, sgr, params := escape(s[i:])
			if sgr {
				if params == "" || params == "0" {
					style = ""
				} else if strings.HasPrefix(params, "0;") {
					style = s[i : i+n]
				} else {
					style += s[i : i+n]
				}
			}
			i += n
			continue
		}
		g, rest, gw, _ := uniseg.FirstGraphemeClusterInString(s[i:], -1)
		i = len(s) - len(rest)
		switch {
		case gw <= 0:
		case gw == 1:
			cells = append(cells, cell{style: style, text: g})
		case len(cells)+gw <= w:
			cells = append(cells, cell{style: style, text: g, wide: true})
			for k := 1; k < gw; k++ {
				cells = append(cells, cell{style: style, tail: true})
			}
		default:
			cells = append(cells, cell{style: style, text: " "})
		}
	}
	for len(cells) < w {
		cells = append(cells, cell{text: " "})
	}
	return cells
}

// escape measures the escape sequence at the start of s: its length, whether
// it is SGR, and the SGR parameters.
func escape(s string) (n int, sgr bool, params string) {
	if len(s) < 2 {
		return len(s), false, ""
	}
	switch s[1] {
	case '[':
		j := 2
		for j < len(s) && (s[j] < 0x40 || s[j] > 0x7e) {
			j++
		}
		if j == len(s) {
			return j, false, ""
		}
		return j + 1, s[j] == 'm', s[2:j]
	case ']': // OSC, to BEL or ST
		for j := 2; j < len(s); j++ {
			if s[j] == 0x07 {
				return j + 1, false, ""
			}
			if s[j] == 0x1b && j+1 < len(s) && s[j+1] == '\\' {
				return j + 2, false, ""
			}
		}
		return len(s), false, ""
	}
	return 2, false, ""
}

const reset = "\x1b[0m"

// palette is the colour band, one escape per tier and hue step, built once per
// theme.
func (a *Anim) palette() []string {
	if a.pal == nil || a.palDark != a.dark {
		n := int(360 / hueStep)
		a.pal = make([]string, tiers*n)
		for tier := 0; tier < tiers; tier++ {
			for i := 0; i < n; i++ {
				a.pal[tier*n+i] = a.cellEsc(float64(i)*hueStep, tier)
			}
		}
		a.palDark = a.dark
	}
	return a.pal
}

// cellEsc is one coloured cell of a tier, text kept readable on top.
func (a *Anim) cellEsc(hue float64, tier int) string {
	hsl := func(s, l float64) string { return colorful.Hsl(hue, s, l).Clamped().Hex() }
	if a.dark {
		switch tier {
		case tierFront:
			return sgr(hsl(0.35, 0.18), hsl(1, 0.86), true)
		case tierStrong:
			return sgr(hsl(0.35, 0.93), hsl(0.85, 0.42), false)
		case tierMedium:
			return sgr(hsl(0.5, 0.86), hsl(0.6, 0.24), false)
		default:
			return sgr(hsl(0.9, 0.68), "", false)
		}
	}
	switch tier {
	case tierFront:
		return sgr(hsl(0.35, 0.12), hsl(1, 0.78), true)
	case tierStrong:
		return sgr(hsl(0.35, 0.14), hsl(0.85, 0.62), false)
	case tierMedium:
		return sgr(hsl(0.5, 0.2), hsl(0.6, 0.82), false)
	default:
		return sgr(hsl(0.9, 0.38), "", false)
	}
}

// sgr is the escape setting fg and bg (hex, "" for none) in truecolor; the
// terminal renderer brings it down to what the terminal can show.
func sgr(fg, bg string, bold bool) string {
	seq := "0" // reset first so nothing from the screen underneath leaks in
	if bold {
		seq += ";1"
	}
	rgb := func(hex string) string {
		c, err := colorful.Hex(hex)
		if err != nil {
			return ""
		}
		r, g, b := c.RGB255()
		return strconv.Itoa(int(r)) + ";" + strconv.Itoa(int(g)) + ";" + strconv.Itoa(int(b))
	}
	if fg != "" {
		if v := rgb(fg); v != "" {
			seq += ";38;2;" + v
		}
	}
	if bg != "" {
		if v := rgb(bg); v != "" {
			seq += ";48;2;" + v
		}
	}
	return "\x1b[" + seq + "m"
}

func easeOut(p float64) float64 { p = clamp01(p); return 1 - (1-p)*(1-p)*(1-p) }
func smooth(p float64) float64  { p = clamp01(p); return p * p * (3 - 2*p) }

func clamp01(p float64) float64 { return math.Max(0, math.Min(1, p)) }
