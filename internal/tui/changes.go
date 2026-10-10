package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/dev-dami/deck/internal/session"
	"github.com/dev-dami/deck/internal/tui/theme"
)

// The changes panel lists every file that differs from a checkpoint (the work
// tree just before one of your messages), whoever changed it, with its diff,
// the agents that touched it, and undo. It rises from the composer like the
// inbox and fills the transcript.

// armFor is how long a first u or U waits for the second press.
const armFor = 4 * time.Second

type changesPanel struct {
	thread string
	since  string // the checkpoint asked for; "" means the first with a snapshot
	set    session.ChangeSet
	loaded bool
	err    string
	seq    int // results of older loads are dropped
	sel    int
	scroll int  // first diff line shown
	busy   bool // a revert is running

	armKey string // "u" or "U" pressed once, waiting for the second press
	armAt  time.Time

	undoID     string // the last revert, which z undoes
	undoThread string

	cacheKey string
	cache    []string // the rendered diff of the selected file
}

// changeSum is the header's summary of a thread's changes since its first
// checkpoint.
type changeSum struct {
	files, add, del int
}

type changesMsg struct {
	seq    int
	thread string
	set    session.ChangeSet
	err    error
}

type changeSumMsg struct {
	thread string
	seq    int
	set    session.ChangeSet
}

type revertDoneMsg struct {
	thread string
	id     string
	n      int
	undo   bool
	err    error
}

func sumOf(set session.ChangeSet) changeSum {
	return changeSum{files: len(set.Files), add: set.Added(), del: set.Removed()}
}

// ---- loading ----

func (m *model) openChanges() []tea.Cmd {
	th, ok := m.selThread()
	if !ok || len(th.Members) == 0 {
		return []tea.Cmd{m.toast.show("nothing has happened in this thread yet", false, m.now)}
	}
	if m.chg.thread != th.ID {
		m.chg = changesPanel{undoID: m.chg.undoID, undoThread: m.chg.undoThread}
	}
	m.chg.thread, m.chg.since = th.ID, ""
	m.chg.sel, m.chg.scroll, m.chg.armKey = 0, 0, ""
	m.openOverlay(ovChanges)
	return []tea.Cmd{m.loadChanges()}
}

func (m *model) loadChanges() tea.Cmd {
	c := &m.chg
	c.seq++
	c.loaded, c.err = false, ""
	seq, tid, since, mgr := c.seq, c.thread, c.since, m.mgr
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		set, err := mgr.Changes(ctx, tid, since)
		return changesMsg{seq: seq, thread: tid, set: set, err: err}
	}
}

func (m *model) onChanges(msg changesMsg) {
	c := &m.chg
	if msg.seq != c.seq || msg.thread != c.thread {
		return
	}
	prev := ""
	if f, ok := c.selFile(); ok {
		prev = f.Path
	}
	c.loaded, c.set, c.cacheKey = true, msg.set, ""
	if msg.err != nil {
		c.err = msg.err.Error()
		return
	}
	c.sel = 0
	for i, f := range c.set.Files {
		if f.Path == prev {
			c.sel = i
		}
	}
	if c.since == "" && c.set.Git {
		m.chgSum[c.thread] = sumOf(c.set)
	}
}

// refreshSum re-reads the header summary of a thread in the background.
func (m *model) refreshSum(threadID string) tea.Cmd {
	if !m.cfg.TrackChanges || threadID == "" {
		return nil
	}
	m.chgSumSeq[threadID]++
	seq, mgr := m.chgSumSeq[threadID], m.mgr
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		set, err := mgr.Changes(ctx, threadID, "")
		if err != nil {
			return nil
		}
		return changeSumMsg{thread: threadID, seq: seq, set: set}
	}
}

func (m *model) onChangeSum(msg changeSumMsg) {
	if msg.seq != m.chgSumSeq[msg.thread] {
		return
	}
	if !msg.set.Git {
		delete(m.chgSum, msg.thread)
		return
	}
	m.chgSum[msg.thread] = sumOf(msg.set)
}

