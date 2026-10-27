package model

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/dev-dami/deck/internal/deck/config"
	"github.com/dev-dami/deck/internal/ui/tone"
	"github.com/dev-dami/deck/internal/ui/util"
	"github.com/dev-dami/deck/internal/workspace"
)

// The Tasks pane: the thread's shared task board (what is to be done, who
// owns it, what it waits for), then one row per subagent the session's agents
// have running (Claude Code's Task/Agent, OpenCode's task, …) with how long it
// has run. enter shows a row's detail. On a task, x cancels it, r retries a
// failed or blocked one, and o steps the orchestration level (off, assist,
// auto, full): how far members start subagents, and whether the scheduler
// runs ready tasks. On a task whose
// subagent worked on a worktree of its own, a applies that work to your tree
// and d (twice) discards it. A race's entries sit under it, each with what it
// changed, spent and took; a on an entry keeps it and discards the others.

func init() { registerPane(paneTasks, func() pane { return &tasksPane{} }) }

const taskKeyPrefix = "task:"

type tasksPane struct {
	list paneList
	open map[string]bool // expanded rows, by task key or tool id
	// discard is the task whose worktree a first d asked to discard, and
	// when: a second d on it within discardWindow does it.
	discard   string
	discardAt time.Time
}

const discardWindow = 3 * time.Second

