package model

import (
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/dev-dami/deck/internal/ui/tone"
)

// The Commands pane: the commands the session's agents are running, the
// shells they left in the background, and Deck's own jobs (a git push, a
// fetch). enter on a row shows its whole command.

func init() { registerPane(paneCommands, func() pane { return &commandsPane{} }) }

type commandsPane struct {
	list paneList
	open map[string]bool // expanded rows, by key
}

func (p *commandsPane) id() paneID       { return paneCommands }
func (p *commandsPane) title(*UI) string { return "Commands" }
func (p *commandsPane) width() int       { return 0 }

func (p *commandsPane) badge(m *UI) string {
	r := m.activityReport()
	if n := len(r.Commands) + len(r.Background); n > 0 {
		return strconv.Itoa(n)
	}
	return ""
}

func (p *commandsPane) height(m *UI, width int) int {
	return rowsHeight(p.rows(m, width-1, time.Now()))
}

func (p *commandsPane) render(m *UI, width, height int, focused bool) string {
	rows := p.rows(m, width-1, time.Now())
	return p.list.view(rows, width, height, focused, m.com.Styles.Header.Mark.GetForeground())
}

func (p *commandsPane) key(m *UI, msg tea.KeyPressMsg) (bool, tea.Cmd) {
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

func (p *commandsPane) click(m *UI, _, y int) tea.Cmd {
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

func (p *commandsPane) toggle(key string) {
	if key == "" {
		return
	}
	if p.open == nil {
		p.open = map[string]bool{}
	}
	p.open[key] = !p.open[key]
}

// rows are the pane's sections at width (the gutter left out).
func (p *commandsPane) rows(m *UI, width int, now time.Time) []paneRow {
	r := m.activityReport()
	jobs := m.runningJobs()
	t := m.com.Styles
	muted := t.ModelInfo.Provider
	if len(r.Commands)+len(r.Background)+len(jobs) == 0 {
		return []paneRow{{lines: []string{muted.Render("Nothing running")}}}
	}
	head := func(s string) paneRow {
		return paneRow{lines: []string{t.Section.Title.Render(s)}}
	}
	elapsed := func(since time.Time) string {
		if d := now.Sub(since).Truncate(time.Second); d >= time.Second {
			return fmtElapsed(d)
		}
		return ""
	}
	var rows []paneRow
	if len(r.Commands) > 0 {
		rows = append(rows, head("Running"))
		frame := spinnerFrame(now)
		for _, c := range r.Commands {
			key := "run:" + c.ToolID
			tint := lipgloss.NewStyle().Foreground(tone.Tint(c.AgentKind))
			lines := []string{spread(tint.Render(frame)+" "+oneLine(c.Summary), muted.Render(elapsed(c.Started)), width)}
			if p.open[key] {
				lines = append(lines, commandDetail(m, c.Summary, c.AgentName, c.Detail, "", width)...)
			}
			rows = append(rows, paneRow{key: key, selectable: true, lines: lines})
		}
	}
	if len(r.Background) > 0 {
		rows = append(rows, head("Background"))
		for _, s := range r.Background {
			key := "bg:" + s.ShellID
			tint := lipgloss.NewStyle().Foreground(tone.Tint(s.AgentKind))
			right := s.ShellID
			if e := elapsed(s.Started); e != "" {
				right += " · since " + e
			}
			lines := []string{spread(tint.Render("⧗")+" "+oneLine(s.Command), muted.Render(right), width)}
			if p.open[key] {
				lines = append(lines, commandDetail(m, s.Command, s.AgentName, "", s.Output, width)...)
			}
			rows = append(rows, paneRow{key: key, selectable: true, lines: lines})
		}
	}
	if len(jobs) > 0 {
		rows = append(rows, head("Deck"))
		frame := spinnerFrame(now)
		accent := lipgloss.NewStyle().Foreground(t.Header.Mark.GetForeground())
		for _, j := range jobs {
			key := "job:" + j.ID
			lines := []string{spread(accent.Render(frame)+" "+j.Label, muted.Render(elapsed(j.Started)), width)}
			if p.open[key] {
				lines = append(lines, "  "+muted.Render(j.ID))
			}
			rows = append(rows, paneRow{key: key, selectable: true, lines: lines})
		}
	}
	return rows
}

// commandDetail is an expanded command: the whole command line, who runs
// it, what it is for and the tail of its output.
func commandDetail(m *UI, command, agent, desc, output string, width int) []string {
	muted := m.com.Styles.ModelInfo.Provider
	inner := max(1, width-2)
	var lines []string
	for _, l := range wrapLines(command, inner, 0) {
		lines = append(lines, "  "+l)
	}
	about := agent
	if desc != "" {
		about += " · " + desc
	}
	if about != "" {
		for _, l := range wrapLines(about, inner, 2) {
			lines = append(lines, "  "+muted.Render(l))
		}
	}
	if out := strings.TrimSpace(output); out != "" {
		ol := strings.Split(out, "\n")
		if len(ol) > 4 {
			ol = ol[len(ol)-4:]
		}
		for _, l := range ol {
			l = strings.ReplaceAll(ansi.Strip(l), "\t", "  ")
			lines = append(lines, "  "+muted.Render("│ "+l))
		}
	}
	return lines
}

// oneLine is s on one line.
func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
