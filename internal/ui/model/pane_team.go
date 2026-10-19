package model

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

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
// picks another agent, n declines. c on a teammate asks it for a contract:
// the person types the brief, and enter sends it for the open session. o
// on a teammate asks it for a report (an ops contract): the person types
// the request, enter shows it as a card, y proposes it and n drops it. Its
// result shows as a table (enter on its row). s
// on a teammate shares the open session's thread with it, stepping through
// observer, participant, editor and not shared. The pane reads a snapshot a command keeps
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
		m.sharedNotices(*prev, snap)
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
	known := map[string]string{}
	for _, c := range prev.Contracts {
		known[c.Peer+"/"+c.ID] = c.State
	}
	open := &notice.Action{Label: "Open the Team pane", Msg: focusPaneMsg{id: paneTeam}}
	for _, c := range snap.Contracts {
		was, seen := known[c.Peer+"/"+c.ID]
		switch {
		case c.Kind == peer.KindOps && c.Inbound && !seen:
			// It ran without the person: it was inside the teammate's
			// max_scope. The person hears of it.
			m.Notify(notice.Notice{Level: notice.Info, Source: notice.SourceTeam,
				Title: c.Peer + " runs a report here: " + c.Title, Body: "scope " + strings.Join(c.Scope, ", ") + ", inside its max_scope", Action: open})
			continue
		case c.Kind == peer.KindOps && !c.Inbound && was != c.State && c.Final():
			level, title := notice.Info, c.Peer+" reported: "+c.Title
			if c.State != peer.ContractDone {
				level, title = notice.Warn, c.Peer+" did not report: "+c.Title
			}
			m.Notify(notice.Notice{Level: level, Source: notice.SourceTeam, Title: title, Body: c.Note, Action: open})
			continue
		}
		if !c.Inbound || c.State != peer.ContractProposed || c.Kind == peer.KindOps || seen {
			continue
		}
		m.Notify(notice.Notice{
			Level: notice.Warn, Source: notice.SourceTeam,
			Title: c.Peer + " proposes a contract", Body: c.Title, Action: open,
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
	// asking is the teammate the person writes a contract's brief for
	// (c on its row), draft the brief so far.
	asking, draft string
	// opsFor is the teammate the person writes an ops request for (o on
	// its row), in draft. card is the request as parsed: it waits for y
	// or n.
	opsFor string
	card   *opsCard
	// open is the row key of the ops contract whose result shows as a
	// table (enter on its row).
	open string
}

// opsCard is an ops request the person typed, as ParseOps read it.
type opsCard struct {
	peer string
	c    peer.Contract
}

// opsKeyPrefix starts the row key of an ops contract with a result:
// "ops:<peer>/<id>".
const opsKeyPrefix = "ops:"

// opsProposeTimeout bounds proposing an ops contract from the pane.
const opsProposeTimeout = 40 * time.Second

// agentKind is the agent a confirmed contract runs with: the default agent
// first, then the others in turn as a is pressed.
func (p *teamPane) agentKind(m *UI) string {
	editor, ok := m.com.Workspace.(workspace.SettingsEditor)
	if !ok {
		return ""
	}
	kinds := editor.AgentKinds()
	if c, ok := m.com.Workspace.(workspace.Contracts); ok {
		kinds = c.ContractKinds()
	}
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

// shareRoles are the roles s steps through on a teammate: "" is not
// shared.
var shareRoles = []string{"", "observer", "participant", "editor"}

// threadShares are the teammates the open session's thread is shared with,
// and their roles; nil without a session or sharing.
func (m *UI) threadShares() map[string]string {
	sh, ok := m.com.Workspace.(workspace.Sharing)
	if !ok || m.session == nil {
		return nil
	}
	return sh.ThreadShares(m.session.ID)
}

// cycleShare shares the open session's thread with teammate name in the
// next role, or stops sharing it after editor.
func (m *UI) cycleShare(name string) tea.Cmd {
	sh, ok := m.com.Workspace.(workspace.Sharing)
	if !ok || m.session == nil {
		return util.ReportInfo("Open a session first: s shares its thread")
	}
	cur := m.threadShares()[name]
	next := shareRoles[0]
	for i, r := range shareRoles {
		if r == cur {
			next = shareRoles[(i+1)%len(shareRoles)]
		}
	}
	sid := m.session.ID
	return func() tea.Msg {
		if err := sh.ShareThread(sid, name, next); err != nil {
			return util.NewErrorMsg(err)
		}
		if next == "" {
			return util.NewInfoMsg("This thread is no longer shared with " + name)
		}
		return util.NewInfoMsg("Shared this thread with " + name + " as " + next)
	}
}

// isTeammate reports whether row key sel is a teammate's row.
func (m *UI) isTeammate(sel string) bool {
	if m.teamSnap == nil || sel == "" {
		return false
	}
	for _, ps := range m.teamSnap.Peers {
		if ps.Name == sel {
			return true
		}
	}
	return false
}

// briefKey types the brief of a contract for p.asking: enter sends it, esc
// drops it.
func (p *teamPane) briefKey(m *UI, msg tea.KeyPressMsg) (bool, tea.Cmd) {
	switch msg.String() {
	case "esc":
		p.asking, p.draft = "", ""
		return true, nil
	case "enter":
		brief, name := strings.TrimSpace(p.draft), p.asking
		if brief == "" {
			return true, nil
		}
		p.asking, p.draft = "", ""
		cs, ok := m.com.Workspace.(workspace.Contracts)
		if !ok || m.session == nil {
			return true, nil
		}
		sid := m.session.ID
		return true, func() tea.Msg {
			id, queued, err := cs.ProposeContract(sid, name, brief)
			switch {
			case err != nil:
				return util.NewErrorMsg(err)
			case queued:
				return util.NewInfoMsg(name + " cannot be reached now: " + id + " goes to it when its link is back")
			}
			return util.NewInfoMsg("Asked " + name + " to do " + id + "; its person confirms it first")
		}
	}
	p.typeDraft(msg)
	return true, nil // the brief keeps the keys until enter or esc
}

// typeDraft edits p.draft with one key: text, backspace or ctrl+u.
func (p *teamPane) typeDraft(msg tea.KeyPressMsg) {
	switch msg.String() {
	case "backspace":
		if p.draft != "" {
			_, n := utf8.DecodeLastRuneInString(p.draft)
			p.draft = p.draft[:len(p.draft)-n]
		}
		return
	case "ctrl+u":
		p.draft = ""
		return
	}
	if msg.Text != "" && msg.Mod&(tea.ModCtrl|tea.ModAlt) == 0 && len(p.draft) < 4000 {
		p.draft += msg.Text
	}
}

// opsKey types an ops request for p.opsFor: enter reads it into a card,
// esc drops it.
func (p *teamPane) opsKey(msg tea.KeyPressMsg) (bool, tea.Cmd) {
	switch msg.String() {
	case "esc":
		p.opsFor, p.draft = "", ""
		return true, nil
	case "enter":
		name, c, err := peer.ParseOps(p.draft)
		if err != nil {
			return true, util.ReportError(err)
		}
		p.opsFor, p.draft, p.card = "", "", &opsCard{peer: name, c: c}
		return true, nil
	}
	p.typeDraft(msg)
	return true, nil
}

// cardKey answers the ops card: y proposes the contract, n or esc drops
// it. Nothing is proposed until the person presses y.
func (p *teamPane) cardKey(m *UI, msg tea.KeyPressMsg) (bool, tea.Cmd) {
	switch msg.String() {
	case "n", "esc":
		p.card = nil
		return true, util.ReportInfo("Dropped the request")
	case "y":
		card, link := *p.card, m.peerLink()
		p.card = nil
		if link == nil {
			return true, util.ReportError(fmt.Errorf("no peer link in this Deck"))
		}
		return true, func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), opsProposeTimeout)
			defer cancel()
			_, err := link.ProposeContract(ctx, card.peer, card.c)
			switch {
			case errors.As(err, new(peer.QueuedError)):
				return util.NewInfoMsg(card.peer + " cannot be reached now: the request goes when its link is back")
			case err != nil:
				return util.NewErrorMsg(err)
			}
			return util.NewInfoMsg("Asked " + card.peer + " to " + card.c.Title + "; the result shows in the Team pane")
		}
	}
	return true, nil // the card keeps the keys until y or n
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
	switch {
	case p.asking != "":
		return p.briefKey(m, msg)
	case p.opsFor != "":
		return p.opsKey(msg)
	case p.card != nil:
		return p.cardKey(m, msg)
	}
	rows := p.rows(m, 80)
	if p.list.navKey(rows, msg) {
		return true, nil
	}
	sel := p.list.selected(rows)
	if msg.String() == "s" && m.isTeammate(sel) {
		return true, m.cycleShare(sel)
	}
	if msg.String() == "o" && m.isTeammate(sel) && m.peerLink() != nil {
		p.opsFor, p.draft = sel, "ask "+sel+" to report "
		return true, nil
	}
	if msg.String() == "enter" && strings.HasPrefix(sel, opsKeyPrefix) {
		if p.open == sel {
			p.open = ""
		} else {
			p.open = sel
		}
		return true, nil
	}
	if msg.String() == "c" && m.isTeammate(sel) {
		if _, ok := m.com.Workspace.(workspace.Contracts); !ok || m.session == nil {
			return true, util.ReportInfo("Open a session first: the contract is a task on its board")
		}
		p.asking, p.draft = sel, ""
		return true, nil
	}
	c, ok := m.waitingContract(sel)
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
	if p.card != nil {
		rows = append(rows, p.cardRow(m, width))
	}
	rows = append(rows, p.contractRows(m, width)...)
	if len(snap.Peers) == 0 {
		rows = append(rows, paneRow{lines: []string{
			ansi.Truncate(muted.Render("No teammates yet."), width, "…"),
			ansi.Truncate(muted.Render("Pair one in Settings › Team."), width, "…"),
		}})
		return rows
	}
	now := time.Now()
	shares := m.threadShares()
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
		if role := shares[ps.Name]; role != "" {
			lines = append(lines, ansi.Truncate("  "+muted.Render("sees this thread as "+role), width, "…"))
		}
		if p.asking == ps.Name {
			lines = append(lines,
				ansi.Truncate("  contract for "+ps.Name+": "+p.draft+"▏", width, "…"),
				ansi.Truncate("  "+muted.Render("enter asks · esc drops"), width, "…"))
		}
		if p.opsFor == ps.Name {
			lines = append(lines,
				ansi.Truncate("  report: "+p.draft+"▏", width, "…"),
				ansi.Truncate("  "+muted.Render("e.g. ask "+ps.Name+" to report podman containers: name, state for web · enter checks it · esc drops"), width, "…"))
		}
		rows = append(rows, paneRow{key: ps.Name, selectable: true, lines: lines})
	}
	if _, ok := m.com.Workspace.(workspace.Contracts); ok && p.asking == "" && p.opsFor == "" {
		rows = append(rows, paneRow{lines: []string{ansi.Truncate(muted.Render("on a teammate: c asks for a contract · o asks for a report · s shares this thread"), width, "…")}})
	}
	return rows
}

