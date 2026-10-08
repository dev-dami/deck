package motion

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/charmbracelet/lipgloss"
	"github.com/dev-dami/deck/internal/tui/theme"
)

// withReduced sets the global switch for one test and restores it afterwards.
func withReduced(t *testing.T, v bool) {
	t.Helper()
	old := Reduced()
	SetReduced(v)
	t.Cleanup(func() { SetReduced(old) })
}

func TestSpringSettlesPerPreset(t *testing.T) {
	withReduced(t, false)
	cases := []struct {
		name      string
		p         Preset
		maxFrames int
	}{
		{"snappy", Snappy, 30}, // ~500ms worst case, ~230ms typical
		{"soft", Soft, 20},
		{"glide", Glide, 90},
	}
	for _, c := range cases {
		s := c.p.New(60)
		s.Set(20)
		frames := 0
		for s.Update() {
			frames++
			if frames > c.maxFrames {
				t.Fatalf("%s: not settled after %d frames (pos=%.4f vel=%.4f)", c.name, c.maxFrames, s.Pos, s.Vel)
			}
		}
		if s.Pos != 20 || s.Vel != 0 {
			t.Fatalf("%s: settled at pos=%v vel=%v, want exactly 20/0", c.name, s.Pos, s.Vel)
		}
		if s.Update() {
			t.Fatalf("%s: Update on a settled spring reports moving", c.name)
		}
	}
}

func TestSnappyDurationAboutExpandCollapse(t *testing.T) {
	withReduced(t, false)
	s := Snappy.New(60)
	s.Set(6) // a typical 6-row expand
	frames := 0
	for s.Update() {
		frames++
	}
	if d := time.Duration(frames) * time.Second / 60; d > 300*time.Millisecond {
		t.Fatalf("snappy took %v for 6 rows, want <= 300ms", d)
	}
}

func TestCriticallyDampedIsMonotone(t *testing.T) {
	withReduced(t, false)
	for name, p := range map[string]Preset{"snappy": Snappy, "soft": Soft} {
		s := p.New(60)
		s.Set(10)
		prev := s.Pos
		for i := 0; i < 200 && s.Update(); i++ {
			if s.Pos < prev-1e-9 {
				t.Fatalf("%s: moved backwards %v -> %v", name, prev, s.Pos)
			}
			if s.Pos > 10+0.01 {
				t.Fatalf("%s: overshoot to %v", name, s.Pos)
			}
			prev = s.Pos
		}
	}
	// Glide is nearly critical: overshoot must stay visually negligible.
	s := Glide.New(60)
	s.Set(10)
	for i := 0; i < 300 && s.Update(); i++ {
		if s.Pos > 10.2 {
			t.Fatalf("glide overshoot %v", s.Pos)
		}
	}
}

func TestSpringRetargetsMidFlight(t *testing.T) {
	withReduced(t, false)
	s := Glide.New(60)
	s.Set(10)
	for i := 0; i < 5; i++ {
		s.Update()
	}
	s.Set(0)
	n := 0
	for s.Update() {
		if n++; n > 300 {
			t.Fatal("did not settle after retarget")
		}
	}
	if s.Pos != 0 {
		t.Fatalf("pos=%v want 0", s.Pos)
	}
}

func TestReducedSnaps(t *testing.T) {
	withReduced(t, true)

	s := Snappy.New(60)
	s.Set(42)
	if s.Pos != 42 || s.Vel != 0 {
		t.Fatalf("Set did not snap: %+v", s)
	}
	s.Target = 7
	if s.Update() || s.Pos != 7 {
		t.Fatalf("Update should snap and report not moving, pos=%v", s.Pos)
	}

	now := time.Unix(1000, 0)
	f := NewFade(time.Second, EaseOutCubic)
	if f.Progress(now) != 0 {
		t.Fatal("unstarted fade should be 0")
	}
	f.Start(now)
	if f.Progress(now) != 1 || !f.Done(now) {
		t.Fatal("reduced fade should be complete immediately")
	}

	tw := NewTypewriter("◆ deck", 400*time.Millisecond)
	if tw.Visible(now) != "◆ deck" {
		t.Fatalf("reduced typewriter = %q", tw.Visible(now))
	}
	if Pulse(now, PulsePeriod) != 1 {
		t.Fatal("reduced pulse should hold at 1")
	}
	if got := NewSpinner().Frame(now); got != theme.SpinnerFrames[0] {
		t.Fatalf("reduced spinner = %q", got)
	}
}

func TestEnvInitialValue(t *testing.T) {
	// The package-level initialiser reads DECK_REDUCED_MOTION; SetReduced round-trips.
	withReduced(t, false)
	SetReduced(true)
	if !Reduced() {
		t.Fatal("SetReduced(true) not reflected")
	}
	SetReduced(false)
	if Reduced() {
		t.Fatal("SetReduced(false) not reflected")
	}
}

