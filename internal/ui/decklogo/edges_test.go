package decklogo

import (
	"strings"
	"testing"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

func TestGlyph(t *testing.T) {
	t.Parallel()
	cases := []struct {
		e    Edge
		want string
	}{
		{Edge{Kind: "claude", Pct: 0, Known: true}, "▁"},
		{Edge{Kind: "claude", Pct: 50, Known: true}, "▄"},
		{Edge{Kind: "claude", Pct: 100, Known: true}, "█"},
		{Edge{Kind: "claude", Pct: 140, Known: true}, "█"},
		{Edge{Kind: "claude", Pct: 50}, "▏"},
	}
	for _, c := range cases {
		if got := Glyph(c.e); got != c.want {
			t.Errorf("Glyph(%+v) = %q, want %q", c.e, got, c.want)
		}
	}
}

func TestSmallMark(t *testing.T) {
	t.Parallel()
	accent, dim := lipgloss.Color("#00ff00"), lipgloss.Color("#333333")
	edges := []Edge{
		{Kind: "claude", Pct: 20, Known: true},
		{Kind: "codex", Pct: 60, Known: true},
		{Kind: "opencode", Pct: 100, Known: true},
		{Kind: "antigravity"},
	}
	if got := ansi.Strip(SmallMark(edges, accent, dim)); got != "▂▅█▏" {
		t.Errorf("SmallMark = %q, want %q", got, "▂▅█▏")
	}
	if got := ansi.Strip(SmallMark(nil, accent, dim)); got != Stack {
		t.Errorf("SmallMark with no edges = %q, want %q", got, Stack)
	}
}

// edgeLines is the logo's rows cut to the card edges beside the wordmark.
func edgeLines(m *Model) []string {
	lines := strings.Split(ansi.Strip(m.View()), "\n")
	for i, l := range lines {
		lines[i] = ansi.Cut(l, 0, m.ew)
	}
	return lines
}

func TestOneEdgePerAgent(t *testing.T) {
	t.Setenv("DECK_REDUCED_MOTION", "1")
	for n := 1; n <= 4; n++ {
		kinds := []string{"claude", "codex", "opencode", "antigravity"}[:n]
		edges := make([]Edge, n)
		for i, k := range kinds {
			edges[i] = Edge{Kind: k, Pct: 100, Known: true}
		}
		m := New(true)
		m.SetEdges(edges)
		m.SetSize(80, 6)
		lines := edgeLines(m)
		// The first wordmark row is full edges' tops.
		if got := strings.Count(lines[m.py], "█"); got != n {
			t.Errorf("%d agents: %d edges in %q", n, got, lines[m.py])
		}
		marks := lines[m.rows-m.py-1]
		for _, mark := range []string{"✻", ">_", "▯", "◎"}[:n] {
			if !strings.Contains(marks, mark) {
				t.Errorf("%d agents: no %s under the edges in %q", n, mark, marks)
			}
		}
		if w, _ := m.Bounds(); w > 80 {
			t.Errorf("%d agents: logo is %d cells, more than 80", n, w)
		}
	}
}

func TestEdgesFillFromTheBottom(t *testing.T) {
	t.Setenv("DECK_REDUCED_MOTION", "1")
	m := New(true)
	m.SetEdges([]Edge{
		{Kind: "claude", Pct: 0, Known: true},
		{Kind: "codex", Pct: 50, Known: true},
		{Kind: "opencode", Pct: 100, Known: true},
		{Kind: "antigravity"},
	})
	m.SetSize(80, 6)
	lines := edgeLines(m)
	col := func(i int) string {
		var b strings.Builder
		for y := m.py; y < m.rows-m.py-1; y++ {
			b.WriteString(ansi.Cut(lines[y], i*edgeStride, i*edgeStride+1))
		}
		return b.String()
	}
	for i, want := range []string{"│││▁", "││██", "████", "▏▏▏▏"} {
		if got := col(i); got != want {
			t.Errorf("edge %d = %q, want %q", i, got, want)
		}
	}
}

func TestReducedMotionDrawsTheFinalEdges(t *testing.T) {
	t.Setenv("DECK_REDUCED_MOTION", "1")
	edges := []Edge{{Kind: "claude", Pct: 75, Known: true}, {Kind: "codex", Pct: 30, Known: true}}
	still := New(true)
	still.SetEdges(edges)
	still.SetSize(80, 6)
	want := still.View()

	m := New(true)
	m.SetEdges(edges)
	m.SetSize(80, 6)
	now := time.Now()
	m.Start(now)
	if m.Advance(now.Add(16 * time.Millisecond)) {
		t.Fatal("edges move with reduced motion")
	}
	if got := m.View(); got != want {
		t.Fatalf("reduced motion does not draw the final edges:\n%s\nwant:\n%s", ansi.Strip(got), ansi.Strip(want))
	}
}

func TestEdgesRise(t *testing.T) {
	t.Setenv("DECK_REDUCED_MOTION", "")
	m := New(true)
	m.SetEdges([]Edge{{Kind: "claude", Pct: 100, Known: true}})
	m.SetSize(80, 6)
	now := time.Now()
	m.Start(now)
	m.Advance(now)
	if got := edgeLines(m)[m.py]; strings.Contains(got, "█") {
		t.Fatalf("edge is full as the intro starts: %q", got)
	}
	end := now.Add(edgeRise)
	for at := now; !at.After(end); at = at.Add(50 * time.Millisecond) {
		m.Advance(at)
	}
	m.Advance(end)
	if got := edgeLines(m)[m.py]; !strings.Contains(got, "█") {
		t.Fatalf("edge has not risen after the intro: %q", got)
	}
}

func TestSetEdgesRelayouts(t *testing.T) {
	t.Parallel()
	m := New(true)
	m.SetSize(80, 6)
	w0, _ := m.Bounds()
	m.SetEdges([]Edge{{Kind: "claude"}, {Kind: "codex"}, {Kind: "opencode"}, {Kind: "antigravity"}})
	w1, _ := m.Bounds()
	if w1 <= w0 {
		t.Fatalf("four edges are not wider than the stack: %d <= %d", w1, w0)
	}
	if lines := strings.Split(m.View(), "\n"); ansi.StringWidth(lines[0]) != w1 {
		t.Fatalf("view is %d cells, bounds say %d", ansi.StringWidth(lines[0]), w1)
	}
}
