package model

import (
	"image"
	"math"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"github.com/dev-dami/deck/internal/config"
	"github.com/dev-dami/deck/internal/ui/common"
	"github.com/dev-dami/deck/internal/ui/util"
	"github.com/dev-dami/deck/internal/workspace"
)

// stripSaveDelay debounces writing the layout: a burst of moves writes the
// config once.
const stripSaveDelay = time.Second

// stripSaveMsg fires stripSaveDelay after a layout change; only the latest
// one (by seq) writes.
type stripSaveMsg struct{ seq int }

// focusPane shows pane id and puts the user in it: the sidebar comes back
// if it was toggled off, a closed pane reopens as a new column right of
// the focused one, the sidebar takes focus and the strip scrolls to the
// pane. Other panes and keys use it to jump to a pane (ctrl+x → git).
func (m *UI) focusPane(id paneID) tea.Cmd {
	if m.strip().live[id] == nil {
		return nil
	}
	if m.state != uiChat || !m.hasSession() {
		return util.ReportInfo("The sidebar shows once a session is open")
	}
	var cmds []tea.Cmd
	if m.forceCompactMode && m.canToggleSidebar() {
		cmds = append(cmds, m.toggleCompactMode())
	}
	if m.isCompact {
		return util.ReportInfo("Widen the window to show the sidebar")
	}
	s := m.strip()
	s.chooser = nil
	if s.open(id) {
		cmds = append(cmds, m.stripChanged())
	}
	m.focusSidebar()
	return tea.Batch(cmds...)
}

// focusSidebar moves the keyboard into the strip.
func (m *UI) focusSidebar() {
	m.focus = uiFocusSidebar
	m.textarea.Blur()
	m.chat.Blur()
}

// leaveSidebar moves the keyboard from the strip to the editor or chat.
func (m *UI) leaveSidebar(to uiFocusState) tea.Cmd {
	m.strip().chooser = nil
	if m.activeInline != nil {
		m.focusActiveInline(to)
		return nil
	}
	m.focus = to
	if to == uiFocusEditor {
		m.chat.Blur()
		return m.textarea.Focus()
	}
	m.textarea.Blur()
	m.chat.Focus()
	return nil
}

// stripChanged follows a change to the strip's columns: the sidebar's
// width may change with them, and the layout is saved.
func (m *UI) stripChanged() tea.Cmd {
	m.updateLayoutAndSize()
	return m.saveStripLayout()
}

// saveStripLayout schedules writing the layout to the config.
func (m *UI) saveStripLayout() tea.Cmd {
	s := m.strip()
	s.saveSeq++
	seq := s.saveSeq
	return tea.Tick(stripSaveDelay, func(time.Time) tea.Msg { return stripSaveMsg{seq: seq} })
}

// handleStripSave writes the layout once changes have settled. The layout
// is captured here, on the UI loop; the command only does the IO.
func (m *UI) handleStripSave(msg stripSaveMsg) tea.Cmd {
	s := m.strip()
	if msg.seq != s.saveSeq || m.com == nil || m.com.Workspace == nil {
		return nil
	}
	return writeSidebarLayout(m.com.Workspace, s.layout())
}

// writeSidebarLayout saves layout in the user's global config.
func writeSidebarLayout(ws workspace.Workspace, layout config.SidebarLayout) tea.Cmd {
	return func() tea.Msg {
		if err := ws.SetConfigField(config.ScopeGlobal, "options.tui.sidebar_layout", layout); err != nil {
			return util.ReportError(err)()
		}
		return nil
	}
}

// resetStrip puts the default layout back.
func (m *UI) resetStrip() tea.Cmd {
	m.strip().reset()
	return tea.Batch(m.stripChanged(), util.ReportInfo("Sidebar layout reset"))
}

// openStripReopen opens the reopen list, bringing the sidebar back and
// into focus first when it has to (the palette's "reopen pane").
func (m *UI) openStripReopen() tea.Cmd {
	if m.state != uiChat || !m.hasSession() {
		return util.ReportInfo("The sidebar shows once a session is open")
	}
	var cmds []tea.Cmd
	if m.forceCompactMode && m.canToggleSidebar() {
		cmds = append(cmds, m.toggleCompactMode())
	}
	if m.isCompact {
		return util.ReportInfo("Widen the window to show the sidebar")
	}
	m.focusSidebar()
	cmds = append(cmds, m.openStripChooser())
	return tea.Batch(cmds...)
}

// openStripChooser opens the inline list of closed panes.
func (m *UI) openStripChooser() tea.Cmd {
	s := m.strip()
	if len(s.closed) == 0 {
		return util.ReportInfo("No closed panes to reopen")
	}
	s.chooser = &stripChooser{}
	return nil
}

