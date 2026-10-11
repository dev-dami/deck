package decklogo

import (
	"image/color"
	"math"
	"slices"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/dev-dami/deck/internal/ui/tone"
)

// Deck's mark is a deck of cards seen edge-on, one edge per agent. Each edge
// is a meter in its agent's colour that fills up from the bottom as the
// agent's usage is used: its plan's session window, or its context when the
// CLI reports no plan limits. An edge nothing is known about is a thin dim
// rule, never a guessed level.

// Edge is one agent's card edge.
type Edge struct {
	Kind  string  // the agent kind ("claude", "codex", ...): its colour and mark
	Pct   float64 // how much is used, 0-100
	Known bool    // false while nothing is known: a thin dim edge
}

const (
	// edgeRise is how long the edges take to rise to their level in the
	// intro.
	edgeRise = 600 * time.Millisecond
	// edgeStride is the cells from one tall edge to the next: room for the
	// widest mark under it (Codex's >_) and a space.
	edgeStride = 3
	// edgeGap is the space between the edges and the wordmark.
	edgeGap = 1
	// Stack is the mark with no agent to show: three cards, edge-on.
	Stack = "▎▍▌"
	// unknownEdge is an edge nothing is known about.
	unknownEdge = "▏"
	// emptyEdge is the unused part of a tall edge.
	emptyEdge = "│"
)

// eighths are the partial blocks, one eighth of a cell to a full one.
var eighths = [8]string{"▁", "▂", "▃", "▄", "▅", "▆", "▇", "█"}

// Glyph is an edge as one cell: a block from ▁ to █ by how much is used
// (▁ up to 12.5%, █ above 87.5%), or ▏ when nothing is known.
func Glyph(e Edge) string {
	if !e.Known {
		return unknownEdge
	}
	return eighths[filled(e.Pct, 8)-1]
}

// filled is how many of n eighths an edge at pct fills: at least one, so a
// known edge always shows its colour.
func filled(pct float64, n int) int {
	pct = math.Max(0, math.Min(100, pct))
	return max(1, min(n, int(math.Ceil(pct/100*float64(n)))))
}

// SmallMark is the one-row mark: a cell per edge in its agent's colour (dim
// when unknown), or the Stack in accent when there are no edges.
func SmallMark(edges []Edge, accent, dim color.Color) string {
	if len(edges) == 0 {
		return lipgloss.NewStyle().Foreground(accent).Render(Stack)
	}
	var b strings.Builder
	for _, e := range edges {
		c := tone.Tint(e.Kind)
		if !e.Known {
			c = dim
		}
		b.WriteString(lipgloss.NewStyle().Foreground(c).Render(Glyph(e)))
	}
	return b.String()
}

// smallWidth is the width of SmallMark for n edges.
func smallWidth(n int) int {
	if n == 0 {
		return ansi.StringWidth(Stack)
	}
	return n
}

// SetEdges sets the agents' edges, in the order they are drawn. A change in
// their number changes the layout.
func (m *Model) SetEdges(edges []Edge) {
	if slices.Equal(edges, m.edges) {
		return
	}
	n := len(m.edges)
	m.edges = append(m.edges[:0], edges...)
	m.epal = nil
	m.dirty = true
	if len(edges) != n && m.kind != kindNone {
		m.rebuild()
	}
}

// edgesWidth is the cells the tall edges take beside the wordmark, the gap
// included.
func (m *Model) edgesWidth() int {
	if len(m.edges) == 0 {
		return ansi.StringWidth(Stack) + edgeGap
	}
	return edgeStride*len(m.edges) - 1 + edgeGap
}

// rise is how far the edges have risen to their level, 0 to 1, easing out.
func (m *Model) rise() float64 {
	if !m.edgeOn {
		return 1
	}
	x := float64(m.now.Sub(m.edgeStart)) / float64(edgeRise)
	if x >= 1 {
		return 1
	}
	x = math.Max(0, x)
	return 1 - math.Pow(1-x, 3)
}

// edgePalette is the escape sequences of each edge's colour, then the dim
// rule's and the accent's.
func (m *Model) edgePalette() []palEntry {
	if m.epal != nil {
		return m.epal
	}
	p := make([]palEntry, 0, len(m.edges)+2)
	for _, e := range m.edges {
		p = append(p, splitStyle(lipgloss.NewStyle().Foreground(tone.Tint(e.Kind))))
	}
	p = append(p, splitStyle(lipgloss.NewStyle().Foreground(m.colors.Low)))
	p = append(p, splitStyle(lipgloss.NewStyle().Foreground(m.colors.Accent)))
	m.epal = p
	return p
}

// appendEdgeRow appends row cy of the tall edges (edgesWidth cells). The
// edges stand as tall as the wordmark less its last row, where each agent's
// mark sits under its edge.
func (m *Model) appendEdgeRow(b []byte, cy int) []byte {
	top, bottom := m.py, m.rows-m.py-1 // the wordmark's first and last rows
	if cy < top || cy > bottom {
		return append(b, strings.Repeat(" ", m.edgesWidth())...)
	}
	pal := m.edgePalette()
	dim, accent := pal[len(pal)-2], pal[len(pal)-1]
	paint := func(b []byte, e palEntry, s string) []byte {
		b = append(b, e.pre...)
		b = append(b, s...)
		return append(b, e.suf...)
	}
	if len(m.edges) == 0 {
		b = paint(b, accent, Stack)
		return append(b, strings.Repeat(" ", edgeGap)...)
	}
	h := bottom - top // the edges' height, in cells
	rise := m.rise()
	for i, e := range m.edges {
		if i > 0 {
			b = append(b, ' ')
		}
		cell, c := unknownEdge, dim
		switch {
		case cy == bottom:
			cell, c = markOf(e), pal[i]
		case e.Known:
			// The eighths filled, counted from the bottom, and this
			// cell's share of them.
			n := int(math.Round(float64(filled(e.Pct, 8*h)) * rise))
			k := n - 8*(bottom-1-cy)
			switch {
			case k <= 0:
				cell = emptyEdge
			case k >= 8:
				cell, c = eighths[7], pal[i]
			default:
				cell, c = eighths[k-1], pal[i]
			}
		}
		b = paint(b, c, cell)
		b = append(b, strings.Repeat(" ", edgeStride-1-ansi.StringWidth(cell))...)
	}
	return append(b, strings.Repeat(" ", edgeGap)...)
}

// markOf is an agent's mark under its edge, cut to the room there is.
func markOf(e Edge) string {
	return ansi.Truncate(tone.Mark(e.Kind), edgeStride-1, "")
}
