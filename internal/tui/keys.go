package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/dev-dami/deck/internal/agent"
	"github.com/dev-dami/deck/internal/session"
)

type quitDisarmMsg struct{}

// ---- approval prompts in the composer ----

type promptKind int

const (
	pNone promptKind = iota
	pReason
	pEdit
)

// promptMode borrows the composer to collect a deny reason or an edited command.
type promptMode struct {
	kind promptKind
	p    *pendingApproval
}

func (p promptMode) active() bool { return p.kind != pNone }

func (p promptMode) glyph() string {
	if p.kind == pEdit {
		return "✎ "
	}
	return "✗ "
}

func (p promptMode) label(m *model) string {
	name := ""
	if p.p != nil {
		name = m.agentName(p.p.req.AgentID)
	}
	if p.kind == pEdit {
		return "edit for " + name + " · enter sends your version, esc cancels"
	}
	return "deny " + name + " · optional reason, enter sends, esc cancels"
}

func (p promptMode) placeholder() string {
	if p.kind == pEdit {
		return "what should it do instead?"
	}
	return "reason (optional)"
}

// ---- key dispatch ----

func (m *model) handleKey(msg tea.KeyMsg) (cmds []tea.Cmd) {
	key := msg.String()
	defer func() {
		m.settleComposer()
	}()

	if key == "ctrl+c" {
		if m.pick.open {
			m.closePicker()
			return nil
		}
		return m.ctrlC()
	}
	if msg.Paste {
		if m.ov.open {
			return nil
		}
		m.focus = focusComposer
		m.pasted(string(msg.Runes))
		return nil
	}
	// A paste that arrives as key presses must not send: see burstKey.
	at := m.keyNow()
	defer func() { m.lastKeyAt = m.keyNow() }()
	if !m.ov.open && !m.pick.open && !m.prompt.active() && m.focus == focusComposer {
		consumed, c := m.burstKey(msg, at)
		cmds = append(cmds, c...)
		if consumed {
			return cmds
		}
	} else {
		m.prevTyped, m.prevPasteKey = false, false
	}
	if (key == "ctrl+v" || key == "alt+v") && !m.ov.open && !m.pick.open && !m.prompt.active() && m.edit == nil {
		return append(cmds, m.clipCmd())
	}
	if m.edit != nil && key == "esc" && !m.menu.open() && !m.ov.open && !m.pick.open && !m.prompt.active() {
		m.cancelEdit()
		return cmds
	}
	if m.qFocus >= 0 && !m.ov.open && !m.pick.open && !m.prompt.active() && m.focus == focusComposer {
		if used, c := m.queueKey(msg); used {
			return append(cmds, c...)
		}
	}
	if m.ov.open {
		return append(cmds, m.overlayKey(key)...)
	}
	if m.pick.open {
		return append(cmds, m.pickKey(msg)...)
	}
	// shift+tab cycles the permission mode from anywhere except inside a menu or
	// a prompt; alt+m is the model picker's key.
	free := !m.prompt.active() && !(m.menu.open() && m.focus == focusComposer)
	if key == "shift+tab" && free {
		return append(cmds, m.cycleMode()...)
	}
	if key == "alt+m" && free {
		return append(cmds, m.openPicker("")...)
	}
	if key == "ctrl+k" && free {
		return append(cmds, m.openSwitcher()...)
	}
	if m.prompt.active() {
		return append(cmds, m.promptKey(msg)...)
	}
	if m.menu.open() && m.focus == focusComposer {
		if handled, c := m.menuKey(msg); handled {
			return append(cmds, c...)
		}
	}
	if m.focus == focusTranscript {
		return append(cmds, m.transcriptKey(msg)...)
	}
	return append(cmds, m.composerKey(msg)...)
}

// settleComposer refreshes composer-derived state after a key.
func (m *model) settleComposer() {
	m.g = m.geo()
	m.syncMenu()
	m.syncPick()
}

