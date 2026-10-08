// Command deckyolo plays Deck's yolo effect on its own, over a sample screen.
// Any key replays it; q or ctrl+c quits.
package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/dev-dami/deck/internal/tui/yolo"
)

type frame time.Time

func tick() tea.Cmd {
	return tea.Tick(time.Second/60, func(t time.Time) tea.Msg { return frame(t) })
}

type model struct {
	w, h int
	anim *yolo.Anim
	now  time.Time
}

func (m model) Init() tea.Cmd { return tick() }

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
	case tea.KeyMsg:
		if s := msg.String(); s == "q" || s == "ctrl+c" || s == "esc" {
			return m, tea.Quit
		}
		m.now = time.Now()
		m.anim.Start(m.now)
		return m, tick()
	case frame:
		m.now = time.Time(msg)
		if m.anim.Advance(m.now) {
			return m, tick()
		}
	}
	return m, nil
}

func (m model) View() string {
	if m.w == 0 {
		return ""
	}
	mint := lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#0E8A6A", Dark: "#5FD7AF"})
	dim := lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#8A9491", Dark: "#5C6664"})
	sample := []string{
		mint.Render("◆ deck") + dim.Render("  claude · codex   ▲ yolo"),
		"",
		mint.Render("dami") + "  make the tests pass and tidy the store package",
		"",
		mint.Render("claude") + "  On it. Running the suite first to see what fails.",
		dim.Render("        ✓ Bash  go test ./internal/store/..."),
		dim.Render("        ✓ Edit  internal/store/store.go  +12 −4"),
		"",
		mint.Render("codex") + "   @claude I'll take the session package in parallel.",
		dim.Render("        ✓ auto-approved  go vet ./internal/session"),
		"",
	}
	rows := make([]string, m.h)
	for i := range rows {
		if i < len(sample) {
			rows[i] = sample[i]
		}
	}
	rows[m.h-1] = dim.Render("any key: play again · q: quit")
	return m.anim.Apply(strings.Join(rows, "\n"), m.w, m.now)
}

func main() {
	a := yolo.New(lipgloss.HasDarkBackground())
	now := time.Now()
	a.Start(now)
	if _, err := tea.NewProgram(model{anim: a, now: now}, tea.WithAltScreen()).Run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
