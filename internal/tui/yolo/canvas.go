package yolo

import (
	"math"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/dev-dami/deck/internal/tui/motion"
	"github.com/dev-dami/deck/internal/tui/theme"
)

// kind is a colour role. Colours come from the theme at render time, so the
// scene code never touches a hex value.
type kind uint8

const (
	kNone kind = iota
	kFg
	kMuted
	kFaint
	kWarn
	kDanger
)

func (k kind) token() lipgloss.AdaptiveColor {
	switch k {
	case kFg:
		return theme.Fg
	case kMuted:
		return theme.Muted
	case kWarn:
		return theme.Warn
	case kDanger:
		return theme.Danger
	}
	return theme.Faint
}

// fadeSteps is how finely a cell's fade towards Faint is quantised. Coarse
// steps keep runs of equal style long, so a frame stays a handful of escapes.
const fadeSteps = 8

// cell is one terminal column. f is its own fade towards Faint in
// 0..fadeSteps (dust fading out); the global end-of-run fade is applied on
// top at render time.
type cell struct {
	r    rune
	k    kind
	bold bool
	f    uint8
}

// canvas is a w x h grid of single-width cells. Everything the animation
// draws goes through put, which clips, so sprites can fly off any edge.
type canvas struct {
	w, h  int
	cells []cell
}

func newCanvas(w, h int) *canvas {
	return &canvas{w: w, h: h, cells: make([]cell, w*h)}
}

func quant(f float64) uint8 {
	if math.IsNaN(f) || f <= 0 {
		return 0
	}
	if f >= 1 {
		return fadeSteps
	}
	return uint8(math.Round(f * fadeSteps))
}

// put draws r at (x, y). A space is transparent. f is the cell's own fade.
func (c *canvas) put(x, y int, r rune, k kind, bold bool, f float64) {
	if r == ' ' || x < 0 || y < 0 || x >= c.w || y >= c.h {
		return
	}
	c.cells[y*c.w+x] = cell{r: r, k: k, bold: bold, f: quant(f)}
}

// text draws s left to right from (x, y).
func (c *canvas) text(x, y int, s string, k kind, bold bool) {
	for _, r := range s {
		c.put(x, y, r, k, bold, 0)
		x++
	}
}

// sprite draws a pose with its top-left cell at (ox, oy). Spaces are
// transparent.
func (c *canvas) sprite(s sprite, ox, oy int, k kind) {
	for dy, row := range s {
		x := ox
		for _, r := range row {
			c.put(x, oy+dy, r, k, false, 0)
			x++
		}
	}
}

// render turns the bottom rows rows of the canvas into styled lines, each
// exactly c.w cells wide. gf is the global fade towards Faint (0..1).
func (c *canvas) render(rows int, gf float64, dark bool) string {
	if rows > c.h {
		rows = c.h
	}
	gq := quant(gf)
	type skey struct {
		k    kind
		bold bool
		f    uint8
	}
	styles := map[skey]lipgloss.Style{}
	style := func(k kind, bold bool, f uint8) lipgloss.Style {
		key := skey{k, bold, f}
		if s, ok := styles[key]; ok {
			return s
		}
		col := motion.Blend(k.token(), theme.Faint, float64(f)/fadeSteps, dark)
		s := lipgloss.NewStyle().Foreground(col).Bold(bold)
		styles[key] = s
		return s
	}
	eff := func(cl cell) uint8 {
		if cl.f > gq {
			return cl.f
		}
		return gq
	}

	var sb strings.Builder
	var run strings.Builder
	for y := c.h - rows; y < c.h; y++ {
		if y > c.h-rows {
			sb.WriteByte('\n')
		}
		for x := 0; x < c.w; {
			cl := c.cells[y*c.w+x]
			if cl.k == kNone {
				sb.WriteByte(' ')
				x++
				continue
			}
			f := eff(cl)
			run.Reset()
			run.WriteRune(cl.r)
			j := x + 1
			for j < c.w {
				n := c.cells[y*c.w+j]
				if n.k != cl.k || n.bold != cl.bold || eff(n) != f {
					break
				}
				run.WriteRune(n.r)
				j++
			}
			sb.WriteString(style(cl.k, cl.bold, f).Render(run.String()))
			x = j
		}
	}
	return sb.String()
}
