package yolo

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"

	"github.com/dev-dami/deck/internal/tui/motion"
)

func screen(w, h int) string {
	styled := lipgloss.NewStyle().Foreground(lipgloss.Color("#5FD7AF")).Render("◆ deck")
	rows := []string{
		styled + "  claude · codex",
		"hello 世界 wide chars and emoji ✦ here",
		strings.Repeat("x", w+10), // longer than the screen
		"",
	}
	out := make([]string, h)
	for i := range out {
		out[i] = rows[i%len(rows)]
	}
	return strings.Join(out, "\n")
}

func withProfile(t *testing.T) {
	t.Helper()
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(prev) })
	r := motion.Reduced()
	motion.SetReduced(false)
	t.Cleanup(func() { motion.SetReduced(r) })
}

func TestEveryFrameKeepsTheScreenShape(t *testing.T) {
	withProfile(t)
	for _, sz := range [][2]int{{80, 24}, {140, 40}, {30, 8}, {12, 3}} {
		w, h := sz[0], sz[1]
		scr := screen(w, h)
		t0 := time.Now()
		for _, dark := range []bool{true, false} {
			a := New(dark)
			a.Start(t0)
			for ms := 0; ms <= int(Duration/time.Millisecond)+50; ms += 16 {
				out := a.Apply(scr, w, t0.Add(time.Duration(ms)*time.Millisecond))
				lines := strings.Split(out, "\n")
				if len(lines) != h {
					t.Fatalf("%dx%d at %dms: %d lines", w, h, ms, len(lines))
				}
				for i, l := range lines {
					if a.Done(t0.Add(time.Duration(ms) * time.Millisecond)) {
						continue // finished: the screen comes back as it was given
					}
					if got := ansi.StringWidth(l); got != w {
						t.Fatalf("%dx%d dark=%v at %dms line %d is %d cells, want %d: %q", w, h, dark, ms, i, got, w, ansi.Strip(l))
					}
				}
			}
		}
	}
}

func TestUntouchedBeforeAndAfter(t *testing.T) {
	withProfile(t)
	scr := screen(80, 24)
	t0 := time.Now()
	a := New(true)
	if got := a.Apply(scr, 80, t0); got != scr {
		t.Fatal("an effect that never started must not touch the screen")
	}
	a.Start(t0)
	if got := a.Apply(scr, 80, t0.Add(Duration)); got != scr {
		t.Fatal("after Duration the screen comes back unchanged")
	}
	a.Start(t0)
	a.Skip()
	if got := a.Apply(scr, 80, t0.Add(500*time.Millisecond)); got != scr {
		t.Fatal("Skip ends it at once")
	}
}

func TestReducedMotionNeverPlays(t *testing.T) {
	withProfile(t)
	motion.SetReduced(true)
	a := New(true)
	t0 := time.Now()
	a.Start(t0)
	if !a.Done(t0) || a.Advance(t0) {
		t.Fatal("reduced motion: done at once")
	}
}

func TestMiddleIsRainbowWithYOLO(t *testing.T) {
	withProfile(t)
	w, h := 80, 24
	t0 := time.Now()
	a := New(true)
	a.Start(t0)
	out := a.Apply(screen(w, h), w, t0.Add(800*time.Millisecond))
	plain := ansi.Strip(out)
	if n := strings.Count(plain, "█"); n < 40 {
		t.Fatalf("YOLO should be drawn in the middle (%d block cells):\n%s", n, plain)
	}
	if !strings.Contains(out, "48;2;") {
		t.Fatal("the screen should be painted with truecolor backgrounds")
	}
	// Text outside the letters is still readable.
	if !strings.Contains(plain, "◆ deck") {
		t.Fatalf("screen text should survive the rainbow:\n%s", plain)
	}
	// Mid-effect, no line keeps its own colours: every cell is repainted.
	for i, l := range strings.Split(out, "\n") {
		if strings.Contains(l, "38;2;95;215;175") {
			t.Fatalf("line %d still has the original accent colour", i)
		}
	}
}

func TestBlastStartsInTheMiddle(t *testing.T) {
	withProfile(t)
	w, h := 80, 24
	t0 := time.Now()
	a := New(true)
	a.Start(t0)
	out := strings.Split(a.Apply(screen(w, h), w, t0.Add(80*time.Millisecond)), "\n")
	if !strings.Contains(out[h/2], "48;2;") {
		t.Fatal("the middle is painted first")
	}
	if strings.Contains(out[0], "48;2;") || strings.Contains(out[h-1], "48;2;") {
		t.Fatal("the top and bottom rows are still untouched early on")
	}
}

func TestTrailingRingHandsBackTheMiddleFirst(t *testing.T) {
	withProfile(t)
	w, h := 80, 24
	t0 := time.Now()
	a := New(true)
	a.Start(t0)
	out := strings.Split(a.Apply(screen(w, h), w, t0.Add(1300*time.Millisecond)), "\n")
	if !strings.Contains(out[0], "48;2;") {
		t.Fatal("the edges are still rippling")
	}
	// Row 14 is all x; near the centre it is back to plain text.
	if !strings.Contains(out[14], strings.Repeat("x", 12)) {
		t.Fatalf("the middle should be back to normal: %q", out[14])
	}
}

func BenchmarkApply(b *testing.B) {
	lipgloss.SetColorProfile(termenv.TrueColor)
	scr := screen(140, 40)
	t0 := time.Now()
	a := New(true)
	a.Start(t0)
	at := t0.Add(800 * time.Millisecond)
	for i := 0; i < b.N; i++ {
		a.Apply(scr, 140, at)
	}
}
