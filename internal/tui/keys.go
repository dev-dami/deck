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
		return m.ctrlC()
	}
	if msg.Paste {
		if m.ov.open {
			return nil
		}
		m.focus = focusComposer
		m.comp.Insert(string(msg.Runes))
		return nil
	}
	if m.ov.open {
		return m.overlayKey(key)
	}
	if m.prompt.active() {
		return m.promptKey(msg)
	}
	if m.menu.open() && m.focus == focusComposer {
		if handled, c := m.menuKey(msg); handled {
			return c
		}
	}
	if m.focus == focusTranscript {
		return m.transcriptKey(msg)
	}
	return m.composerKey(msg)
}

// settleComposer refreshes composer-derived state after a key.
func (m *model) settleComposer() {
	m.g = m.geo()
	m.syncMenu()
}

func (m *model) ctrlC() []tea.Cmd {
	if a, ok := m.info(m.sel); ok && !m.newMode {
		switch a.Status {
		case session.StatusWorking, session.StatusTool, session.StatusApproval:
			m.quitArmed = time.Time{}
			return []tea.Cmd{m.interruptCmd(a.ID)}
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
	if len(m.agents) == 0 {
		return
	}
	idx := m.selIndex()
	if m.newMode {
		if d > 0 {
			return
		}
		idx = len(m.agents)
	}
	ni := max(0, min(len(m.agents)-1, idx+d))
	m.selectAgent(m.agents[ni].ID)
}

// approvalKey handles y/n/a/e for the selected agent's first pending request.
func (m *model) approvalKey(key string) (bool, []tea.Cmd) {
	if m.newMode {
		return false, nil
	}
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
				m.selectAgent(m.pending[m.ov.sel].req.AgentID)
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
	case "up", "down":
		d := -1
		if key == "down" {
			d = 1
		}
		if m.comp.Empty() {
			m.moveSel(d)
		} else {
			m.comp.MoveVertical(d, m.g.mainW)
		}
		return nil
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
				m.compose(v, m.mustInfo(v.id), m.g.mainW-1)
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
	case "n", "y", "a", "e":
		if ok, cmds := m.approvalKey(key); ok {
			return cmds
		}
		if key == "n" {
			m.startNew()
			return nil
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

func (m *model) mustInfo(id string) session.AgentInfo {
	a, _ := m.info(id)
	return a
}

// startNew is /new: an empty composer and no selected agent.
func (m *model) startNew() {
	m.comp.Reset()
	m.newMode = true
	m.newKind = m.cfg.DefaultAgent
	m.focus = focusComposer
}

// ---- submit ----

func (m *model) submit() []tea.Cmd {
	text := m.comp.Value()
	if strings.HasSuffix(text, "\\") {
		m.comp.Backspace()
		m.comp.Insert("\n")
		return nil
	}
	p := ParseInput(text, m.agents, m.sel, m.newMode, m.newKind, m.kinds)
	switch p.Action {
	case ActSend:
		m.comp.Reset()
		return []tea.Cmd{m.sendCmd(p.AgentID, p.Text)}
	case ActStart:
		m.comp.Reset()
		return []tea.Cmd{m.startCmd(p.Kind, p.Text, m.newDir, m.newModel[p.Kind])}
	case ActSelect:
		m.comp.Reset()
		m.selectAgent(p.AgentID)
	case ActNewKind:
		m.comp.Reset()
		m.newMode = true
		m.newKind = p.Kind
	case ActCommand:
		return m.runCommand(p.Cmd, p.Arg)
	case ActError:
		return []tea.Cmd{m.toast.show(p.Err, true, m.now)}
	}
	return nil
}

func (m *model) runCommand(name, arg string) []tea.Cmd {
	danger := func(s string) []tea.Cmd { return []tea.Cmd{m.toast.show(s, true, m.now)} }
	info := func(s string) []tea.Cmd { return []tea.Cmd{m.toast.show(s, false, m.now)} }
	switch name {
	case "new":
		m.startNew()
	case "resume":
		m.comp.Reset()
		m.openOverlay(ovResume)
	case "model":
		if arg == "" {
			return danger("usage: /model <name>")
		}
		kind := m.newKind
		if a, ok := m.info(m.sel); ok && !m.newMode {
			kind = a.Kind
		}
		m.comp.Reset()
		if arg == "default" {
			delete(m.newModel, kind)
			return info("next " + kind + " uses its default model")
		}
		m.newModel[kind] = arg
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
		return info("new agents start in " + tilde(dir, m.home))
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
		m.mgr.SetConfig(m.cfg)
		return info("you are " + arg)
	default:
		return danger(fmt.Sprintf("unknown command /%s", name))
	}
	return nil
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

func (m *model) handleMouse(msg tea.MouseMsg) []tea.Cmd {
	g := m.g
	if msg.Action != tea.MouseActionPress {
		return nil
	}
	v := m.curView()
	switch msg.Button {
	case tea.MouseButtonWheelUp, tea.MouseButtonWheelDown:
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
		switch {
		case g.sidebar && msg.X < g.sbW && msg.Y >= g.topY:
			if row := msg.Y - g.topY; row >= 0 && row < len(m.agents) {
				m.selectAgent(m.agents[row].ID)
			}
		case msg.X >= g.mainX && msg.Y >= g.topY && msg.Y < g.topY+g.trH && v != nil:
			li := v.top() + msg.Y - g.topY - v.padTop(g.trH)
			if li >= 0 && li < len(v.owners) {
				if it := v.owners[li]; it != nil && it.foldable() {
					v.cursor = it
					m.toggle(it, g.mainW-1)
				}
			}
		case msg.Y >= g.compY && msg.Y < g.compY+g.compH:
			m.focus = focusComposer
		}
	}
	return nil
}
