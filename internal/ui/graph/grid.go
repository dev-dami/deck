package graph

import (
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"
)

// cell is one terminal cell of a grid. A zero rune is an empty cell.
type cell struct {
	r    rune
	fg   color.Color
	bold bool
}

// grid is a fixed-size block of cells that charts draw into and overlays
// (crosshairs, tooltips) draw over. It renders runs of same-styled cells
// as one styled span, rather than one escape sequence per cell.
type grid struct {
	w, h  int
	cells [][]cell
}

func newGrid(w, h int) *grid {
	w, h = max(w, 0), max(h, 0)
	g := &grid{w: w, h: h, cells: make([][]cell, h)}
	for y := range g.cells {
		g.cells[y] = make([]cell, w)
	}
	return g
}

// clone is a deep copy, so an overlay can be drawn without touching g.
func (g *grid) clone() *grid {
	c := &grid{w: g.w, h: g.h, cells: make([][]cell, g.h)}
	for y, row := range g.cells {
		c.cells[y] = append([]cell(nil), row...)
	}
	return c
}

func (g *grid) in(x, y int) bool { return x >= 0 && y >= 0 && x < g.w && y < g.h }

func (g *grid) at(x, y int) cell {
	if !g.in(x, y) {
		return cell{}
	}
	return g.cells[y][x]
}

func (g *grid) set(x, y int, r rune, fg color.Color) {
	if g.in(x, y) {
		g.cells[y][x] = cell{r: r, fg: fg}
	}
}

// text writes s from (x, y), one rune per cell, clipped to the grid.
func (g *grid) text(x, y int, s string, fg color.Color, bold bool) {
	for _, r := range s {
		if g.in(x, y) {
			g.cells[y][x] = cell{r: r, fg: fg, bold: bold}
		}
		x++
	}
}

// render is the grid as lines of styled text, every line g.w cells wide.
// Blanks join the run they fall in, since a space looks the same in any
// foreground; blanks before the first mark and after the last are plain.
func (g *grid) render() string {
	var b, run strings.Builder
	for y, row := range g.cells {
		if y > 0 {
			b.WriteByte('\n')
		}
		last := len(row) - 1
		for last >= 0 && blank(row[last]) {
			last--
		}
		var cur cell
		inked := false // whether run holds anything but blanks
		flush := func() {
			if inked {
				b.WriteString(styled(run.String(), cur.fg, cur.bold))
			} else {
				b.WriteString(run.String())
			}
			run.Reset()
			inked = false
		}
		for _, c := range row[:last+1] {
			if blank(c) {
				run.WriteByte(' ')
				continue
			}
			if !inked || !sameStyle(c, cur) {
				flush()
				cur, inked = c, true
			}
			run.WriteRune(c.r)
		}
		flush()
		b.WriteString(strings.Repeat(" ", len(row)-1-last))
	}
	return b.String()
}

func blank(c cell) bool { return c.r == 0 || c.r == ' ' }

// styled is s in fg (and bold), or s as it is when it has no style.
func styled(s string, fg color.Color, bold bool) string {
	if fg == nil && !bold {
		return s
	}
	st := lipgloss.NewStyle().Bold(bold)
	if fg != nil {
		st = st.Foreground(fg)
	}
	return st.Render(s)
}

func sameStyle(a, b cell) bool { return a.bold == b.bold && sameColor(a.fg, b.fg) }

func sameColor(a, b color.Color) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	ar, ag, ab, aa := a.RGBA()
	br, bg, bb, ba := b.RGBA()
	return ar == br && ag == bg && ab == bb && aa == ba
}
