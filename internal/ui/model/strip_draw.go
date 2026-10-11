package model

import (
	"fmt"
	"image"
	"strings"

	"charm.land/lipgloss/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/dev-dami/deck/internal/config"
)

// strip returns the sidebar's strip, building it from the saved layout the
// first time.
func (m *UI) strip() *paneStrip {
	if m.stripState == nil {
		var saved config.SidebarLayout
		if m.com != nil {
			if cfg := m.com.Config(); cfg != nil && cfg.Options != nil && cfg.Options.TUI != nil {
				saved = cfg.Options.TUI.SidebarLayout
			}
		}
		m.stripState = newPaneStrip(saved)
	}
	return m.stripState
}

// stripActive reports whether the sidebar is on screen.
func (m *UI) stripActive() bool {
	return m.state == uiChat && m.hasSession() && !m.isCompact
}

// sidebarWidth is the sidebar's width, its left padding included, in an
// app area appW cells wide. On ordinary terminals it is the classic
// narrow column; from sidebarWideBreakpoint it grows to fit the strip's
// columns side by side, up to sidebarMaxWidth.
func (m *UI) sidebarWidth(appW int) int {
	limit := m.sidebarMaxWidth(appW)
	if limit <= sidebarNarrowWidth {
		return sidebarNarrowWidth
	}
	return max(sidebarNarrowWidth, min(m.strip().fittedWidth(limit-1), limit-1)+1)
}

// sidebarMaxWidth is the widest the sidebar may get: a share of a wide
// terminal that still leaves the chat chatMinWidth cells.
func (m *UI) sidebarMaxWidth(appW int) int {
	if m.width < sidebarWideBreakpoint {
		return sidebarNarrowWidth
	}
	return max(sidebarNarrowWidth, min(appW*sidebarWidePercent/100, appW-chatMinWidth))
}

// stripMaxWidth is the widest the strip (the sidebar without its padding)
// can get at the current terminal width; columns are never wider.
func (m *UI) stripMaxWidth() int {
	return m.sidebarMaxWidth(m.width-2) - 1
}

// drawSidebar draws the sidebar: the logo when there is room for it, and
// the strip of panes below.
func (m *UI) drawSidebar(scr uv.Screen, area uv.Rectangle) {
	if m.session == nil {
		return
	}
	stripArea := area
	if area.Dy() >= sidebarLogoMinHeight && m.sidebarLogo != "" {
		h := lipgloss.Height(m.sidebarLogo)
		if h+1 < area.Dy()/2 {
			logoRect := image.Rect(area.Min.X, area.Min.Y, area.Max.X, area.Min.Y+h)
			uv.NewStyledString(
				lipgloss.NewStyle().MaxWidth(area.Dx()).MaxHeight(h).Render(m.sidebarLogo),
			).Draw(scr, logoRect)
			stripArea.Min.Y += h + 1
		}
	}
	m.layoutStrip(stripArea)
	m.drawStrip(scr)
}

// layoutStrip works out where columns and panes go in area: the visible
// columns, scrolled to the focus when it moved, and each pane's share of
// its column's height.
func (m *UI) layoutStrip(area uv.Rectangle) {
	s := m.strip()
	s.clampFocus()
	g := stripGeom{area: area}
	avail := area.Dx()
	if avail <= 0 || area.Dy() <= 0 || len(s.cols) == 0 {
		s.geo = g
		return
	}
	widths := s.widths(avail)
	if s.reveal {
		s.first = scrollToShow(widths, s.first, s.col, avail)
		s.reveal = false
	}
	s.first = clampFirst(widths, s.first, avail)
	count := fitCount(widths, s.first, avail)

	colsArea := area
	if (s.first > 0 || s.first+count < len(widths)) && colsArea.Dy() > 2 {
		g.indicator = image.Rect(area.Min.X, area.Max.Y-1, area.Max.X, area.Max.Y)
		colsArea.Max.Y--
	}

	x := area.Min.X
	for i := s.first; i < s.first+count; i++ {
		w := widths[i]
		c := s.cols[i]
		needs := make([]int, len(c.panes))
		for k, id := range c.panes {
			needs[k] = 1 + max(0, s.live[id].height(m, w))
		}
		alloc := distributeHeights(colsArea.Dy(), needs, c.row)
		cg := colGeom{index: i, x: x, w: w}
		y := colsArea.Min.Y
		for k, id := range c.panes {
			h := alloc[k]
			if h <= 0 {
				continue
			}
			cg.panes = append(cg.panes, paneGeom{
				id:     id,
				header: image.Rect(x, y, x+w, y+1),
				body:   image.Rect(x, y+1, x+w, y+h),
			})
			y += h
		}
		g.cols = append(g.cols, cg)
		x += w + stripGap
	}
	s.geo = g
}