// ---- reverting ----

func (c *changesPanel) selFile() (session.ChangedFile, bool) {
	if c.sel < 0 || c.sel >= len(c.set.Files) {
		return session.ChangedFile{}, false
	}
	return c.set.Files[c.sel], true
}

func (m *model) revertCmd(all bool) tea.Cmd {
	c := &m.chg
	var paths []string
	if all {
		for _, f := range c.set.Files {
			paths = append(paths, f.Paths()...)
		}
	} else if f, ok := c.selFile(); ok {
		paths = f.Paths()
	}
	n := len(c.set.Files)
	if !all {
		n = 1
	}
	if len(paths) == 0 {
		return nil
	}
	c.busy = true
	tid, since, mgr := c.thread, c.set.Since, m.mgr
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		id, err := mgr.Revert(ctx, tid, since, paths)
		return revertDoneMsg{thread: tid, id: id, n: n, err: err}
	}
}

func (m *model) undoRevertCmd() tea.Cmd {
	c := &m.chg
	c.busy = true
	tid, id, mgr := c.undoThread, c.undoID, m.mgr
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		return revertDoneMsg{thread: tid, id: id, undo: true, err: mgr.UndoRevert(ctx, tid, id)}
	}
}

func (m *model) onRevertDone(msg revertDoneMsg) []tea.Cmd {
	c := &m.chg
	c.busy = false
	if msg.err != nil {
		return []tea.Cmd{m.toast.show(msg.err.Error(), true, m.now)}
	}
	var cmds []tea.Cmd
	if msg.undo {
		c.undoID, c.undoThread = "", ""
		cmds = append(cmds, m.toast.show("undone: the newer versions are back", false, m.now))
	} else {
		c.undoID, c.undoThread = msg.id, msg.thread
		cmds = append(cmds, m.toast.show("put back "+plural(msg.n, "file", "files")+" "+glyphDot+" z undoes", false, m.now))
	}
	if m.ov.kind == ovChanges && c.thread == msg.thread {
		cmds = append(cmds, m.loadChanges())
	}
	return cmds
}

// onReverted shows a revert in the thread's transcript.
func (m *model) onReverted(msg session.RevertedMsg, replay bool) []tea.Cmd {
	it := newItem(kNotice)
	files := listFew(msg.Files, 3)
	switch {
	case msg.Undo:
		it.text = "↷ you undid putting back " + files
	case msg.Since != "":
		it.text = "↶ you put back " + files + " as before “" + trunc(msg.Since, 40) + "”"
	default:
		it.text = "↶ you put back " + files
	}
	m.view(msg.ThreadID).add(it)
	if replay {
		return nil
	}
	return []tea.Cmd{m.refreshSum(msg.ThreadID)}
}

func listFew(items []string, n int) string {
	if len(items) <= n {
		return strings.Join(items, ", ")
	}
	return strings.Join(items[:n], ", ") + fmt.Sprintf(" and %d more", len(items)-n)
}

// ---- keys ----

func (m *model) changesKey(key string) []tea.Cmd {
	c := &m.chg
	armed := c.armKey != "" && m.now.Sub(c.armAt) < armFor
	armKey := c.armKey
	c.armKey = "" // any key but the second press disarms
	page := max(1, m.g.trH/2)
	switch key {
	case "esc", "q":
		m.closeOverlay()
	case "up", "k":
		m.moveChange(-1)
	case "down", "j":
		m.moveChange(1)
	case "pgup", "ctrl+u":
		c.scroll = max(0, c.scroll-page)
	case "pgdown", "ctrl+d", " ":
		c.scroll += page // clamped when drawn
	case "home", "g":
		c.scroll = 0
	case "end", "G":
		c.scroll = 1 << 30
	case "[", "]":
		cps := c.set.Checkpoints
		if !c.loaded || len(cps) == 0 {
			return nil
		}
		i := checkpointIndex(cps, c.set.Since)
		if key == "[" {
			i--
		} else {
			i++
		}
		if i < 0 || i >= len(cps) {
			return nil
		}
		c.since, c.scroll = cps[i].EntryID, 0
		return []tea.Cmd{m.loadChanges()}
	case "r":
		return []tea.Cmd{m.loadChanges()}
	case "u", "U":
		if !c.loaded || c.busy || len(c.set.Files) == 0 {
			return nil
		}
		if !c.set.Git {
			return []tea.Cmd{m.toast.show("undo needs git: "+c.set.Note, true, m.now)}
		}
		if !armed || armKey != key {
			c.armKey, c.armAt = key, m.now
			return nil
		}
		return []tea.Cmd{m.revertCmd(key == "U")}
	case "z":
		if c.busy || c.undoID == "" || c.undoThread != c.thread {
			return []tea.Cmd{m.toast.show("nothing to undo", false, m.now)}
		}
		return []tea.Cmd{m.undoRevertCmd()}
	}
	return nil
}

