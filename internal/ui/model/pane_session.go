package model

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/dev-dami/deck/internal/ui/common"
	"github.com/dev-dami/deck/internal/workspace"
)

// The sidebar's two built-in panes: the session (title, folder and branch,
// files touched) and its agents. Both render all their lines and let the
// strip scroll them (pane.go).

func init() {
	registerPane(paneSession, func() pane { return &sessionPane{} })
	registerPane(paneAgents, func() pane { return &agentsPane{} })
}

// paneText memoizes a pane's text between height and render within one
// frame: the strip asks for the height and then renders at the same width,
// so the text is built once.
type paneText struct {
	text  string
	width int
	fresh bool
}

// get returns the text at width, building it with build unless height just
// built it.
func (c *paneText) get(width int, build func() string) string {
	if c.fresh && c.width == width {
		c.fresh = false
		return c.text
	}
	c.fresh = false
	return build()
}

// lines builds the text at width, keeps it for the render that follows and
// returns its line count.
func (c *paneText) lines(width int, build func() string) int {
	c.text, c.width, c.fresh = build(), width, true
	if c.text == "" {
		return 0
	}
	return strings.Count(c.text, "\n") + 1
}

// sessionPane shows the session's title, its folder and branch, and the
// files it touched.
type sessionPane struct{ cache paneText }

func (*sessionPane) id() paneID       { return paneSession }
func (*sessionPane) title(*UI) string { return "Session" }
func (*sessionPane) width() int       { return 0 }

func (*sessionPane) badge(m *UI) string {
	if n := fileChangeCount(m.sessionFiles); n > 0 {
		return fmt.Sprintf("%d changed", n)
	}
	return ""
}

func (p *sessionPane) height(m *UI, width int) int {
	return p.cache.lines(width, func() string { return p.text(m, width) })
}

func (p *sessionPane) render(m *UI, width, _ int, _ bool) string {
	return p.cache.get(width, func() string { return p.text(m, width) })
}

func (*sessionPane) text(m *UI, width int) string {
	if m.session == nil || width <= 0 {
		return ""
	}
	t := m.com.Styles
	cwd := m.com.Workspace.WorkingDir()
	return lipgloss.JoinVertical(
		lipgloss.Left,
		t.Sidebar.SessionTitle.Width(width).MaxHeight(2).Render(m.session.Title),
		"",
		common.PrettyPathWithBranch(t, cwd, m.gitBranch, width),
		"",
		m.filesInfo(cwd, width, fileChangeCount(m.sessionFiles), true),
	)
}

func (*sessionPane) key(*UI, tea.KeyPressMsg) (bool, tea.Cmd) { return false, nil }
func (*sessionPane) click(*UI, int, int) tea.Cmd              { return nil }

// agentsPane shows the session's agents: model, status, context, cost.
type agentsPane struct{ cache paneText }

func (*agentsPane) id() paneID       { return paneAgents }
func (*agentsPane) title(*UI) string { return "Agents" }
func (*agentsPane) width() int       { return 0 }

func (*agentsPane) badge(m *UI) string {
	if n := len(m.sessionRoster()); n > 0 {
		return fmt.Sprintf("%d", n)
	}
	return ""
}

func (p *agentsPane) height(m *UI, width int) int {
	return p.cache.lines(width, func() string { return p.text(m, width) })
}

func (p *agentsPane) render(m *UI, width, _ int, _ bool) string {
	return p.cache.get(width, func() string { return p.text(m, width) })
}

func (*agentsPane) text(m *UI, width int) string {
	if width <= 0 {
		return ""
	}
	if len(m.sessionRoster()) == 0 {
		return m.stripStyles().muted.Render("No agents yet")
	}
	return m.modelInfo(width)
}

func (*agentsPane) key(*UI, tea.KeyPressMsg) (bool, tea.Cmd) { return false, nil }
func (*agentsPane) click(*UI, int, int) tea.Cmd              { return nil }

// sessionRoster is the current session's agents, or nil.
func (m *UI) sessionRoster() []workspace.AgentLine {
	roster, ok := m.com.Workspace.(workspace.Roster)
	if !ok {
		return nil
	}
	sessionID := ""
	if m.session != nil {
		sessionID = m.session.ID
	}
	return roster.Roster(sessionID)
}