// ctrlC interrupts everything in the thread that is working; failing that it
// clears the composer; failing that it arms (then performs) quit.
func (m *model) ctrlC() []tea.Cmd {
	if m.edit != nil {
		m.cancelEdit()
		return nil
	}
	if th, ok := m.selThread(); ok {
		var ids []string
		for _, a := range th.Members {
			if busy(a) {
				ids = append(ids, a.ID)
			}
		}
		if len(ids) > 0 {
			m.quitArmed = time.Time{}
			return []tea.Cmd{m.interruptCmd(ids...)}
		}
	}
	if !m.comp.Empty() {
		m.comp.Reset()
		return nil
	}
	if !m.quitArmed.IsZero() && m.now.Sub(m.quitArmed) < time.Second {
		return []tea.Cmd{tea.Quit}
	}
	m.quitArmed = m.now
	return []tea.Cmd{m.toast.show("press ^c again to quit", false, m.now)}
}

func (m *model) moveSel(d int) {
	if len(m.rows) == 0 {
		return
	}
	idx := max(0, m.selIndex())
	ni := max(0, min(len(m.rows)-1, idx+d))
	m.selectThread(m.rows[ni].ID)
}

// approvalKey handles y/n/a/e for the selected thread's first pending request.
func (m *model) approvalKey(key string) (bool, []tea.Cmd) {
	pend := m.pendingFor(m.sel)
	if len(pend) == 0 {
		return false, nil
	}
	p := pend[0]
	switch key {
	case "y":
		return true, []tea.Cmd{m.respond(p, agent.Decision{Allow: true})}
	case "a":
		return true, []tea.Cmd{m.respond(p, agent.Decision{Allow: true, Always: true})}
	case "n":
		m.prompt = promptMode{kind: pReason, p: p}
		m.comp.Reset()
		m.focus = focusComposer
		return true, nil
	case "e":
		m.prompt = promptMode{kind: pEdit, p: p}
		m.comp.Reset()
		if p.req.Kind == agent.ApproveCommand {
			m.comp.SetValue(p.req.Summary)
		}
		m.focus = focusComposer
		return true, nil
	}
	return false, nil
}

func (m *model) promptKey(msg tea.KeyMsg) []tea.Cmd {
	switch msg.String() {
	case "esc":
		m.prompt = promptMode{}
		m.comp.Reset()
		return nil
	case "enter":
		p := m.prompt.p
		kind := m.prompt.kind
		text := strings.TrimSpace(m.comp.Value())
		m.prompt = promptMode{}
		m.comp.Reset()
		if p == nil {
			return nil
		}
		d := agent.Decision{Allow: false, Reason: text}
		if kind == pEdit {
			switch {
			case text == "":
				d.Reason = ""
			case p.req.Kind == agent.ApproveCommand && text == strings.TrimSpace(p.req.Summary):
				d = agent.Decision{Allow: true}
			case p.req.Kind == agent.ApproveCommand:
				// the protocol cannot carry an edited command: deny and say what to run instead
				d.Reason = "Do not run that as written. Run this instead: " + text
			default:
				d.Reason = "Do this instead: " + text
			}
		}
		return []tea.Cmd{m.respond(p, d)}
	}
	return m.editKey(msg)
}

func (m *model) overlayKey(key string) []tea.Cmd {
	if m.ov.kind == ovChanges {
		return m.changesKey(key)
	}
	if m.ov.kind == ovKeys { // any key closes it
		m.closeOverlay()
		return nil
	}
	switch key {
	case "esc", "q":
		m.closeOverlay()
	case "up", "k":
		m.moveOverlay(-1)
	case "down", "j":
		m.moveOverlay(1)
	case "enter":
		switch m.ov.kind {
		case ovInbox:
			if m.ov.sel < len(m.pending) {
				m.selectThread(m.pending[m.ov.sel].thread)
			}
			m.closeOverlay()
		case ovResume:
			if m.ov.sel < len(m.ov.threads) {
				id := m.ov.threads[m.ov.sel].ID
				m.closeOverlay()
				return []tea.Cmd{m.resumeCmd(id)}
			}
		}
	case "y", "n", "a":
		if m.ov.kind != ovInbox || m.ov.sel >= len(m.pending) {
			return nil
		}
		p := m.pending[m.ov.sel]
		d := agent.Decision{Allow: key != "n", Always: key == "a"}
		cmd := m.respond(p, d)
		if len(m.pending) == 0 {
			m.closeOverlay()
		}
		m.clampOverlay()
		return []tea.Cmd{cmd}
	}
	return nil
}