// stripStyles are the strip's colours, taken from the theme.
type stripStyles struct {
	accent, title, muted, rule lipgloss.Style
}

func (m *UI) stripStyles() stripStyles {
	t := m.com.Styles
	return stripStyles{
		accent: lipgloss.NewStyle().Foreground(t.Header.Mark.GetForeground()).Bold(true),
		title:  lipgloss.NewStyle().Foreground(t.Section.Title.GetForeground()).Bold(true),
		muted:  lipgloss.NewStyle().Foreground(t.Dialog.SecondaryText.GetForeground()),
		rule:   lipgloss.NewStyle().Foreground(t.Section.Line.GetForeground()),
	}
}

// drawStrip paints the strip laid out by layoutStrip.
func (m *UI) drawStrip(scr uv.Screen) {
	s := m.strip()
	g := &s.geo
	st := m.stripStyles()
	if g.area.Dx() <= 0 || g.area.Dy() <= 0 {
		return
	}
	if len(s.cols) == 0 {
		hint := st.muted.Render(ansi.Truncate("No panes open · o reopens one", g.area.Dx(), "…"))
		uv.NewStyledString(hint).Draw(scr, g.area)
		m.drawStripChooser(scr, st)
		return
	}

	sidebarFocused := m.focus == uiFocusSidebar
	fid, _ := s.focused()
	for ci := range g.cols {
		c := &g.cols[ci]
		for pi := range c.panes {
			p := &c.panes[pi]
			pn := s.live[p.id]
			focused := p.id == fid
			bodyH := p.body.Dy()
			up, down := false, false
			if bodyH > 0 {
				content := pn.render(m, c.w, bodyH, focused && sidebarFocused)
				var lines []string
				if content != "" {
					lines = strings.Split(content, "\n")
				}
				p.lines = len(lines)
				if len(lines) > bodyH {
					off := max(0, min(s.scroll[p.id], len(lines)-bodyH))
					s.scroll[p.id] = off
					up, down = off > 0, off+bodyH < len(lines)
					lines = lines[off : off+bodyH]
				}
				for i, l := range lines {
					lines[i] = ansi.Truncate(l, c.w, "")
				}
				uv.NewStyledString(strings.Join(lines, "\n")).Draw(scr, p.body)
			}
			level := 0
			switch {
			case focused && sidebarFocused:
				level = 2
			case focused:
				level = 1
			}
			header := stripHeader(st, pn.title(m), pn.badge(m), c.w, level, up, down)
			uv.NewStyledString(header).Draw(scr, p.header)
		}
	}

	// Separators between the visible columns.
	top, bottom := g.area.Min.Y, g.area.Max.Y
	if !g.indicator.Empty() {
		bottom = g.indicator.Min.Y
	}
	if h := bottom - top; h > 0 {
		sep := st.rule.Render(strings.TrimSuffix(strings.Repeat("│\n", h), "\n"))
		for ci := 0; ci+1 < len(g.cols); ci++ {
			x := g.cols[ci].x + g.cols[ci].w + stripGap/2
			uv.NewStyledString(sep).Draw(scr, image.Rect(x, top, x+1, bottom))
		}
	}

	if !g.indicator.Empty() {
		count := len(g.cols)
		line := stripIndicator(st, len(s.cols), s.first, count, s.col, g.indicator.Dx())
		uv.NewStyledString(line).Draw(scr, g.indicator)
	}
	m.drawStripChooser(scr, st)
}

