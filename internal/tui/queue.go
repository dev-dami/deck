package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/dev-dami/deck/internal/session"
)

// The queue: messages you posted to a busy agent wait here, one row each,
// above the composer. Click a row (or focus it with alt+up and press enter) to
// steer it into the running turn; ✕ (click, or delete when focused) drops it.

const maxQueueShown = 3

// steerDoneMsg is the result of a steer or cancel.
type steerDoneMsg struct {
	id  string
	err error
}

func (m *model) queueOf(thread string) []session.QueuedItem { return m.queue[thread] }

// queueRowCount is how many screen rows the queue of the selected thread takes.
func (m *model) queueRowCount() int {
	n := len(m.queueOf(m.sel))
	switch {
	case n == 0:
		return 0
	case n > maxQueueShown:
		return maxQueueShown + 1
	}
	return n
}

// queued reports whether the user message entryID still waits in the queue.
func (m *model) queued(thread, entryID string) bool {
	if entryID == "" {
		return false
	}
	for _, q := range m.queue[thread] {
		if q.EntryID == entryID && !q.Edit {
			return true
		}
	}
	return false
}

// onQueueChanged stores a thread's queue and drops state about items that left it.
func (m *model) onQueueChanged(msg session.QueueChangedMsg) {
	if len(msg.Items) == 0 {
		delete(m.queue, msg.ThreadID)
	} else {
		m.queue[msg.ThreadID] = msg.Items
	}
	live := map[string]bool{}
	for _, items := range m.queue {
		for _, q := range items {
			live[q.ID] = true
		}
	}
	for id := range m.steering {
		if !live[id] {
			delete(m.steering, id)
		}
	}
	if n := min(len(m.queueOf(m.sel)), maxQueueShown); m.qFocus >= n {
		m.qFocus = n - 1
	}
}

// queueRow draws one queue row and returns where its ✕ region starts (-1 when
// the row has none).
func (m *model) queueRow(w int, it session.QueuedItem, focused bool) (string, int) {
	mark := " "
	if focused {
		mark = sAccent.Render(glyphBarSel)
	}
	who := m.markR(m.kindOf(it.AgentID), tintStyle(m.kindOf(it.AgentID))) + tintStyle(m.kindOf(it.AgentID)).Render(" "+m.agentName(it.AgentID))
	if it.Steer || m.steering[it.ID] {
		return trunc(mark+" "+sWarn.Render("↪ steering ")+who+sWarn.Render("…"), w), -1
	}
	verb := "queued for "
	if it.Edit {
		verb = "queued edit for "
	}
	text := oneLine(it.Text)
	if text == "" && len(it.Images) > 0 {
		text = "(image)"
	}
	right := sFaint.Render("steer ⏎ ") + sFaint.Render(glyphDot+" ") + sMuted.Render("✕")
	if w < 44 {
		right = sMuted.Render("✕")
	}
	rw := sw(right)
	head := mark + " " + sMuted.Render("⏳ "+verb) + who + sFaint.Render(" "+glyphDot+" ")
	room := w - rw - 2 - sw(head)
	body := ""
	if room >= 3 {
		body = sFg.Render(trunc(text, room))
	}
	left := head + body
	if sw(left) > w-rw-1 {
		left = trunc(left, w-rw-1)
	}
	line := padTo(left, w-rw-1) + right + " "
	return line, w - rw - 1 + (rw - 3)
}

// queueLines draws the queue rows for the selected thread.
func (m *model) queueLines(w int) []string {
	items := m.queueOf(m.sel)
	var out []string
	for i, it := range items {
		if i >= maxQueueShown {
			out = append(out, "  "+sFaint.Render(trunc("+"+itoa(len(items)-maxQueueShown)+" more", w-2)))
			break
		}
		l, _ := m.queueRow(w, it, m.qFocus == i)
		out = append(out, l)
	}
	return out
}

// queueMouse handles a click on queue row i at column x of the main area.
func (m *model) queueMouse(i, x int) []tea.Cmd {
	items := m.queueOf(m.sel)
	if i < 0 || i >= len(items) || i >= maxQueueShown {
		return nil
	}
	it := items[i]
	_, xc := m.queueRow(m.g.mainW, it, false)
	if xc >= 0 && x >= xc {
		return m.cancelQueued(it)
	}
	return m.steerQueued(it)
}

func (m *model) steerQueued(it session.QueuedItem) []tea.Cmd {
	if it.Steer || m.steering[it.ID] {
		return nil
	}
	m.steering[it.ID] = true
	mgr, id := m.mgr, it.ID
	return []tea.Cmd{func() tea.Msg { return steerDoneMsg{id: id, err: mgr.Steer(id)} }}
}