func (m *model) menuKey(msg tea.KeyMsg) (bool, []tea.Cmd) {
	switch msg.String() {
	case "up", "ctrl+p":
		m.menu.move(-1)
		return true, nil
	case "down", "ctrl+n":
		m.menu.move(1)
		return true, nil
	case "tab":
		m.completeMenu(false)
		return true, nil
	case "enter":
		if m.completeMenu(true) {
			return true, m.submit()
		}
		return true, nil
	case "esc":
		m.menu.dismissed = m.comp.Value()
		return true, nil
	}
	return false, nil
}

func (m *model) composerKey(msg tea.KeyMsg) []tea.Cmd {
	key := msg.String()
	if m.comp.Empty() {
		if ok, cmds := m.approvalKey(key); ok {
			return cmds
		}
		if key == "i" && len(m.pending) > 0 {
			m.openOverlay(ovInbox)
			return nil
		}
	}
	switch key {
	case "tab", "esc":
		m.focus = focusTranscript
		return nil
	case "enter":
		return m.submit()
	case "alt+enter", "ctrl+j", "shift+enter":
		m.comp.Insert("\n")
		return nil
	case "alt+up", "alt+down":
		d := -1
		if key == "alt+down" {
			d = 1
		}
		if !m.focusQueue(d) {
			m.moveSel(d)
		}
		return nil
	case "up", "down":
		d := -1
		if key == "down" {
			d = 1
		}
		switch {
		case !m.comp.Empty():
			m.comp.MoveVertical(d, m.g.mainW)
		case d < 0 && m.edit == nil && len(m.queueOf(m.sel)) == 0:
			// up on an empty composer edits your last message, like Claude Code
			if it := lastUserItem(m.curView()); it != nil {
				return m.startEdit(it)
			}
			m.moveSel(d)
		default:
			m.moveSel(d)
		}
		return nil
	case "backspace", "ctrl+h":
		if m.comp.Empty() && len(m.attach) > 0 {
			m.removeAttach(len(m.attach) - 1)
			return nil
		}
	}
	return m.editKey(msg)
}

func (m *model) editKey(msg tea.KeyMsg) []tea.Cmd {
	c := &m.comp
	switch msg.String() {
	case "backspace", "ctrl+h":
		c.Backspace()
	case "delete", "ctrl+d":
		c.Delete()
	case "left":
		c.Left()
	case "right":
		c.Right()
	case "home", "ctrl+a":
		c.Home()
	case "end", "ctrl+e":
		c.End()
	case "ctrl+w", "alt+backspace":
		c.DeleteWordBack()
	case "ctrl+u":
		c.DeleteToStart()
	case "ctrl+k":
		c.DeleteToEnd()
	case "alt+left", "ctrl+left", "alt+b":
		c.WordLeft()
	case "alt+right", "ctrl+right", "alt+f":
		c.WordRight()
	case "alt+enter", "ctrl+j":
		c.Insert("\n")
	default:
		switch {
		case msg.Type == tea.KeySpace:
			c.Insert(" ")
		case msg.Type == tea.KeyRunes && !msg.Alt:
			c.Insert(string(msg.Runes))
		}
	}
	return nil
}

