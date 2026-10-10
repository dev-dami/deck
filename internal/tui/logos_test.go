package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/dev-dami/deck/internal/termimg"
)

func TestKittyTerminalsShowTheRealLogos(t *testing.T) {
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(prev) })

	r := newRig(t, 120, 30)
	r.m.setupLogos(termimg.Kitty)
	r.start("go")
	first := r.m.View()
	if strings.Count(first, "\x1b_Ga=T,U=1,") != 2 {
		t.Fatal("the first frame sends both logos")
	}
	if strings.Contains(r.m.View(), "\x1b_G") {
		t.Fatal("the logos are sent once")
	}
	mark := r.m.markR("claude", sAccent)
	if !strings.ContainsRune(mark, '\U0010EEEE') || sw(mark) != logoCells {
		t.Fatalf("claude's mark = %q (%d cells), want %d placeholder cells", mark, sw(mark), logoCells)
	}
	if !strings.Contains(mark, "38;2;122;0;1") { // 0x7A0001: the colour names the image
		t.Fatalf("placeholder colour missing: %q", mark)
	}
	if !strings.ContainsRune(r.m.View(), '\U0010EEEE') {
		t.Fatal("the speaker header uses the logo")
	}
}

func TestOtherTerminalsKeepTheTextMarks(t *testing.T) {
	for _, p := range []termimg.Protocol{termimg.None, termimg.Sixel, termimg.ITerm} {
		r := newRig(t, 120, 30)
		r.m.setupLogos(p)
		r.start("go")
		if v := r.m.View(); strings.Contains(v, "\x1b_G") || !strings.Contains(ansiStrip(v), "✻ claude") {
			t.Fatalf("%v: want the text mark", p)
		}
	}
}