func (m *model) moveChange(d int) {
	c := &m.chg
	if n := len(c.set.Files); n > 0 {
		c.sel = max(0, min(n-1, c.sel+d))
		c.scroll = 0
	}
}

func checkpointIndex(cps []session.Checkpoint, id string) int {
	for i, cp := range cps {
		if cp.EntryID == id {
			return i
		}
	}
	return 0
}

// ---- drawing ----

func (m *model) changesTitle() string {
	c := &m.chg
	parts := []string{"changes"}
	if cps := c.set.Checkpoints; c.loaded && len(cps) > 0 {
		i := checkpointIndex(cps, c.set.Since)
		since := "since “" + trunc(cps[i].Text, 28) + "”"
		if len(cps) > 1 {
			since += fmt.Sprintf(" (%d/%d)", i+1, len(cps))
		}
		parts = append(parts, since)
	}
	if c.loaded && len(c.set.Files) > 0 {
		parts = append(parts, plural(len(c.set.Files), "file", "files"),
			fmt.Sprintf("+%d %s%d", c.set.Added(), glyphMinus, c.set.Removed()))
	}
	return strings.Join(parts, " "+glyphDot+" ")
}

// changesLines draws the whole panel in exactly rows lines.
func (m *model) changesLines(w, rows int) []string {
	c := &m.chg
	out := []string{ruleTitle(m.changesTitle(), w-1)}
	body := rows - 3 // the title, and a blank line and the hints at the bottom
	var lines []string
	switch {
	case !c.loaded:
		lines = append(lines, "  "+sFaint.Render("reading changes…"))
	case c.err != "":
		lines = append(lines, "  "+sDanger.Render(trunc(c.err, w-3)))
	default:
		if c.set.Note != "" {
			lines = append(lines, "  "+sFaint.Render(trunc(c.set.Note, w-3)))
		}
		if len(c.set.Files) == 0 {
			lines = append(lines, "  "+sFaint.Render("no changes since that message"))
			break
		}
		listRows := min(len(c.set.Files), max(1, min(8, (body-len(lines))/3)))
		s, e := window(c.sel, len(c.set.Files), listRows)
		for i := s; i < e; i++ {
			lines = append(lines, m.changeRow(c.set.Files[i], i == c.sel, w))
		}
		lines = append(lines, " "+st(theme.Rule).Render(strings.Repeat(theme.GlyphRule, max(0, w-2))))
		diff := m.selDiff(w - 2)
		room := max(0, body-len(lines))
		c.scroll = max(0, min(c.scroll, len(diff)-room))
		for _, l := range diff[c.scroll:min(len(diff), c.scroll+room)] {
			lines = append(lines, " "+l)
		}
	}
	if len(lines) > body {
		lines = lines[:max(0, body)]
	}
	out = append(out, lines...)
	for len(out) < rows-2 {
		out = append(out, "")
	}
	return append(out, "", " "+m.changesHints(w-2))
}

