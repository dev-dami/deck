package model

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/dev-dami/deck/internal/ui/tone"
	"github.com/dev-dami/deck/internal/workspace"
)

// modelInfo renders the session's agents: each one's mark and name in its
// own tint, the model and effort it runs, and what it is doing, how full its
// context is and what it has cost. Before anyone joins it lists the agent
// CLIs that are installed.
func (m *UI) modelInfo(width int) string {
	if _, ok := m.roster(); !ok {
		return ""
	}
	return m.modelInfoFor(m.sessionRoster(), width)
}

// modelInfoFor is modelInfo for the given roster lines.
func (m *UI) modelInfoFor(agents []workspace.AgentLine, width int) string {
	t := m.com.Styles
	muted := t.ModelInfo.Provider
	var lines []string
	for _, a := range agents {
		tint := lipgloss.NewStyle().Foreground(tone.Tint(a.Kind))
		head := tint.Render(tone.Mark(a.Kind)) + " " + tint.Bold(true).Render(a.ID)
		model := a.Model
		if model == "" {
			model = "default model"
		}
		if a.Effort != "" {
			model += " · " + a.Effort
		}
		head += " " + muted.Render(model)
		lines = append(lines, ansi.Truncate(head, width, "…"))

		var facts []string
		switch a.Status {
		case "working", "tool":
			facts = append(facts, t.ModelInfo.Name.Render("working"))
		case "approval":
			facts = append(facts, t.ModelInfo.Reasoning.Render(fmt.Sprintf("needs you (%d)", max(1, a.Pending))))
		case "error", "exited":
			facts = append(facts, muted.Render(a.Status))
		case "ready":
			facts = append(facts, muted.Render("ready"))
		default:
			facts = append(facts, muted.Render("idle"))
		}
		if a.ContextWindow > 0 && a.ContextTokens > 0 {
			facts = append(facts, muted.Render(fmt.Sprintf("%d%% context", a.ContextTokens*100/a.ContextWindow)))
		}
		if a.CostUSD > 0 {
			facts = append(facts, muted.Render(fmt.Sprintf("$%.2f", a.CostUSD)))
		}
		lines = append(lines, ansi.Truncate("  "+strings.Join(facts, muted.Render(" · ")), width, "…"))
	}
	return lipgloss.NewStyle().Width(width).Render(lipgloss.JoinVertical(lipgloss.Left, lines...))
}

// fileChangeCount returns the number of session files with non-zero additions
// or deletions.
func fileChangeCount(files []SessionFile) int {
	count := 0
	for _, f := range files {
		if f.Additions == 0 && f.Deletions == 0 {
			continue
		}
		count++
	}
	return count
}
