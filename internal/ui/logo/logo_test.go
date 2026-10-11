package logo

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/dev-dami/deck/internal/ui/styles"
)

func TestRenderHasTaglineAndVersion(t *testing.T) {
	t.Parallel()
	for _, compact := range []bool{false, true} {
		out := ansi.Strip(Render(lipgloss.NewStyle(), "v1.2.3", compact, Opts{Dark: true}))
		if !strings.Contains(out, Tagline) {
			t.Errorf("compact=%v: no tagline in %q", compact, out)
		}
		if !strings.Contains(out, "v1.2.3") {
			t.Errorf("compact=%v: no version in %q", compact, out)
		}
	}
	if Tagline != "one deck for all your agents" {
		t.Errorf("Tagline = %q", Tagline)
	}
}

func TestRenderFitsWidth(t *testing.T) {
	t.Parallel()
	const width = 30
	out := Render(lipgloss.NewStyle(), "v1.2.3", false, Opts{Width: width, Dark: true})
	for i, l := range strings.Split(out, "\n") {
		if got := ansi.StringWidth(l); got > width {
			t.Errorf("line %d is %d cells, more than %d", i, got, width)
		}
	}
}

func TestSmallRender(t *testing.T) {
	t.Parallel()
	sty := styles.DeckDark()
	out := ansi.Strip(SmallRender(&sty, 30, Opts{}))
	if !strings.Contains(out, "deck") || !strings.HasPrefix(out, "◆ deck") {
		t.Fatalf("SmallRender = %q", out)
	}
	if got := ansi.StringWidth(out); got != 30 {
		t.Errorf("SmallRender width = %d, want 30", got)
	}
	if got := ansi.Strip(SmallRender(&sty, 4, Opts{})); got != "◆ deck" {
		t.Errorf("narrow SmallRender = %q", got)
	}
}

func TestRenderFollowsThemePalette(t *testing.T) {
	t.Parallel()
	sty, err := styles.LoadTheme("tokyo-night")
	if err != nil {
		t.Fatalf("LoadTheme: %v", err)
	}
	deck := Render(lipgloss.NewStyle(), "v1.2.3", true, Opts{Width: 40, Dark: true})
	themed := Render(lipgloss.NewStyle(), "v1.2.3", true, Opts{Width: 40, Dark: true, Mark: PaletteFor(&sty)})
	if ansi.Strip(themed) != ansi.Strip(deck) {
		t.Fatal("the theme palette changes the wordmark's shape")
	}
	if themed == deck {
		t.Fatal("the wordmark ignores the theme palette")
	}
}
