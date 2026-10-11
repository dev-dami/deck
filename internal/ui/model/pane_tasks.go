package model

import (
	"strconv"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/dev-dami/deck/internal/ui/tone"
	"github.com/dev-dami/deck/internal/workspace"
)

// The Subagents pane: one row per subagent the session's agents have
// running (Claude Code's Task/Agent, OpenCode's task, …), with how long it
// has run. enter shows its type and prompt.

func init() { registerPane(paneTasks, func() pane { return &tasksPane{} }) }

type tasksPane struct {
	list paneList
	open map[string]bool // expanded rows, by tool id
}

func (p *tasksPane) id() paneID       { return paneTasks }
func (p *tasksPane) title(*UI) string { return "Subagents" }
func (p *tasksPane) width() int       { return 0 }
func (p *tasksPane) badge(m *UI) string {
	if n := len(m.activityReport().Subagents); n > 0 {
		return strconv.Itoa(n)
	}
	return ""
}

func (p *tasksPane) height(m *UI, width int) int {
	return rowsHeight(p.rows(m, width-1, time.Now()))
}

func (p *tasksPane) render(m *UI, width, height int, focused bool) string {
	rows := p.rows(m, width-1, time.Now())
	return p.list.view(rows, width, height, focused, m.com.Styles.Header.Mark.GetForeground())
}

func (p *tasksPane) key(m *UI, msg tea.KeyPressMsg) (bool, tea.Cmd) {
	rows := p.rows(m, 80, time.Now())
	if p.list.navKey(rows, msg) {
		return true, nil
	}
	switch msg.String() {
	case "enter", "space":
		p.toggle(p.list.selected(rows))
		return true, nil
	}
	return false, nil
}

func (p *tasksPane) click(m *UI, _, y int) tea.Cmd {
	i := p.list.rowAt(y)
	rows := p.rows(m, 80, time.Now())
	if i < 0 || i >= len(rows) || !rows[i].selectable {
		return nil
	}
	if i == p.list.cursor {
		p.toggle(rows[i].key)
	}
	p.list.cursor = i
	return nil
}

func (p *tasksPane) toggle(key string) {
	if key == "" {
		return
	}
	if p.open == nil {
		p.open = map[string]bool{}
	}
	p.open[key] = !p.open[key]
}

// rows are the pane's rows at width (the gutter left out).
func (p *tasksPane) rows(m *UI, width int, now time.Time) []paneRow {
	subs := m.activityReport().Subagents
	muted := m.com.Styles.ModelInfo.Provider
	if len(subs) == 0 {
		return []paneRow{{lines: []string{muted.Render("No subagents running")}}}
	}
	frame := spinnerFrame(now)
	rows := make([]paneRow, 0, len(subs))
	for _, s := range subs {
		rows = append(rows, paneRow{key: s.ToolID, selectable: true, lines: subagentLines(m, s, width, now, frame, p.open[s.ToolID])})
	}
	return rows
}

// subagentLines is one subagent's row.
func subagentLines(m *UI, s workspace.RunningTool, width int, now time.Time, frame string, open bool) []string {
	muted := m.com.Styles.ModelInfo.Provider
	tint := lipgloss.NewStyle().Foreground(tone.Tint(s.AgentKind))
	left := tint.Render(frame+" "+tone.Mark(s.AgentKind)) + " " + s.Summary
	right := ""
	if d := now.Sub(s.Started).Truncate(time.Second); d >= time.Second {
		right = muted.Render(fmtElapsed(d))
	}
	lines := []string{spread(left, right, width)}
	if !open {
		return lines
	}
	lines = append(lines, "  "+muted.Render(s.AgentName+" · "+s.Name))
	if s.Detail != "" {
		for _, l := range wrapLines(s.Detail, max(1, width-2), 6) {
			lines = append(lines, "  "+muted.Render(l))
		}
	}
	return lines
}
