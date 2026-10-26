package graph_test

import (
	"fmt"
	"math"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/exp/golden"

	"github.com/dev-dami/deck/internal/ui/graph"
	"github.com/dev-dami/deck/internal/ui/styles"
	"github.com/dev-dami/deck/internal/ui/tone"
)

// These tests share tone's light switch, so none of them run in parallel.

var now = time.Date(2026, 10, 11, 14, 0, 0, 0, time.UTC)

type namedTheme struct {
	name  string
	theme graph.Theme
}

func themes() []namedTheme {
	defer tone.SetLight(false)
	tone.SetLight(false)
	dark := styles.DeckDark()
	d := graph.FromStyles(&dark)
	tone.SetLight(true)
	light := styles.DeckLight()
	l := graph.FromStyles(&light)
	return []namedTheme{{"dark", d}, {"light", l}}
}

// eachTheme runs f under a subtest per theme and width.
func eachTheme(t *testing.T, widths []int, f func(t *testing.T, th graph.Theme, w int) string) {
	t.Helper()
	for _, nt := range themes() {
		t.Run(nt.name, func(t *testing.T) {
			for _, w := range widths {
				t.Run(fmt.Sprintf("w%03d", w), func(t *testing.T) {
					golden.RequireEqual(t, f(t, nt.theme, w))
				})
			}
		})
	}
}

// requireWidth fails unless every line of out is exactly w cells wide.
func requireWidth(t *testing.T, out string, w int) {
	t.Helper()
	for i, l := range strings.Split(out, "\n") {
		if got := ansi.StringWidth(l); got != w {
			t.Fatalf("line %d is %d cells wide, want %d: %q", i, got, w, ansi.Strip(l))
		}
	}
}

// hourly is a day of hourly token counts for three agents, ending at now.
func hourly() map[string][]graph.Point {
	data := map[string][]graph.Point{}
	for h := range 24 {
		at := now.Add(time.Duration(h-23) * time.Hour)
		f := float64(h)
		data["claude"] = append(data["claude"], graph.Point{T: at, V: math.Round(4000 + 3500*math.Sin(f/3))})
		data["codex"] = append(data["codex"], graph.Point{T: at, V: math.Round(2500 + 2000*math.Cos(f/4))})
		if h%3 == 0 {
			data["opencode"] = append(data["opencode"], graph.Point{T: at, V: float64(300 * (h % 7))})
		}
	}
	return data
}

func daily(days int) map[string][]graph.Point {
	data := map[string][]graph.Point{}
	for d := range days {
		at := time.Date(2026, 10, 11, 0, 0, 0, 0, time.UTC).AddDate(0, 0, d-days+1)
		data["claude"] = append(data["claude"], graph.Point{T: at, V: float64(20000 + 9000*((d*7)%5))})
		data["codex"] = append(data["codex"], graph.Point{T: at, V: float64(8000 + 6000*((d*3)%4))})
	}
	return data
}

func chart(th graph.Theme, w, h int, data map[string][]graph.Point, opts ...graph.TimeSeriesOption) graph.TimeSeries {
	m := graph.NewTimeSeries(th, w, h, opts...)
	m.SetData(data)
	return m
}

func key(s string) tea.Msg {
	switch s {
	case "left":
		return tea.KeyPressMsg{Code: tea.KeyLeft}
	case "right":
		return tea.KeyPressMsg{Code: tea.KeyRight}
	case "home":
		return tea.KeyPressMsg{Code: tea.KeyHome}
	case "end":
		return tea.KeyPressMsg{Code: tea.KeyEnd}
	}
	return tea.KeyPressMsg{Code: rune(s[0]), Text: s}
}

func TestTimeSeries(t *testing.T) {
	eachTheme(t, []int{40, 64, 96}, func(t *testing.T, th graph.Theme, w int) string {
		out := chart(th, w, 12, hourly()).View()
		requireWidth(t, out, w)
		return out
	})
}