func (p *tasksPane) id() paneID       { return paneTasks }
func (p *tasksPane) title(*UI) string { return "Tasks" }
func (p *tasksPane) width() int       { return 0 }
func (p *tasksPane) badge(m *UI) string {
	n := len(m.activityReport().Subagents) + m.boardReport().Open()
	if n > 0 {
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
	sel := p.list.selected(rows)
	switch msg.String() {
	case "enter", "space":
		p.toggle(sel)
		return true, nil
	case "a", "d":
		return p.worktree(m, sel, msg.String() == "d")
	case "x":
		return p.act(m, sel, func(o workspace.Orchestration, sid, id string) error { return o.CancelTask(sid, id) })
	case "r":
		return p.act(m, sel, func(o workspace.Orchestration, sid, id string) error { return o.RetryTask(sid, id) })
	case "o":
		// Only with a board to run: otherwise o stays the strip's own key.
		o, ok := m.com.Workspace.(workspace.Orchestration)
		board := m.boardReport()
		if !ok || m.session == nil || len(board.Tasks) == 0 {
			return false, nil
		}
		next, sid := nextLevel(board.Level), m.session.ID
		return true, func() tea.Msg {
			if err := o.SetLevel(sid, next); err != nil {
				return util.NewErrorMsg(err)
			}
			return util.NewInfoMsg("Orchestration " + next + ": " + levelBlurb[next])
		}
	}
	return false, nil
}

// levelBlurb says in a line what each orchestration level does.
var levelBlurb = map[string]string{
	config.LevelOff:    "agents start helpers only when you ask",
	config.LevelAssist: "agents may start a couple of helpers",
	config.LevelAuto:   "agents plan, split and hand off; ready tasks run by themselves",
	config.LevelFull:   "deeper trees, races and automatic reviews",
}

// nextLevel is the level after level, round from full to off.
func nextLevel(level string) string {
	i := slices.Index(config.LevelNames, level)
	return config.LevelNames[(i+1)%len(config.LevelNames)]
}

// act runs f on the selected task. It does nothing (and is not handled) when
// a subagent row or nothing is selected.
func (p *tasksPane) act(m *UI, sel string, f func(o workspace.Orchestration, sessionID, taskID string) error) (bool, tea.Cmd) {
	id, ok := strings.CutPrefix(sel, taskKeyPrefix)
	o, isO := m.com.Workspace.(workspace.Orchestration)
	if !ok || !isO || m.session == nil {
		return false, nil
	}
	sid := m.session.ID
	return true, func() tea.Msg {
		if err := f(o, sid, id); err != nil {
			return util.NewErrorMsg(err)
		}
		return nil
	}
}

// worktree applies, or with discard set discards, the work the selected
// task's subagent did on its own worktree. Discarding asks for a second d.
// Not handled when the selection is no such task.
func (p *tasksPane) worktree(m *UI, sel string, discard bool) (bool, tea.Cmd) {
	id, ok := strings.CutPrefix(sel, taskKeyPrefix)
	w, isW := m.com.Workspace.(workspace.Worktrees)
	if !ok || !isW || m.session == nil {
		return false, nil
	}
	branch, entry := "", false
	for _, t := range m.boardReport().Tasks {
		if t.ID == id {
			branch, entry = t.Branch, t.Entry
		}
	}
	if branch == "" {
		return false, nil
	}
	sid := m.session.ID
	if !discard {
		return true, func() tea.Msg {
			files, err := w.ApplyTask(sid, id)
			switch {
			case err != nil:
				return util.NewErrorMsg(err)
			case entry:
				return util.NewInfoMsg(fmt.Sprintf("Kept %s: applied %d files from %s and discarded the other entries", id, len(files), branch))
			case len(files) == 0:
				return util.NewInfoMsg(branch + " has nothing to apply")
			}
			return util.NewInfoMsg(fmt.Sprintf("Applied %d files from %s", len(files), branch))
		}
	}
	if p.discard != id || time.Since(p.discardAt) > discardWindow {
		p.discard, p.discardAt = id, time.Now()
		return true, util.ReportInfo("Press d again to discard " + branch + " and everything on it")
	}
	p.discard = ""
	return true, func() tea.Msg {
		if err := w.DiscardTask(sid, id); err != nil {
			return util.NewErrorMsg(err)
		}
		return util.NewInfoMsg("Discarded " + branch)
	}
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
	board := m.boardReport()
	muted := m.com.Styles.ModelInfo.Provider
	if len(subs) == 0 && len(board.Tasks) == 0 {
		return []paneRow{{lines: []string{muted.Render("No subagents running")}}}
	}
	frame := spinnerFrame(now)
	var rows []paneRow
	header := func(s string) {
		rows = append(rows, paneRow{lines: []string{muted.Render(s)}})
	}
	if len(board.Tasks) > 0 {
		head := "Tasks"
		if board.Level != "" {
			head += " · orchestration " + board.Level + " (o)"
		}
		header(head)
		for _, t := range board.Tasks {
			key := taskKeyPrefix + t.ID
			rows = append(rows, paneRow{key: key, selectable: true, lines: taskLines(m, t, width, now, frame, p.open[key])})
		}
	}
	if len(subs) > 0 {
		if len(board.Tasks) > 0 {
			header("Subagents")
		}
		for _, s := range subs {
			rows = append(rows, paneRow{key: s.ToolID, selectable: true, lines: subagentLines(m, s, width, now, frame, p.open[s.ToolID])})
		}
	}
	return rows
}

// taskGlyph is the status mark of a board task.
func taskGlyph(status, frame string) string {
	switch status {
	case "running":
		return frame
	case "ready":
		return "○"
	case "review":
		return "◇"
	case "blocked":
		return "!"
	case "done":
		return "✓"
	case "failed":
		return "✗"
	case "cancelled":
		return "–"
	}
	return "·" // pending
}

// taskLines is one board task's row.
func taskLines(m *UI, t workspace.BoardTask, width int, now time.Time, frame string, open bool) []string {
	muted := m.com.Styles.ModelInfo.Provider
	left := taskGlyph(t.Status, frame) + " " + muted.Render(t.ID) + " " + t.Title
	right := ""
	if t.Owner != "" {
		right = muted.Render(t.Owner)
	}
	if t.Reviewer != "" {
		right = muted.Render(t.Owner + " · review: " + t.Reviewer)
	}
	if t.Entry {
		// Under its race, which has the title: the entry is who ran it and
		// how it compares.
		left = "  " + taskGlyph(t.Status, frame) + " " + muted.Render(t.ID) + " " + t.Owner
		right = muted.Render(t.Stats)
	}
	lines := []string{spread(left, right, width)}
	if !open {
		return lines
	}
	add := func(label, text string, n int) {
		if strings.TrimSpace(text) == "" {
			return
		}
		for i, l := range wrapLines(text, max(1, width-2), n) {
			if i == 0 {
				l = label + l
			}
			lines = append(lines, "  "+muted.Render(l))
		}
	}
	info := t.Status
	if t.Attempts > 0 {
		info += fmt.Sprintf(" · attempt %d of %d", t.Attempts, t.Tries)
	}
	if t.Reviews > 0 {
		info += fmt.Sprintf(" · %d reviews", t.Reviews)
	}
	if len(t.Deps) > 0 {
		info += " · after " + strings.Join(t.Deps, ",")
	}
	add("", info, 2)
	if !t.Entry { // an entry's brief is its race's
		add("", t.Brief, 4)
	}
	add("→ ", t.Result, 6)
	add("✗ ", t.Error, 4)
	add("files ", strings.Join(t.Files, ", "), 3)
	switch {
	case t.Branch != "" && t.Entry:
		add("branch ", t.Branch+" · a keep this one · d discard", 1)
	case t.Branch != "":
		add("branch ", t.Branch+" · a apply · d discard", 1)
	}
	return lines
}

// boardReport is the session's task board.
func (m *UI) boardReport() (r workspace.BoardReport) {
	o, ok := m.com.Workspace.(workspace.Orchestration)
	if !ok || m.session == nil {
		return r
	}
	return o.Board(m.session.ID)
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
