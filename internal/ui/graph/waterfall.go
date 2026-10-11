package graph

import "math"

// Span is one step of a turn's trace.
type Span struct {
	Kind    string // "think", "text", "tool" or "wait"
	Name    string
	StartMs int64 // from the start of the turn
	DurMs   int64
	Tokens  int
}

// Waterfall draws spans as rows on a shared time axis: the name, a bar
// from the span's start to its end in its kind's colour (a dashed line for
// waits), then its duration and tokens. The last row labels the axis.
func Waterfall(t Theme, width int, spans []Span) string {
	if width <= 0 || len(spans) == 0 {
		return ""
	}
	var total int64
	nameW, rightW := 0, 0
	right := make([]string, len(spans))
	for i, s := range spans {
		total = max(total, s.StartMs+max(s.DurMs, 0))
		nameW = max(nameW, len([]rune(s.Name)))
		right[i] = Millis(s.DurMs)
		if s.Tokens > 0 {
			right[i] += " · " + Compact(float64(s.Tokens))
		}
		rightW = max(rightW, len([]rune(right[i])))
	}
	total = max(total, 1)
	nameW = min(nameW, max(6, width/4))
	trackW := width - nameW - rightW - 2
	if trackW < 4 {
		nameW = max(0, nameW+trackW-4)
		trackW = 4
	}
	x0 := nameW + 1

	g := newGrid(width, len(spans)+1)
	scale := float64(trackW) / float64(total)
	for y, s := range spans {
		g.text(0, y, truncate(s.Name, nameW), t.Fg, false)
		c := t.spanColor(s.Kind)
		// Lay the span out in half cells, at least one half long.
		a := int(math.Floor(float64(s.StartMs) * scale * 2))
		b := int(math.Ceil(float64(s.StartMs+max(s.DurMs, 0)) * scale * 2))
		a = max(0, min(a, 2*trackW-1))
		b = max(a+1, min(b, 2*trackW))
		for cx := a / 2; cx <= (b-1)/2; cx++ {
			lh, rh := 2*cx >= a, 2*cx+1 < b
			g.set(x0+cx, y, spanRune(s.Kind == "wait", lh, rh), c)
		}
		g.text(width-len([]rune(right[y])), y, right[y], t.Subtle, false)
	}

	// The axis: zero, the middle and the end of the turn.
	y := len(spans)
	end, mid := Millis(total), Millis(total/2)
	g.text(x0, y, "0", t.Subtle, false)
	if trackW >= len(mid)+len(end)+6 {
		g.text(x0+trackW/2-len(mid)/2, y, mid, t.Subtle, false)
	}
	if trackW >= len(end)+2 {
		g.text(x0+trackW-len(end), y, end, t.Subtle, false)
	}
	return g.render()
}

// spanRune is the glyph of a cell a span covers on its left half, right
// half or both: a heavy rule, or a light dashed one for a wait.
func spanRune(wait, left, right bool) rune {
	switch {
	case left && right && wait:
		return '┄'
	case left && right:
		return '━'
	case left && wait:
		return '╴'
	case left:
		return '╸'
	case wait:
		return '╶'
	}
	return '╺'
}
