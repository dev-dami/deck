package logo

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

var ansi = regexp.MustCompile("\x1b\\[[0-9;]*m")

func strip(s string) string { return ansi.ReplaceAllString(s, "") }

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

func TestStaticGolden(t *testing.T) {
	reduced(t, true)
	for _, c := range []struct {
		name string
		w, h int
		kind sizeKind
	}{
		{"static_full", 60, 10, kindFull},
		{"static_compact", 30, 3, kindCompact},
	} {
		m := New(true)
		m.SetSize(c.w, c.h)
		m.Start(time.Now())
		if m.kind != c.kind {
			t.Fatalf("%s: kind = %d, want %d", c.name, m.kind, c.kind)
		}
		got := strip(m.View())
		golden(t, c.name, got)
		if m.Advance(time.Now().Add(time.Second)) {
			t.Fatalf("%s: reduced logo must not move", c.name)
		}
		if strip(m.View()) != got {
			t.Fatalf("%s: reduced logo changed", c.name)
		}
	}
}

func checkShape(t *testing.T, m *Model, label string) {
	t.Helper()
	w, h := m.Bounds()
	lines := strings.Split(m.View(), "\n")
	if len(lines) != h {
		t.Fatalf("%s: %d lines, want %d", label, len(lines), h)
	}
	for i, l := range lines {
		if lw := lipgloss.Width(l); lw != w {
			t.Fatalf("%s: line %d has width %d, want %d: %q", label, i, lw, w, l)
		}
	}
}

func TestLineWidths(t *testing.T) {
	trueColor(t)
	reduced(t, false)
	for _, sz := range [][2]int{{140, 40}, {60, 10}, {44, 7}, {40, 5}, {30, 3}, {19, 2}, {10, 1}, {7, 1}} {
		for _, dark := range []bool{true, false} {
			m := New(dark)
			m.SetSize(sz[0], sz[1])
			checkShape(t, m, "static")
			if w, h := m.Bounds(); w > sz[0] || h > sz[1] {
				t.Fatalf("bounds %dx%d exceed area %v", w, h, sz)
			}
			now := time.Unix(100, 0)
			m.Start(now)
			for ms := 0; ms < 2500; ms += 70 {
				at := now.Add(time.Duration(ms) * time.Millisecond)
				m.Advance(at)
				if ms == 700 {
					bw, bh := m.Bounds()
					m.Mouse(bw/2, bh/2, true, at)
				}
				checkShape(t, m, "animated")
			}
		}
	}
	m := New(true)
	m.SetSize(5, 1)
	if w, h := m.Bounds(); w != 0 || h != 0 || m.View() != "" {
		t.Fatalf("too small should draw nothing, got %dx%d %q", w, h, m.View())
	}
}

func TestSizePicking(t *testing.T) {
	for _, c := range []struct {
		w, h int
		k    sizeKind
	}{{140, 40, kindFull}, {40, 5, kindFull}, {40, 4, kindCompact}, {39, 9, kindCompact}, {20, 2, kindCompact}, {19, 2, kindTiny}, {18, 2, kindTiny}, {6, 1, kindTiny}, {5, 5, kindNone}} {
		m := New(true)
		m.SetSize(c.w, c.h)
		if m.kind != c.k {
			t.Errorf("%dx%d: kind %d, want %d", c.w, c.h, m.kind, c.k)
		}
	}
}

// settle steps the logo at ~60fps until it reports idle or limit passes.
func settle(m *Model, from time.Time, limit time.Duration) (time.Time, bool) {
	now := from
	for end := from.Add(limit); now.Before(end); now = now.Add(16 * time.Millisecond) {
		if !m.Advance(now) {
			return now, true
		}
	}
	return now, false
}

func TestIntroConverges(t *testing.T) {
	reduced(t, false)
	m := New(true)
	m.SetSize(60, 10)
	static := strip(m.View())
	now := time.Unix(1000, 0)
	m.Start(now)
	if !m.Advance(now) {
		t.Fatal("intro should be moving at t=0")
	}
	if strip(m.View()) == static {
		t.Fatal("intro frame 0 should differ from the finished logo")
	}
	if !m.Advance(now.Add(2 * time.Second)) {
		t.Fatal("shimmer should still be running 2s in")
	}
	end, ok := settle(m, now.Add(2*time.Second), 8*time.Second)
	if !ok {
		t.Fatal("logo never went idle")
	}
	if d := end.Sub(now); d < 4*time.Second || d > 8*time.Second {
		t.Fatalf("idle after %v, want roughly 6s", d)
	}
	if m.Advance(end.Add(time.Second)) || m.Advance(end.Add(time.Minute)) {
		t.Fatal("must stay idle")
	}
	if got := strip(m.View()); got != static {
		t.Fatalf("settled logo differs from static:\n%s\nwant\n%s", got, static)
	}
}