func TestTimeSeriesDaily(t *testing.T) {
	for _, days := range []int{7, 30} {
		t.Run(fmt.Sprintf("days%02d", days), func(t *testing.T) {
			eachTheme(t, []int{48, 80}, func(t *testing.T, th graph.Theme, w int) string {
				m := chart(th, w, 10, daily(days))
				m.SetCursor(days / 2)
				out := m.View()
				requireWidth(t, out, w)
				return out
			})
		})
	}
}

func TestTimeSeriesNoAxes(t *testing.T) {
	eachTheme(t, []int{12, 24}, func(t *testing.T, th graph.Theme, w int) string {
		out := chart(th, w, 3, hourly(), graph.WithoutAxes()).View()
		requireWidth(t, out, w)
		return out
	})
}

func TestTimeSeriesCrosshair(t *testing.T) {
	eachTheme(t, []int{48, 72}, func(t *testing.T, th graph.Theme, w int) string {
		m := chart(th, w, 12, hourly())
		m.Focus()
		steps := []struct {
			key  string
			want int
		}{
			{"left", 23}, // a hidden crosshair appears on the latest
			{"home", 0},
			{"left", 0}, // clamped
			{"l", 1},
			{"right", 2},
			{"end", 23},
			{"right", 23}, // clamped
			{"h", 22},
			{"left", 21},
			{"left", 20},
		}
		for _, s := range steps {
			m, _ = m.Update(key(s.key))
			if got := m.Cursor(); got != s.want {
				t.Fatalf("after %q cursor = %d, want %d", s.key, got, s.want)
			}
		}
		at, ok := m.CursorTime()
		if !ok || !at.Equal(now.Add(-3*time.Hour)) {
			t.Fatalf("cursor time = %v, want %v", at, now.Add(-3*time.Hour))
		}
		out := m.View()
		requireWidth(t, out, w)
		return out
	})
}

func TestTimeSeriesBlurredIgnoresKeys(t *testing.T) {
	m := chart(themes()[0].theme, 48, 12, hourly())
	m, _ = m.Update(key("left"))
	if m.Cursor() != -1 {
		t.Fatalf("blurred chart moved its cursor to %d", m.Cursor())
	}
}

// TestTimeSeriesTooltip pins the tooltip inside the chart at either edge
// and on a chart too short for every line.
func TestTimeSeriesTooltip(t *testing.T) {
	th := themes()[0].theme
	cases := []struct {
		name   string
		w, h   int
		cursor int
	}{
		{"first", 48, 12, 0},
		{"last", 48, 12, 23},
		{"middle", 48, 12, 12},
		{"short", 48, 7, 23},
		{"narrow", 20, 12, 10},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := chart(th, c.w, c.h, hourly())
			m.SetCursor(c.cursor)
			out := m.View()
			requireWidth(t, out, c.w)
			lines := strings.Split(ansi.Strip(out), "\n")
			if len(lines) != c.h {
				t.Fatalf("got %d lines, want %d", len(lines), c.h)
			}
			if !strings.Contains(lines[0], "╭") {
				t.Fatalf("no tooltip on the top line: %q", lines[0])
			}
			top := []rune(lines[0])
			l, r := strings.IndexRune(lines[0], '╭'), strings.IndexRune(lines[0], '╮')
			if l < 0 || r < 0 || top[len(top)-1] == '─' {
				t.Fatalf("tooltip runs off the chart: %q", lines[0])
			}
			golden.RequireEqual(t, out)
		})
	}
}

func TestTimeSeriesMouse(t *testing.T) {
	m := chart(themes()[0].theme, 48, 12, hourly())
	m.SetOffset(10, 4)
	// Before the plot: the label gutter.
	m, _ = m.Update(tea.MouseMotionMsg(tea.Mouse{X: 11, Y: 6}))
	if m.Cursor() != -1 {
		t.Fatalf("hover on the gutter moved the cursor to %d", m.Cursor())
	}
	m, _ = m.Update(tea.MouseMotionMsg(tea.Mouse{X: 10 + 47, Y: 6}))
	if m.Cursor() != 23 {
		t.Fatalf("hover on the right edge: cursor = %d, want 23", m.Cursor())
	}
	m, _ = m.Update(tea.MouseClickMsg(tea.Mouse{X: 10 + 4, Y: 5, Button: tea.MouseLeft}))
	if m.Cursor() != 0 {
		t.Fatalf("click on the left edge: cursor = %d, want 0", m.Cursor())
	}
	if m.HoverAt(0, 100) {
		t.Fatal("hover below the chart was taken")
	}
}

