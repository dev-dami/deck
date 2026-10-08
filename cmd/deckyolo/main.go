// Command deckyolo previews the YOLO-switch animation on its own.
//
//	go run ./cmd/deckyolo
//
// The strip plays just above a mock composer, which rises back down as the
// strip collapses, and the mode chip flips to yolo at the end. space replays,
// any other key skips, q or esc quits. DECK_REDUCED_MOTION=1 flips the chip
// at once.
package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/dev-dami/deck/internal/tui/motion"
	"github.com/dev-dami/deck/internal/tui/theme"
	"github.com/dev-dami/deck/internal/tui/yolo"
)

const (
	fps        = 60
	stripWidth = 60
)

type model struct {
	anim  *yolo.Anim
	clock *motion.Clock
	dark  bool

	w, h int
	now  time.Time
	yolo bool // the mode chip: false = ask, true = yolo
}

func newModel() *model {
	dark := lipgloss.HasDarkBackground()
	return &model{
		anim:  yolo.New(dark),
		clock: motion.NewClock(fps),
		dark:  dark,
		now:   time.Now(),
	}
}

func (m *model) stripW() int { return max(min(stripWidth, m.w), 1) }

func (m *model) start(now time.Time) tea.Cmd {
	m.now = now
	m.yolo = false
	m.anim.SetWidth(m.stripW())
	m.anim.Start(now)
	if m.anim.Done(now) { // reduced motion: just flip the chip
		m.yolo = true
		return nil
	}
	return m.clock.Wake()
}

// Init waits for the first WindowSizeMsg, which gives the strip its width.
func (m *model) Init() tea.Cmd { return nil }

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		first := m.w == 0 && m.h == 0
		m.w, m.h = msg.Width, msg.Height
		m.anim.SetWidth(m.stripW())
		if first {
			return m, m.start(time.Now())
		}
		return m, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "q", "esc", "ctrl+c":
			return m, tea.Quit
		case " ":
			return m, m.start(time.Now())
		}
		// Any other key skips to the end.
		now := time.Now()
		if !m.anim.Done(now) {
			m.anim.Skip()
			m.now = now
			m.yolo = true
		}
		return m, nil

	case motion.FrameMsg:
		if !m.clock.Accept(msg) {
			return m, nil
		}
		m.now = msg.At
		moving := m.anim.Advance(msg.At)
		if !moving {
			m.yolo = true
		}
		m.clock.SetMoving(moving)
		return m, m.clock.Tick()
	}
	return m, nil
}

func (m *model) View() string {
	if m.w == 0 || m.h == 0 {
		return ""
	}
	w := m.stripW()
	center := func(s string) string { return lipgloss.PlaceHorizontal(m.w, lipgloss.Center, s) }

	// The composer sits at a fixed row; the strip fills the rows above it.
	const reserve = yolo.FullHeight
	composerRow := max(m.h/2, reserve)

	var strip []string
	if v := m.anim.View(w, m.now); v != "" {
		for _, l := range strings.Split(v, "\n") {
			strip = append(strip, center(l))
		}
	}

	lines := make([]string, 0, m.h)
	for len(lines) < composerRow-len(strip) {
		lines = append(lines, "")
	}
	lines = append(lines, strip...)
	lines = append(lines, center(m.composer(w)))
	if m.yolo && m.anim.Height(m.now) == 0 {
		lines = append(lines, center(theme.Ghost.Render("yolo on")))
	}

	hint := theme.Ghost.Render("space: replay   any key: skip   q: quit")
	for len(lines) < m.h-1 {
		lines = append(lines, "")
	}
	lines = append(lines[:min(len(lines), m.h-1)], center(hint))
	clip := lipgloss.NewStyle().MaxWidth(m.w)
	for i, l := range lines {
		lines[i] = clip.Render(l)
	}
	return strings.Join(lines, "\n")
}

// composer is a one-line mock of the composer with the mode chip at the right.
func (m *model) composer(w int) string {
	chip := theme.Dim.Render("ask")
	if m.yolo {
		chip = lipgloss.NewStyle().Foreground(theme.Danger).Bold(true).Render("yolo")
	}
	prompt := theme.Accented.Render("›") + " " + theme.Ghost.Render("message")
	gap := max(w-lipgloss.Width(prompt)-lipgloss.Width(chip)-2, 1)
	return " " + prompt + strings.Repeat(" ", gap) + chip + " "
}

func main() {
	p := tea.NewProgram(newModel(), tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "deckyolo:", err)
		os.Exit(1)
	}
}