func (m *model) changeRow(f session.ChangedFile, sel bool, w int) string {
	bar, path := " ", sFg
	if sel {
		bar, path = sAccent.Render(glyphBarSel), sBold
	}
	var mark string
	switch {
	case f.New:
		mark = sAdd.Render("A")
	case f.Deleted:
		mark = sDanger.Render("D")
	case f.OldPath != "":
		mark = sAccent.Render("R")
	default:
		mark = sMuted.Render("M")
	}
	right := diffStat(&f.FileDiff)
	if f.Binary {
		right = sFaint.Render("binary")
	}
	dots := ""
	for _, id := range f.Agents {
		dots += tintStyle(m.kindOf(id)).Render(theme.Mark(m.kindOf(id)))
	}
	if dots != "" {
		right = dots + "  " + right
	}
	name := f.Path
	if f.OldPath != "" {
		name = f.OldPath + " " + glyphArrow + " " + f.Path
	}
	room := w - sw(right) - 7
	left := " " + bar + mark + " " + path.Render(shortPath(name, max(4, room)))
	return rightAlign(left, right+" ", w)
}

// selDiff is the rendered diff of the selected file, cached: highlighting is
// too slow to redo every frame.
func (m *model) selDiff(w int) []string {
	c := &m.chg
	f, ok := c.selFile()
	if !ok {
		return nil
	}
	key := fmt.Sprintf("%d|%d|%d|%v", c.seq, c.sel, w, m.dark)
	if c.cacheKey == key {
		return c.cache
	}
	var lines []string
	switch {
	case f.Binary:
		lines = []string{sFaint.Render("binary file")}
	case len(f.Hunks) == 0 && f.New:
		lines = []string{sFaint.Render("new empty file")}
	case len(f.Hunks) == 0 && f.Deleted:
		lines = []string{sFaint.Render("deleted an empty file")}
	case len(f.Hunks) == 0 && f.OldPath != "":
		lines = []string{sFaint.Render("renamed, content unchanged")}
	case len(f.Hunks) == 0:
		lines = []string{sFaint.Render("file mode changed")}
	default:
		lines = renderDiff(&f.FileDiff, w, m.dark, 0)
	}
	c.cacheKey, c.cache = key, lines
	return lines
}

func (m *model) changesHints(w int) string {
	c := &m.chg
	if c.armKey != "" && m.now.Sub(c.armAt) < armFor {
		what := "all " + plural(len(c.set.Files), "file", "files")
		if f, ok := c.selFile(); ok && c.armKey == "u" {
			what = f.Path
		}
		head, tail := c.armKey+" again puts back "+what, " · other keys cancel"
		since := ""
		if cps := c.set.Checkpoints; len(cps) > 0 {
			// the quote gives way first, so the keys always show
			if room := min(20, w-sw(head)-sw(tail)-sw(" as before “”")); room >= 4 {
				since = " as before “" + trunc(cps[checkpointIndex(cps, c.set.Since)].Text, room) + "”"
			}
		}
		return sWarn.Render(trunc(head+since+tail, w))
	}
	if c.busy {
		return sFaint.Render("putting files back…")
	}
	hs := []string{"↑↓ file", "pgup/pgdn scroll"}
	if c.set.Git && len(c.set.Files) > 0 {
		hs = append(hs, "u undo file", "U undo all")
	}
	if c.undoID != "" && c.undoThread == c.thread {
		hs = append(hs, "z redo")
	}
	if len(c.set.Checkpoints) > 1 {
		hs = append(hs, "[ ] since")
	}
	return hintFit(w, append(hs, "r reload", "esc close")...)
}

// ---- header ----

// changeSumText is the header's summary for the selected thread: "3 files +40
// −12"; "" when there is nothing to show.
func (m *model) changeSumText() string {
	s, ok := m.chgSum[m.sel]
	if !ok || s.files == 0 || m.emptyVisible() {
		return ""
	}
	return sMuted.Render(plural(s.files, "file", "files")) + " " +
		sAdd.Render(fmt.Sprintf("+%d", s.add)) + " " + sDanger.Render(fmt.Sprintf("%s%d", glyphMinus, s.del))
}
