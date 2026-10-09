package tui

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/dev-dami/deck/internal/session"
)

// Thread lifecycle in the UI: rename, pin, resolve, archive, delete; the
// composer draft each thread keeps; and which threads have news you have not
// seen.

// draft is what a thread's composer held when you left it.
type draft struct {
	text   string
	attach []attachment
}

// deleteArmFor is how long a first /delete waits for the second.
const deleteArmFor = 4 * time.Second

// sortRows puts pinned threads first and resolved ones last, keeping the
// order among the rest (so a row does not jump when its agents finish).
func sortRows(rows []session.ThreadInfo) {
	rank := func(t session.ThreadInfo) int {
		switch {
		case t.Pinned:
			return 0
		case t.Resolved:
			return 2
		}
		return 1
	}
	sort.SliceStable(rows, func(i, j int) bool { return rank(rows[i]) < rank(rows[j]) })
}

// leaveThread keeps the composer's text and attachments for the thread you
// leave; enterThread gives the next thread back its own.
func (m *model) leaveThread(id string) {
	if id == "" {
		return
	}
	if m.edit != nil {
		m.cancelEdit() // puts the draft from before the edit back in the composer
	}
	if m.comp.Empty() && len(m.attach) == 0 {
		delete(m.drafts, id)
		return
	}
	m.drafts[id] = draft{text: m.comp.Value(), attach: m.attach}
}

func (m *model) enterThread(id string) {
	d := m.drafts[id]
	delete(m.drafts, id)
	m.comp.SetValue(d.text)
	m.attach = d.attach
	delete(m.unread, id)
}

// markUnread notes news in a thread you are not looking at.
func (m *model) markUnread(threadID string) {
	if threadID != "" && threadID != m.sel {
		m.unread[threadID] = true
	}
}

// threadCommand runs /rename, /pin, /done, /archive and /delete on the
// selected thread.
func (m *model) threadCommand(name, arg string) []tea.Cmd {
	danger := func(s string) []tea.Cmd { return []tea.Cmd{m.toast.show(s, true, m.now)} }
	info := func(s string) []tea.Cmd { return []tea.Cmd{m.toast.show(s, false, m.now)} }
	th, ok := m.selThread()
	if !ok {
		return danger("no thread selected")
	}
	if name != "delete" {
		m.delArm = time.Time{}
	}
	var err error
	switch name {
	case "rename":
		if arg == "" {
			return danger("usage: /rename <title>")
		}
		err = m.mgr.RenameThread(th.ID, arg)
	case "pin":
		err = m.mgr.SetPinned(th.ID, !th.Pinned)
		if err == nil {
			m.comp.Reset()
			if th.Pinned {
				return info("unpinned")
			}
			return info("pinned to the top")
		}
	case "done":
		if th.Resolved {
			m.comp.Reset()
			return info("already resolved; a new message wakes it")
		}
		err = m.mgr.Resolve(th.ID)
		if err == nil {
			m.comp.Reset()
			return info("resolved · a new message wakes it")
		}
	case "archive":
		err = m.mgr.Archive(th.ID)
		if err == nil {
			m.comp.Reset()
			delete(m.drafts, th.ID)
			return info("archived · /resume brings it back")
		}
	case "delete":
		if m.delArm.IsZero() || m.now.Sub(m.delArm) > deleteArmFor || m.delThread != th.ID {
			m.delArm, m.delThread = m.now, th.ID
			m.comp.Reset()
			return []tea.Cmd{m.toast.showWarn("/delete again to delete “"+trunc(threadTitle(th), 24)+"” for good", m.now)}
		}
		m.delArm = time.Time{}
		err = m.mgr.Delete(th.ID)
		if err == nil {
			m.comp.Reset()
			delete(m.drafts, th.ID)
			return info("deleted")
		}
	}
	if err != nil {
		return danger(err.Error())
	}
	m.comp.Reset()
	return nil
}

// onThreadChanged re-reads the threads after a lifecycle change.
func (m *model) onThreadChanged(msg session.ThreadChangedMsg) {
	if msg.Gone {
		delete(m.unread, msg.ThreadID)
	}
	m.refresh()
	if i := m.selIndex(); i >= 0 {
		m.barY.Set(float64(i))
	}
}

// Sidebar marks: a resolved thread's status, a pinned thread, unread news.
const (
	glyphResolved = "✓"
	glyphPinned   = "⌃"
	glyphUnread   = "•"
)

// openSwitcher opens the picker on the thread list (ctrl+k).
func (m *model) openSwitcher() []tea.Cmd {
	if m.ov.open || m.prompt.active() || len(m.rows) == 0 {
		return nil
	}
	m.skipFight()
	p := &m.pick
	p.draft = m.comp.Value()
	m.comp.Reset()
	m.focus = focusComposer
	*p = picker{open: true, h: p.h, draft: p.draft, thread: m.sel, seq: p.seq, stage: pickThreads}
	m.menu.kind = 0
	m.menu.h.Set(0)
	m.syncPick()
	for i, it := range p.items { // start on the thread you are in
		if it.cur {
			p.sel = i
		}
	}
	return nil
}

// newPill is the "↓ 3 new" marker at the bottom right of a transcript you
// scrolled up in; G or a click on it goes back to the latest. Empty when the
// view follows the bottom.
func (m *model) newPill(v *threadView, vh int) string {
	if v.follow || len(v.lines) <= vh || v.top() >= len(v.lines)-vh {
		return ""
	}
	if n := len(v.items) - v.seen; n > 0 {
		return sAccent.Render("↓ " + plural(n, "new", "new"))
	}
	return sFaint.Render("↓ latest")
}

