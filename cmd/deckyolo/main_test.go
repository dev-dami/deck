package main

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/dev-dami/deck/internal/tui/motion"
)

func key(s string) tea.KeyMsg {
	if s == " " {
		return tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

func TestPreviewPlaysReplaysSkipsQuits(t *testing.T) {
	prev := motion.Reduced()
	motion.SetReduced(false)
	defer motion.SetReduced(prev)

	m := newModel()
	if m.Init() != nil {
		t.Fatal("Init should wait for the window size")
	}
	if _, cmd := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30}); cmd == nil {
		t.Fatal("the first window size should start the animation")
	}
	if _, cmd := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30}); cmd != nil {
		t.Fatal("a resize should not replay")
	}

	// The strip is centred and the view fits the window.
	m.now = m.now.Add(150 * time.Millisecond)
	v := m.View()
	if lines := strings.Split(v, "\n"); len(lines) > 30 {
		t.Fatalf("view is %d lines in a 30-line window", len(lines))
	}
	for i, l := range strings.Split(v, "\n") {
		if w := lipgloss.Width(l); w > 100 {
			t.Fatalf("line %d is %d cells wide in a 100-cell window", i, w)
		}
	}
	if m.yolo {
		t.Fatal("chip should read ask while playing")
	}

	// Drive frames until the clock stops; the chip ends on yolo.
	now := time.Now()
	for i := 0; i < 1000; i++ {
		now = now.Add(16 * time.Millisecond)
		m.clock.Wake()
		_, next := m.Update(motion.FrameMsg{ID: i, At: now})
		if next == nil && !m.clock.Active() {
			break
		}
	}
	if !m.yolo {
		t.Fatal("chip should flip to yolo when the animation ends")
	}

	// space replays; another key skips straight to the end.
	if _, c := m.Update(key(" ")); c == nil {
		t.Fatal("space should restart the clock")
	}
	if m.yolo {
		t.Fatal("replay should reset the chip to ask")
	}
	m.Update(key("x"))
	if !m.yolo {
		t.Fatal("a key should skip and flip the chip")
	}

	_, c := m.Update(key("q"))
	if c == nil {
		t.Fatal("q should quit")
	}
	if _, ok := c().(tea.QuitMsg); !ok {
		t.Fatal("q did not produce QuitMsg")
	}
}

func TestPreviewReducedMotionFlipsAtOnce(t *testing.T) {
	prev := motion.Reduced()
	motion.SetReduced(true)
	defer motion.SetReduced(prev)

	m := newModel()
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	if !m.yolo {
		t.Fatal("reduced motion: chip should flip immediately")
	}
	if !strings.Contains(m.View(), "yolo on") {
		t.Fatal("reduced motion: expected the yolo on line")
	}
}

func TestPreviewNarrowWindow(t *testing.T) {
	prev := motion.Reduced()
	motion.SetReduced(false)
	defer motion.SetReduced(prev)

	m := newModel()
	m.Update(tea.WindowSizeMsg{Width: 30, Height: 12})
	m.now = m.now.Add(500 * time.Millisecond)
	for i, l := range strings.Split(m.View(), "\n") {
		if w := lipgloss.Width(l); w > 30 {
			t.Fatalf("line %d is %d cells wide in a 30-cell window", i, w)
		}
	}
}
