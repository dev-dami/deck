// Package graph is Deck's chart kit: a thin, themed layer over ntcharts plus
// a few small pieces drawn with lipgloss (meters, share bars, rings and
// trace waterfalls). Every chart takes a Theme, so they all share the same
// quiet axes, the same accent and each agent's own colour.
//
// The package draws strings and does no IO; callers own the data and pass
// it in. Only fromstyles.go knows about Deck's styles.
package graph

import (
	"image/color"

	"charm.land/lipgloss/v2"
	"github.com/lucasb-eyer/go-colorful"
)

// HeatLevels is how many colours a heatmap uses: none plus four quartiles.
const HeatLevels = 5

// Theme is the palette every chart draws with.
type Theme struct {
	Fg     color.Color // values and totals
	Subtle color.Color // axis labels and legends
	Dim    color.Color // the crosshair, tooltip border and quiet marks
	Grid   color.Color // baselines and gridlines
	Accent color.Color // the default series colour
	Bg     color.Color // the ground charts sit on

	// Series is each series' colour, keyed by agent kind ("claude", ...).
	Series map[string]color.Color
	// Spans is each trace span kind's colour ("think", "text", "tool",
	// "wait"), for Waterfall.
	Spans map[string]color.Color
	// Heat is the heatmap scale, faintest first; Heat[0] is a day with
	// nothing on it.
	Heat []color.Color
}

// SeriesColor is the colour of a named series, the accent when it has none.
func (t Theme) SeriesColor(name string) color.Color {
	if c, ok := t.Series[name]; ok && c != nil {
		return c
	}
	return t.Accent
}

// spanColor is the colour of a trace span kind, Dim when it has none.
func (t Theme) spanColor(kind string) color.Color {
	if c, ok := t.Spans[kind]; ok && c != nil {
		return c
	}
	return t.Dim
}

// heat is the colour of a heatmap level, clamped to the scale.
func (t Theme) heat(level int) color.Color {
	if len(t.Heat) == 0 {
		return t.Accent
	}
	return t.Heat[max(0, min(level, len(t.Heat)-1))]
}

// HeatScale blends n colours from just above from to to, in CIELAB. The
// first step keeps a little of to, so an empty day still reads as a cell
// rather than a hole in the ground.
func HeatScale(from, to color.Color, n int) []color.Color {
	if n <= 0 {
		return nil
	}
	a, _ := colorful.MakeColor(from)
	b, _ := colorful.MakeColor(to)
	const floor = 0.14
	out := make([]color.Color, n)
	for i := range out {
		f := 1.0
		if n > 1 {
			f = floor + (1-floor)*float64(i)/float64(n-1)
		}
		out[i] = lipgloss.Color(a.BlendLab(b, f).Clamped().Hex())
	}
	return out
}