// turnFooter is the faint line that ends an agent's turn:
// "42s · 1.2k tokens · 3 files · $0.04". Empty when there is nothing to say.
func turnFooter(s *turnStats, end time.Time) string {
	var parts []string
	if !s.start.IsZero() && end.After(s.start) {
		parts = append(parts, fmtDur(end.Sub(s.start)))
	}
	if s.tokens > 0 {
		parts = append(parts, fmtTokens(s.tokens)+" tokens")
	}
	if n := len(s.files); n > 0 {
		parts = append(parts, plural(n, "file", "files"))
	}
	if c := fmtCost(s.cost); c != "" {
		parts = append(parts, c)
	}
	if len(parts) < 2 { // a bare duration is noise
		return ""
	}
	return strings.Join(parts, " "+glyphDot+" ")
}

func fmtDur(d time.Duration) string {
	switch {
	case d < time.Second:
		return "<1s"
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm %02ds", int(d.Minutes()), int(d.Seconds())%60)
	}
	return fmt.Sprintf("%dh %02dm", int(d.Hours()), int(d.Minutes())%60)
}

func fmtTokens(n int) string {
	if n < 1000 {
		return fmt.Sprintf("%d", n)
	}
	return strings.TrimSuffix(fmt.Sprintf("%.1f", float64(n)/1000), ".0") + "k"
}

// titleCmd keeps the terminal's title on what needs you: "deck · 2 working ·
// 1 needs you", or just "deck".
func (m *model) titleCmd() tea.Cmd {
	nWork, nNeed := 0, 0
	for _, t := range m.threads {
		need, work := t.Paused || t.Pending > 0, false
		for _, a := range t.Members {
			if working(a) {
				work = true
			}
		}
		switch {
		case need:
			nNeed++
		case work:
			nWork++
		}
	}
	title := "deck"
	if nWork > 0 {
		title += " · " + plural(nWork, "working", "working")
	}
	if nNeed > 0 {
		title += " · " + plural(nNeed, "needs you", "need you")
	}
	if title == m.winTitle {
		return nil
	}
	m.winTitle = title
	return tea.SetWindowTitle(title)
}

// notify raises a desktop notification while the terminal is in the
// background (and the bell is on: both are "get my attention").
func (m *model) notify(title, body string) tea.Cmd {
	if !m.blurred || !m.cfg.Bell {
		return nil
	}
	if m.opts.Notify != nil {
		return m.opts.Notify(title, body)
	}
	clean := func(s string) string {
		return strings.Map(func(r rune) rune {
			if r < 0x20 || r == 0x7f || r == ';' {
				return ' '
			}
			return r
		}, s)
	}
	title, body = clean(title), clean(body)
	return func() tea.Msg {
		// OSC 777 (foot, ghostty, wezterm, konsole…) and OSC 9 (iTerm2,
		// Windows Terminal ignores it); terminals skip the one they don't know.
		_, _ = os.Stdout.WriteString("\x1b]777;notify;" + title + ";" + body + "\x07\x1b]9;" + title + ": " + body + "\x07")
		return nil
	}
}

func threadTitleOf(m *model, id string) string {
	for _, t := range m.threads {
		if t.ID == id {
			return threadTitle(t)
		}
	}
	return ""
}

// keyGroups is what the ? overlay lists.
var keyGroups = []struct {
	title string
	keys  [][2]string
}{
	{"anywhere", [][2]string{
		{"ctrl+k", "switch thread"}, {"shift+tab", "cycle plan · ask · yolo"}, {"alt+m", "pick a model"},
		{"ctrl+c", "interrupt; twice quits"}, {"alt+↑↓", "previous / next thread"},
	}},
	{"composer", [][2]string{
		{"enter", "send"}, {"alt+enter", "new line"}, {"@name", "talk to an agent"}, {"/", "commands"},
		{"↑", "edit your last message"}, {"ctrl+v", "paste an image"}, {"tab · esc", "to the transcript"},
	}},
	{"transcript", [][2]string{
		{"enter", "fold or unfold"}, {"[ ]", "previous / next row"}, {"e", "edit a message of yours"},
		{"d", "changes"}, {"p", "pin"}, {"x", "resolve"}, {"r", "rename"}, {"i", "approval inbox"},
		{"g G", "top / bottom"}, {"y a n", "allow · always · deny"}, {"q", "quit"},
	}},
}

// keysLines is the ? overlay: every key, grouped, in two columns when wide.
func (m *model) keysLines(w, maxRows int) []string {
	var col []string
	var bounds []int // where each group starts
	for _, g := range keyGroups {
		bounds = append(bounds, len(col))
		col = append(col, "  "+sMuted.Render(g.title))
		for _, k := range g.keys {
			col = append(col, "    "+sFg.Render(padTo(k[0], 11))+sFaint.Render(k[1]))
		}
	}
	out := []string{ruleTitle("keys", w-1)}
	half := len(col) // the second column starts at the group start nearest the middle
	for _, b := range bounds[1:] {
		if abs(b-len(col)/2) < abs(half-len(col)/2) {
			half = b
		}
	}
	if w >= 60 && len(col) > maxRows-1 { // two columns when one does not fit
		cw := w / 2
		for i := 0; i < max(half, len(col)-half); i++ {
			l := strings.Repeat(" ", cw)
			if i < half {
				l = padTo(trunc(col[i], cw), cw)
			}
			if i+half < len(col) {
				l += trunc(col[i+half], w-cw)
			}
			out = append(out, l)
		}
	} else {
		for _, l := range col {
			out = append(out, trunc(l, w))
		}
	}
	if len(out) > maxRows {
		out = out[:maxRows]
	}
	return out
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