func TestTimeSeriesEmpty(t *testing.T) {
	m := chart(themes()[0].theme, 30, 6, nil)
	m.Step(1)
	if m.Cursor() != -1 {
		t.Fatalf("empty chart has cursor %d", m.Cursor())
	}
	requireWidth(t, m.View(), 30)
}

func TestSpark(t *testing.T) {
	values := []float64{0, 120, 340, 90, 0, 800, 1500, 1200, 400, 650, 980, 300, 50, 700, 1100, 9800}
	eachTheme(t, []int{8, 16, 24}, func(t *testing.T, th graph.Theme, w int) string {
		plain := graph.Spark(values, w, th, th.Series["claude"])
		label := graph.Spark(values, w, th, th.Series["codex"], graph.WithLastValue())
		if got := ansi.StringWidth(label); got > w {
			t.Fatalf("spark is %d cells wide, want at most %d", got, w)
		}
		return plain + "\n" + label
	})
}

func TestHeatmap(t *testing.T) {
	values := make([]float64, 182)
	for i := range values {
		if i%5 != 0 {
			values[i] = float64((i * 37) % 23 * 1000)
		}
	}
	eachTheme(t, []int{24, 60, 120}, func(t *testing.T, th graph.Theme, w int) string {
		out := graph.Heatmap(th, w, now, values)
		for _, l := range strings.Split(out, "\n") {
			if ansi.StringWidth(l) > w {
				t.Fatalf("heatmap line wider than %d: %q", w, ansi.Strip(l))
			}
		}
		return out
	})
}

func TestBars(t *testing.T) {
	bars := []graph.Bar{
		{Label: "claude", Value: 182_400},
		{Label: "codex", Value: 96_100},
		{Label: "opencode", Value: 12_800},
		{Label: "antigravity", Value: 640},
		{Label: "idle", Value: 0},
	}
	eachTheme(t, []int{24, 40, 72}, func(t *testing.T, th graph.Theme, w int) string {
		out := graph.Bars(th, w, bars)
		requireWidth(t, out, w)
		return out
	})
}

func TestMeter(t *testing.T) {
	cases := []struct {
		used     float64
		window   int
		resetsIn time.Duration
	}{
		{80, 300, 3 * time.Hour},                  // ahead of pace
		{38, 300, 3 * time.Hour},                  // on pace
		{10, 10080, 2*time.Hour + 13*time.Minute}, // behind
		{55, 0, 0}, // no window known
	}
	eachTheme(t, []int{20, 40, 64}, func(t *testing.T, th graph.Theme, w int) string {
		var lines []string
		for _, c := range cases {
			var at time.Time
			if c.resetsIn > 0 {
				at = now.Add(c.resetsIn)
			}
			out := graph.Meter(th, w, c.used, c.window, at, now)
			requireWidth(t, out, w)
			lines = append(lines, out)
		}
		return strings.Join(lines, "\n")
	})
}

func TestShare(t *testing.T) {
	parts := []graph.Part{
		{Label: "input", Value: 48_000, Color: tone.Sky},
		{Label: "output", Value: 12_500, Color: tone.Mint},
		{Label: "cache read", Value: 210_000, Color: tone.Plum},
		{Label: "cache write", Value: 900, Color: tone.Amber},
	}
	eachTheme(t, []int{24, 48, 96}, func(t *testing.T, th graph.Theme, w int) string {
		out := graph.Share(th, w, parts)
		for _, l := range strings.Split(out, "\n") {
			if ansi.StringWidth(l) > w {
				t.Fatalf("share line wider than %d: %q", w, ansi.Strip(l))
			}
		}
		return out
	})
}