// cardRow is the ops request the person typed, as it would be proposed:
// nothing goes until y.
func (p *teamPane) cardRow(m *UI, width int) paneRow {
	t := m.com.Styles
	muted := t.ModelInfo.Provider
	c := p.card.c
	targets := "all"
	if len(c.Targets) > 0 {
		targets = strings.Join(c.Targets, ", ")
	}
	fields := make([]string, len(c.Schema))
	for i, f := range c.Schema {
		fields[i] = f.Name
	}
	return paneRow{lines: []string{
		ansi.Truncate("▸ ask "+t.ModelInfo.Name.Bold(true).Render(p.card.peer)+" to "+c.Title, width, "…"),
		ansi.Truncate("  "+muted.Render("scope: "+strings.Join(c.Scope, ", ")+" · targets: "+targets), width, "…"),
		ansi.Truncate("  "+muted.Render("fields: "+strings.Join(fields, ", ")+" (text, at most 200 characters)"), width, "…"),
		ansi.Truncate("  "+muted.Render("y proposes · n drops"), width, "…"),
	}}
}

// maxContractLines caps the contracts listed besides those that wait.
const maxContractLines = 4

// contractRows are the contracts teammates proposed that wait for the
// person, as cards, then the latest others, one line each.
func (p *teamPane) contractRows(m *UI, width int) []paneRow {
	t := m.com.Styles
	muted := t.ModelInfo.Provider
	var rows, others []paneRow
	for _, c := range m.teamSnap.Contracts {
		if !c.Inbound || c.State != peer.ContractProposed || c.Kind == peer.KindOps {
			arrow, state := "→ ", c.State
			if c.Inbound {
				arrow = "← "
			}
			if c.Agent != "" && c.State == peer.ContractAccepted {
				state += " by " + c.Agent
			}
			line := arrow + c.Peer + " · " + c.Title + " · " + state
			if c.Kind != peer.KindOps || c.Inbound || c.Result == nil {
				others = append(others, paneRow{lines: []string{ansi.Truncate(muted.Render(line), width, "…")}})
				continue
			}
			key := opsKeyPrefix + c.Peer + "/" + c.ID
			if p.open != key {
				others = append(others, paneRow{key: key, selectable: true, lines: []string{
					ansi.Truncate(muted.Render(line+" (enter shows the rows)"), width, "…")}})
				continue
			}
			others = append(others, paneRow{key: key, selectable: true, lines: append([]string{
				ansi.Truncate(muted.Render(line+" (enter hides the rows)"), width, "…")}, opsTable(c, width)...)})
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
	return append(rows, others...)
}

// maxTableRows caps the rows of a result table; maxCellWidth its columns.
const (
	maxTableRows = 50
	maxCellWidth = 30
)

// opsTable is the result of ops contract c as lines at width: a header of
// its fields, then one line a row. The receiver and this Deck both checked
// the result against the schema, and it is shown as text.
func opsTable(c peer.Contract, width int) []string {
	var res struct {
		Rows []map[string]any `json:"rows"`
	}
	if json.Unmarshal(c.Result, &res) != nil {
		return nil
	}
	cells := make([][]string, 0, len(res.Rows)+1)
	head := make([]string, len(c.Schema))
	for i, f := range c.Schema {
		head[i] = f.Name
	}
	cells = append(cells, head)
	for _, r := range res.Rows {
		line := make([]string, len(c.Schema))
		for i, f := range c.Schema {
			switch v := r[f.Name].(type) {
			case string:
				line[i] = v
			case float64:
				line[i] = strconv.FormatFloat(v, 'f', -1, 64)
			case bool:
				line[i] = strconv.FormatBool(v)
			}
		}
		cells = append(cells, line)
	}
	widths := make([]int, len(head))
	for _, line := range cells {
		for i, s := range line {
			widths[i] = min(max(widths[i], ansi.StringWidth(s)), maxCellWidth)
		}
	}
	var out []string
	for n, line := range cells {
		if n > maxTableRows {
			out = append(out, fmt.Sprintf("  and %d more rows", len(cells)-n))
			break
		}
		var b strings.Builder
		for i, s := range line {
			s = ansi.Truncate(s, widths[i], "…")
			b.WriteString(s + strings.Repeat(" ", widths[i]-ansi.StringWidth(s)+2))
		}
		out = append(out, ansi.Truncate("  "+strings.TrimRight(b.String(), " "), width, "…"))
	}
	return out
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
