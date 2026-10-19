package model

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/dev-dami/deck/internal/deck/peer"
	"github.com/dev-dami/deck/internal/ui/dialog"
	"github.com/dev-dami/deck/internal/ui/notice"
	"github.com/dev-dami/deck/internal/ui/util"
	"github.com/dev-dami/deck/internal/workspace"
)

// The Team pane: this Deck's teammates, the other Decks paired with it over
// the peer link (docs/peers-design.md), each with a status dot, how the link
// is doing and its role, then what its agents do ("vps/claude2 ▸ working ·
// fix the flaky test"), the files they hold in repositories both Decks have,
// and its plan windows when it shares them. A code contract a teammate
// proposes shows as a card at the top: y runs it with the agent shown, a
// picks another agent, n declines. The pane reads a snapshot a command keeps
// current through PeerLink.Wait; it never touches the link in Update or
// render.

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
	first := m.teamSnap == nil
	prev := m.teamSnap
	m.teamSnap = &snap
	cmds := []tea.Cmd{m.watchTeam()}
	if !first {
		cmds = append(cmds, m.teamNotices(snap)...)
		m.contractNotices(*prev, snap)
	}
	m.teamSeen = teamMaxSeq(snap)
	return tea.Batch(cmds...)
}

// teamMaxSeq is the newest chat message or request in snap.
func teamMaxSeq(snap peer.Snapshot) uint64 {
	var n uint64
	for _, c := range snap.Chat {
		n = max(n, c.Seq)
	}
	for _, r := range snap.Requests {
		n = max(n, r.Seq)
	}
	return n
}

// teamNotices tells the person what is new in the team since the last
// snapshot: messages from teammates, and capsules they asked for. A
// private capsule's request offers to share it.
func (m *UI) teamNotices(snap peer.Snapshot) []tea.Cmd {
	var cmds []tea.Cmd
	for _, c := range snap.Chat {
		if c.Seq <= m.teamSeen || c.Mine {
			continue
		}
		m.Notify(notice.Notice{
			Level: notice.Info, Source: notice.SourceTeam, Title: c.Author(), Body: c.Text,
			Action: &notice.Action{Label: "Open team chat", Msg: focusPaneMsg{id: paneChat}},
		})
	}
	for _, r := range snap.Requests {
		if r.Seq <= m.teamSeen {
			continue
		}
		short := shortCapsule(string(r.ID))
		if r.Served {
			m.Notify(notice.Notice{Level: notice.Info, Source: notice.SourceTeam, Quiet: true,
				Title: r.From + " fetched capsule " + short})
			continue
		}
		m.Notify(notice.Notice{
			Level: notice.Warn, Source: notice.SourceTeam,
			Title: r.From + " asks for capsule " + short,
			Body:  "It is private. Share it so they can fetch it?",
			Action: &notice.Action{Label: "Share capsule " + short,
				Msg: capsuleShareMsg{id: string(r.ID), from: r.From, public: true}},
		})
		cmds = append(cmds, m.loadCapsules())
	}
	return cmds
}

// contractNotices tells the person of each contract a teammate proposed
// since prev.
func (m *UI) contractNotices(prev, snap peer.Snapshot) {
	known := map[string]bool{}
	for _, c := range prev.Contracts {
		known[c.Peer+"/"+c.ID] = true
	}
	for _, c := range snap.Contracts {
		if !c.Inbound || c.State != peer.ContractProposed || known[c.Peer+"/"+c.ID] {
			continue
		}
		m.Notify(notice.Notice{
			Level: notice.Warn, Source: notice.SourceTeam,
			Title: c.Peer + " proposes a contract", Body: c.Title,
			Action: &notice.Action{Label: "Open the Team pane", Msg: focusPaneMsg{id: paneTeam}},
		})
	}
}

// contractKeyPrefix starts the row key of a contract that waits for the
// person: "contract:<peer>/<id>".
const contractKeyPrefix = "contract:"

type teamPane struct {
	list paneList
	// kind counts a presses: it picks the agent a contract runs with.
	kind int
}

// agentKind is the agent a confirmed contract runs with: the default agent
// first, then the others in turn as a is pressed.
func (p *teamPane) agentKind(m *UI) string {
	editor, ok := m.com.Workspace.(workspace.SettingsEditor)
	if !ok {
		return ""
	}
	kinds := editor.AgentKinds()
	if len(kinds) == 0 {
		return ""
	}
	def := editor.DeckSettings().DefaultAgent
	for i, k := range kinds {
		if k == def {
			kinds = append([]string{k}, append(kinds[:i:i], kinds[i+1:]...)...)
			break
		}
	}
	return kinds[p.kind%len(kinds)]
}

