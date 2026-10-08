package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/dev-dami/deck/internal/session"
	"github.com/dev-dami/deck/internal/tui/motion"
	"github.com/dev-dami/deck/internal/tui/theme"
)

// Everything that tells the agents of a shared thread apart lives here: the tint
// of a speaker, its label, and the small ◆ chip with its status.

// glyphPause marks a paused thread.
const glyphPause = "‖"

// tintStyle is an agent kind's tint as a foreground style.
func tintStyle(kind string) lipgloss.Style { return st(theme.Tint(kind)) }

// agentInfo finds a member of any thread.
func (m *model) agentInfo(id string) (session.AgentInfo, bool) {
	for _, a := range m.agents {
		if a.ID == id {
			return a, true
		}
	}
	return session.AgentInfo{}, false
}

// kindOf is the kind of an agent id. It also works for ids no longer listed
// (a replayed thread): the views remember what joined, and failing that the id
// without its trailing digits ("claude2" -> "claude").
func (m *model) kindOf(id string) string {
	if a, ok := m.agentInfo(id); ok {
		return a.Kind
	}
	for _, v := range m.views {
		if k, ok := v.known[id]; ok {
			return k
		}
	}
	return strings.TrimRight(id, "0123456789")
}

// agentName is the display name of an agent id.
func (m *model) agentName(id string) string {
	if a, ok := m.agentInfo(id); ok && a.Name != "" {
		return a.Name
	}
	return id
}

func (m *model) agentNames(ids []string) []string {
	out := make([]string, len(ids))
	for i, id := range ids {
		out[i] = m.agentName(id)
	}
	return out
}

// speakerLabel is the line that opens an agent's block: ◆ and the bold name,
// both in the agent's tint.
func (m *model) speakerLabel(id string, w int) string {
	ts := tintStyle(m.kindOf(id))
	return ts.Render(theme.GlyphAgent) + " " + ts.Bold(true).Render(trunc(m.agentName(id), max(1, w-2)))
}

// memberStyle colours a member's ◆: its tint, breathing while it works, in the
// warning colour while it waits for you, faint once it has exited.
func (m *model) memberStyle(a session.AgentInfo) lipgloss.Style {
	tint := theme.Tint(a.Kind)
	switch a.Status {
	case session.StatusExited:
		return sFaint
	case session.StatusError:
		return sDanger
	case session.StatusApproval:
		return sWarn
	case session.StatusWorking, session.StatusTool:
		p := motion.Pulse(m.now, motion.PulsePeriod)
		return lipglossFg(m.blend(theme.Faint, tint, 0.4+0.6*p))
	}
	return lipglossFg(m.blend(theme.Faint, tint, 0.75))
}

// chip is "◆claude" in the member's tint and status.
func (m *model) chip(a session.AgentInfo) string {
	return m.memberStyle(a).Render(theme.GlyphAgent + a.Name)
}

func (m *model) chips(members []session.AgentInfo, w int) string {
	parts := make([]string, 0, len(members))
	for _, a := range members {
		c := m.chip(a)
		if pct, ok := ctxPct(a); ok {
			c += " " + ctxStyle(pct).Render(ctxGlyph(pct))
		}
		parts = append(parts, c)
	}
	return trunc(strings.Join(parts, " "), w)
}

// working reports whether an agent is mid-turn.
func working(a session.AgentInfo) bool {
	return a.Status == session.StatusWorking || a.Status == session.StatusTool
}

// busy reports whether Ctrl+C would interrupt something.
func busy(a session.AgentInfo) bool { return working(a) || a.Status == session.StatusApproval }

// ---- context meter ----

// ctxPct is how full a member's context window is, in percent; ok is false when
// the window is not known.
func ctxPct(a session.AgentInfo) (int, bool) {
	if a.ContextWindow <= 0 {
		return 0, false
	}
	p := a.ContextTokens * 100 / a.ContextWindow
	return max(0, min(100, p)), true
}

// ctxGlyph is ○ ◔ ◑ ◕ ● by quarter.
func ctxGlyph(pct int) string {
	return []string{"○", "◔", "◑", "◕", "●"}[max(0, min(4, pct/25))]
}

// ctxStyle is muted until 70%, warning to 90%, danger above.
func ctxStyle(pct int) lipgloss.Style {
	switch {
	case pct > 90:
		return sDanger
	case pct >= 70:
		return sWarn
	}
	return sMuted
}

// meter is "◔ 42%" in its colour.
func meter(a session.AgentInfo) string {
	pct, ok := ctxPct(a)
	if !ok {
		return ""
	}
	return ctxStyle(pct).Render(ctxGlyph(pct) + " " + itoa(pct) + "%")
}
