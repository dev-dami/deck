package model

import (
	"context"
	"strconv"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/dev-dami/deck/internal/deck/peer"
	"github.com/dev-dami/deck/internal/ui/dialog"
	"github.com/dev-dami/deck/internal/workspace"
)

// The Team pane: this Deck's teammates, the other Decks paired with it over
// the peer link (docs/peers-design.md), each with a status dot, how the link
// is doing and its role. The pane reads a snapshot a command keeps current
// through PeerLink.Wait; it never touches the link in Update or render.

const paneTeam paneID = "team"

// teamWatchTimeout bounds one wait for a link change, so the snapshot is
// refreshed now and then even when nothing changes (backoff countdowns).
const teamWatchTimeout = 30 * time.Second

func init() { registerPane(paneTeam, func() pane { return &teamPane{} }) }

// teamSnapMsg carries a fresh snapshot of the peer link.
type teamSnapMsg struct{ snap peer.Snapshot }

// peerLink is the workspace's peer link, or nil.
func (m *UI) peerLink() workspace.PeerLink {
	if m.com == nil || m.com.Workspace == nil {
		return nil
	}
	p, ok := m.com.Workspace.(workspace.Peers)
	if !ok {
		return nil
	}
	return p.PeerLink()
}

// watchTeam reads the link at once the first time, then each time it
// changes. One watch is out at a time.
func (m *UI) watchTeam() tea.Cmd {
	link := m.peerLink()
	if link == nil || m.teamWatching {
		return nil
	}
	m.teamWatching = true
	first, since := m.teamSnap == nil, uint64(0)
	if !first {
		since = m.teamSnap.Version
	}
	return func() tea.Msg {
		if !first {
			ctx, cancel := context.WithTimeout(context.Background(), teamWatchTimeout)
			link.Wait(ctx, since)
			cancel()
		}
		return teamSnapMsg{snap: link.Snapshot()}
	}
}

func (m *UI) applyTeamSnap(msg teamSnapMsg) tea.Cmd {
	m.teamWatching = false
	snap := msg.snap
	m.teamSnap = &snap
	return m.watchTeam()
}

type teamPane struct {
	list paneList
}

func (p *teamPane) id() paneID       { return paneTeam }
func (p *teamPane) title(*UI) string { return "Team" }
func (p *teamPane) width() int       { return 0 }

func (p *teamPane) badge(m *UI) string {
	if m.teamSnap == nil || len(m.teamSnap.Peers) == 0 {
		return ""
	}
	on := 0
	for _, ps := range m.teamSnap.Peers {
		if ps.Link.Inbound > 0 || (ps.Link.Supervised && ps.Link.Status.State == peer.StateConnected) {
			on++
		}
	}
	return strconv.Itoa(on) + "/" + strconv.Itoa(len(m.teamSnap.Peers)) + " online"
}

func (p *teamPane) height(m *UI, width int) int {
	return rowsHeight(p.rows(m, width-1))
}

func (p *teamPane) render(m *UI, width, height int, focused bool) string {
	return p.list.view(p.rows(m, width-1), width, height, focused, m.com.Styles.Header.Mark.GetForeground())
}

func (p *teamPane) key(m *UI, msg tea.KeyPressMsg) (bool, tea.Cmd) {
	return p.list.navKey(p.rows(m, 80), msg), nil
}

func (p *teamPane) click(m *UI, _, y int) tea.Cmd {
	if i := p.list.rowAt(y); i >= 0 {
		p.list.cursor = i
	}
	return nil
}

// rows are this Deck and its teammates at width (the gutter left out).
func (p *teamPane) rows(m *UI, width int) []paneRow {
	t := m.com.Styles
	muted := t.ModelInfo.Provider
	snap := m.teamSnap
	if snap == nil {
		if m.peerLink() == nil {
			return []paneRow{{lines: []string{muted.Render("No peer link in this Deck")}}}
		}
		return []paneRow{{lines: []string{muted.Render("Reading the team…")}}}
	}
	self := "you: " + snap.Name
	switch {
	case !snap.Enabled:
		self += " · link off"
	case snap.Listening != "":
		self += " · listening"
	}
	rows := []paneRow{{lines: []string{ansi.Truncate(muted.Render(self), width, "…")}}}
	if len(snap.Peers) == 0 {
		rows = append(rows, paneRow{lines: []string{
			ansi.Truncate(muted.Render("No teammates yet."), width, "…"),
			ansi.Truncate(muted.Render("Pair one in Settings › Team."), width, "…"),
		}})
		return rows
	}
	now := time.Now()
	for _, ps := range snap.Peers {
		dot, c, text := dialog.PeerStatus(t, ps, *snap, now)
		role := ps.Role
		if role == "" {
			role = "observer"
		}
		head := lipgloss.NewStyle().Foreground(c).Render(dot) + " " + t.ModelInfo.Name.Bold(true).Render(ps.Name) + " " + muted.Render(role)
		rows = append(rows, paneRow{
			key:        ps.Name,
			selectable: true,
			lines: []string{
				ansi.Truncate(head, width, "…"),
				ansi.Truncate("  "+muted.Render(text), width, "…"),
			},
		})
	}
	return rows
}