func (m *model) transcriptKey(msg tea.KeyMsg) []tea.Cmd {
	key := msg.String()
	v := m.curView()
	vh := m.g.trH
	switch key {
	case "tab", "esc":
		m.focus = focusComposer
	case "j", "down":
		m.moveSel(1)
	case "k", "up":
		m.moveSel(-1)
	case "pgup", "ctrl+u":
		if v != nil {
			v.scrollBy(-max(1, vh/2), vh)
		}
	case "pgdown", "ctrl+d":
		if v != nil {
			v.scrollBy(max(1, vh/2), vh)
		}
	case "g", "home":
		if v != nil {
			v.scrollTo(false, vh)
		}
	case "G", "end":
		if v != nil {
			v.scrollTo(true, vh)
		}
	case "[", "]":
		if v != nil {
			d := -1
			if key == "]" {
				d = 1
			}
			if it := v.moveCursor(d); it != nil {
				m.compose(v, m.curThread(), m.g.mainW-1)
				if li := v.lineOf(it); li >= 0 {
					v.ensureVisible(li, vh)
				}
			}
		}
	case "enter", " ":
		if v != nil {
			m.toggle(v.cursorItem(), m.g.mainW-1)
		}
	case "q":
		if m.comp.Empty() {
			return []tea.Cmd{tea.Quit}
		}
		m.focus = focusComposer
		m.comp.Insert(key)
	case "i":
		if len(m.pending) > 0 {
			m.openOverlay(ovInbox)
		} else {
			return []tea.Cmd{m.toast.show("no pending approvals", false, m.now)}
		}
	case "d":
		return m.openChanges()
	case "?":
		m.openOverlay(ovKeys)
	case "p":
		return m.threadCommand("pin", "")
	case "x":
		if th, ok := m.selThread(); ok && th.Resolved {
			return []tea.Cmd{m.toast.show("resolved · a new message wakes it", false, m.now)}
		}
		return m.threadCommand("done", "")
	case "r":
		if th, ok := m.selThread(); ok {
			m.focus = focusComposer
			m.comp.SetValue("/rename " + threadTitle(th))
		}
	case "n", "y", "a", "e":
		if ok, cmds := m.approvalKey(key); ok {
			return cmds
		}
		if key == "e" && v != nil {
			if it := v.cursorItem(); it != nil && it.kind == kUser {
				return m.startEdit(it)
			}
		}
		if key == "n" {
			return m.startNew()
		}
		m.focus = focusComposer
		m.comp.Insert(key)
	default:
		if msg.Type == tea.KeyRunes && !msg.Alt && len(msg.Runes) > 0 {
			m.focus = focusComposer
			m.comp.Insert(string(msg.Runes))
		}
	}
	return nil
}

// startNew is /new: an empty thread, selected, and the composer to type in. An
// empty thread that is already there is reused rather than piling up.
func (m *model) startNew() []tea.Cmd {
	m.comp.Reset()
	m.focus = focusComposer
	if th, ok := m.selThread(); ok && len(th.Members) == 0 {
		return nil
	}
	for _, t := range m.threads {
		if len(t.Members) == 0 {
			m.selectThread(t.ID)
			return nil
		}
	}
	id, err := m.mgr.NewThread(m.newDir)
	if err != nil {
		return []tea.Cmd{m.toast.show(err.Error(), true, m.now)}
	}
	m.threads = m.mgr.Threads()
	m.selectThread(id)
	return nil
}

// ensureThread makes sure there is a selected thread to type into.
func (m *model) ensureThread() (string, error) {
	if _, ok := m.selThread(); ok {
		return m.sel, nil
	}
	id, err := m.mgr.NewThread(m.newDir)
	if err != nil {
		return "", err
	}
	m.threads = m.mgr.Threads()
	m.selectThread(id)
	return id, nil
}

// ---- submit ----