func (m *model) cancelQueued(it session.QueuedItem) []tea.Cmd {
	if it.Steer || m.steering[it.ID] {
		return nil
	}
	mgr, id := m.mgr, it.ID
	return []tea.Cmd{func() tea.Msg { return steerDoneMsg{id: id, err: mgr.CancelQueued(id)} }}
}

func (m *model) onSteerDone(msg steerDoneMsg) []tea.Cmd {
	if msg.err == nil {
		return nil
	}
	delete(m.steering, msg.id)
	return []tea.Cmd{m.toast.show(msg.err.Error(), true, m.now)}
}

// queueKey handles keys while a queue row has the focus. It reports whether the
// key was used; any other key drops the focus and goes on to its normal job.
func (m *model) queueKey(msg tea.KeyMsg) (bool, []tea.Cmd) {
	items := m.queueOf(m.sel)
	n := min(len(items), maxQueueShown)
	if n == 0 {
		m.qFocus = -1
		return false, nil
	}
	m.qFocus = min(m.qFocus, n-1)
	switch msg.String() {
	case "alt+up":
		m.qFocus = max(0, m.qFocus-1)
	case "alt+down":
		m.qFocus++
		if m.qFocus >= n {
			m.qFocus = -1
		}
	case "esc":
		m.qFocus = -1
	case "enter":
		return true, m.steerQueued(items[m.qFocus])
	case "delete", "backspace", "ctrl+x":
		cmds := m.cancelQueued(items[m.qFocus])
		return true, cmds
	default:
		m.qFocus = -1
		return false, nil
	}
	return true, nil
}

// focusQueue is alt+up / alt+down from the composer. It reports whether there
// was a queue to move in.
func (m *model) focusQueue(dir int) bool {
	n := min(len(m.queueOf(m.sel)), maxQueueShown)
	if n == 0 {
		return false
	}
	switch {
	case m.qFocus < 0 && dir < 0:
		m.qFocus = n - 1
	case m.qFocus < 0:
		m.qFocus = 0
	default:
		m.qFocus = max(0, min(n-1, m.qFocus+dir))
	}
	return true
}

// ---- editing your own messages ----

// editState is the composer borrowed to edit an earlier message.
type editState struct {
	thread, entryID string
	orig            string
	draft           string
	draftAttach     []attachment
}

const editLabel = "editing your message · enter resend · esc cancel"

// lastUserItem is the newest message of the user that can be edited.
func lastUserItem(v *threadView) *item {
	if v == nil {
		return nil
	}
	for i := len(v.items) - 1; i >= 0; i-- {
		if it := v.items[i]; it.kind == kUser && it.entryID != "" {
			return it
		}
	}
	return nil
}

func (m *model) startEdit(it *item) []tea.Cmd {
	if it == nil || it.kind != kUser || it.entryID == "" {
		return []tea.Cmd{m.toast.show("that message can't be edited", false, m.now)}
	}
	if m.prompt.active() || m.pick.open || m.ov.open {
		return nil
	}
	if m.edit != nil { // already editing: switch to this message, keep the first draft
		m.edit.thread, m.edit.entryID, m.edit.orig = m.sel, it.entryID, it.text
	} else {
		m.edit = &editState{thread: m.sel, entryID: it.entryID, orig: it.text, draft: m.comp.Value(), draftAttach: m.attach}
	}
	m.attach = nil
	m.comp.SetValue(it.text)
	m.focus = focusComposer
	m.qFocus = -1
	return nil
}

func (m *model) cancelEdit() {
	e := m.edit
	if e == nil {
		return
	}
	m.edit = nil
	m.comp.SetValue(e.draft)
	m.attach = e.draftAttach
}

func (m *model) submitEdit() []tea.Cmd {
	e := m.edit
	text := strings.TrimSpace(m.comp.Value())
	if text == "" {
		return []tea.Cmd{m.toast.show("a message can't be empty; esc cancels", true, m.now)}
	}
	m.cancelEdit()
	if text == strings.TrimSpace(e.orig) {
		return nil
	}
	mgr := m.mgr
	return []tea.Cmd{func() tea.Msg { return sentMsg{err: mgr.Edit(e.thread, e.entryID, text)} }}
}

// applyEdit shows the new text of an edited message.
func (m *model) applyEdit(msg session.EditedMsg) {
	v := m.view(msg.ThreadID)
	for _, it := range v.items {
		if it.kind == kUser && it.entryID == msg.EntryID {
			it.text = msg.NewText
			it.edited = true
			it.bump()
			return
		}
	}
}
