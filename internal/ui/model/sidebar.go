package model

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/dev-dami/deck/internal/ui/graph"
	"github.com/dev-dami/deck/internal/ui/tone"
	"github.com/dev-dami/deck/internal/workspace"
)

// modelInfo renders the session's agents: each one's mark and name in its
// own tint (and the role of a named agent: "✳ charles · reviewer"), the
// model and effort it runs, and what it is doing, how full its context is
// and what it has cost. Before anyone joins it lists the agent
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
		// Subagents sit under the member that started them, one step in per
		// level.
		indent := strings.Repeat("  ", min(a.Depth, 6))
		tint := lipgloss.NewStyle().Foreground(tone.Tint(a.Kind))
		// "✳ charles · reviewer": a named agent keeps its kind's glyph
		// and tint, and its role follows the name.
		head := indent + tint.Render(tone.Mark(a.Kind)) + " " + tint.Bold(true).Render(a.ID)
		if role := strings.Join(strings.Fields(a.Role), " "); role != "" {
			head += tint.Render(" · " + role)
		}
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
		case "ready", "restarting":
			facts = append(facts, muted.Render(a.Status))
		default:
			facts = append(facts, muted.Render("idle"))
		}
		if pct, approx, ok := a.ContextPct(); ok {
			// "~" marks Deck's estimate (or its assumed window) rather than
			// the CLI's own figure.
			mark := ""
			if approx {
				mark = "~"
			}
			facts = append(facts, muted.Render(fmt.Sprintf("%s%d%% context", mark, pct)))
		} else if a.ContextTokens > 0 {
			// The fill without a window to measure it against: dim, as the
			// least certain figure on the line.
			facts = append(facts, muted.Faint(true).Render(graph.Compact(float64(a.ContextTokens))+" context"))
		}
		if a.CostUSD > 0 {
			facts = append(facts, muted.Render(fmt.Sprintf("$%.2f", a.CostUSD)))
		}
		if a.Branch != "" {
			facts = append(facts, muted.Render(a.Branch))
		}
		if a.Leases != "" {
			facts = append(facts, muted.Render("holds "+a.Leases))
		}
		lines = append(lines, ansi.Truncate(indent+"  "+strings.Join(facts, muted.Render(" · ")), width, "…"))
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
