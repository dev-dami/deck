package dialog

import (
	"strings"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/dev-dami/deck/internal/ui/styles"
)

// MoreKey shows every key hint of a dialog that has more than fit.
var MoreKey = key.NewBinding(key.WithKeys("?"), key.WithHelp("?", "more"))

// renderFittedHelp renders key hints at contentWidth (the dialog's inner
// width) without ever cutting one off with an ellipsis. Hints go in order,
// most important first: those that fit on one line, then "? more" for the
// rest. With all set, or no more key (a step where ? would be typed into a
// field), every hint shows, wrapped over as many lines as it takes.
func renderFittedHelp(t *styles.Styles, h *help.Model, bindings []key.Binding, more *key.Binding, all bool, contentWidth int) string {
	width := max(1, contentWidth-t.Dialog.HelpView.GetHorizontalFrameSize())
	sep := h.Styles.ShortSeparator.Inline(true).Render(h.ShortSeparator)
	seg := func(kb key.Binding) string {
		return h.Styles.ShortKey.Inline(true).Render(kb.Help().Key) + " " +
			h.Styles.ShortDesc.Inline(true).Render(kb.Help().Desc)
	}
	var segs []string
	for _, kb := range bindings {
		if kb.Enabled() && kb.Help().Key != "" {
			segs = append(segs, seg(kb))
		}
	}
	if !all && more != nil {
		tail := seg(*more)
		line, total := "", 0
		for i, s := range segs {
			w := lipgloss.Width(s)
			if total > 0 {
				w += lipgloss.Width(sep)
			}
			room := width
			if i < len(segs)-1 {
				room -= lipgloss.Width(sep) + lipgloss.Width(tail) // keep room for "? more"
			}
			if total+w > room {
				if total > 0 {
					line += sep
				}
				return t.Dialog.HelpView.Render(line + tail)
			}
			if total > 0 {
				line += sep
			}
			line += s
			total += w
		}
		return t.Dialog.HelpView.Render(line)
	}
	// Every hint, packed into lines; one wider than a line is wrapped.
	var lines []string
	line, total := "", 0
	for _, s := range segs {
		w := lipgloss.Width(s)
		switch {
		case total == 0:
		case total+lipgloss.Width(sep)+w <= width:
			line += sep
			total += lipgloss.Width(sep)
		default:
			lines = append(lines, line)
			line, total = "", 0
		}
		if w > width {
			s = ansi.Wrap(s, width, "")
		}
		line += s
		total += w
	}
	if line != "" {
		lines = append(lines, line)
	}
	return t.Dialog.HelpView.Render(strings.Join(lines, "\n"))
}

// wrapText wraps str to width and renders each line with style, so a long
// sentence takes more lines instead of being cut off.
func wrapText(style lipgloss.Style, str string, width int) []string {
	if width <= 0 {
		return nil
	}
	w := max(1, width-style.GetHorizontalFrameSize())
	parts := strings.Split(ansi.Wrap(str, w, ""), "\n")
	for i, p := range parts {
		parts[i] = style.Render(p)
	}
	return parts
}

// hardLines splits a word that must stay whole, a fingerprint say, into
// lines of width cells: every character of it shows, none is replaced by an
// ellipsis.
func hardLines(s string, width int) []string {
	if width <= 0 || ansi.StringWidth(s) <= width {
		return []string{s}
	}
	return strings.Split(ansi.Hardwrap(s, width, false), "\n")
}
