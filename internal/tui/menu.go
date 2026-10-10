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
	kind   string // agent kind: tints the label
	args   string // slash commands: argument placeholder, "" when none
	idx    []int  // matched rune indexes in label
	val    string // model picker: the id (or member id) a row stands for
	tag    string // model picker: faint tag at the right edge
	cur    bool   // model picker: the model in use now
	free   bool   // model picker: the typed text, as a model id
	path   string // @file: what goes after "@" when picked
	note   string // model picker: shown after desc, only when both fit whole
}

// menu is the upward completion menu above the composer, for "@" and "/".
type menu struct {
	kind      byte // '@', '/', or 0 when closed
	items     []menuItem
	sel       int
	h         motion.Spring
	dismissed string
	// from, to is the "@word" being completed mid-message (rune range); 0, 0
	// when the menu completes the first word.
	from, to int
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
	if !(m.prompt.active() || m.ov.open || m.pick.open || t == "" || t == mn.dismissed) {
		if from, to, q, ok := atToken(m.comp.text, m.comp.cur); ok { // @file mid-message
			mn.items = m.fileItems(q)
			if len(mn.items) == 0 {
				closeMenu()
				return
			}
			mn.kind, mn.from, mn.to = '@', from, to
			mn.sel = min(mn.sel, len(mn.items)-1)
			mn.h.Set(float64(min(len(mn.items), maxMenuRows)))
			return
		}
	}
	mn.from, mn.to = 0, 0
	if m.prompt.active() || m.ov.open || m.pick.open || t == "" || (t[0] != '@' && t[0] != '/') || t == mn.dismissed {
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
		all = m.atMenuItems()
	} else {
		for _, c := range Commands {
			all = append(all, menuItem{label: c.Name, desc: c.Desc, args: c.Args})
		}
	}
	mn.items = filterMenu(all, query)
	if t[0] == '@' { // files after the agents, once something is typed
		mn.items = append(mn.items, m.fileItems(query)...)
	}
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

// statusWord says what a member is doing, for the @ menu.
func statusWord(a session.AgentInfo) string {
	switch a.Status {
	case session.StatusWorking:
		return "working"
	case session.StatusTool:
		return "running a tool"
	case session.StatusApproval:
		return "waiting for you"
	case session.StatusError:
		return "error"
	}
	return "idle"
}

// atMenuItems are the members of the current thread first, then @all (with two
// or more), then the kinds the thread has no live member of: "add codex".
func (m *model) atMenuItems() []menuItem {
	var out []menuItem
	th, _ := m.selThread()
	have := map[string]bool{}
	live := 0
	for _, a := range th.Members {
		if a.Status == session.StatusExited {
			continue
		}
		live++
		have[a.Kind] = true
		g, gs := m.statusGlyph(a)
		desc := statusWord(a)
		if a.Name != a.Kind {
			desc = a.Kind + " " + glyphDot + " " + desc
		}
		out = append(out, menuItem{label: a.Name, glyph: g, gStyle: gs, desc: desc, kind: a.Kind})
	}
	if live >= 2 {
		out = append(out, menuItem{label: "all", glyph: theme.GlyphAgent, gStyle: sAccent, desc: "everyone in this thread"})
	}
	for _, k := range m.kinds {
		if have[k] {
			continue
		}
		desc := "add " + k
		if len(th.Members) == 0 {
			desc = "start with " + k
		}
		out = append(out, menuItem{label: k, glyph: "+", gStyle: sFaint, desc: desc, isNew: true, kind: k})
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
		if it.kind != "" {
			name = tintStyle(it.kind)
		}
		if i == mn.sel {
			bar = sAccent.Render(glyphBarSel)
			name = name.Bold(true)
			if it.kind == "" {
				name = sBold
			}
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
	if it.path != "" {
		from, to := mn.from, mn.to
		if from == 0 && to == 0 { // the first word: up to its end
			to = len([]rune(m.comp.Value()))
			if i := strings.IndexAny(m.comp.Value(), " \t\r\n"); i >= 0 {
				to = len([]rune(m.comp.Value()[:i]))
			}
		}
		m.insertMention(from, to, it.path)
		return false
	}
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