func TestRippleWakesAndSettles(t *testing.T) {
	reduced(t, false)
	m := New(true)
	m.SetSize(60, 10)
	now := time.Unix(1000, 0)
	if m.Advance(now) {
		t.Fatal("a never-started logo is static")
	}
	m.Mouse(10, 3, false, now)
	if !m.Advance(now.Add(16 * time.Millisecond)) {
		t.Fatal("ripple should make Advance report moving")
	}
	end, ok := settle(m, now.Add(32*time.Millisecond), 10*time.Second)
	if !ok {
		t.Fatal("never settled after a ripple")
	}
	if end.Sub(now) < rippleLife {
		t.Fatalf("settled in %v, before the ripple ended", end.Sub(now))
	}
	// Out-of-bounds does nothing.
	m.Mouse(-1, 0, true, end)
	m.Mouse(999, 0, true, end)
	if m.Advance(end.Add(16 * time.Millisecond)) {
		t.Fatal("out-of-bounds mouse must not wake the logo")
	}
}

func TestClickDisplacesAndReturns(t *testing.T) {
	reduced(t, false)
	m := New(true)
	m.SetSize(60, 10)
	static := strip(m.View())
	now := time.Unix(1000, 0)
	w, h := m.Bounds()
	m.Mouse(w/2, h/2, true, now)
	changed := false
	for ms := 16; ms < 500; ms += 16 {
		m.Advance(now.Add(time.Duration(ms) * time.Millisecond))
		if strip(m.View()) != static {
			changed = true
		}
	}
	if !changed {
		t.Fatal("click did not alter the logo")
	}
	if _, ok := settle(m, now.Add(500*time.Millisecond), 10*time.Second); !ok {
		t.Fatal("never settled")
	}
	if got := strip(m.View()); got != static {
		t.Fatalf("dots did not spring back:\n%s\nwant\n%s", got, static)
	}
}

func TestCalmSettlesSooner(t *testing.T) {
	reduced(t, false)
	idle := func(calm bool) time.Duration {
		m := New(true)
		m.SetSize(60, 10)
		now := time.Unix(1000, 0)
		m.Mouse(10, 3, false, now)
		if calm {
			m.Calm(now.Add(100 * time.Millisecond))
		}
		end, ok := settle(m, now.Add(16*time.Millisecond), 20*time.Second)
		if !ok {
			t.Fatal("never settled")
		}
		return end.Sub(now)
	}
	calm, normal := idle(true), idle(false)
	if calm >= normal {
		t.Fatalf("calm %v should settle before normal %v", calm, normal)
	}
	// Calm never wakes a static logo.
	m := New(true)
	m.SetSize(60, 10)
	m.Calm(time.Unix(1000, 0))
	if m.Advance(time.Unix(1001, 0)) {
		t.Fatal("Calm woke a static logo")
	}
}

func TestReducedNeverMoves(t *testing.T) {
	reduced(t, true)
	m := New(true)
	m.SetSize(60, 10)
	want := m.View()
	now := time.Unix(1000, 0)
	m.Start(now)
	for i := 0; i < 20; i++ {
		at := now.Add(time.Duration(i) * 50 * time.Millisecond)
		m.Mouse(10, 3, i%2 == 0, at)
		if m.Advance(at) {
			t.Fatal("reduced motion must never move")
		}
		if m.View() != want {
			t.Fatal("reduced motion must stay static")
		}
	}
}

func TestDarkAndLightDiffer(t *testing.T) {
	trueColor(t)
	d, l := New(true), New(false)
	d.SetSize(60, 10)
	l.SetSize(60, 10)
	if d.View() == l.View() {
		t.Fatal("dark and light palettes should differ")
	}
	if strip(d.View()) != strip(l.View()) {
		t.Fatal("palette must not change shapes")
	}
	d.SetDark(false)
	if d.View() != l.View() {
		t.Fatal("SetDark(false) should match a light logo")
	}
}

func TestGlyphBitmaps(t *testing.T) {
	if b := fullBitmap(); b.h != fullH {
		t.Fatalf("full height %d", b.h)
	}
	if b := compactBitmap(); b.h != 8 {
		t.Fatalf("compact height %d", b.h)
	}
}

func BenchmarkView(b *testing.B) {
	prev := motion.Reduced()
	motion.SetReduced(false)
	defer motion.SetReduced(prev)
	m := New(true)
	m.SetSize(140, 40)
	now := time.Unix(1000, 0)
	m.Start(now)
	now = now.Add(2 * time.Second)
	m.Advance(now)
	w, h := m.Bounds()
	m.Mouse(w/3, h/2, true, now)
	m.Mouse(w/2, h/2, false, now.Add(100*time.Millisecond))
	m.Mouse(2*w/3, h/2, true, now.Add(200*time.Millisecond))
	now = now.Add(300 * time.Millisecond)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		now = now.Add(time.Millisecond)
		m.now = now
		m.dirty = true
		_ = m.View()
	}
}