func TestRing(t *testing.T) {
	th := themes()[0].theme
	for pct, want := range map[float64]string{0: "○", 10: "○", 25: "◔", 50: "◑", 70: "◕", 95: "●", 140: "●", -3: "○"} {
		if got := ansi.Strip(graph.Ring(th, pct)); got != want {
			t.Errorf("Ring(%v) = %q, want %q", pct, got, want)
		}
	}
}

func TestWaterfall(t *testing.T) {
	spans := []graph.Span{
		{Kind: "wait", Name: "first token", StartMs: 0, DurMs: 820},
		{Kind: "think", Name: "thinking", StartMs: 820, DurMs: 2400, Tokens: 1_840},
		{Kind: "tool", Name: "Read main.go", StartMs: 3220, DurMs: 140},
		{Kind: "tool", Name: "Bash go test ./...", StartMs: 3400, DurMs: 6100},
		{Kind: "text", Name: "answer", StartMs: 9500, DurMs: 2300, Tokens: 12_400},
	}
	eachTheme(t, []int{40, 64, 96}, func(t *testing.T, th graph.Theme, w int) string {
		out := graph.Waterfall(th, w, spans)
		requireWidth(t, out, w)
		return out
	})
}

func TestCompact(t *testing.T) {
	for v, want := range map[float64]string{
		0: "0", 7: "7", 2.5: "2.5", 980: "980", 999.6: "1k", 9800: "9.8k",
		10_000: "10k", 12_345: "12k", 999_960: "1m", 1_200_000: "1.2m", -1500: "-1.5k",
	} {
		if got := graph.Compact(v); got != want {
			t.Errorf("Compact(%v) = %q, want %q", v, got, want)
		}
	}
}

func TestNiceCeil(t *testing.T) {
	for v, want := range map[float64]float64{0: 1, 0.3: 0.5, 1: 1, 3000: 5000, 7300: 10_000, 12_000: 20_000, 50_000: 50_000} {
		if got := graph.NiceCeil(v); got != want {
			t.Errorf("NiceCeil(%v) = %v, want %v", v, got, want)
		}
	}
}

func TestCountdown(t *testing.T) {
	for d, want := range map[time.Duration]string{
		20 * time.Second:              "<1m",
		13 * time.Minute:              "13m",
		2*time.Hour + 13*time.Minute:  "2h 13m",
		2*time.Hour + 5*time.Minute:   "2h 05m",
		50*time.Hour + 20*time.Minute: "2d 2h",
	} {
		if got := graph.Countdown(d); got != want {
			t.Errorf("Countdown(%v) = %q, want %q", d, got, want)
		}
	}
}

func TestHeatScale(t *testing.T) {
	th := themes()[0].theme
	if len(th.Heat) != graph.HeatLevels {
		t.Fatalf("heat has %d steps, want %d", len(th.Heat), graph.HeatLevels)
	}
	if tone.Hex(th.Heat[len(th.Heat)-1]) != tone.Hex(th.Accent) {
		t.Errorf("heat ends on %s, want the accent %s", tone.Hex(th.Heat[len(th.Heat)-1]), tone.Hex(th.Accent))
	}
	if tone.Hex(th.Heat[0]) == tone.Hex(th.Bg) {
		t.Error("an empty day is the colour of the ground")
	}
}

func TestStack(t *testing.T) {
	th := themes()[0].theme
	parts := []graph.Part{{Label: "base", Value: 20, Color: th.Subtle}, {Label: "chat", Value: 30, Color: tone.Sky}, {Label: "deck", Value: 1, Color: th.Accent}}
	for _, w := range []int{1, 10, 40} {
		out := graph.Stack(th, w, parts, 100)
		if got := ansi.StringWidth(out); got != w {
			t.Fatalf("width %d: drew %d cells", w, got)
		}
		if strings.Count(ansi.Strip(out), "━") != w {
			t.Fatalf("width %d: %q", w, ansi.Strip(out))
		}
	}
	if graph.Stack(th, 0, parts, 100) != "" {
		t.Fatal("no width, no bar")
	}
	if got := ansi.StringWidth(graph.Stack(th, 8, nil, 0)); got != 8 {
		t.Fatalf("an empty whole still draws its track: %d", got)
	}
}