// stripHeader is a pane's header line: title and badge, a rule, the ↑↓
// marks of a scrolled body and the close glyph. level is 2 for the pane
// the user is in, 1 for the strip's focused pane while the user is
// elsewhere, 0 otherwise.
func stripHeader(st stripStyles, title, badge string, width, level int, up, down bool) string {
	if width <= 0 {
		return ""
	}
	titleSty := st.muted
	switch level {
	case 2:
		titleSty = st.accent
	case 1:
		titleSty = st.title
	}
	right := ""
	if up {
		right += "↑"
	}
	if down {
		right += "↓"
	}
	if right != "" {
		right += " "
	}
	right += "×"
	rightW := ansi.StringWidth(right)
	if rightW+1 >= width {
		return titleSty.Render(ansi.Truncate(title, width, "…"))
	}
	left := titleSty.Render(title)
	if badge != "" {
		left += " " + st.muted.Render(badge)
	}
	left = ansi.Truncate(left, width-rightW-1, "…")
	fill := width - ansi.StringWidth(left) - rightW
	rule := ""
	if fill >= 3 {
		rule = " " + st.rule.Render(strings.Repeat("─", fill-2)) + " "
	} else {
		rule = strings.Repeat(" ", max(0, fill))
	}
	closeSty := st.muted
	if level == 2 {
		closeSty = st.accent
	}
	return left + rule + st.muted.Render(strings.TrimSuffix(right, "×")) + closeSty.Render("×")
}

// stripIndicator is the row under the strip when columns are off screen:
// how many are hidden on each side and a dot per column, the visible ones
// filled and the focused one in the accent colour.
func stripIndicator(st stripStyles, total, first, count, focus, width int) string {
	left, right := "", ""
	if first > 0 {
		left = fmt.Sprintf("‹ %d", first)
	}
	if hidden := total - first - count; hidden > 0 {
		right = fmt.Sprintf("%d ›", hidden)
	}
	var dots []string
	for i := range total {
		dot := "○"
		if i >= first && i < first+count {
			dot = "●"
		}
		if i == focus {
			dots = append(dots, st.accent.Render(dot))
		} else {
			dots = append(dots, st.muted.Render(dot))
		}
	}
	mid := strings.Join(dots, " ")
	lw, rw, mw := ansi.StringWidth(left), ansi.StringWidth(right), ansi.StringWidth(mid)
	free := width - lw - rw - mw
	if free < 2 {
		mid, mw = "", 0
		free = width - lw - rw
	}
	if free < 0 {
		return ansi.Truncate(st.muted.Render(left+" "+right), width, "")
	}
	padL := free / 2
	padR := free - padL
	if mid == "" {
		padL, padR = free, 0
	}
	return st.muted.Render(left) + strings.Repeat(" ", padL) + mid + strings.Repeat(" ", padR) + st.muted.Render(right)
}

// stripChooserWidth caps the reopen list's width.
const stripChooserWidth = 30

// drawStripChooser draws the reopen list over the top of the strip and
// records where its rows are for the mouse.
func (m *UI) drawStripChooser(scr uv.Screen, st stripStyles) {
	s := m.strip()
	g := &s.geo
	g.chooser = image.Rectangle{}
	if s.chooser == nil {
		return
	}
	items := s.reopenList()
	w := min(stripChooserWidth, g.area.Dx())
	if w <= 0 || len(items) == 0 {
		return
	}
	pad := lipgloss.NewStyle().Width(w)
	lines := []string{pad.Render(st.accent.Render("Reopen pane"))}
	for i, id := range items {
		name := string(id)
		if p := s.live[id]; p != nil {
			name = p.title(m)
		}
		row := "  " + name
		sty := st.muted
		if i == s.chooser.sel {
			row = "› " + name
			sty = st.accent
		}
		lines = append(lines, pad.Render(sty.Render(ansi.Truncate(row, w, "…"))))
	}
	lines = append(lines,
		pad.Render(st.muted.Render(ansi.Truncate("enter open · esc close", w, "…"))),
		st.rule.Render(strings.Repeat("─", w)),
	)
	h := min(len(lines), g.area.Dy())
	rect := image.Rect(g.area.Min.X, g.area.Min.Y, g.area.Min.X+w, g.area.Min.Y+h)
	// Blank the cells under the list so the panes do not show through.
	blank := strings.TrimSuffix(strings.Repeat(strings.Repeat(" ", w)+"\n", h), "\n")
	uv.NewStyledString(blank).Draw(scr, rect)
	uv.NewStyledString(strings.Join(lines[:h], "\n")).Draw(scr, rect)
	g.chooser = image.Rect(rect.Min.X, rect.Min.Y+1, rect.Max.X, rect.Min.Y+1+len(items))
}
