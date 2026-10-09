package tui

import (
	"math"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/dev-dami/deck/internal/agent"
	"github.com/dev-dami/deck/internal/session"
	"github.com/dev-dami/deck/internal/tui/motion"
	"github.com/dev-dami/deck/internal/tui/theme"
)

// statusGlyph returns the glyph and style for one member (the @ menu).
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

// threadGlyph is the status of a whole thread: ⚠ when anything waits for you,
// ‖ when paused, ● while any member works, ○ at rest.
func (m *model) threadGlyph(t session.ThreadInfo) (string, lipgloss.Style) {
	var approval, work, tool, errored bool
	for _, a := range t.Members {
		switch a.Status {
		case session.StatusApproval:
			approval = true
		case session.StatusWorking:
			work = true
		case session.StatusTool:
			tool = true
		case session.StatusError:
			errored = true
		}
	}
	switch {
	case approval:
		level := 1.0
		if p := m.firstPendingIn(t.ID); p != nil {
			level = p.badgeLevel(m.now)
		}
		return glyphWarn, lipglossFg(m.blend(theme.Faint, theme.Warn, level))
	case t.Paused:
		return glyphPause, sWarn
	case work:
		p := motion.Pulse(m.now, motion.PulsePeriod)
		return theme.GlyphWorking, lipglossFg(m.blend(theme.Muted, theme.Accent, p))
	case tool:
		return motion.NewSpinner().Frame(m.now), sAccent
	case errored:
		return theme.GlyphWorking, sDanger
	}
	return theme.GlyphIdle, sFaint
}

// exited reports whether a thread has members and all of them are gone.
func exited(t session.ThreadInfo) bool {
	if len(t.Members) == 0 {
		return false
	}
	for _, a := range t.Members {
		if a.Status != session.StatusExited {
			return false
		}
	}
	return true
}

// threadTitle is what a sidebar row calls a thread.
func threadTitle(t session.ThreadInfo) string {
	if s := oneLine(t.Title); s != "" {
		return s
	}
	if len(t.Members) == 0 {
		return "new thread"
	}
	names := make([]string, len(t.Members))
	for i, a := range t.Members {
		names[i] = a.Name
	}
	return strings.Join(names, " + ")
}

// sidebarRowHeight is 2 on wide terminals (title, then member chips) and 1
// otherwise.
func (g geom) sidebarRowHeight() int {
	if g.sbW >= 31 {
		return 2
	}
	return 1
}

// sidebarStart is the first row shown when the rows do not all fit.
func (m *model) sidebarStart(g geom, lines int) int {
	fit := max(1, lines/g.sidebarRowHeight())
	if si := m.selIndex(); len(m.rows) > fit && si+1 > fit {
		return si + 1 - fit
	}
	return 0
}

// sidebarLines renders rows lines, each exactly g.sbW cells wide. Each thread
// is a row: its status glyph and title, cost on the right; wide terminals add
// a faint line of member chips.
func (m *model) sidebarLines(g geom, rows int) []string {
	cw := g.sbW - 3 // one cell of screen padding on the left, two of gap on the right
	wide := g.sbW >= 31
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

	for i := m.sidebarStart(g, rows); i < len(m.rows) && len(out) < rows; i++ {
		t := m.rows[i]
		bar := barAt(i)
		selected := t.ID == m.sel

		glyph, gs := m.threadGlyph(t)
		if t.Resolved && glyph == theme.GlyphIdle {
			glyph, gs = glyphResolved, sFaint
		}
		unread := m.unread[t.ID] && !selected
		baseCol := theme.Muted
		titleStyle := sMuted
		switch {
		case exited(t), len(t.Members) == 0, t.Resolved: // finished, settled, or "new thread"
			baseCol, titleStyle = theme.Faint, sFaint
		case selected:
			baseCol, titleStyle = theme.Fg, sBold
		case unread: // news you have not seen reads brighter
			baseCol, titleStyle = theme.Fg, sFg
		}
		if v := m.views[t.ID]; v != nil && v.flash.Started() && v.flash.Moving(m.now) {
			titleStyle = lipglossFg(m.blend(theme.Accent, baseCol, v.flash.Progress(m.now))).Bold(selected)
		}

		prefix := bar + gs.Render(glyph)
		if m.modeOf(t) == agent.ModeYolo { // yolo threads are marked in the list
			prefix += sDanger.Render(glyphYolo)
		}
		prefix += " "
		if !wide { // no chip line: a tinted ◆ per member says who is in it
			for k, a := range t.Members {
				if k == 3 {
					break
				}
				prefix += m.memberStyle(a).Render(theme.GlyphAgent)
			}
			if len(t.Members) > 0 {
				prefix += " "
			}
		}
		cost := fmtCost(t.CostUSD)
		if !wide && !selected { // narrow sidebar: cost only on the selected row
			cost = ""
		}
		costW := 0
		if cost != "" {
			costW = sw(cost) + 1
		}
		marks := ""
		if t.Pinned {
			marks += " " + sFaint.Render(glyphPinned)
		}
		if unread {
			marks += " " + sAccent.Render(glyphUnread)
		}
		titleW := max(1, cw-sw(prefix)-costW-sw(marks))
		line := prefix + titleStyle.Render(trunc(threadTitle(t), titleW)) + marks
		if cost != "" {
			line = padTo(line, cw-sw(cost)) + sFaint.Render(cost)
		}
		out = append(out, fit(" "+line, g.sbW))

		if wide && len(out) < rows {
			line2 := bar + "  "
			if len(t.Members) > 0 {
				line2 += m.chips(t.Members, cw-3)
			}
			out = append(out, fit(" "+line2, g.sbW))
		}
	}
	for len(out) < rows {
		out = append(out, strings.Repeat(" ", g.sbW))
	}
	return out
}

// selIndex is the sidebar row of the selected thread, or -1.
func (m *model) selIndex() int {
	for i, t := range m.rows {
		if t.ID == m.sel {
			return i
		}
	}
	return -1
}

func (m *model) firstPending(agentID string) *pendingApproval {
	for _, p := range m.pending {
		if p.req.AgentID == agentID {
			return p
		}
	}
	return nil
}

func (m *model) firstPendingIn(threadID string) *pendingApproval {
	for _, p := range m.pending {
		if p.thread == threadID {
			return p
		}
	}
	return nil
}
