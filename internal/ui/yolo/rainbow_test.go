package yolo

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
)

// screen is h lines of exactly w cells.
func screen(w, h int) string {
	row := strings.Repeat("deck yolo ", w/10+1)[:w]
	lines := make([]string, h)
	for i := range lines {
		lines[i] = row
	}
	return strings.Join(lines, "\n")
}

func TestNewIsDone(t *testing.T) {
	t.Setenv("DECK_REDUCED_MOTION", "")
	a := New(true)
	now := time.Now()
	if !a.Done(now) {
		t.Fatal("a fresh effect should be done")
	}
	if a.Advance(now) {
		t.Fatal("a fresh effect should need no frames")
	}
	in := screen(20, 4)
	if got := a.Apply(in, 20, now); got != in {
		t.Fatal("a finished effect changed the screen")
	}
}

func TestApplyKeepsTheShape(t *testing.T) {
	t.Setenv("DECK_REDUCED_MOTION", "")
	const w, h = 40, 12
	in := screen(w, h)
	for _, dark := range []bool{true, false} {
		a := New(dark)
		start := time.Now()
		a.Start(start)
		if a.Done(start) {
			t.Fatal("effect done right after Start")
		}
		for _, at := range []time.Duration{0, 100 * time.Millisecond, 400 * time.Millisecond, Duration - time.Millisecond} {
			out := a.Apply(in, w, start.Add(at))
			lines := strings.Split(out, "\n")
			if len(lines) != h {
				t.Fatalf("at %v: %d lines, want %d", at, len(lines), h)
			}
			for i, l := range lines {
				if got := ansi.StringWidth(l); got != w {
					t.Fatalf("at %v: line %d is %d cells, want %d", at, i, got, w)
				}
			}
		}
	}
}

func TestApplyAfterDurationIsUnchanged(t *testing.T) {
	t.Setenv("DECK_REDUCED_MOTION", "")
	a := New(true)
	start := time.Now()
	a.Start(start)
	in := screen(30, 8)
	end := start.Add(Duration)
	if !a.Done(end) {
		t.Fatal("effect not done after Duration")
	}
	if got := a.Apply(in, 30, end); got != in {
		t.Fatal("screen changed after Duration")
	}
}

func TestSkipEndsIt(t *testing.T) {
	t.Setenv("DECK_REDUCED_MOTION", "")
	a := New(true)
	start := time.Now()
	a.Start(start)
	a.Skip()
	if !a.Done(start) {
		t.Fatal("effect not done after Skip")
	}
	in := screen(30, 8)
	if got := a.Apply(in, 30, start.Add(100*time.Millisecond)); got != in {
		t.Fatal("screen changed after Skip")
	}
	a.Start(start)
	if a.Done(start) {
		t.Fatal("Start after Skip should play again")
	}
}

func TestReducedMotionNeverPlays(t *testing.T) {
	t.Setenv("DECK_REDUCED_MOTION", "1")
	a := New(true)
	start := time.Now()
	a.Start(start)
	if !a.Done(start) {
		t.Fatal("effect plays with reduced motion")
	}
}
