package graph

import (
	"fmt"
	"image/color"
	"math"
	"strings"
	"time"
)

// paceSlack is how many points used may run ahead of or behind the time
// elapsed in a window and still count as on pace.
const paceSlack = 5

// Meter draws a plan window's remaining quota: a bar filled to the share
// left, a │ where even use would have left it by now, a pace glyph (▲ using
// faster than time passes, ● on pace, ▼ slower), then "62% left · resets in
// 2h 13m". The pace parts are left out when the window's length or reset
// time is unknown (windowMins <= 0 or a zero resetsAt).
func Meter(t Theme, width int, usedPct float64, windowMins int, resetsAt, now time.Time) string {
	if width <= 0 {
		return ""
	}
	used := max(0, min(usedPct, 100))
	left := 100 - used
	paced := windowMins > 0 && !resetsAt.IsZero()

	glyph, glyphFg := "", t.Accent
	evenLeft := -1.0
	if paced {
		window := time.Duration(windowMins) * time.Minute
		elapsed := 100 * float64(window-resetsAt.Sub(now)) / float64(window)
		elapsed = max(0, min(elapsed, 100))
		evenLeft = 100 - elapsed
		switch d := used - elapsed; {
		case d > paceSlack:
			glyph, glyphFg = "▲", t.Fg
		case d < -paceSlack:
			glyph, glyphFg = "▼", t.Subtle
		default:
			glyph = "●"
		}
	}

	status := fmt.Sprintf("%d%% left", int(math.Round(left)))
	reset := ""
	if !resetsAt.IsZero() {
		if d := resetsAt.Sub(now); d > 0 {
			reset = " · resets in " + Countdown(d)
		} else {
			reset = " · resetting"
		}
	}
	text := func() int {
		n := len([]rune(status + reset))
		if glyph != "" {
			n += 2
		}
		return n
	}
	barW := width - text() - 1
	if barW < 12 {
		reset = ""
		barW = width - text() - 1
	}
	if barW < 4 {
		barW = width
		glyph, status = "", ""
	}

	g := newGrid(width, 1)
	meterBar(g, barW, left, t.Accent, t.Grid)
	if evenLeft >= 0 {
		x := max(0, min(int(math.Round(evenLeft/100*float64(barW))), barW-1))
		g.set(x, 0, '│', t.Fg)
	}
	x := barW + 1
	if glyph != "" {
		g.text(x, 0, glyph, glyphFg, false)
		x += 2
	}
	g.text(x, 0, status, t.Fg, false)
	g.text(x+len([]rune(status)), 0, reset, t.Subtle, false)
	return g.render()
}

// meterBar draws a heavy rule w cells long into the first row of g, filled
// to pct in fill at half-cell steps, the rest in empty.
func meterBar(g *grid, w int, pct float64, fill, empty color.Color) {
	halves := int(math.Round(max(0, min(pct, 100)) / 100 * float64(2*w)))
	for x := range w {
		switch {
		case 2*x+2 <= halves:
			g.set(x, 0, '━', fill)
		case 2*x+1 == halves:
			g.set(x, 0, '╸', fill)
		default:
			g.set(x, 0, '━', empty)
		}
	}
}

// Part is one share of a Share bar.
type Part struct {
	Label string
	Value float64
	Color color.Color // nil for the label's series colour
}

