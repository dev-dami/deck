package tui

import (
	"math"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/sahilm/fuzzy"

	"github.com/dev-dami/deck/internal/session"
	"github.com/dev-dami/deck/internal/tui/motion"
	"github.com/dev-dami/deck/internal/tui/theme"
)

const maxMenuRows = 7

type menuItem struct {
	label  string // matched and inserted
	glyph  string
	gStyle lipgloss.Style
	desc   string
	isNew  bool
	args   string // slash commands: argument placeholder, "" when none
	idx    []int  // matched rune indexes in label
}

// menu is the upward completion menu above the composer, for "@" and "/".
type menu struct {
	kind      byte // '@', '/', or 0 when closed
	items     []menuItem
	sel       int
	h         motion.Spring
	dismissed string
}

func (mn *menu) open() bool { return mn.kind != 0 }

func (mn *menu) visible() bool { return mn.open() || mn.h.Pos > 0.5 }

// syncMenu rebuilds the menu from the composer text.
func (m *model) syncMenu() {
	t := m.comp.Value()
	mn := &m.menu
	closeMenu := func() {
		mn.kind = 0
		mn.h.Set(0)
	}
	if m.prompt.active() || m.ov.open || t == "" || (t[0] != '@' && t[0] != '/') || t == mn.dismissed {
		if t != mn.dismissed {
			mn.dismissed = ""
		}
		closeMenu()
		return
	}
	tokEnd := strings.IndexAny(t, " \t\r\n")
	if tokEnd < 0 {
		tokEnd = len(t)
	}
	if m.comp.cur > len([]rune(t[:tokEnd])) {
		closeMenu()
		return
	}
	query := t[1:tokEnd]
	prevSel := ""
	if mn.open() && mn.sel >= 0 && mn.sel < len(mn.items) {
		prevSel = mn.items[mn.sel].label
	}
	var all []menuItem
	if t[0] == '@' {
		all = m.agentMenuItems()
	} else {
		for _, c := range Commands {
			all = append(all, menuItem{label: c.Name, desc: c.Desc, args: c.Args})
		}
	}
	mn.items = filterMenu(all, query)
	mn.kind = t[0]
	mn.sel = 0
	for i, it := range mn.items {
		if it.label == prevSel {
			mn.sel = i
		}
	}
	if len(mn.items) == 0 {
		closeMenu()
		mn.kind = 0
		return
	}
	mn.h.Set(float64(min(len(mn.items), maxMenuRows)))
}

func (m *model) agentMenuItems() []menuItem {
	var out []menuItem
	for _, a := range m.agents {
		if a.Status == session.StatusExited {
			continue
		}
		g, gs := m.statusGlyph(a)
		desc := oneLine(a.Title)
		if a.Name != a.Kind {
			desc = strings.TrimSpace(a.Kind + " " + glyphDot + " " + desc)
		}
		out = append(out, menuItem{label: a.Name, glyph: g, gStyle: gs, desc: desc})
	}
	for _, k := range m.kinds {
		if !m.newMode {
			if _, running := findRunning(m.agents, k); running {
				continue
			}
		}
		out = append(out, menuItem{label: k, glyph: "+", gStyle: sFaint, desc: "new agent", isNew: true})
	}
	return out
}

func filterMenu(all []menuItem, query string) []menuItem {
	if query == "" {
		return all
	}
	names := make([]string, len(all))
	for i, it := range all {
		names[i] = it.label
	}
	var out []menuItem
	for _, mt := range fuzzy.Find(query, names) {
		it := all[mt.Index]
		it.idx = mt.MatchedIndexes
		out = append(out, it)
	}
	return out
}

func (mn *menu) move(d int) {
	if len(mn.items) == 0 {
		return
	}
	mn.sel = (mn.sel + d + len(mn.items)) % len(mn.items)
}

// menuLines renders the menu rows that are currently revealed (bottom-anchored).
func (m *model) menuLines(w int) []string {
	mn := &m.menu
	if !mn.visible() || len(mn.items) == 0 {
		return nil
	}
	rows := min(len(mn.items), maxMenuRows)
	start := 0
	if mn.sel >= rows {
		start = mn.sel - rows + 1
	}
	labelW := 0
	for _, it := range mn.items {
		labelW = max(labelW, sw(it.label)+len(it.args)+1)
	}
	prefix := ""
	if mn.kind == '/' {
		prefix = "/"
	}
	var all []string
	for i := start; i < start+rows && i < len(mn.items); i++ {
		it := mn.items[i]
		bar := " "
		name := sMuted
		if i == mn.sel {
			bar = sAccent.Render(glyphBarSel)
			name = sBold
		}
		g := " "
		if it.glyph != "" {
			g = it.gStyle.Render(it.glyph)
		}
		label := highlightMatches(prefix+it.label, it.idx, len(prefix), name)
		if it.args != "" {
			label += sFaint.Render(" " + it.args)
		}
		line := " " + bar + g + " " + padTo(label, labelW+len(prefix)) + "  " + sFaint.Render(it.desc)
		all = append(all, trunc(line, w))
	}
	n := int(math.Round(mn.h.Pos))
	if !mn.open() && n < 1 {
		return nil
	}
	n = max(1, min(n, len(all)))
	return all[len(all)-n:]
}

// highlightMatches renders s with the matched rune indexes (offset by off) in accent.
func highlightMatches(s string, idx []int, off int, base lipgloss.Style) string {
	if len(idx) == 0 {
		return base.Render(s)
	}
	hit := map[int]bool{}
	for _, i := range idx {
		hit[i+off] = true
	}
	acc := base.Foreground(theme.Accent).Bold(true)
	var b strings.Builder
	for i, r := range []rune(s) {
		if hit[i] {
			b.WriteString(acc.Render(string(r)))
		} else {
			b.WriteString(base.Render(string(r)))
		}
	}
	return b.String()
}

// completeMenu applies the highlighted entry. It reports whether the entry was
// a no-argument command that should run right away.
func (m *model) completeMenu(execNow bool) (run bool) {
	mn := &m.menu
	if !mn.open() || mn.sel >= len(mn.items) {
		return false
	}
	it := mn.items[mn.sel]
	t := m.comp.Value()
	rest := ""
	if i := strings.IndexAny(t, " \t\r\n"); i >= 0 {
		rest = strings.TrimLeft(t[i:], " \t")
	}
	if mn.kind == '/' {
		if it.args == "" && execNow {
			m.comp.SetValue("/" + it.label)
			return true
		}
		if it.args == "" {
			m.comp.SetValue("/" + it.label)
			return false
		}
		m.comp.SetValue("/" + it.label + " " + rest)
		return false
	}
	m.comp.SetValue("@" + it.label + " " + rest)
	return false
}
