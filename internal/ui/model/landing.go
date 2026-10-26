package model

import (
	"image"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
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
// agents fit beside it.
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

// landingBlock is the landing page laid out as one block, centered across
// the window and in the height above the prompt: the wordmark with the
// agents beside it on top, the folder and the hint under them.
type landingBlock struct {
	header, agents, text image.Rectangle
	agentsView, textView string
}

// landingLayout places the landing block for this frame.
func (m *UI) landingLayout() landingBlock {
	var b landingBlock
	hdr, main := m.layout.header, m.layout.main
	left, side := m.landingColumns()

	agentsW := 0
	if side {
		agentsW = naturalWidth(m.modelInfo(landingAgentsMax))
		b.agentsView = m.modelInfo(agentsW)
	}
	blockW := max(left, min(landingHintMax, main.Dx()))
	if side {
		blockW = max(blockW, left+landingGap+agentsW)
	}
	blockW = min(blockW, hdr.Dx())

	b.textView = m.landingText(blockW, !side)
	topH := hdr.Dy()
	agentsH := lipgloss.Height(b.agentsView)
	if b.agentsView == "" {
		agentsH = 0
	}
	// Under the wordmark, or under the agents when they run taller.
	gap := 1 + max(0, agentsH-topH)
	blockH := topH + gap + lipgloss.Height(b.textView)

	x0 := hdr.Min.X + max(0, (hdr.Dx()-blockW)/2)
	y0 := hdr.Min.Y + max(0, (main.Max.Y-hdr.Min.Y-blockH)/2)
	b.header = image.Rect(x0, y0, hdr.Max.X, y0+topH)
	if side {
		ax := x0 + left + landingGap
		b.agents = image.Rect(ax, y0, ax+agentsW, y0+agentsH)
	}
	b.text = image.Rect(x0, min(y0+topH+gap, main.Max.Y), x0+blockW, main.Max.Y)
	return b
}

// landingText is the folder, the agents when they are not beside the
// wordmark, and the hint, wrapped to width.
func (m *UI) landingText(width int, withAgents bool) string {
	t := m.com.Styles
	parts := []string{common.PrettyPathInline(t, m.com.Workspace.WorkingDir(), m.gitBranch, width)}
	if withAgents {
		if info := m.modelInfo(width); info != "" {
			parts = append(parts, "", info)
		}
	}
	hint := t.ModelInfo.Provider.Render("Type @ to pick an agent and its model (@claude, @codex, @opencode, @antigravity, or a named agent from deck.json; @all asks everyone; $ works too). # adds a file.")
	parts = append(parts, "", lipgloss.NewStyle().Width(min(width, landingHintMax)).Render(hint))
	return lipgloss.JoinVertical(lipgloss.Left, parts...)
}

// naturalWidth is the widest line of s without its trailing padding.
func naturalWidth(s string) int {
	w := 0
	for line := range strings.SplitSeq(s, "\n") {
		w = max(w, ansi.StringWidth(strings.TrimRight(ansi.Strip(line), " ")))
	}
	return w
}
