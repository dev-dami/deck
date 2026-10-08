package main

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/dev-dami/deck/internal/tui/motion"
	"github.com/dev-dami/deck/internal/tui/theme"
)

func TestPreviewSettlesAndKeys(t *testing.T) {
	prev := motion.Reduced()
	motion.SetReduced(false)
	defer motion.SetReduced(prev)

	m := newModel()
	if m.Init() != nil {
		t.Fatal("Init should wait for the window size")
	}
	if _, cmd := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30}); cmd == nil {
		t.Fatal("the first window size should start the intro")
	}
	if _, cmd := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30}); cmd != nil {
		t.Fatal("a resize should not replay the intro")
	}
	v := m.View()
	if !strings.Contains(v, theme.Tagline) {
		t.Fatalf("tagline missing:\n%s", v)
	}
	if h := strings.Count(v, "\n") + 1; h > 30 {
		t.Fatalf("view is %d lines in a 30-line window", h)
	}

	// Drive frames until the clock stops.
	now := time.Now()
	for i := 0; i < 1000; i++ {
		now = now.Add(33 * time.Millisecond)
		_, next := m.Update(motion.FrameMsg{ID: i, At: now})
		if next == nil && !m.clock.Active() {
			break
		}
	}

	// r replays, other keys calm, q quits.
	if _, c := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}}); c == nil {
		t.Fatal("r should restart the clock")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	_, c := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	if c == nil {
		t.Fatal("q should quit")
	}
	if _, ok := c().(tea.QuitMsg); !ok {
		t.Fatal("q did not produce QuitMsg")
	}

	// Mouse over the logo wakes an idle clock.
	m.clock.Stop()
	bw, bh := m.logo.Bounds()
	_, c = m.Update(tea.MouseMsg{X: m.ox + bw/2, Y: m.oy + bh/2, Action: tea.MouseActionMotion})
	if c == nil {
		t.Fatal("mouse motion over the logo should wake the clock")
	}
}
