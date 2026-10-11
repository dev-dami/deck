package decklogo

import (
	"strings"
	"testing"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

func TestStaticLinesHaveEqualWidth(t *testing.T) {
	t.Parallel()
	for _, size := range [][2]int{{80, 6}, {40, 4}} {
		w, h := size[0], size[1]
		out := Static(w, h, true)
		if strings.TrimSpace(ansi.Strip(out)) == "" {
			t.Fatalf("Static(%d, %d) is empty", w, h)
		}
		lines := strings.Split(out, "\n")
		width := ansi.StringWidth(lines[0])
		if width == 0 || width > w {
			t.Fatalf("Static(%d, %d) width = %d", w, h, width)
		}
		if len(lines) > h {
			t.Fatalf("Static(%d, %d) has %d lines", w, h, len(lines))
		}
		for i, l := range lines {
			if got := ansi.StringWidth(l); got != width {
				t.Errorf("Static(%d, %d) line %d width = %d, want %d", w, h, i, got, width)
			}
		}
	}
}

func TestStaticLightIsNotEmpty(t *testing.T) {
	t.Parallel()
	if strings.TrimSpace(ansi.Strip(Static(80, 6, false))) == "" {
		t.Fatal("light logo is empty")
	}
}

func TestStaticTinyArea(t *testing.T) {
	t.Parallel()
	got := ansi.Strip(Static(10, 1, true))
	if got != "▎▍▌ deck" {
		t.Fatalf("tiny logo = %q, want %q", got, "▎▍▌ deck")
	}
	if got := Static(3, 1, true); got != "" {
		t.Fatalf("logo with no room = %q, want empty", got)
	}
}

func TestReducedMotionDoesNotMove(t *testing.T) {
	t.Setenv("DECK_REDUCED_MOTION", "1")
	m := New(true)
	m.SetSize(80, 6)
	now := time.Now()
	m.Start(now)
	if m.Advance(now.Add(16 * time.Millisecond)) {
		t.Fatal("logo moves with reduced motion")
	}
}

func TestIntroMoves(t *testing.T) {
	t.Setenv("DECK_REDUCED_MOTION", "")
	m := New(true)
	m.SetSize(80, 6)
	now := time.Now()
	m.Start(now)
	if !m.Advance(now.Add(16 * time.Millisecond)) {
		t.Fatal("intro does not move")
	}
}

func TestStaticPaletteUsesItsColours(t *testing.T) {
	t.Parallel()
	p := Palette{
		Low:    lipgloss.Color("#202020"),
		Accent: lipgloss.Color("#bd93f9"),
		High:   lipgloss.Color("#ff79c6"),
		Name:   lipgloss.Color("#f8f8f2"),
	}
	themed := StaticPalette(80, 6, p)
	if ansi.Strip(themed) != ansi.Strip(Static(80, 6, true)) {
		t.Fatal("a palette changes the letterforms")
	}
	if themed == Static(80, 6, true) {
		t.Fatal("a palette does not change the colours")
	}
}

func TestSetPaletteRecolours(t *testing.T) {
	t.Parallel()
	m := New(true)
	m.SetSize(80, 6)
	before := m.View()
	m.SetPalette(Palette{Accent: lipgloss.Color("#7aa2f7")})
	if m.View() == before {
		t.Fatal("SetPalette does not recolour the logo")
	}
	// Unset colours keep their current value, and SetDark restores Deck's.
	if m.colors.Low == nil || m.colors.High == nil || m.colors.Name == nil {
		t.Fatal("SetPalette dropped the unset colours")
	}
	m.SetDark(true)
	if m.View() != before {
		t.Fatal("SetDark does not restore Deck's palette")
	}
}

func TestTinyUsesPalette(t *testing.T) {
	t.Parallel()
	p := Palette{Accent: lipgloss.Color("#88c0d0"), Name: lipgloss.Color("#eceff4")}
	got := StaticPalette(10, 1, p)
	if ansi.Strip(got) != "▎▍▌ deck" {
		t.Fatalf("tiny logo = %q", ansi.Strip(got))
	}
	if got == Static(10, 1, true) {
		t.Fatal("tiny logo ignores the palette")
	}
}