// waitingContract is the contract that row key sel names, when it waits for the
// person.
func (m *UI) waitingContract(sel string) (peer.Contract, bool) {
	ref, ok := strings.CutPrefix(sel, contractKeyPrefix)
	if !ok || m.teamSnap == nil {
		return peer.Contract{}, false
	}
	for _, c := range m.teamSnap.Contracts {
		if c.Inbound && c.State == peer.ContractProposed && c.Peer+"/"+c.ID == ref {
			return c, true
		}
	}
	return peer.Contract{}, false
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
	rows := p.rows(m, 80)
	if p.list.navKey(rows, msg) {
		return true, nil
	}
	c, ok := m.waitingContract(p.list.selected(rows))
	link := m.peerLink()
	if !ok || link == nil {
		return false, nil
	}
	switch msg.String() {
	case "a":
		p.kind++
		return true, nil
	case "y", "n":
		accept, kind := msg.String() == "y", p.agentKind(m)
		if accept && kind == "" {
			return true, util.ReportError(fmt.Errorf("no agent on this Deck can run it"))
		}
		return true, func() tea.Msg {
			if err := link.AnswerContract(c.Peer, c.ID, accept, kind); err != nil {
				return util.NewErrorMsg(err)
			}
			if accept {
				return util.NewInfoMsg("Accepted " + c.Peer + "'s contract: " + kind + " works on it in a new thread")
			}
			return util.NewInfoMsg("Declined " + c.Peer + "'s contract")
		}
	}
	return false, nil
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
	rows = append(rows, p.contractRows(m, width)...)
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
		lines := []string{
			ansi.Truncate(head, width, "…"),
			ansi.Truncate("  "+muted.Render(text), width, "…"),
		}
		for _, l := range presenceLines(ps) {
			lines = append(lines, ansi.Truncate("  "+muted.Render(l), width, "…"))
		}
		rows = append(rows, paneRow{key: ps.Name, selectable: true, lines: lines})
	}
	return rows
}

// maxContractLines caps the contracts listed besides those that wait.
const maxContractLines = 4

// contractRows are the contracts teammates proposed that wait for the
// person, as cards, then the latest others, one line each.
func (p *teamPane) contractRows(m *UI, width int) []paneRow {
	t := m.com.Styles
	muted := t.ModelInfo.Provider
	var rows []paneRow
	var others []string
	for _, c := range m.teamSnap.Contracts {
		if !c.Inbound || c.State != peer.ContractProposed {
			arrow, state := "→ ", c.State
			if c.Inbound {
				arrow = "← "
			}
			if c.Agent != "" && c.State == peer.ContractAccepted {
				state += " by " + c.Agent
			}
			others = append(others, arrow+c.Peer+" · "+c.Title+" · "+state)
			continue
		}
		lines := []string{ansi.Truncate("▸ "+t.ModelInfo.Name.Bold(true).Render(c.Peer)+" asks: "+c.Title, width, "…")}
		if brief, _, _ := strings.Cut(c.Brief, "\n"); brief != "" && brief != c.Title {
			lines = append(lines, ansi.Truncate("  "+muted.Render(brief), width, "…"))
		}
		files := "a copy of its tree"
		if c.Files > 0 {
			files = fmt.Sprintf("a copy of its tree (%d files)", c.Files)
		}
		lines = append(lines,
			ansi.Truncate("  "+muted.Render("runs on "+files+" in a new thread"), width, "…"),
			ansi.Truncate("  "+muted.Render("y run with "+p.agentKind(m)+" · a other agent · n decline"), width, "…"))
		rows = append(rows, paneRow{key: contractKeyPrefix + c.Peer + "/" + c.ID, selectable: true, lines: lines})
	}
	if n := len(others); n > maxContractLines {
		others = others[n-maxContractLines:]
	}
	for _, l := range others {
		rows = append(rows, paneRow{lines: []string{ansi.Truncate(muted.Render(l), width, "…")}})
	}
	return rows
}

// maxPresenceLines caps the agents and leases shown for one teammate.
const maxPresenceLines = 6

// presenceLines is what a teammate said of its work: its agents, the files
// they hold, and its plan windows.
func presenceLines(ps peer.PeerState) []string {
	p := ps.Presence
	if p == nil {
		return nil
	}
	var out []string
	for _, a := range p.Agents {
		l := ps.Name + "/" + a.Name + " ▸ " + a.Status
		if a.Task != "" {
			l += " · " + a.Task
		}
		if a.ContextPct > 0 {
			l += " · " + strconv.Itoa(a.ContextPct) + "%"
		}
		out = append(out, l)
	}
	for _, le := range p.Leases {
		l := le.Agent + " holds " + strings.Join(le.Paths, ", ")
		if le.Exclusive {
			l += " (exclusive)"
		}
		out = append(out, l)
	}
	if n := len(out); n > maxPresenceLines {
		out = append(out[:maxPresenceLines-1], fmt.Sprintf("and %d more", n-maxPresenceLines+1))
	}
	var plans []string
	for _, w := range p.Limits {
		plans = append(plans, fmt.Sprintf("%s %s %.0f%%", w.Kind, w.Window, w.UsedPct))
	}
	if len(plans) > 0 {
		out = append(out, "plans: "+strings.Join(plans, " · "))
	}
	return out
}
