package graph

import (
	"image/color"
	"math"
	"strings"
)

// barEighths are the partial blocks a bar ends on, by eighths.
var barEighths = []rune(" ▏▎▍▌▋▊▉")

// Bar is one row of Bars.
type Bar struct {
	Label string
	Value float64
	Color color.Color // nil for the label's series colour
	Text  string      // the value as shown; "" for Compact(Value)
}

// text is the bar's value as shown.
func (b Bar) text() string {
	if b.Text != "" {
		return b.Text
	}
	return Compact(b.Value)
}

// Bars draws one horizontal bar per row, scaled to the largest: the label,
// an eighth-block bar in the bar's colour, then the value in a right-hand
// column. Anything above zero shows at least a sliver.
func Bars(t Theme, width int, bars []Bar) string {
	if width <= 0 || len(bars) == 0 {
		return ""
	}
	labelW, valueW, top := 0, 0, 0.0
	for _, b := range bars {
		labelW = max(labelW, len([]rune(b.Label)))
		valueW = max(valueW, len([]rune(b.text())))
		top = max(top, b.Value)
	}
	labelW = min(labelW, max(width/3, 4))
	barW := width - labelW - valueW - 2
	if barW < 1 {
		labelW = max(0, labelW+barW-1)
		barW = 1
	}

	g := newGrid(width, len(bars))
	for y, b := range bars {
		g.text(0, y, truncate(b.Label, labelW), t.Fg, false)
		c := b.Color
		if c == nil {
			c = t.SeriesColor(b.Label)
		}
		eighths := 0
		if top > 0 && b.Value > 0 {
			eighths = max(1, int(math.Round(b.Value/top*float64(barW*8))))
		}
		bar := strings.Repeat("█", eighths/8)
		if r := eighths % 8; r > 0 {
			bar += string(barEighths[r])
		}
		g.text(labelW+1, y, bar, c, false)
		v := b.text()
		g.text(width-len([]rune(v)), y, v, t.Subtle, false)
	}
	return g.render()
}

// truncate cuts s to n runes, ending on "…" when it cuts.
func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	if n <= 0 {
		return ""
	}
	return string(r[:n-1]) + "…"
}