func (m *model) submit() []tea.Cmd {
	if m.edit != nil {
		return m.submitEdit()
	}
	text := m.comp.Value()
	if strings.HasSuffix(text, "\\") {
		m.comp.Backspace()
		m.comp.Insert("\n")
		return nil
	}
	t := strings.TrimSpace(text)
	th, _ := m.selThread()

	if t == "" && len(m.attach) == 0 {
		switch {
		case th.Paused: // enter on an empty composer releases a pause
			m.comp.Reset()
			return []tea.Cmd{m.continueCmd(th.ID)}
		case m.emptyVisible(): // enter on an empty new thread starts the default agent
			m.comp.Reset()
			id, err := m.ensureThread()
			if err != nil {
				return []tea.Cmd{m.toast.show(err.Error(), true, m.now)}
			}
			return []tea.Cmd{m.addCmd(id, m.newKind)}
		}
		return nil
	}
	if name, arg, ok := parseCommand(t); ok {
		return m.runCommand(name, arg)
	}

	// Addressing mistakes are caught before the text leaves the composer.
	ad := session.ParseAddressing(t, refsOf(th), m.kinds)
	if len(ad.Unknown) > 0 {
		return []tea.Cmd{m.toast.show("no agent @"+ad.Unknown[0]+" in this thread", true, m.now)}
	}
	if ad.Mentioned() && strings.TrimSpace(ad.Body) == "" && len(ad.NewKinds) == 0 && len(m.attach) == 0 {
		// "@claude" alone has nothing to send: keep it, ready for the message,
		// and let the hint say who it is for.
		hint := addressHint(t, th, m.kinds)
		if hintIsWarning(hint) {
			return []tea.Cmd{m.toast.show(hint, true, m.now)}
		}
		m.focus = focusComposer
		m.comp.SetValue(t + " ")
		return []tea.Cmd{m.toast.show("type your message "+hint, false, m.now)}
	}
	id, err := m.ensureThread()
	if err != nil {
		return []tea.Cmd{m.toast.show(err.Error(), true, m.now)}
	}
	m.comp.Reset()
	return []tea.Cmd{m.postCmd(id, t, m.takeAttach())}
}

// pushConfig tells the manager about the models picked with /model, which are
// not saved, on top of the saved config.
func (m *model) pushConfig() {
	c := m.cfg
	if v := m.newModel["claude"]; v != "" {
		c.Models.Claude = v
	}
	if v := m.newModel["codex"]; v != "" {
		c.Models.Codex = v
	}
	m.mgr.SetConfig(c)
}

func (m *model) runCommand(name, arg string) []tea.Cmd {
	danger := func(s string) []tea.Cmd { return []tea.Cmd{m.toast.show(s, true, m.now)} }
	info := func(s string) []tea.Cmd { return []tea.Cmd{m.toast.show(s, false, m.now)} }
	switch name {
	case "new":
		return m.startNew()
	case "resume":
		m.comp.Reset()
		m.openOverlay(ovResume)
	case "changes":
		m.comp.Reset()
		return m.openChanges()
	case "rename", "pin", "done", "archive", "delete":
		return m.threadCommand(name, arg)
	case "model":
		if arg == "" { // no name: pick one from what the CLI offers
			m.comp.Reset()
			return m.openPicker("")
		}
		if hasKind(m.kinds, arg) { // /model codex: the models of that kind
			m.comp.Reset()
			return m.openPickerKind(arg)
		}
		kind := m.newKind
		if a, ok := m.defaultTarget(); ok {
			kind = a.Kind
		}
		if first, rest := splitToken(arg); rest != "" && hasKind(m.kinds, first) {
			kind, arg = first, rest // /model codex gpt-5
		}
		m.comp.Reset()
		if arg == "default" {
			delete(m.newModel, kind)
			m.newDefault[kind] = true
			m.mgr.SetDefaultModel(kind, "")
			m.pushConfig()
			return info("next " + kind + " uses its default model")
		}
		m.newModel[kind] = arg
		delete(m.newDefault, kind)
		m.mgr.SetDefaultModel(kind, arg)
		m.pushConfig()
		return info("next " + kind + " starts on " + arg)
	case "cd":
		if arg == "" {
			return danger("usage: /cd <path>")
		}
		dir, err := m.resolveDir(arg)
		if err != nil {
			return danger(err.Error())
		}
		m.newDir = dir
		m.comp.Reset()
		return info("new threads start in " + tilde(dir, m.home))
	case "name":
		if arg == "" {
			return danger("usage: /name <name>")
		}
		m.cfg.UserName = arg
		m.comp.Reset()
		if m.opts.SaveConfig != nil {
			if err := m.opts.SaveConfig(m.cfg); err != nil {
				return danger("could not save config: " + err.Error())
			}
		}
		m.pushConfig()
		return info("you are " + arg)
	default:
		return danger(fmt.Sprintf("unknown command /%s", name))
	}
	return nil
}

