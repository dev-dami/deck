package tui

import (
	"errors"
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

// forkedMsg is the result of Fork.
type forkedMsg struct {
	info    session.ThreadInfo
	history []tea.Msg
	text    string
	err     error
}

// forkCmd forks the selected thread before the user message entryID, or
// copies all of it with "".
func (m *model) forkCmd(entryID string) []tea.Cmd {
	th, ok := m.selThread()
	if !ok {
		return nil
	}
	mgr := m.mgr
	return []tea.Cmd{func() tea.Msg {
		info, hist, text, err := mgr.Fork(th.ID, entryID)
		return forkedMsg{info: info, history: hist, text: text, err: err}
	}}
}

// onForked opens the new thread with its history; a fork at a message puts
// that message in the composer to change and send.
func (m *model) onForked(msg forkedMsg) []tea.Cmd {
	if msg.err != nil {
		return []tea.Cmd{m.toast.show(msg.err.Error(), true, m.now)}
	}
	m.hydrate(msg.info.ID, msg.history)
	m.threads = m.mgr.Threads()
	m.selectThread(msg.info.ID)
	m.focus = focusComposer
	if msg.text != "" {
		m.comp.SetValue(msg.text)
		return []tea.Cmd{m.toast.show("forked · change the message and send", false, m.now)}
	}
	return []tea.Cmd{m.toast.show("forked into a new thread", false, m.now)}
}

// movedMsg is the result of Move.
type movedMsg struct {
	info    session.ThreadInfo
	history []tea.Msg
	err     error
	note    string // the toast instead of "moved to …"
	// a refused /unisolate arms the second one
	refusedAt time.Time
	thread    string
}

func (m *model) moveCmd(dir string) []tea.Cmd {
	th, ok := m.selThread()
	if !ok {
		return nil
	}
	mgr := m.mgr
	return []tea.Cmd{func() tea.Msg {
		info, hist, err := mgr.Move(th.ID, dir)
		return movedMsg{info: info, history: hist, err: err}
	}}
}

// onMoved redraws the moved thread from its history (its agents restarted).
func (m *model) onMoved(msg movedMsg) []tea.Cmd {
	var ue session.UnappliedError
	if errors.As(msg.err, &ue) {
		m.unisoArm, m.unisoThread = msg.refusedAt, msg.thread
		return []tea.Cmd{m.toast.showWarn(plural(ue.Files, "changed file", "changed files")+" not applied · /apply first, or /unisolate again to drop them", m.now)}
	}
	if msg.err != nil {
		return []tea.Cmd{m.toast.show(msg.err.Error(), true, m.now)}
	}
	if msg.history != nil {
		delete(m.views, msg.info.ID)
		m.hydrate(msg.info.ID, msg.history)
	}
	m.refresh()
	text := "moved to " + tilde(msg.info.Dir, m.home)
	if len(msg.info.Members) > 0 {
		text += " · agents start fresh there, briefed on the thread"
	}
	if msg.note != "" {
		text = msg.note
	}
	return []tea.Cmd{m.toast.show(text, false, m.now)}
}

// glyphBranch marks an isolated thread (its own worktree and branch).
const glyphBranch = "⑂"

// appliedMsg is the result of ApplyIsolated.
type appliedMsg struct {
	files []string
	err   error
}

// unisolateArmFor is how long a refused /unisolate waits for the second.
const unisolateArmFor = 5 * time.Second

// isolateCommand runs /isolate, /apply and /unisolate. Git can take a moment,
// so each runs as a command; isolate and unisolate come back as a move.
func (m *model) isolateCommand(name string) []tea.Cmd {
	th, ok := m.selThread()
	if !ok {
		return []tea.Cmd{m.toast.show("no thread selected", true, m.now)}
	}
	mgr, id := m.mgr, th.ID
	switch name {
	case "isolate":
		return []tea.Cmd{
			m.toast.show("making a worktree…", false, m.now),
			func() tea.Msg {
				info, hist, err := mgr.Isolate(id)
				return movedMsg{info: info, history: hist, err: err, note: "isolated on " + info.Branch + " · from your last commit; /apply brings changes back"}
			},
		}
	case "apply":
		return []tea.Cmd{func() tea.Msg {
			files, err := mgr.ApplyIsolated(id)
			return appliedMsg{files: files, err: err}
		}}
	}
	discard := !m.unisoArm.IsZero() && m.now.Sub(m.unisoArm) < unisolateArmFor && m.unisoThread == id
	m.unisoArm = time.Time{}
	now := m.now
	return []tea.Cmd{func() tea.Msg {
		info, hist, err := mgr.Unisolate(id, discard)
		return movedMsg{info: info, history: hist, err: err, note: "back on the shared tree · worktree removed", refusedAt: now, thread: id}
	}}
}

func (m *model) onApplied(msg appliedMsg) []tea.Cmd {
	switch {
	case msg.err != nil:
		return []tea.Cmd{m.toast.show(msg.err.Error(), true, m.now)}
	case len(msg.files) == 0:
		return []tea.Cmd{m.toast.show("nothing new to apply", false, m.now)}
	}
	return []tea.Cmd{m.toast.show("applied "+plural(len(msg.files), "file", "files")+" to your tree, uncommitted", false, m.now)}
}