// handleStripKey handles a key while the sidebar has focus: the reopen
// list when it is open, else the focused pane, else the strip. handled
// false leaves the key to the UI's global keys.
func (m *UI) handleStripKey(msg tea.KeyPressMsg) (bool, tea.Cmd) {
	s := m.strip()
	if s.chooser != nil {
		return m.handleStripChooserKey(msg)
	}
	if p := s.focusedPane(); p != nil {
		if handled, cmd := p.key(m, msg); handled {
			return true, cmd
		}
	}
	k := m.keyMap.Sidebar
	id, hasPane := s.focused()
	page := 1
	if g, ok := s.geo.paneGeomOf(id); ok {
		page = max(1, g.body.Dy()-1)
	}
	switch {
	case key.Matches(msg, k.Back):
		return true, m.leaveSidebar(uiFocusEditor)
	case key.Matches(msg, k.Left):
		if !s.focusColumn(-1) {
			return true, m.leaveSidebar(uiFocusMain)
		}
	case key.Matches(msg, k.Right):
		s.focusColumn(1)
	case key.Matches(msg, k.Down):
		if hasPane && s.canScrollBody(id, 1) {
			s.scrollBody(id, 1)
		} else {
			s.focusRow(1)
		}
	case key.Matches(msg, k.Up):
		if hasPane && s.canScrollBody(id, -1) {
			s.scrollBody(id, -1)
		} else {
			s.focusRow(-1)
		}
	case key.Matches(msg, k.PageDown):
		s.scrollBody(id, page)
	case key.Matches(msg, k.PageUp):
		s.scrollBody(id, -page)
	case key.Matches(msg, k.Top):
		s.scrollBody(id, math.MinInt32)
	case key.Matches(msg, k.Bottom):
		s.scrollBody(id, math.MaxInt32)
	case key.Matches(msg, k.Close):
		if _, ok := s.closeFocused(); ok {
			return true, m.stripChanged()
		}
	case key.Matches(msg, k.PaneLeft):
		if s.movePane(-1) {
			return true, m.stripChanged()
		}
	case key.Matches(msg, k.PaneRight):
		if s.movePane(1) {
			return true, m.stripChanged()
		}
	case key.Matches(msg, k.ColumnLeft):
		if s.moveColumn(-1) {
			return true, m.stripChanged()
		}
	case key.Matches(msg, k.ColumnRight):
		if s.moveColumn(1) {
			return true, m.stripChanged()
		}
	case key.Matches(msg, k.Narrow):
		if s.resizeColumn(-stripColumnStep, m.stripMaxWidth()) {
			return true, m.stripChanged()
		}
	case key.Matches(msg, k.Widen):
		if s.resizeColumn(stripColumnStep, m.stripMaxWidth()) {
			return true, m.stripChanged()
		}
	case key.Matches(msg, k.Reopen):
		return true, m.openStripChooser()
	default:
		return false, nil
	}
	return true, nil
}

// handleStripChooserKey drives the reopen list.
func (m *UI) handleStripChooserKey(msg tea.KeyPressMsg) (bool, tea.Cmd) {
	s := m.strip()
	k := m.keyMap.Sidebar
	items := s.reopenList()
	if len(items) == 0 {
		s.chooser = nil
		return false, nil
	}
	ch := s.chooser
	ch.sel = max(0, min(ch.sel, len(items)-1))
	switch {
	case key.Matches(msg, k.Back), key.Matches(msg, k.Reopen):
		s.chooser = nil
	case key.Matches(msg, k.Up):
		ch.sel = max(0, ch.sel-1)
	case key.Matches(msg, k.Down):
		ch.sel = min(len(items)-1, ch.sel+1)
	case key.Matches(msg, k.Select):
		return true, m.reopenPane(items[ch.sel])
	default:
		return false, nil
	}
	return true, nil
}

// reopenPane reopens a closed pane from the list and focuses it.
func (m *UI) reopenPane(id paneID) tea.Cmd {
	s := m.strip()
	s.chooser = nil
	if s.open(id) {
		return m.stripChanged()
	}
	return nil
}

// moveStripColumn moves the strip's focused column from anywhere (alt+h,
// alt+l). It reports whether the key applied.
func (m *UI) moveStripColumn(dir int) bool {
	if !m.stripActive() {
		return false
	}
	m.strip().focusColumn(dir)
	return true
}

// handleStripClick handles a click in the sidebar: it focuses the sidebar,
// then a click on a pane focuses that pane and is passed on to it (on its
// header's × it closes the pane), and a click on the side indicator
// scrolls the strip.
func (m *UI) handleStripClick(msg tea.MouseClickMsg) tea.Cmd {
	s := m.strip()
	m.focusSidebar()
	if msg.Button != tea.MouseLeft {
		return nil
	}
	x, y := msg.X, msg.Y
	g := &s.geo
	if s.chooser != nil {
		if image.Pt(x, y).In(g.chooser) {
			items := s.reopenList()
			if i := y - g.chooser.Min.Y; i >= 0 && i < len(items) {
				return m.reopenPane(items[i])
			}
		}
		s.chooser = nil
		return nil
	}
	if !g.indicator.Empty() && y == g.indicator.Min.Y {
		if x < g.indicator.Min.X+g.indicator.Dx()/2 {
			s.scrollSideways(-1)
		} else {
			s.scrollSideways(1)
		}
		return nil
	}
	c, p, ok := g.paneAt(x, y)
	if !ok {
		return nil
	}
	s.focusOn(p.id)
	if y == p.header.Min.Y {
		if x == c.x+c.w-1 {
			if _, ok := s.closeFocused(); ok {
				return m.stripChanged()
			}
		}
		return nil
	}
	return s.live[p.id].click(m, x-p.body.Min.X, y-p.body.Min.Y+s.scroll[p.id])
}

// handleStripWheel scrolls what is under the pointer: vertically the pane
// body (the pane's own wheel first), sideways (shift+wheel or a horizontal
// wheel) the strip.
func (m *UI) handleStripWheel(msg common.CoalescedWheelMsg) {
	s := m.strip()
	dx, dy := int(msg.DeltaX), int(msg.DeltaY)
	if msg.Mouse.Mod&tea.ModShift != 0 && dx == 0 {
		dx, dy = dy, 0
	}
	switch {
	case dx < 0:
		s.scrollSideways(-1)
	case dx > 0:
		s.scrollSideways(1)
	}
	if dy == 0 {
		return
	}
	_, p, ok := s.geo.paneAt(msg.Mouse.X, msg.Mouse.Y)
	if !ok {
		return
	}
	if w, ok := s.live[p.id].(paneWheeler); ok && w.wheel(m, dy) {
		return
	}
	s.scrollBody(p.id, dy)
}