func TestClockSingleOutstandingTick(t *testing.T) {
	withReduced(t, false)
	c := NewClock(60)
	if c.Active() || c.Tick() != nil {
		t.Fatal("idle clock must not tick")
	}
	cmd := c.Wake()
	if cmd == nil {
		t.Fatal("Wake should start ticking")
	}
	if c.Wake() != nil || c.Tick() != nil {
		t.Fatal("a second tick must not be scheduled while one is outstanding")
	}
	msg, ok := cmd().(FrameMsg)
	if !ok {
		t.Fatal("tick did not produce FrameMsg")
	}
	if !c.Accept(msg) {
		t.Fatal("current frame rejected")
	}
	if c.Accept(msg) {
		t.Fatal("duplicate frame accepted")
	}
	// Still moving: next tick allowed, exactly one.
	next := c.Tick()
	if next == nil || c.Tick() != nil {
		t.Fatal("expected exactly one new tick")
	}
	m2 := next().(FrameMsg)
	if m2.ID == msg.ID {
		t.Fatal("frame ids should advance")
	}
	if !c.Accept(m2) {
		t.Fatal("second frame rejected")
	}
	// Nothing moves any more: the loop ends.
	c.SetMoving(false)
	if c.Active() || c.Tick() != nil {
		t.Fatal("clock must stop when inactive")
	}
}

func TestClockAcceptRejectsStale(t *testing.T) {
	withReduced(t, false)
	c := NewClock(60)
	old := c.Wake()().(FrameMsg)
	c.Stop()
	if c.Accept(old) {
		t.Fatal("frame from before Stop accepted")
	}
	fresh := c.Wake()
	if fresh == nil {
		t.Fatal("Wake after Stop should start a new tick")
	}
	if c.Accept(old) {
		t.Fatal("stale frame accepted after restart")
	}
	if !c.Accept(fresh().(FrameMsg)) {
		t.Fatal("fresh frame rejected")
	}
	if c.Accept(FrameMsg{ID: 9999}) {
		t.Fatal("unknown id accepted")
	}
}

func TestClockSilentWhenReduced(t *testing.T) {
	withReduced(t, true)
	c := NewClock(60)
	if c.Wake() != nil || c.Active() {
		t.Fatal("reduced clock must not tick")
	}
}

func TestFadeEasing(t *testing.T) {
	withReduced(t, false)
	now := time.Unix(1000, 0)
	f := NewFade(100*time.Millisecond, EaseOutCubic)
	if f.Progress(now) != 0 || f.Done(now) {
		t.Fatal("unstarted fade")
	}
	f.Start(now)
	if f.Progress(now) != 0 {
		t.Fatal("progress at start should be 0")
	}
	mid := f.Progress(now.Add(50 * time.Millisecond))
	if mid <= 0.5 || mid >= 1 {
		t.Fatalf("ease-out midpoint = %v, want in (0.5,1)", mid)
	}
	if f.Done(now.Add(99 * time.Millisecond)) {
		t.Fatal("done too early")
	}
	end := now.Add(100 * time.Millisecond)
	if !f.Done(end) || f.Progress(end) != 1 || f.Progress(end.Add(time.Hour)) != 1 {
		t.Fatal("fade should end at exactly 1")
	}
	if got := EaseInOutSine(0.5); got < 0.499 || got > 0.501 {
		t.Fatalf("EaseInOutSine(0.5)=%v", got)
	}
	if EaseInOutSine(0) != 0 || EaseInOutSine(1) < 0.999999 {
		t.Fatal("EaseInOutSine endpoints")
	}
}

func TestBlendEndpointsAndMid(t *testing.T) {
	for _, dark := range []bool{true, false} {
		a, b := theme.Muted, theme.Accent
		pa, pb := a.Light, b.Light
		if dark {
			pa, pb = a.Dark, b.Dark
		}
		if got := string(Blend(a, b, 0, dark)); got != pa {
			t.Fatalf("t=0 dark=%v: %s != %s", dark, got, pa)
		}
		if got := string(Blend(a, b, 1, dark)); got != pb {
			t.Fatalf("t=1 dark=%v: %s != %s", dark, got, pb)
		}
		mid := string(Blend(a, b, 0.5, dark))
		if len(mid) != 7 || mid[0] != '#' || mid == pa || mid == pb {
			t.Fatalf("mid blend = %q", mid)
		}
	}
	// Grey to grey and black to white must not yield NaN artefacts.
	g := lipgloss.AdaptiveColor{Light: "#000000", Dark: "#000000"}
	w := lipgloss.AdaptiveColor{Light: "#ffffff", Dark: "#ffffff"}
	if m := string(Blend(g, w, 0.5, true)); len(m) != 7 || strings.Contains(strings.ToLower(m), "nan") {
		t.Fatalf("grey blend = %q", m)
	}
	if s := Ramp(theme.Muted, theme.Accent, 0.3, true); s.GetForeground() == nil {
		t.Fatal("Ramp should set a foreground")
	}
}