// Share draws parts as one stacked bar width cells long, then a legend of
// "■ label · value · pct" items wrapped to width. Every part above zero
// gets at least a cell when there is room.
func Share(t Theme, width int, parts []Part) string {
	if width <= 0 || len(parts) == 0 {
		return ""
	}
	total := 0.0
	for _, p := range parts {
		total += max(p.Value, 0)
	}
	colour := func(p Part) color.Color {
		if p.Color != nil {
			return p.Color
		}
		return t.SeriesColor(p.Label)
	}

	bar := newGrid(width, 1)
	if total <= 0 {
		meterBar(bar, width, 0, nil, t.Grid)
	} else {
		x := 0
		for i, n := range shareCells(parts, total, width) {
			for range n {
				bar.set(x, 0, '━', colour(parts[i]))
				x++
			}
		}
	}

	var lines []string
	line := newGrid(width, 1)
	x := 0
	flush := func() {
		lines = append(lines, strings.TrimRight(line.render(), " "))
		line, x = newGrid(width, 1), 0
	}
	for _, p := range parts {
		pct := "0%"
		if total > 0 {
			switch f := 100 * max(p.Value, 0) / total; {
			case f > 0 && f < 1:
				pct = "<1%"
			default:
				pct = fmt.Sprintf("%d%%", int(math.Round(f)))
			}
		}
		label, rest := p.Label, " · "+Compact(p.Value)+" · "+pct
		w := 2 + len([]rune(label)) + len([]rune(rest))
		if w > width {
			// Too narrow for the item: drop the value, then cut the label.
			rest = " · " + pct
			label = truncate(label, width-2-len([]rune(rest)))
			w = 2 + len([]rune(label)) + len([]rune(rest))
		}
		if x > 0 && x+3+w > width {
			flush()
		}
		if x > 0 {
			x += 3
		}
		line.set(x, 0, '■', colour(p))
		line.text(x+2, 0, label, t.Fg, false)
		line.text(x+2+len([]rune(label)), 0, rest, t.Subtle, false)
		x += w
	}
	flush()
	return bar.render() + "\n" + strings.Join(lines, "\n")
}

// Stack draws parts as one stacked bar width cells long, the parts in their
// colours from the left and what is left of total in the grid colour, with
// no legend: a one-line breakdown of a whole (a context window).
func Stack(t Theme, width int, parts []Part, total float64) string {
	if width <= 0 {
		return ""
	}
	sum := 0.0
	for _, p := range parts {
		sum += max(p.Value, 0)
	}
	all := append(append([]Part(nil), parts...), Part{Value: max(0, total-sum), Color: t.Grid})
	if total = max(total, sum); total <= 0 {
		g := newGrid(width, 1)
		meterBar(g, width, 0, nil, t.Grid)
		return g.render()
	}
	g := newGrid(width, 1)
	x := 0
	for i, n := range shareCells(all, total, width) {
		c := all[i].Color
		if c == nil {
			c = t.SeriesColor(all[i].Label)
		}
		for range n {
			g.set(x, 0, '━', c)
			x++
		}
	}
	return g.render()
}

// shareCells splits width cells between parts by value, largest remainder
// first, giving each part above zero at least one cell when there is room.
func shareCells(parts []Part, total float64, width int) []int {
	cells := make([]int, len(parts))
	rem := make([]float64, len(parts))
	used, nonzero := 0, 0
	for i, p := range parts {
		exact := max(p.Value, 0) / total * float64(width)
		cells[i] = int(exact)
		rem[i] = exact - float64(cells[i])
		used += cells[i]
		if p.Value > 0 {
			nonzero++
		}
	}
	for used < width {
		best := 0
		for i := range rem {
			if rem[i] > rem[best] {
				best = i
			}
		}
		cells[best]++
		rem[best] = -1
		used++
	}
	if nonzero > width {
		return cells
	}
	// Lend a cell to each slivered part from the largest.
	for i, p := range parts {
		if p.Value > 0 && cells[i] == 0 {
			big := 0
			for j := range cells {
				if cells[j] > cells[big] {
					big = j
				}
			}
			cells[big]--
			cells[i]++
		}
	}
	return cells
}

// ringRunes fill a circle by quarters.
var ringRunes = []rune("○◔◑◕●")

// Ring is pct as one cell, a circle filled by quarters (○◔◑◕●), in the
// accent.
func Ring(t Theme, pct float64) string {
	i := int(math.Round(max(0, min(pct, 100)) / 25))
	return styled(string(ringRunes[i]), t.Accent, false)
}
