package model

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/dev-dami/deck/internal/ui/tone"
	"github.com/dev-dami/deck/internal/workspace"
)

// The working line: while any agent in the session is at work, the status
// bar says who, for how long, with a spinner in each agent's own colour.

// workFrames are the spinner's frames; workFrame how long each shows.
var workFrames = []string{"◐", "◓", "◑", "◒"}

const workFrame = 120 * time.Millisecond

// workTickMsg asks for the working line's next frame.
type workTickMsg time.Time

func workTick() tea.Cmd {
	return tea.Tick(workFrame, func(t time.Time) tea.Msg { return workTickMsg(t) })
}

// busyAgents is the session's agents that are at work now.
func (m *UI) busyAgents() []workspace.AgentLine {
	roster, ok := m.roster()
	if !ok || m.session == nil {
		return nil
	}
	var out []workspace.AgentLine
	for _, a := range roster.Roster(m.session.ID) {
		switch a.Status {
		case "working", "tool", "approval":
			out = append(out, a)
		}
	}
	return out
}

// workingLine says which agents are at work, "" when none is.
func (m *UI) workingLine(width int) string {
	busy := m.busyAgents()
	if len(busy) == 0 {
		m.workSeen = nil
		return ""
	}
	now := time.Now()
	if m.workSeen == nil {
		m.workSeen = map[string]time.Time{}
	}
	frame := workFrames[int(now.UnixMilli()/workFrame.Milliseconds())%len(workFrames)]
	muted := m.com.Styles.ModelInfo.Provider
	var parts []string
	for _, a := range busy {
		since := a.Since
		if since.IsZero() {
			if m.workSeen[a.ID].IsZero() {
				m.workSeen[a.ID] = now
			}
			since = m.workSeen[a.ID]
		}
		tint := lipgloss.NewStyle().Foreground(tone.Tint(a.Kind))
		what := "working"
		if a.Status == "approval" {
			what = "needs you"
		}
		part := tint.Render(frame+" "+tone.Mark(a.Kind)) + " " + tint.Bold(true).Render(a.ID) + " " + muted.Render(what)
		if d := now.Sub(since).Truncate(time.Second); d >= time.Second {
			part += " " + muted.Render(fmtElapsed(d))
		}
		parts = append(parts, part)
	}
	line := strings.Join(parts, muted.Render(" · ")) + muted.Render("   esc esc stops")
	return ansi.Truncate(line, max(0, width), "…")
}

// fmtElapsed is a short duration: 42s, 3m05s, 1h02m.
func fmtElapsed(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
	}
	return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
}
