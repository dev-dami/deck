package model

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	decks "github.com/dev-dami/deck/internal/deck/session"
	"github.com/dev-dami/deck/internal/ui/util"
	"github.com/dev-dami/deck/internal/workspace"
)

// The Capsules pane: the session's SUB capsules, each private (only this
// Deck has it) or public (paired teammates can fetch it). p switches the
// selected one; a teammate asking for a private one shows as a notification
// that offers to share it.

const paneCapsules paneID = "capsules"

// capsulesTTL is how often the list is read again while nothing else
// prompts it (agents make capsules on their own).
const capsulesTTL = 10 * time.Second

func init() { registerPane(paneCapsules, func() pane { return &capsulesPane{} }) }

// capsulesMsg carries the session's capsules, read off the UI loop.
type capsulesMsg struct {
	session string
	list    []decks.CapsuleInfo
}

// capsuleShareMsg publishes a capsule (from a request notice or the pane);
// from is the teammate who asked, "" from the pane.
type capsuleShareMsg struct {
	id, from string
	public   bool
}

// capsuleSharedMsg reports a capsule's new state.
type capsuleSharedMsg struct {
	id, from string
	public   bool
	err      error
}

func (m *UI) teamDesk() workspace.TeamDesk {
	if m.com == nil || m.com.Workspace == nil {
		return nil
	}
	d, _ := m.com.Workspace.(workspace.TeamDesk)
	return d
}

// loadCapsules reads the session's capsules off the UI loop.
func (m *UI) loadCapsules() tea.Cmd {
	d := m.teamDesk()
	if d == nil || m.capsulesLoading {
		return nil
	}
	m.capsulesLoading = true
	m.capsulesAt = time.Now()
	sid := ""
	if m.session != nil {
		sid = m.session.ID
	}
	return func() tea.Msg { return capsulesMsg{session: sid, list: d.Capsules(sid)} }
}

func (m *UI) applyCapsules(msg capsulesMsg) {
	m.capsulesLoading = false
	if m.session != nil && msg.session != m.session.ID {
		return // a switch raced the read; the next one is on its way
	}
	m.capsules = msg.list
}

// shareCapsule makes a capsule public or private off the UI loop.
func (m *UI) shareCapsule(msg capsuleShareMsg) tea.Cmd {
	d := m.teamDesk()
	if d == nil {
		return util.ReportWarn("Capsules need Deck's workspace")
	}
	return func() tea.Msg {
		err := d.SetCapsulePublic(msg.id, msg.public)
		return capsuleSharedMsg{id: msg.id, from: msg.from, public: msg.public, err: err}
	}
}

func (m *UI) applyCapsuleShared(msg capsuleSharedMsg) tea.Cmd {
	short := shortCapsule(msg.id)
	cmds := []tea.Cmd{m.loadCapsules()}
	switch {
	case msg.err != nil:
		cmds = append(cmds, util.ReportError(msg.err))
	case !msg.public:
		cmds = append(cmds, util.ReportInfo("Capsule "+short+" is private again"))
	case msg.from != "":
		cmds = append(cmds, util.ReportInfo("Shared capsule "+short+": "+msg.from+" can fetch it now"))
	default:
		cmds = append(cmds, util.ReportInfo("Capsule "+short+" is public: paired teammates can fetch it"))
	}
	return tea.Batch(cmds...)
}

func shortCapsule(id string) string {
	id = strings.TrimPrefix(id, "blake3:")
	if len(id) > 10 {
		id = id[:10]
	}
	return id
}

type capsulesPane struct {
	list paneList
}

func (p *capsulesPane) id() paneID       { return paneCapsules }
func (p *capsulesPane) title(*UI) string { return "Capsules" }
func (p *capsulesPane) width() int       { return 0 }

func (p *capsulesPane) badge(m *UI) string {
	pub := 0
	for _, c := range m.capsules {
		if c.Public {
			pub++
		}
	}
	if len(m.capsules) == 0 {
		return ""
	}
	return fmt.Sprintf("%d · %d public", len(m.capsules), pub)
}

func (p *capsulesPane) height(m *UI, width int) int {
	return rowsHeight(p.rows(m, width-1))
}

func (p *capsulesPane) render(m *UI, width, height int, focused bool) string {
	return p.list.view(p.rows(m, width-1), width, height, focused, m.com.Styles.Header.Mark.GetForeground())
}

func (p *capsulesPane) key(m *UI, msg tea.KeyPressMsg) (bool, tea.Cmd) {
	rows := p.rows(m, 80)
	if p.list.navKey(rows, msg) {
		return true, nil
	}
	switch msg.String() {
	case "p", "enter":
		i := p.list.cursor
		if i < 0 || i >= len(m.capsules) {
			return true, nil
		}
		c := m.capsules[i]
		return true, m.shareCapsule(capsuleShareMsg{id: c.ID, public: !c.Public})
	case "r":
		return true, m.loadCapsules()
	}
	return false, nil
}

func (p *capsulesPane) click(m *UI, _, y int) tea.Cmd {
	i := p.list.rowAt(y)
	if i < 0 || i >= len(m.capsules) {
		return nil
	}
	again := i == p.list.cursor
	p.list.cursor = i
	if again {
		c := m.capsules[i]
		return m.shareCapsule(capsuleShareMsg{id: c.ID, public: !c.Public})
	}
	return nil
}

// rows are the capsules at width (the gutter left out), in the same order
// as m.capsules so the cursor indexes both.
func (p *capsulesPane) rows(m *UI, width int) []paneRow {
	t := m.com.Styles
	muted := t.ModelInfo.Provider
	if len(m.capsules) == 0 {
		return []paneRow{{lines: []string{
			ansi.Truncate(muted.Render("No capsules yet. Agents make them with sub.capsule.capture."), width, "…"),
		}}}
	}
	rows := make([]paneRow, 0, len(m.capsules)+1)
	for _, c := range m.capsules {
		state := t.Tool.IconSuccess.Render("● public")
		if !c.Public {
			state = muted.Render("○ private")
		}
		head := state + " " + t.ModelInfo.Name.Render(shortCapsule(c.ID))
		detail := fmt.Sprintf("%d files (+%d ~%d -%d)", c.Files, c.Add, c.Mod, c.Del)
		if c.Source != "" {
			detail += " · " + c.Source
		}
		lines := []string{ansi.Truncate(head, width, "…"), ansi.Truncate("  "+muted.Render(detail), width, "…")}
		if c.Task != "" {
			lines = append(lines, ansi.Truncate("  "+muted.Render(c.Task+" "+c.TaskTitle), width, "…"))
		}
		rows = append(rows, paneRow{key: c.ID, selectable: true, lines: lines})
	}
	return rows
}
