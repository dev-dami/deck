package yolo

import (
	"flag"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/dev-dami/deck/internal/tui/motion"
)

var update = flag.Bool("update", false, "rewrite golden files")

var ansiRE = regexp.MustCompile("\x1b\\[[0-9;]*m")

func strip(s string) string { return ansiRE.ReplaceAllString(s, "") }

func reduced(t *testing.T, v bool) {
	t.Helper()
	prev := motion.Reduced()
	motion.SetReduced(v)
	t.Cleanup(func() { motion.SetReduced(prev) })
}

func trueColor(t *testing.T) {
	t.Helper()
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(prev) })
}

func golden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name+".golden")
	if *update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run with -update)", err)
	}
	w := strings.TrimRight(strings.ReplaceAll(string(want), "\r\n", "\n"), "\n")
	if w != got {
		t.Fatalf("%s differs from golden:\n--- got\n%s\n--- want\n%s", name, got, w)
	}
}

var t0 = time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

func at(ms int) time.Time { return t0.Add(time.Duration(ms) * time.Millisecond) }

func started(width int) *Anim {
	a := New(true)
	a.SetWidth(width)
	a.Start(t0)
	return a
}

// TestFrameWidths samples the whole run every 16ms: every frame has exactly
// Height lines of exactly width cells, plain or coloured.
func TestFrameWidths(t *testing.T) {
	reduced(t, false)
	for _, coloured := range []bool{false, true} {
		if coloured {
			trueColor(t)
		}
		for _, w := range []int{120, 60, 40, 30, 12} {
			a := started(w)
			wantRows := FullHeight
			if w < MinWidth {
				wantRows = 1
			}
			for ms := 0; ms <= 2300; ms += 16 {
				now := at(ms)
				h := a.Height(now)
				v := a.View(w, now)
				if h == 0 {
					if v != "" {
						t.Fatalf("w=%d t=%dms: height 0 but view %q", w, ms, v)
					}
					continue
				}
				if h > wantRows {
					t.Fatalf("w=%d t=%dms: height %d > %d", w, ms, h, wantRows)
				}
				lines := strings.Split(v, "\n")
				if len(lines) != h {
					t.Fatalf("w=%d t=%dms: %d lines, height %d", w, ms, len(lines), h)
				}
				for i, l := range lines {
					if got := lipgloss.Width(l); got != w {
						t.Fatalf("w=%d t=%dms line %d: width %d\n%s", w, ms, i, got, strip(v))
					}
					if strings.ContainsAny(strip(l), "\t\r") {
						t.Fatalf("w=%d t=%dms line %d has control characters", w, ms, i)
					}
				}
			}
		}
	}
}

func TestHeightSequence(t *testing.T) {
	reduced(t, false)
	a := started(60)
	prev := -1
	var seq []int
	for ms := 0; ms <= 2300; ms += 16 {
		h := a.Height(at(ms))
		if prev >= 0 && h > prev {
			t.Fatalf("height grew from %d to %d at %dms", prev, h, ms)
		}
		if h != prev {
			seq = append(seq, h)
		}
		prev = h
		if ms < 2000 && h != FullHeight {
			t.Fatalf("height %d at %dms, want %d before the collapse", h, ms, FullHeight)
		}
	}
	want := []int{4, 3, 2, 1, 0}
	if len(seq) != len(want) {
		t.Fatalf("height steps = %v, want %v", seq, want)
	}
	for i := range want {
		if seq[i] != want[i] {
			t.Fatalf("height steps = %v, want %v", seq, want)
		}
	}

	c := started(30)
	if h := c.Height(at(500)); h != 1 {
		t.Fatalf("compact height = %d, want 1", h)
	}
	if h := c.Height(at(2300)); h != 0 {
		t.Fatalf("compact height after the run = %d, want 0", h)
	}
}

func TestDoneAndAdvance(t *testing.T) {
	reduced(t, false)
	a := New(true)
	if !a.Done(at(0)) || a.Height(at(0)) != 0 || a.Advance(at(0)) {
		t.Fatal("an unstarted Anim should be done")
	}
	a.Start(t0)
	if a.Done(at(0)) || !a.Advance(at(0)) {
		t.Fatal("started Anim should be moving")
	}
	if a.Done(at(2199)) || !a.Advance(at(2199)) {
		t.Fatal("should still be moving at 2199ms")
	}
	if !a.Done(at(2200)) || a.Advance(at(2200)) {
		t.Fatal("should be done at 2200ms")
	}
	if a.Height(at(2200)) != 0 || a.View(60, at(2200)) != "" {
		t.Fatal("a finished Anim draws nothing")
	}
	a.Start(at(5000)) // replay
	if a.Done(at(5100)) {
		t.Fatal("restart should play again")
	}
}

