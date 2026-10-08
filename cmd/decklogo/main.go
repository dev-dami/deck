// Command decklogo previews the animated Deck logo on its own.
//
//	go run ./cmd/decklogo
//
// Move the mouse over the logo for ripples, click for a splash, press r to
// replay the intro and q or esc to quit. DECK_REDUCED_MOTION=1 shows the
// static logo.
package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/dev-dami/deck/internal/tui/logo"
	"github.com/dev-dami/deck/internal/tui/motion"
	"github.com/dev-dami/deck/internal/tui/theme"
)

const (
	fps        = 30
	tagFadeLag = 700 * time.Millisecond
	tagFade    = 600 * time.Millisecond
)

type model struct {
	logo  *logo.Model
	clock *motion.Clock
	tag   motion.Fade
	dark  bool

	w, h   int
	ox, oy int // logo top-left in screen cells
	now    time.Time
}

func newModel() *model {
	dark := lipgloss.HasDarkBackground()
	return &model{
		logo:  logo.New(dark),
		clock: motion.NewClock(fps),
		tag:   motion.NewFade(tagFade, motion.EaseOutCubic),
		dark:  dark,
		now:   time.Now(),
	}
}

func (m *model) start(now time.Time) tea.Cmd {
	m.now = now
	m.logo.Start(now)
	m.tag.Reset()
	m.tag.Start(now.Add(tagFadeLag))
	return m.clock.Wake()
}

func (m *model) Init() tea.Cmd { return m.start(time.Now()) }

func (m *model) layout() {
	// Reserve a blank line and the tagline under the logo, and the hint row.
	m.logo.SetSize(m.w, max(m.h-4, 0))
	bw, bh := m.logo.Bounds()
	m.ox = (m.w - bw) / 2
	m.oy = (m.h - (bh + 2)) / 2
	if m.oy < 0 {
		m.oy = 0
	}
}

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		m.layout()
		return m, nil

	case tea.KeyMsg:
		now := time.Now()
		switch msg.String() {
		case "q", "esc", "ctrl+c":
			return m, tea.Quit
		case "r":
			return m, m.start(now)
		}
		// Any other key just calms the logo; the composer owns the keyboard.
		m.logo.Calm(now)
		return m, nil

	case tea.MouseMsg:
		now := time.Now()
		x, y := msg.X-m.ox, msg.Y-m.oy
		switch {
		case msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft:
			m.logo.Mouse(x, y, true, now)
		case msg.Action == tea.MouseActionMotion:
			m.logo.Mouse(x, y, false, now)
		default:
			return m, nil
		}
		return m, m.clock.Wake()

	case motion.FrameMsg:
		if !m.clock.Accept(msg) {
			return m, nil
		}
		m.now = msg.At
		moving := m.logo.Advance(msg.At)
		moving = moving || m.tag.Moving(msg.At) || !m.tag.Done(msg.At)
		m.clock.SetMoving(moving)
		return m, m.clock.Tick()
	}
	return m, nil
}

func (m *model) View() string {
	if m.w == 0 || m.h == 0 {
		return ""
	}
	lines := make([]string, 0, m.h)
	for i := 0; i < m.oy; i++ {
		lines = append(lines, "")
	}
	pad := strings.Repeat(" ", max(m.ox, 0))
	if v := m.logo.View(); v != "" {
		for _, l := range strings.Split(v, "\n") {
			lines = append(lines, pad+l)
		}
	}
	tagStyle := lipgloss.NewStyle().Foreground(
		motion.Blend(theme.Faint, theme.Muted, m.tag.Progress(m.now), m.dark))
	lines = append(lines, "", lipgloss.PlaceHorizontal(m.w, lipgloss.Center, tagStyle.Render(theme.Tagline)))

	hint := theme.Ghost.Render("move: ripple   click: splash   r: replay   q: quit")
	for len(lines) < m.h-1 {
		lines = append(lines, "")
	}
	lines = append(lines, lipgloss.PlaceHorizontal(m.w, lipgloss.Center, hint))
	return strings.Join(lines, "\n")
}

func main() {
	p := tea.NewProgram(newModel(), tea.WithAltScreen(), tea.WithMouseCellMotion())
	if _, err := p.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "decklogo:", err)
		os.Exit(1)
	}
}