func TestPulseRange(t *testing.T) {
	withReduced(t, false)
	base := time.Unix(1_700_000_000, 0)
	lo, hi := 1.0, 0.0
	for i := 0; i < 1600; i++ {
		p := Pulse(base.Add(time.Duration(i)*time.Millisecond), PulsePeriod)
		if p < 0 || p > 1 {
			t.Fatalf("pulse out of range: %v", p)
		}
		lo, hi = min(lo, p), max(hi, p)
	}
	if lo > 0.01 || hi < 0.99 {
		t.Fatalf("pulse should span the range, got %v..%v", lo, hi)
	}
	if Pulse(base, 0) != 1 {
		t.Fatal("zero period should hold at 1")
	}
}

func TestTypewriterRuneSafe(t *testing.T) {
	withReduced(t, false)
	now := time.Unix(1000, 0)
	for _, s := range []string{"◆ deck", "hi 👋🏽 there 👨‍👩‍👧 ok", "é́ñ日本語"} {
		tw := NewTypewriter(s, 400*time.Millisecond)
		if tw.Visible(now) != "" {
			t.Fatalf("unstarted should be empty, got %q", tw.Visible(now))
		}
		tw.Start(now)
		prev := ""
		for ms := 0; ms <= 450; ms += 5 {
			v := tw.Visible(now.Add(time.Duration(ms) * time.Millisecond))
			if !utf8.ValidString(v) {
				t.Fatalf("%q: invalid UTF-8 at %dms: %q", s, ms, v)
			}
			if !strings.HasPrefix(s, v) || len(v) < len(prev) {
				t.Fatalf("%q: %q is not a growing prefix (prev %q)", s, v, prev)
			}
			prev = v
		}
		if prev != s || !tw.Done(now.Add(400*time.Millisecond)) {
			t.Fatalf("%q: final %q", s, prev)
		}
	}
	// Emoji clusters are revealed whole: a ZWJ family never appears half-built.
	fam := "👨‍👩‍👧"
	tw := NewTypewriter(fam+"x", 100*time.Millisecond)
	tw.Start(now)
	for ms := 0; ms <= 100; ms++ {
		v := tw.Visible(now.Add(time.Duration(ms) * time.Millisecond))
		if v != "" && v != fam && v != fam+"x" {
			t.Fatalf("split grapheme at %dms: %q", ms, v)
		}
	}
	empty := NewTypewriter("", time.Second)
	empty.Start(now)
	if empty.Visible(now) != "" || !empty.Done(now) {
		t.Fatal("empty typewriter")
	}
}

func TestSpinnerFrames(t *testing.T) {
	withReduced(t, false)
	sp := NewSpinner()
	base := time.Unix(1_700_000_000, 0)
	seen := map[string]bool{}
	for i := 0; i < 1000; i += 10 {
		seen[sp.Frame(base.Add(time.Duration(i)*time.Millisecond))] = true
	}
	if len(seen) != len(theme.SpinnerFrames) {
		t.Fatalf("saw %d distinct frames, want %d", len(seen), len(theme.SpinnerFrames))
	}
	// 8fps means a frame lasts 125ms: two reads 1ms apart inside one slot agree.
	slot := time.Unix(1_700_000_000, 0).Truncate(125 * time.Millisecond).Add(10 * time.Millisecond)
	if sp.Frame(slot) != sp.Frame(slot.Add(time.Millisecond)) {
		t.Fatal("spinner changed within a frame slot")
	}
	if (Spinner{}).Frame(base) != "" {
		t.Fatal("empty spinner")
	}
}

func TestBudgetDegrades(t *testing.T) {
	withReduced(t, false)

	fast := NewBudget()
	for i := 0; i < 100; i++ {
		fast.Observe(2 * time.Millisecond)
	}
	if fast.Degraded() || Reduced() {
		t.Fatal("fast frames must not degrade")
	}

	// A few outliers (under p90) are tolerated.
	outliers := NewBudget()
	for i := 0; i < 30; i++ {
		d := 3 * time.Millisecond
		if i%15 == 0 {
			d = 50 * time.Millisecond
		}
		outliers.Observe(d)
	}
	if outliers.Degraded() {
		t.Fatal("2 outliers in 30 frames should not degrade")
	}

	slow := NewBudget()
	for i := 0; i < 29; i++ {
		slow.Observe(20 * time.Millisecond)
	}
	if slow.Degraded() {
		t.Fatal("must wait for a full 30-frame window")
	}
	slow.Observe(20 * time.Millisecond)
	if !slow.Degraded() || !Reduced() {
		t.Fatal("slow frames should degrade and switch on reduced motion")
	}
}
