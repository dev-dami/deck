package tui

import (
	"sort"
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
