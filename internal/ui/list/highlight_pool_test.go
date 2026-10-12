package list

import (
	"image"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/stretchr/testify/require"
)

// TestHighlightReusedBufferMatchesFresh asserts Highlight, which recycles
// its scratch buffer, renders exactly what a fresh buffer does, whatever
// size and content the recycled buffer last held.
func TestHighlightReusedBufferMatchesFresh(t *testing.T) {
	t.Parallel()

	styled := lipgloss.NewStyle().Foreground(lipgloss.Color("#ff0000")).Render("red words") + " plain\n\tsecond line here\nthird"
	cases := []struct {
		content string
		area    image.Rectangle
		sl, sc  int
		el, ec  int
	}{
		{styled, image.Rect(0, 0, 40, 3), 0, 2, 1, 6},
		{"short", image.Rect(0, 0, 10, 1), 0, 0, -1, -1},
		{styled, image.Rect(0, 0, 25, 5), 1, 0, 2, -1},
		{"a much longer single line of text that is wide", image.Rect(0, 0, 60, 2), 0, 3, 0, 20},
	}
	for range 3 {
		for _, c := range cases {
			want := HighlightBuffer(c.content, c.area, c.sl, c.sc, c.el, c.ec, nil).Render()
			got := Highlight(c.content, c.area, c.sl, c.sc, c.el, c.ec, nil)
			require.Equal(t, want, got)
		}
	}
}