func TestSkip(t *testing.T) {
	reduced(t, false)
	a := started(60)
	if a.Done(at(500)) {
		t.Fatal("done too early")
	}
	a.Skip()
	if !a.Done(at(500)) || a.Advance(at(500)) || a.Height(at(500)) != 0 || a.View(60, at(500)) != "" {
		t.Fatal("Skip should end the animation")
	}
	// Skipping an Anim that never started changes nothing, and a restart
	// clears an earlier skip.
	b := New(true)
	b.Skip()
	b.Start(t0)
	if b.Done(at(100)) {
		t.Fatal("Skip before Start must not stick")
	}
	a.Start(at(1000))
	if a.Done(at(1100)) {
		t.Fatal("Start should clear a skip")
	}
}

func TestReducedMotionIsInstant(t *testing.T) {
	reduced(t, true)
	a := New(true)
	a.SetWidth(60)
	a.Start(t0)
	if !a.Done(t0) || a.Advance(t0) {
		t.Fatal("reduced motion: done immediately")
	}
	if a.Height(t0) != 0 || a.View(60, t0) != "" {
		t.Fatal("reduced motion: height 0, nothing to draw")
	}
}

// TestFrameRateIndependent: a frame depends only on elapsed time, not on
// which frames were drawn before it.
func TestFrameRateIndependent(t *testing.T) {
	reduced(t, false)
	fast, slow := started(60), started(60)
	for ms := 0; ms <= 2200; ms += 7 {
		fast.View(60, at(ms))
	}
	for _, ms := range []int{1999, 0, 1500, 320, 1170, 700, 1999} {
		if f, s := fast.View(60, at(ms)), slow.View(60, at(ms)); f != s {
			t.Fatalf("frame at %dms depends on history:\n%s\n%s", ms, f, s)
		}
	}
}

func TestSpritesAreRectangular(t *testing.T) {
	for _, s := range tumble {
		if len(s) != 2 {
			t.Fatalf("tumble pose has %d rows", len(s))
		}
		for _, row := range s {
			if n := len([]rune(row)); n != 3 {
				t.Fatalf("tumble row %q has %d columns", row, n)
			}
		}
	}
	for _, s := range []sprite{yoloKick, yoloCheerA, askBlock} {
		if got := flip(flip(s)); strings.Join(got, "|") != strings.Join(s, "|") {
			t.Fatalf("flip is not an involution: %q -> %q", s, got)
		}
	}
	if got := flip(sprite{"?/|──"}); got[0] != "──|\\?" {
		t.Fatalf("flip = %q", got[0])
	}
}

// TestBeats pins the story, so a tweak that breaks the order is loud: the
// ask figure and its sign are on screen until the kick and gone after the
// flight; the word only appears once the winner celebrates.
func TestBeats(t *testing.T) {
	reduced(t, false)
	a := started(60)
	view := func(ms int) string { return strip(a.View(60, at(ms))) }

	if v := view(150); !strings.Contains(v, "?") || strings.Contains(v, "yolo") {
		t.Fatalf("face-off should show the sign and no word:\n%s", v)
	}
	if v := view(700); !strings.ContainsAny(v, "*✦") {
		t.Fatalf("scuffle should show impact sparks:\n%s", v)
	}
	if v := view(1170); !strings.ContainsAny(v, "*✦") {
		t.Fatalf("the kick should show impact sparks:\n%s", v)
	}
	if v := view(1300); !strings.Contains(v, "·") && !strings.Contains(v, ".") {
		t.Fatalf("flight should leave dust:\n%s", v)
	}
	if v := view(1700); strings.Contains(v, "?") || strings.Contains(v, "¿") {
		t.Fatalf("the sign should be gone by the victory:\n%s", v)
	}
	if v := view(1500); !strings.Contains(v, "YOLO") {
		t.Fatalf("the word should flash at 1500ms:\n%s", v)
	}
	if v := view(1700); !strings.Contains(v, "yolo") || strings.Contains(v, "YOLO") {
		t.Fatalf("the word should settle to lowercase:\n%s", v)
	}
}

func TestKeyFrameGoldens(t *testing.T) {
	reduced(t, false)
	for _, c := range []struct {
		name string
		ms   int
	}{
		{"1_faceoff", 150},
		{"2_scuffle", 700},
		{"3_kick", 1170},
		{"4_flight", 1280},
		{"5_victory", 1520},
	} {
		a := started(60)
		got := strip(a.View(60, at(c.ms)))
		golden(t, c.name, got)
	}
}

func TestCompactGolden(t *testing.T) {
	reduced(t, false)
	var sb strings.Builder
	for _, ms := range []int{150, 700, 1170, 1280, 1520} {
		a := started(30)
		sb.WriteString(strip(a.View(30, at(ms))) + "|\n")
	}
	golden(t, "compact", strings.TrimRight(sb.String(), "\n"))
}
