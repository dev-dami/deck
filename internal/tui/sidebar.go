package tui

import (
	"math"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/dev-dami/deck/internal/session"
	"github.com/dev-dami/deck/internal/tui/motion"
	"github.com/dev-dami/deck/internal/tui/theme"
)

// statusGlyph returns the glyph and style for an agent row.
func (m *model) statusGlyph(a session.AgentInfo) (string, lipgloss.Style) {
	switch a.Status {
	case session.StatusWorking:
		p := motion.Pulse(m.now, motion.PulsePeriod)
		return theme.GlyphWorking, lipglossFg(m.blend(theme.Muted, theme.Accent, p))
	case session.StatusTool:
		return motion.NewSpinner().Frame(m.now), sAccent
	case session.StatusApproval:
		level := 1.0
		if p := m.firstPending(a.ID); p != nil {
			level = p.badgeLevel(m.now)
		}
		return glyphWarn, lipglossFg(m.blend(theme.Faint, theme.Warn, level))
	case session.StatusError:
		return theme.GlyphWorking, sDanger
	}
	return theme.GlyphIdle, sFaint
}

// sidebarLines renders rows lines, each exactly g.sbW cells wide.
func (m *model) sidebarLines(g geom, rows int) []string {
	cw := g.sbW - 3     // one cell of screen padding on the left, two of gap on the right
	wide := g.sbW >= 31 // titles (and every row's cost) only fit on wide terminals
	out := make([]string, 0, rows)
	// The accent bar glides between rows: each row's bar fades with its
	// distance from the spring position, so adjacent rows cross-fade.
	barAt := func(i int) string {
		lvl := 1 - math.Abs(m.barY.Pos-float64(i))
		if lvl < 0.12 {
			return " "
		}
		return lipglossFg(m.blend(theme.Rule, theme.Accent, lvl)).Render(glyphBarSel)
	}

	total := len(m.agents)
	if m.newMode {
		total++
	}
	start := 0
	selIdx := m.selIndex()
	if m.newMode {
		selIdx = len(m.agents)
	}
	if total > rows && selIdx+1 > rows {
		start = selIdx + 1 - rows
	}

	for i := start; i < total && len(out) < rows; i++ {
		bar := barAt(i)
		if i >= len(m.agents) { // the "+ new agent" row
			line := bar + sFaint.Render("+") + " " + sMuted.Render("new agent")
			out = append(out, fit(" "+line, g.sbW))
			continue
		}
		a := m.agents[i]
		selected := a.ID == m.sel && !m.newMode

		glyph, gs := m.statusGlyph(a)
		baseCol := theme.Muted
		nameStyle := sMuted
		switch {
		case a.Status == session.StatusExited:
			baseCol, nameStyle = theme.Faint, sFaint
		case selected:
			baseCol, nameStyle = theme.Fg, sBold
		}
		if v := m.views[a.ID]; v != nil && v.flash.Started() && v.flash.Moving(m.now) {
			nameStyle = lipglossFg(m.blend(theme.Accent, baseCol, v.flash.Progress(m.now))).Bold(selected)
		}
		name := trunc(a.Name, 10)
		left := bar + gs.Render(glyph) + " " + nameStyle.Render(name)
		cost := fmtCost(a.CostUSD)
		if !wide && !selected { // narrow sidebar: cost only on the selected row
			cost = ""
		}
		costW := 0
		if cost != "" {
			costW = sw(cost) + 1
		}
		titleW := cw - sw(left) - 2 - costW
		line := left
		if t := oneLine(a.Title); wide && t != "" && titleW >= 4 {
			line += "  " + sFaint.Render(trunc(t, titleW))
		}
		if cost != "" {
			line = padTo(line, cw-sw(cost)) + sFaint.Render(cost)
		}
		out = append(out, fit(" "+line, g.sbW))
	}
	for len(out) < rows {
		out = append(out, strings.Repeat(" ", g.sbW))
	}
	return out
}

func (m *model) selIndex() int {
	for i, a := range m.agents {
		if a.ID == m.sel {
			return i
		}
	}
	return -1
}

func (m *model) firstPending(id string) *pendingApproval {
	for _, p := range m.pending {
		if p.req.AgentID == id {
			return p
		}
	}
	return nil
}
