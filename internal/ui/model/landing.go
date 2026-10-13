package model

import (
	"image"

	"charm.land/lipgloss/v2"
	"github.com/dev-dami/deck/internal/ui/common"
	"github.com/dev-dami/deck/internal/ui/logo"
	"github.com/dev-dami/deck/internal/version"
	"github.com/dev-dami/deck/internal/workspace"
)

const (
	// landingGap is the space between the wordmark and the agents beside it.
	landingGap = 6
	// landingAgentsMin is the narrowest the agents column may be; below it
	// the agents stack under the wordmark instead.
	landingAgentsMin = 30
	// landingAgentsMax keeps the agents column from stretching across a
	// wide terminal.
	landingAgentsMax = 56
	// landingHintMax is the widest the hint wraps to.
	landingHintMax = 72
)

// selectedLargeModel returns the currently selected large language model as
// memoized by the off-thread busy/agent probe (see workspace_cache.go), or
// nil when the agent isn't ready. It must never probe the workspace: it is
// called on every frame and AgentIsReady/AgentModel are synchronous HTTP
// round-trips in client/server mode.
func (m *UI) selectedLargeModel() *workspace.AgentModel {
	if m.agentReady {
		model := m.agentModel
		return &model
	}
	return nil
}

// landingColumns returns the width of the wordmark column and whether the
// agents fit beside it: on a wide terminal the landing page is two columns,
// the wordmark, folder and hint on the left and the agents on the right,
// level with the wordmark.
func (m *UI) landingColumns() (int, bool) {
	w, _ := m.header.anim.Bounds()
	if w <= 0 {
		return 0, false
	}
	left := max(w, lipgloss.Width(logo.Tagline)+2+lipgloss.Width(version.Version))
	if _, ok := m.roster(); !ok {
		return left, false
	}
	return left, m.layout.header.Dx() >= left+landingGap+landingAgentsMin
}

// landingAgentsRect is where the agents column goes beside the wordmark:
// from the wordmark's top to the bottom of the main area.
func (m *UI) landingAgentsRect(left int) image.Rectangle {
	r := m.layout.header
	r.Min.X += left + landingGap
	r.Max.X = min(r.Max.X, r.Min.X+landingAgentsMax)
	r.Max.Y = m.layout.main.Max.Y
	return r
}

// landingView renders the landing page under the wordmark: the working
// directory, the agents (unless they sit beside the wordmark) and the hint.
// The folder and hint always use the full width; when the agents column
// beside the wordmark runs taller than it, they start below the column.
func (m *UI) landingView() string {
	t := m.com.Styles
	width := m.layout.main.Dx()
	cwd := common.PrettyPathInline(t, m.com.Workspace.WorkingDir(), m.gitBranch, width)

	parts := []string{cwd}
	pad := 1
	if agents, r := m.landingAgentsView(); agents != "" {
		pad = max(pad, r.Dy()-m.layout.header.Dy()+1)
	} else {
		parts = append(parts, "", m.modelInfo(width))
	}
	infoSection := lipgloss.JoinVertical(lipgloss.Left, parts...)

	hint := t.ModelInfo.Provider.Render("Type $ to pick an agent and its model ($claude, $codex, $opencode, $antigravity; $all asks everyone). @ adds a file.")

	return lipgloss.NewStyle().
		Width(width).
		Height(max(0, m.layout.main.Dy()-1)).
		PaddingTop(pad).
		Render(
			lipgloss.JoinVertical(lipgloss.Left, infoSection, "", lipgloss.NewStyle().Width(min(width, landingHintMax)).Render(hint)),
		)
}

// landingAgentsView is the agents column beside the wordmark, or "" when
// the agents are in landingView.
func (m *UI) landingAgentsView() (string, image.Rectangle) {
	left, side := m.landingColumns()
	if !side {
		return "", image.Rectangle{}
	}
	r := m.landingAgentsRect(left)
	agents := m.modelInfo(r.Dx())
	// Only as tall as the list, so landingView knows where the column ends.
	r.Max.Y = min(r.Max.Y, r.Min.Y+lipgloss.Height(agents))
	return agents, r
}