func hasKind(kinds []string, k string) bool {
	for _, x := range kinds {
		if x == k {
			return true
		}
	}
	return false
}

func (m *model) resolveDir(arg string) (string, error) {
	p := arg
	if p == "~" || strings.HasPrefix(p, "~/") || strings.HasPrefix(p, `~\`) {
		p = filepath.Join(m.home, strings.TrimLeft(p[1:], `/\`))
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(m.newDir, p)
	}
	p = filepath.Clean(p)
	fi, err := os.Stat(p)
	if err != nil || !fi.IsDir() {
		return "", fmt.Errorf("no such directory: %s", arg)
	}
	return p, nil
}

// ---- mouse ----

// handleMouse deals with presses and the wheel; moves and releases never get
// here (see Update).
func (m *model) handleMouse(msg tea.MouseMsg) []tea.Cmd {
	g := m.g
	v := m.curView()
	switch msg.Button {
	case tea.MouseButtonWheelUp, tea.MouseButtonWheelDown:
		if m.ov.open && m.ov.kind == ovChanges {
			if msg.Button == tea.MouseButtonWheelUp {
				m.chg.scroll = max(0, m.chg.scroll-3)
			} else {
				m.chg.scroll += 3 // clamped when drawn
			}
			return nil
		}
		if v != nil && !m.ov.open {
			d := 3
			if msg.Button == tea.MouseButtonWheelUp {
				d = -3
			}
			v.scrollBy(d, g.trH)
		}
	case tea.MouseButtonLeft:
		if m.ov.open {
			return nil
		}
		if m.pick.open {
			return m.pickMouse(msg)
		}
		switch {
		case msg.Y == 0 && m.overChangeSum(msg.X):
			return m.openChanges()
		case v != nil && msg.Y == g.topY+g.trH-1 && m.newPill(v, g.trH) != "" &&
			msg.X >= m.w-sw(m.newPill(v, g.trH))-2:
			v.scrollTo(true, g.trH)
		case g.qRows > 0 && msg.Y >= g.qY && msg.Y < g.qY+g.qRows && msg.X >= g.mainX:
			return m.queueMouse(msg.Y-g.qY, msg.X-g.mainX)
		case g.attH > 0 && msg.Y == g.attY && msg.X >= g.mainX:
			m.attachMouse(msg.X - g.mainX)
		case msg.Y == g.chipY && msg.X >= g.mainX && !m.prompt.active():
			if h, ok := m.chipAt(msg.X - g.mainX); ok {
				if h.kind == hitMode {
					return m.cycleMode()
				}
				return m.openPicker(h.agent)
			}
			m.focus = focusComposer
		case g.sidebar && msg.X < g.sbW && msg.Y >= g.topY:
			rh := g.sidebarRowHeight()
			row := m.sidebarStart(g, g.footY-g.topY) + (msg.Y-g.topY)/rh
			if row >= 0 && row < len(m.rows) {
				m.selectThread(m.rows[row].ID)
			}
		case v == nil && msg.Y >= g.topY && msg.Y < g.topY+g.trH:
			if cmd := m.logoMouse(msg, true); cmd != nil {
				return []tea.Cmd{cmd}
			}
		case msg.X >= g.mainX && msg.Y >= g.topY && msg.Y < g.topY+g.trH && v != nil:
			li := v.top() + msg.Y - g.topY - v.padTop(g.trH)
			if li >= 0 && li < len(v.owners) {
				if it := v.owners[li]; it != nil && it.foldable() {
					v.cursor = it
					m.toggle(it, g.mainW-1)
				} else if it != nil && it.kind == kUser && it.entryID != "" {
					v.cursor = it
					return m.startEdit(it)
				}
			}
		case msg.Y >= g.compY && msg.Y < g.compY+g.compH:
			m.focus = focusComposer
		}
	}
	return nil
}
