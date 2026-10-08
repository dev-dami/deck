package tui

import (
	"math"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/dev-dami/deck/internal/agent"
	"github.com/dev-dami/deck/internal/tui/motion"
	"github.com/dev-dami/deck/internal/tui/theme"
)

func lipglossFg(c lipgloss.Color) lipgloss.Style { return lipgloss.NewStyle().Foreground(c) }

// displayName maps protocol tool names to the short names users know.
func displayName(n string) string {
	switch n {
	case "command_execution", "commandExecution", "shell", "local_shell":
		return "Bash"
	case "file_change", "fileChange", "apply_patch":
		return "Edit"
	case "":
		return "tool"
	}
	return n
}

// renderFold renders a one-line row plus its animated body.
func (it *item) renderFold(c *rctx) []string {
	head := it.header(c)
	body := it.bodyLines(c)
	if it.expanded {
		it.h.Set(float64(len(body)))
	} else {
		it.h.Set(0)
	}
	n := int(math.Round(it.h.Pos))
	if n > len(body) {
		n = len(body)
	}
	out := make([]string, 0, 1+n)
	out = append(out, head)
	for _, l := range body[:n] {
		out = append(out, "    "+l)
	}
	return out
}

func (it *item) header(c *rctx) string {
	m := c.m
	switch it.kind {
	case kReasoning:
		t := "∴ thinking…"
		if it.expanded {
			t = "∴ thinking"
		}
		return "  " + sItalic.Render(t)

	case kExit:
		first := strings.SplitN(strings.TrimSpace(clean(it.text)), "\n", 2)[0]
		s := glyphErr + " exited · " + first
		tail := ""
		if it.foldable() {
			g := theme.GlyphFolded
			if it.expanded {
				g = theme.GlyphOpen
			}
			tail = " " + sFaint.Render(g)
		}
		return "  " + sDanger.Render(trunc(s, c.w-4)) + tail

	case kTool:
		t := it.tool
		var glyph string
		switch {
		case t.Status == agent.ToolError:
			glyph = sDanger.Render(glyphErr)
		case t.Status == agent.ToolRunning:
			glyph = sAccent.Render(motion.NewSpinner().Frame(m.now))
		case it.expanded:
			glyph = sMuted.Render(theme.GlyphOpen)
		default:
			glyph = sFaint.Render(theme.GlyphFolded)
		}
		name := displayName(t.Name)
		nameStyle, titleStyle := sMuted, m.dimTitle()
		switch t.Status {
		case agent.ToolRunning:
			nameStyle, titleStyle = sFg, sMuted
		case agent.ToolError:
			nameStyle, titleStyle = sDanger, sMuted
		}
		stats := diffStat(it.diff)
		used := 2 + 1 + 1 + sw(name) + 1
		statsW := 0
		if stats != "" {
			statsW = sw(stats) + 1
		}
		title := trunc(oneLine(t.Title), max(0, c.w-used-statsW))
		line := "  " + glyph + " " + nameStyle.Render(name)
		if title != "" {
			line += " " + titleStyle.Render(title)
		}
		if stats != "" {
			line += " " + stats
		}
		return line
	}
	return ""
}

// bodyLines is the (capped) content shown when the row is expanded.
func (it *item) bodyLines(c *rctx) []string {
	bw := c.w - 4
	key := lineCacheKey{w: c.w, ver: it.ver, dark: c.m.dark}
	if it.body.ok && it.body.key == key {
		return it.body.lines
	}
	var ls []string
	switch it.kind {
	case kTool:
		switch {
		case it.diff != nil:
			ls = renderDiff(it.diff, bw, c.m.dark, bodyCap)
		case strings.TrimSpace(it.tool.Output) != "":
			ls = renderOutput(it.tool.Output, bw, bodyCap)
		case it.tool.Status == agent.ToolRunning:
			ls = []string{sFaint.Render("running…")}
		default:
			ls = []string{sFaint.Render("(no output)")}
		}
	case kReasoning:
		var all []string
		for _, l := range wrapText(it.text, bw) {
			all = append(all, sItalic.Render(l))
		}
		ls = capLinesWithMore(all, bodyCap)
	case kExit:
		parts := strings.SplitN(strings.TrimSpace(clean(it.text)), "\n", 2)
		if len(parts) == 2 {
			ls = renderOutput(parts[1], bw, bodyCap)
		}
	}
	if len(ls) == 0 {
		ls = []string{sFaint.Render("(empty)")}
	}
	it.body = lineCache{key: key, lines: ls, ok: true}
	return ls
}

// toggle expands or collapses a foldable row.
func (m *model) toggle(it *item, w int) {
	if it == nil || !it.foldable() {
		return
	}
	it.expanded = !it.expanded
	if it.kind == kRelay { // no animated body: the quote just grows
		it.bump()
		return
	}
	if it.expanded {
		it.h.Set(float64(len(it.bodyLines(m.newRctx(w, m.curThread())))))
	} else {
		it.h.Set(0)
	}
}
