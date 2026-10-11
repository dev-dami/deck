package notice

import (
	"image/color"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/dev-dami/deck/internal/ui/styles"
)

// Icon is a level's glyph.
func Icon(l Level) string {
	switch l {
	case Success:
		return "✓"
	case Warn:
		return "▲"
	case Error:
		return "✕"
	}
	return "◆"
}

// Color is a level's colour in the theme.
func Color(t *styles.Styles, l Level) color.Color {
	switch l {
	case Success:
		return t.Status.SuccessIndicator.GetBackground()
	case Warn:
		return t.Status.WarnIndicator.GetBackground()
	case Error:
		return t.Status.ErrorIndicator.GetBackground()
	}
	return t.Header.Mark.GetForeground()
}

// Stamp is when a notice came: "15:04" today, "Jan 2" before.
func Stamp(at, now time.Time) string {
	y1, m1, d1 := at.Date()
	y2, m2, d2 := now.Date()
	if y1 == y2 && m1 == m2 && d1 == d2 {
		return at.Format("15:04")
	}
	return at.Format("Jan 2")
}

// wrap breaks s into lines of at most width cells.
func wrap(s string, width int) []string {
	if width <= 0 {
		return nil
	}
	var out []string
	for _, para := range strings.Split(strings.TrimRight(s, "\n"), "\n") {
		w := ansi.Wrap(para, width, "")
		out = append(out, strings.Split(w, "\n")...)
	}
	return out
}

// Lines is a notice as the history draws it at width: the icon, time and
// title on the first line, the body wrapped under it, and the action, if
// any, last. selected marks the row under the cursor.
func Lines(t *styles.Styles, n Notice, width int, now time.Time, selected bool) []string {
	if width <= 0 {
		return []string{""}
	}
	tint := lipgloss.NewStyle().Foreground(Color(t, n.Level))
	muted := lipgloss.NewStyle().Foreground(t.Dialog.SecondaryText.GetForeground())
	text := lipgloss.NewStyle().Foreground(t.Dialog.NormalItem.GetForeground())
	if selected {
		text = text.Bold(true)
	}
	const indent = 2
	head := tint.Render(Icon(n.Level)) + " " + muted.Render(Stamp(n.Time, now)) + " "
	title, body := n.Title, n.Body
	if title == "" {
		title, body = firstLine(body)
	}
	room := max(1, width-lipgloss.Width(head))
	tl := wrap(title, room)
	lines := []string{head + text.Render(tl[0])}
	pad := strings.Repeat(" ", indent)
	for _, l := range tl[1:] {
		lines = append(lines, pad+text.Render(l))
	}
	if body != "" {
		for _, l := range wrap(body, max(1, width-indent)) {
			lines = append(lines, pad+muted.Render(l))
		}
	}
	if n.Action != nil && n.Action.Label != "" {
		act := "↵ " + n.Action.Label
		lines = append(lines, pad+tint.Render(ansi.Truncate(act, max(1, width-indent), "…")))
	}
	for i, l := range lines {
		lines[i] = ansi.Truncate(l, width, "…")
	}
	return lines
}

// clip keeps the first n of lines (at least one), marking a cut with "…".
func clip(lines []string, n, width int) []string {
	if len(lines) == 0 {
		return []string{""}
	}
	n = max(1, n)
	if len(lines) <= n {
		return lines
	}
	lines = lines[:n]
	lines[n-1] = ansi.Truncate(lines[n-1]+" …", width, "…")
	return lines
}

func firstLine(s string) (head, rest string) {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i], strings.TrimSpace(s[i+1:])
	}
	return s, ""
}

// HistoryKey is the key that opens the notification history.
const HistoryKey = "alt+n"

// CardWidth is the widest a toast gets, border included.
const CardWidth = 44

// cardLines is the most lines of text a toast shows.
const cardLines = 3

// Card is a notice drawn as a toast: a small bordered card in its level's
// colour, at most maxWidth (and CardWidth) wide and three lines of text
// tall. hovered draws it held.
func Card(t *styles.Styles, n Notice, maxWidth int, hovered bool) string {
	width := min(CardWidth, maxWidth)
	if width < 8 {
		return ""
	}
	col := Color(t, n.Level)
	border := lipgloss.RoundedBorder()
	if hovered {
		border = lipgloss.ThickBorder()
	}
	box := lipgloss.NewStyle().
		Border(border).
		BorderForeground(col).
		BorderBackground(t.Background).
		Background(t.Background).
		Padding(0, 1).
		Width(width)
	inner := width - box.GetHorizontalFrameSize()
	tint := lipgloss.NewStyle().Foreground(col).Background(t.Background)
	text := lipgloss.NewStyle().Foreground(t.Dialog.NormalItem.GetForeground()).Background(t.Background).Bold(true)
	muted := lipgloss.NewStyle().Foreground(t.Dialog.SecondaryText.GetForeground()).Background(t.Background)

	title, body := n.Title, n.Body
	if title == "" {
		title, body = firstLine(body)
	}
	icon := Icon(n.Level) + " "
	iw := lipgloss.Width(icon)
	room := max(1, inner-iw)
	hasAction := n.Action != nil && n.Action.Label != ""
	budget := cardLines
	if hasAction {
		budget-- // keep a line for the action
	}
	// The title wraps when there is no body to show under it (a status
	// message is one long line); otherwise it takes one line.
	var tl []string
	if body == "" {
		tl = clip(wrap(title, room), budget, room)
	} else {
		tl = []string{ansi.Truncate(title, room, "…")}
	}
	lines := []string{tint.Render(icon) + text.Render(tl[0])}
	for _, l := range tl[1:] {
		lines = append(lines, strings.Repeat(" ", iw)+text.Render(l))
	}
	if body != "" && len(lines) < budget {
		for _, l := range clip(wrap(body, inner), budget-len(lines), inner) {
			lines = append(lines, muted.Render(l))
		}
	}
	if len(lines) < cardLines && hasAction {
		lines = append(lines, tint.Render(ansi.Truncate("↵ "+n.Action.Label+"  ("+HistoryKey+")", inner, "…")))
	}
	return box.Render(strings.Join(lines, "\n"))
}
