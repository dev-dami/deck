package model

import (
	"image/color"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/dev-dami/deck/internal/workspace"
)

// A small list the activity panes share: rows of one or more lines, a
// cursor that rests on the selectable ones, and a scroll offset that keeps
// the cursor's row in view.

// paneRow is one entry of a pane list.
type paneRow struct {
	key        string // identifies the row across renders (a tool id, a notice id)
	lines      []string
	selectable bool
}

// paneList is a pane list's cursor and scroll.
type paneList struct {
	cursor int // index of the selected row
	offset int // first line shown
	// starts is each row's first line as last rendered, for clicks.
	starts []int
	height int // the body height last rendered
}

// rowsHeight is how many lines rows take (at least one).
func rowsHeight(rows []paneRow) int {
	n := 0
	for _, r := range rows {
		n += len(r.lines)
	}
	return max(1, n)
}

// clamp puts the cursor on a selectable row, nearest to where it was.
func (l *paneList) clamp(rows []paneRow) {
	if len(rows) == 0 {
		l.cursor = 0
		return
	}
	l.cursor = max(0, min(l.cursor, len(rows)-1))
	if rows[l.cursor].selectable {
		return
	}
	for d := 1; d < len(rows); d++ {
		if i := l.cursor + d; i < len(rows) && rows[i].selectable {
			l.cursor = i
			return
		}
		if i := l.cursor - d; i >= 0 && rows[i].selectable {
			l.cursor = i
			return
		}
	}
}

// move steps the cursor by delta selectable rows.
func (l *paneList) move(rows []paneRow, delta int) {
	l.clamp(rows)
	step := 1
	if delta < 0 {
		step, delta = -1, -delta
	}
	for ; delta > 0; delta-- {
		for i := l.cursor + step; i >= 0 && i < len(rows); i += step {
			if rows[i].selectable {
				l.cursor = i
				break
			}
		}
	}
}

// home and end jump to the first and last selectable rows.
func (l *paneList) home(rows []paneRow) { l.cursor = 0; l.clamp(rows) }
func (l *paneList) end(rows []paneRow)  { l.cursor = len(rows) - 1; l.clamp(rows) }

// selected is the selected row's key, "" when nothing is selectable.
func (l *paneList) selected(rows []paneRow) string {
	l.clamp(rows)
	if l.cursor < len(rows) && rows[l.cursor].selectable {
		return rows[l.cursor].key
	}
	return ""
}

// view draws rows in width x height, scrolled to keep the cursor's row in
// view; a bar in accent marks the selected row while focused.
func (l *paneList) view(rows []paneRow, width, height int, focused bool, accent color.Color) string {
	if width <= 0 || height <= 0 {
		return ""
	}
	l.clamp(rows)
	l.height = height
	bar := lipgloss.NewStyle().Foreground(accent).Render("▌")
	var lines []string
	l.starts = l.starts[:0]
	selStart, selEnd := 0, 0
	for i, r := range rows {
		l.starts = append(l.starts, len(lines))
		sel := focused && i == l.cursor && r.selectable
		if i == l.cursor {
			selStart, selEnd = len(lines), len(lines)+len(r.lines)
		}
		for _, ln := range r.lines {
			gutter := " "
			if sel {
				gutter = bar
			}
			lines = append(lines, gutter+ansi.Truncate(ln, width-1, "…"))
		}
	}
	if selStart < l.offset {
		l.offset = selStart
	}
	if selEnd > l.offset+height {
		l.offset = min(selStart, selEnd-height)
	}
	l.offset = max(0, min(l.offset, len(lines)-height))
	end := min(len(lines), l.offset+height)
	return strings.Join(lines[l.offset:end], "\n")
}

// rowAt is the row at body line y as last rendered, -1 for none.
func (l *paneList) rowAt(y int) int {
	line := l.offset + y
	for i := len(l.starts) - 1; i >= 0; i-- {
		if line >= l.starts[i] {
			return i
		}
	}
	return -1
}

// navKey moves the cursor for the usual list keys and reports whether the
// key was one of them.
func (l *paneList) navKey(rows []paneRow, msg tea.KeyPressMsg) bool {
	switch msg.String() {
	case "up", "k":
		l.move(rows, -1)
	case "down", "j":
		l.move(rows, 1)
	case "pgup":
		l.move(rows, -max(1, l.height-1))
	case "pgdown":
		l.move(rows, max(1, l.height-1))
	case "home", "g":
		l.home(rows)
	case "end", "G":
		l.end(rows)
	default:
		return false
	}
	return true
}

// spread puts left and right on one line of width, right-aligned, cutting
// left when they do not both fit.
func spread(left, right string, width int) string {
	rw := lipgloss.Width(right)
	if right == "" || rw+2 > width {
		return ansi.Truncate(left, width, "…")
	}
	left = ansi.Truncate(left, width-rw-1, "…")
	gap := width - lipgloss.Width(left) - rw
	return left + strings.Repeat(" ", max(1, gap)) + right
}

// spinnerFrame is the working spinner's frame at now.
func spinnerFrame(now time.Time) string {
	return workFrames[int(now.UnixMilli()/workFrame.Milliseconds())%len(workFrames)]
}

// wrapLines breaks s into lines of at most width cells, at most n of them
// (n <= 0: all).
func wrapLines(s string, width, n int) []string {
	if width <= 0 {
		return nil
	}
	var out []string
	for _, para := range strings.Split(strings.TrimRight(s, "\n"), "\n") {
		out = append(out, strings.Split(ansi.Wrap(para, width, ""), "\n")...)
	}
	if n > 0 && len(out) > n {
		out = out[:n]
		out[n-1] = ansi.Truncate(out[n-1]+" …", width, "…")
	}
	return out
}

// activityReport is what the session's agents are running.
func (m *UI) activityReport() (r workspace.ActivityReport) {
	act, ok := m.com.Workspace.(workspace.Activity)
	if !ok || m.session == nil {
		return r
	}
	return act.Activity(m.session.ID)
}
