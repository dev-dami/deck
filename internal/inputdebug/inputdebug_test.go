package inputdebug

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// fakeClock returns a clock that advances by step on every call.
func fakeClock(start time.Time, step time.Duration) func() time.Time {
	now := start
	return func() time.Time {
		t := now
		now = now.Add(step)
		return t
	}
}

func noEnv(string) string { return "" }

func send(m model, msgs ...tea.Msg) (model, tea.Cmd) {
	var cmd tea.Cmd
	for _, msg := range msgs {
		var next tea.Model
		next, cmd = m.Update(msg)
		m = next.(model)
	}
	return m, cmd
}

func TestFormatsSyntheticMessages(t *testing.T) {
	m := newModel(fakeClock(time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC), 5*time.Millisecond), noEnv)

	m, _ = send(m,
		tea.WindowSizeMsg{Width: 100, Height: 30},
		tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")},
		tea.KeyMsg{Type: tea.KeyShiftTab},
		tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("line1\nline2"), Paste: true},
		tea.MouseMsg{X: 3, Y: 4, Action: tea.MouseActionPress, Button: tea.MouseButtonWheelDown, Shift: true},
		tea.FocusMsg{},
		tea.BlurMsg{},
	)

	if len(m.entries) != 7 {
		t.Fatalf("entries = %d, want 7", len(m.entries))
	}
	if m.entries[0].delta != 0 || m.entries[1].delta != 5*time.Millisecond {
		t.Fatalf("deltas = %v, %v", m.entries[0].delta, m.entries[1].delta)
	}

	view := m.View()
	for _, want := range []string{
		"window",
		"100x30",
		"shift+tab",
		`string="a"`,
		"paste=true alt=false",
		`string="[line1\nline2]"`,
		"x=3 y=4 action=press button=wheel-down shift=true",
		"focus",
		"blur",
		"+5ms",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("view missing %q\n%s", want, view)
		}
	}
	if !strings.Contains(view, footerText) {
		t.Errorf("view missing footer\n%s", view)
	}
}

func TestViewFillsHeightAndFitsWidth(t *testing.T) {
	m := newModel(fakeClock(time.Now(), time.Millisecond), noEnv)
	m, _ = send(m, tea.WindowSizeMsg{Width: 40, Height: 12})
	for i := 0; i < 50; i++ {
		m, _ = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	}

	lines := strings.Split(m.View(), "\n")
	if len(lines) != 12 {
		t.Fatalf("lines = %d, want 12", len(lines))
	}
	for i, l := range lines {
		if w := ansi.StringWidth(l); w > 40 {
			t.Fatalf("line %d width %d > 40: %q", i, w, l)
		}
	}
	if !strings.HasPrefix(lines[len(lines)-1], "click, scroll") {
		t.Fatalf("footer not on last row: %q", lines[len(lines)-1])
	}
}

func TestKeepsLastThirtyEntries(t *testing.T) {
	m := newModel(fakeClock(time.Now(), time.Millisecond), noEnv)
	for i := 0; i < 35; i++ {
		m, _ = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{rune('a' + i%26)}})
	}
	if len(m.entries) != maxEntries {
		t.Fatalf("entries = %d, want %d", len(m.entries), maxEntries)
	}
	// The newest message is the last one sent: index 34 % 26 = 8, which is 'i'.
	if got := m.entries[len(m.entries)-1].text; !strings.Contains(got, `string="i"`) {
		t.Fatalf("newest entry = %q", got)
	}
}

func TestDoubleQQuits(t *testing.T) {
	m := newModel(fakeClock(time.Now(), time.Millisecond), noEnv)
	q := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")}

	m, cmd := send(m, q)
	if cmd != nil {
		t.Fatal("first q must not quit")
	}
	if !m.armed {
		t.Fatal("first q should arm quit")
	}
	if !strings.Contains(m.View(), "press q again") {
		t.Fatal("armed hint missing from view")
	}

	_, cmd = send(m, q)
	if cmd == nil {
		t.Fatal("second q must quit")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatalf("command = %T, want QuitMsg", cmd())
	}
}

func TestOtherKeyDisarmsQuit(t *testing.T) {
	m := newModel(fakeClock(time.Now(), time.Millisecond), noEnv)
	q := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")}
	a := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")}

	m, cmd := send(m, q, a, q)
	if cmd != nil {
		t.Fatal("q, a, q must not quit")
	}
}

func TestPastedQDoesNotQuit(t *testing.T) {
	m := newModel(fakeClock(time.Now(), time.Millisecond), noEnv)
	pq := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q"), Paste: true}

	m, cmd := send(m, pq, pq)
	if cmd != nil {
		t.Fatal("pasted q must not quit")
	}
}

func TestCtrlCQuitsAtOnce(t *testing.T) {
	m := newModel(fakeClock(time.Now(), time.Millisecond), noEnv)
	_, cmd := send(m, tea.KeyMsg{Type: tea.KeyCtrlC})
	if cmd == nil {
		t.Fatal("ctrl+c must quit")
	}
}

func TestHeaderHints(t *testing.T) {
	env := map[string]string{
		"TERM":                   "xterm-256color",
		"TERM_PROGRAM":           "vscode",
		"WT_SESSION":             "abc",
		"CLAUDE_CODE_ENTRYPOINT": "cli",
	}
	lines := headerLines(func(k string) string { return env[k] }, "windows")
	all := strings.Join(lines, "\n")
	for _, want := range []string{
		"os=windows",
		"TERM=xterm-256color",
		"TERM_PROGRAM=vscode",
		"WT_SESSION=set",
		"VS Code terminal",
		"Windows Terminal",
		"Claude Code",
	} {
		if !strings.Contains(all, want) {
			t.Errorf("header missing %q:\n%s", want, all)
		}
	}

	none := strings.Join(headerLines(noEnv, "linux"), "\n")
	if !strings.Contains(none, "host hints: none") || !strings.Contains(none, "TERM=-") {
		t.Errorf("empty env header:\n%s", none)
	}
}
