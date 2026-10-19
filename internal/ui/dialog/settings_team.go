package dialog

import (
	"context"
	"fmt"
	"image/color"
	"net"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	deckconfig "github.com/dev-dami/deck/internal/deck/config"
	"github.com/dev-dami/deck/internal/deck/peer"
	"github.com/dev-dami/deck/internal/ui/common"
	"github.com/dev-dami/deck/internal/ui/styles"
	"github.com/dev-dami/deck/internal/ui/util"
	"github.com/dev-dami/deck/internal/workspace"
)

// The Settings dialog's Team page: the peer link between Deck instances
// (docs/peers-design.md). Everything `deck peer` does can be done here, and
// every change is in effect once saved. The page follows the link through
// PeerLink.Wait in a command, never by polling in Update.

const (
	// teamWatchTimeout bounds one wait for a change, so a watch left behind
	// by a closed dialog ends on its own.
	teamWatchTimeout = 30 * time.Second
	// teamSettle lets a burst of link changes draw once.
	teamSettle = 250 * time.Millisecond
	// teamOpTimeout bounds a ping.
	teamOpTimeout = 15 * time.Second
)

// What a change on the Team page does to the running Deck.
const (
	badgeAppliesNow   = "applies now"
	badgeNeedsRestart = "needs restart"
)

// Status dots of the Team page.
const (
	dotOn  = "●"
	dotOff = "○"
)

// teamState is the Team page's part of the Settings dialog.
type teamState struct {
	link     workspace.PeerLink // nil when this Deck has no peer link
	snap     *peer.Snapshot     // nil until read
	watching bool               // a watch command is out
	ticking  bool               // a countdown tick is out
	busy     string             // the change being saved
	note     string             // the last change saved
	err      string             // why the last change failed
	edit     *teamEdit
	menu     *peerMenu
	pings    map[string]teamPing
	now      func() time.Time // tests
}

// teamEdit is a value being typed in place of a row's value.
type teamEdit struct {
	label string
	input textinput.Model
	apply func(string) Action
}

// peerMenu is the action menu open under a peer's row.
type peerMenu struct {
	peer    string
	sel     int
	confirm bool // asking whether to remove the peer
}

// peerMenuItem is one action of a peer's menu; run is nil for one that
// cannot be done now (its label says why).
type peerMenuItem struct {
	label string
	run   func() Action
}

// teamPing is the last ping of a peer.
type teamPing struct {
	busy bool
	res  peer.PingResult
	err  string
}

// teamSnapshotMsg carries the link as it is now.
type teamSnapshotMsg struct{ snap peer.Snapshot }

// teamDoneMsg is the outcome of a change, with the link after it.
type teamDoneMsg struct {
	label string
	err   error
	snap  peer.Snapshot
}

// teamPingMsg is a ping's outcome.
type teamPingMsg struct {
	name string
	res  peer.PingResult
	err  error
}

// teamTickMsg redraws countdowns.
type teamTickMsg struct{}

// These reach the Settings dialog even with another dialog in front.
func (teamSnapshotMsg) TargetDialog() string { return SettingsID }
func (teamDoneMsg) TargetDialog() string     { return SettingsID }
func (teamPingMsg) TargetDialog() string     { return SettingsID }
func (teamTickMsg) TargetDialog() string     { return SettingsID }

func (s *Settings) teamNow() time.Time {
	if s.team.now != nil {
		return s.team.now()
	}
	return time.Now()
}

// watchTeam reads the link at once the first time, then each time it
// changes. One watch is out at a time, and only while the Team page shows.
func (s *Settings) watchTeam() tea.Cmd {
	link := s.team.link
	if link == nil || s.team.watching {
		return nil
	}
	s.team.watching = true
	first, since := s.team.snap == nil, uint64(0)
	if !first {
		since = s.team.snap.Version
	}
	return func() tea.Msg {
		if !first {
			ctx, cancel := context.WithTimeout(context.Background(), teamWatchTimeout)
			link.Wait(ctx, since)
			cancel()
			time.Sleep(teamSettle)
		}
		return teamSnapshotMsg{snap: link.Snapshot()}
	}
}

// tickTeam redraws the page in a second while a countdown shows.
func (s *Settings) tickTeam() tea.Cmd {
	if s.team.ticking || s.tab != SettingsTabTeam || !s.teamCountdown() {
		return nil
	}
	s.team.ticking = true
	return tea.Tick(time.Second, func(time.Time) tea.Msg { return teamTickMsg{} })
}

// teamCountdown reports whether a peer is waiting to retry.
func (s *Settings) teamCountdown() bool {
	if s.team.snap == nil {
		return false
	}
	for _, p := range s.team.snap.Peers {
		if p.Link.Supervised && p.Link.Status.State == peer.StateBackoff && !p.Link.Status.Permanent {
			return true
		}
	}
	return false
}

// handleTeamMsg handles the Team page's messages.
func (s *Settings) handleTeamMsg(msg tea.Msg) Action {
	switch msg := msg.(type) {
	case teamSnapshotMsg:
		s.team.watching = false
		s.setTeamSnapshot(msg.snap)
		if s.tab != SettingsTabTeam {
			return nil // watched again when the page shows
		}
		return ActionCmd{Cmd: tea.Batch(s.watchTeam(), s.tickTeam())}
	case teamDoneMsg:
		s.team.busy = ""
		s.setTeamSnapshot(msg.snap)
		if msg.err != nil {
			s.team.err, s.team.note = friendlyErr(msg.err), ""
			return ActionCmd{Cmd: util.ReportWarn(msg.label + ": " + s.team.err)}
		}
		s.team.note, s.team.err = msg.label, ""
		return ActionCmd{Cmd: tea.Batch(util.ReportInfo(msg.label), s.tickTeam())}
	case teamPingMsg:
		p := teamPing{res: msg.res}
		if msg.err != nil {
			p.err = friendlyErr(msg.err)
		}
		if s.team.pings == nil {
			s.team.pings = map[string]teamPing{}
		}
		s.team.pings[msg.name] = p
	case teamTickMsg:
		s.team.ticking = false
		return ActionCmd{Cmd: s.tickTeam()}
	}
	return nil
}

// setTeamSnapshot shows snap unless a newer one is shown, and closes a menu
// whose peer is gone.
func (s *Settings) setTeamSnapshot(snap peer.Snapshot) {
	if s.team.snap != nil && snap.Version < s.team.snap.Version {
		return
	}
	s.team.snap = &snap
	if s.team.menu != nil && s.findPeer(s.team.menu.peer) == nil {
		s.team.menu = nil
	}
}

func (s *Settings) findPeer(name string) *peer.PeerState {
	if s.team.snap == nil {
		return nil
	}
	for i := range s.team.snap.Peers {
		if s.team.snap.Peers[i].Name == name {
			return &s.team.snap.Peers[i]
		}
	}
	return nil
}

// teamDo saves one change off the UI loop and reads the link after it.
func (s *Settings) teamDo(label string, fn func(workspace.PeerLink) error) Action {
	link := s.team.link
	if link == nil {
		return nil
	}
	s.team.busy, s.team.note, s.team.err = label, "", ""
	return ActionCmd{Cmd: func() tea.Msg {
		err := fn(link)
		return teamDoneMsg{label: label, err: err, snap: link.Snapshot()}
	}}
}

// pingPeer pings a peer off the UI loop.
func (s *Settings) pingPeer(name string) Action {
	link := s.team.link
	if link == nil {
		return nil
	}
	if s.team.pings == nil {
		s.team.pings = map[string]teamPing{}
	}
	s.team.pings[name] = teamPing{busy: true}
	return ActionCmd{Cmd: func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), teamOpTimeout)
		defer cancel()
		res, err := link.Ping(ctx, name)
		return teamPingMsg{name: name, res: res, err: err}
	}}
}

// startTeamEdit opens a text field in place of a row's value.
func (s *Settings) startTeamEdit(label, value, placeholder string, apply func(string) Action) Action {
	in := textinput.New()
	in.SetStyles(s.com.Styles.TextInput)
	// The field sits inside a row, not at a fixed place, so it draws its
	// own cursor.
	in.SetVirtualCursor(true)
	in.Placeholder = placeholder
	in.CharLimit = 64
	in.SetValue(value)
	in.CursorEnd()
	_ = in.Focus()
	s.team.edit = &teamEdit{label: label, input: in, apply: apply}
	return nil
}

// handleTeamKey handles the keys the Team page takes before the shared row
// keys; ok is false for a key it leaves to them.
func (s *Settings) handleTeamKey(msg tea.KeyPressMsg) (Action, bool) {
	if e := s.team.edit; e != nil {
		switch {
		case key.Matches(msg, s.keyMap.Back):
			s.team.edit = nil
		case key.Matches(msg, s.keyMap.Save):
			s.team.edit = nil
			return e.apply(strings.TrimSpace(e.input.Value())), true
		default:
			var cmd tea.Cmd
			e.input, cmd = e.input.Update(msg)
			return ActionCmd{Cmd: cmd}, true
		}
		return nil, true
	}
	if m := s.team.menu; m != nil {
		return s.handleMenuKey(m, msg), true
	}
	if s.team.link == nil || s.team.snap == nil {
		return nil, false
	}
	switch {
	case key.Matches(msg, s.keyMap.AddPeer):
		return ActionOpenDialog{DialogID: PeerPairID}, true
	case key.Matches(msg, s.keyMap.CopyFingerprint):
		return s.copyFingerprint(), true
	case key.Matches(msg, s.keyMap.ApplyNow):
		if s.team.snap.Pending {
			return s.teamDo("Applied the config", workspace.PeerLink.Reload), true
		}
		return nil, true
	case key.Matches(msg, s.keyMap.Edit):
		rows := s.teamRows()
		sel := min(max(0, s.sel[SettingsTabTeam]), len(rows)-1)
		switch rows[sel].label {
		case teamLabelName:
			return s.editName(), true
		case teamLabelListenOn:
			return s.editListen(), true
		}
		return nil, true
	}
	return nil, false
}

// handleMenuKey drives a peer's action menu and its remove confirmation.
func (s *Settings) handleMenuKey(m *peerMenu, msg tea.KeyPressMsg) Action {
	p := s.findPeer(m.peer)
	if p == nil {
		s.team.menu = nil
		return nil
	}
	if m.confirm {
		switch {
		case key.Matches(msg, s.keyMap.Yes):
			s.team.menu = nil
			name := p.Name
			return s.teamDo("Removed "+name, func(l workspace.PeerLink) error { return l.Remove(name) })
		case key.Matches(msg, s.keyMap.No):
			m.confirm = false
		}
		return nil
	}
	items := s.peerMenuItems(*p)
	m.sel = min(max(0, m.sel), len(items)-1)
	switch {
	case key.Matches(msg, s.keyMap.Back):
		s.team.menu = nil
	case key.Matches(msg, s.keyMap.Up):
		m.sel = (m.sel - 1 + len(items)) % len(items)
	case key.Matches(msg, s.keyMap.Down):
		m.sel = (m.sel + 1) % len(items)
	case key.Matches(msg, s.keyMap.Run):
		if run := items[m.sel].run; run != nil {
			return run()
		}
	}
	return nil
}

// peerMenuItems are the actions of p's menu.
func (s *Settings) peerMenuItems(p peer.PeerState) []peerMenuItem {
	name := p.Name
	ping := peerMenuItem{label: "Ping", run: func() Action { return s.pingPeer(name) }}
	if p.Addr == "" {
		ping = peerMenuItem{label: "Ping (it dials in: no address to ping)"}
	}
	other := deckconfig.PeerRoleContractor
	if p.Role == deckconfig.PeerRoleContractor {
		other = deckconfig.PeerRoleObserver
	}
	items := []peerMenuItem{
		ping,
		{label: "Role: " + p.Role + " (make " + other + ")", run: func() Action {
			return s.teamDo(name+" is now "+other, func(l workspace.PeerLink) error { return l.SetRole(name, other) })
		}},
	}
	if p.Addr != "" {
		dial := !p.Dial
		items = append(items, peerMenuItem{label: "Connect automatically: " + onOff(p.Dial), run: func() Action {
			return s.teamDo("Connect to "+name+" automatically "+onOff(dial), func(l workspace.PeerLink) error { return l.SetDial(name, dial) })
		}})
	}
	if p.Link.Status.Permanent || !p.Pinned {
		items = append(items, peerMenuItem{label: "Pair again…", run: func() Action {
			s.team.menu = nil
			return ActionOpenDialog{DialogID: PeerPairID}
		}})
	}
	return append(items, peerMenuItem{label: "Remove…", run: func() Action {
		s.team.menu.confirm = true
		return nil
	}})
}

// copyFingerprint copies this Deck's fingerprint (public, safe to share).
func (s *Settings) copyFingerprint() Action {
	if s.team.snap == nil || s.team.snap.Fingerprint == "" {
		return nil
	}
	return ActionCmd{Cmd: common.CopyToClipboard(s.team.snap.Fingerprint, "Fingerprint copied")}
}

func (s *Settings) editName() Action {
	snap := *s.team.snap
	value := snap.Name
	if snap.HostName {
		value = ""
	}
	return s.startTeamEdit(teamLabelName, value, peer.SelfName(nil), func(v string) Action {
		label := "Device name " + v
		if v == "" {
			label = "Device name from the host name"
		}
		return s.teamDo(label, func(l workspace.PeerLink) error { return l.SetName(v) })
	})
}

func (s *Settings) editListen() Action {
	l := s.team.snap.Listen
	return s.startTeamEdit(teamLabelListenOn, l.ListenAddr(), peer.LocalListenAddr, func(v string) Action {
		if v == "" {
			return nil
		}
		return s.teamDo("Listen on "+v, func(link workspace.PeerLink) error { return link.SetListen(l.Enabled, v) })
	})
}

// Team page row labels.
const (
	teamLabelLink      = "Team link"
	teamLabelName      = "Device name"
	teamLabelPrint     = "Fingerprint"
	teamLabelAccept    = "Accept connections"
	teamLabelListenOn  = "Listen on"
	teamLabelStatus    = "Status"
	teamLabelAdd       = "+ Add a teammate"
	teamSectionThis    = "This Deck"
	teamSectionConns   = "Connections"
	teamSectionPeers   = "Teammates"
	teamUnavailable    = "The team link is only here when Deck runs its agents."
	teamReading        = "Reading the team link…"
	teamAllIfacesWarn  = "Warning: any network this machine is on can reach it; only paired Decks get in."
	teamFingerprintNew = "made when the team link is first on"
)

// teamRows are the Team page's rows: this Deck, its listener, then one row
// per peer and the way to add one.
func (s *Settings) teamRows() []settingsRow {
	if s.team.link == nil || s.team.snap == nil {
		return nil
	}
	snap := *s.team.snap
	t := s.com.Styles

	name := snap.Name
	if snap.HostName {
		name += " (host name)"
	}
	fp := snap.Fingerprint
	var copyFP func(int) Action
	if fp == "" {
		fp = teamFingerprintNew
	} else {
		copyFP = func(int) Action { return s.copyFingerprint() }
	}
	listenHint := "Only this machine can connect. ←/→ switch · e types an address."
	switch {
	case snap.Listen.AllInterfaces():
		listenHint = teamAllIfacesWarn
	case !isLoopbackListen(snap.Listen):
		listenHint = "Only that address can be reached, e.g. a WireGuard IP. e edits it."
	}

	rows := []settingsRow{
		{
			section: teamSectionThis, label: teamLabelLink, value: onOff(snap.Enabled),
			hint: "Link this Deck with Decks you pair. Off: no listener, no connections.",
			change: func(int) Action {
				v := !snap.Enabled
				s.team.snap.Enabled = v
				return s.teamDo("Team link "+onOff(v), func(l workspace.PeerLink) error { return l.SetEnabled(v) })
			},
		},
		{
			section: teamSectionThis, label: teamLabelName, value: name,
			hint:   "How peers see this Deck. Enter edits; empty uses the host name.",
			change: func(int) Action { return s.editName() },
		},
		{
			section: teamSectionThis, label: teamLabelPrint, value: fp,
			hint:   "Compare it with the other screen when pairing. Enter or c copies it.",
			change: copyFP,
		},
		{
			section: teamSectionConns, label: teamLabelAccept, value: onOff(snap.Listen.Enabled),
			hint: "Let paired Decks connect to this one. Off: it only dials out.",
			change: func(int) Action {
				v := !snap.Listen.Enabled
				addr := ""
				if v && snap.Listen.Addr == "" {
					addr = peer.LocalListenAddr // a first listener is this machine only
				}
				s.team.snap.Listen.Enabled = v
				return s.teamDo("Accept connections "+onOff(v), func(l workspace.PeerLink) error { return l.SetListen(v, addr) })
			},
		},
		{
			section: teamSectionConns, label: teamLabelListenOn, value: listenLabel(snap.Listen),
			hint: listenHint,
			change: func(int) Action {
				addr := otherListenAddr(snap.Listen)
				enabled := snap.Listen.Enabled
				return s.teamDo("Listen on "+addr, func(l workspace.PeerLink) error { return l.SetListen(enabled, addr) })
			},
		},
	}
	status, statusColor := s.teamStatus(snap)
	statusHint := "What the link is doing now."
	if snap.Pending {
		statusHint = "Edited outside Deck: r applies it now (or restart Deck)."
	}
	rows = append(rows, settingsRow{
		section: teamSectionConns, label: teamLabelStatus, value: status,
		mark: dotOn, markColor: statusColor, hint: statusHint,
	})

	now := s.teamNow()
	for _, p := range snap.Peers {
		dot, c, text := peerStatus(t, p, snap, now)
		name := p.Name
		rows = append(rows, settingsRow{
			section: teamSectionPeers, label: p.Name, peer: p.Name,
			mark: dot, markColor: c, value: peerDetails(p, text, now),
			hint: peerHint(p),
			change: func(int) Action {
				s.team.menu = &peerMenu{peer: name}
				return nil
			},
		})
	}
	return append(rows, settingsRow{
		section: teamSectionPeers, label: teamLabelAdd, value: "pair with another Deck",
		hint:   "Pair with another Deck using a one-time code. a works on any row.",
		change: func(int) Action { return ActionOpenDialog{DialogID: PeerPairID} },
	})
}

// teamStatus is the Status row: what the listener does, with its dot color.
func (s *Settings) teamStatus(snap peer.Snapshot) (string, color.Color) {
	t := s.com.Styles
	muted := t.Dialog.ListItem.InfoBlurred.GetForeground()
	var text string
	c := muted
	switch {
	case !snap.Enabled:
		text = "off"
	case snap.ListenErr != "":
		text, c = "error: "+snap.ListenErr, t.Tool.IconError.GetForeground()
	case snap.Listening != "":
		text, c = "listening on "+snap.Listening, t.Tool.IconSuccess.GetForeground()
	case snap.Listen.Enabled:
		text = "not listening"
	default:
		text = "not listening (dials out only)"
	}
	if snap.Pairing {
		text += " · pairing"
	}
	if snap.Pending {
		text += " · " + badgeNeedsRestart
		c = t.LSP.WarningDiagnostic.GetForeground()
	}
	return text, c
}

// PeerStatus is a peer's dot, its color and a few words on its state, as
// the Team page shows them; the sidebar's Team pane shows the same.
func PeerStatus(t *styles.Styles, p peer.PeerState, snap peer.Snapshot, now time.Time) (string, color.Color, string) {
	return peerStatus(t, p, snap, now)
}

// peerStatus is a peer's dot, its color and a few words on its state.
func peerStatus(t *styles.Styles, p peer.PeerState, snap peer.Snapshot, now time.Time) (string, color.Color, string) {
	dot, text := peerStatusText(p, snap, now)
	switch dot {
	case peerDotOK:
		return dotOn, t.Tool.IconSuccess.GetForeground(), text
	case peerDotWarn:
		return dotOn, t.LSP.WarningDiagnostic.GetForeground(), text
	case peerDotBad:
		return dotOn, t.Tool.IconError.GetForeground(), text
	}
	return dotOff, t.Dialog.ListItem.InfoBlurred.GetForeground(), text
}

// peerDot is how a peer's link is doing, as its dot shows it.
type peerDot int

const (
	peerDotOff peerDot = iota
	peerDotOK
	peerDotWarn
	peerDotBad
)

// peerStatusText classifies a peer: its dot and a few words.
func peerStatusText(p peer.PeerState, snap peer.Snapshot, now time.Time) (peerDot, string) {
	l := p.Link
	switch {
	case !p.Pinned:
		return peerDotBad, "not paired"
	case l.Supervised:
		st := l.Status
		switch st.State {
		case peer.StateConnected:
			if st.Via != "" {
				return peerDotOK, "connected via " + st.Via
			}
			return peerDotOK, "connected"
		case peer.StateDegraded:
			return peerDotWarn, "degraded"
		case peer.StateDialing:
			return peerDotOff, "connecting…"
		case peer.StateBackoff:
			if st.Permanent {
				return peerDotBad, causeText(st.Cause)
			}
			wait := max(0, st.NextRetry.Sub(now).Round(time.Second))
			return peerDotOff, "not reachable · retry in " + formatSpan(wait)
		}
		return peerDotOff, "idle"
	case l.Inbound > 0 && l.Via != "":
		return peerDotOK, "connected (it dialed in via " + l.Via + ")"
	case l.Inbound > 0:
		return peerDotOK, "connected (it dialed in)"
	case !snap.Enabled:
		return peerDotOff, "team link off"
	case p.Addr == "" && snap.Listening != "":
		return peerDotOff, "waiting for it to connect"
	case p.Addr == "":
		return peerDotOff, "not reachable: it dials in, and this Deck is not listening"
	case !p.Dial:
		return peerDotOff, "not connected (auto-connect off)"
	}
	return peerDotOff, "not reachable"
}

// causeText says why a link failed for good.
func causeText(c peer.Cause) string {
	switch c {
	case peer.CauseHostKey:
		return "host key changed"
	case peer.CauseRefused:
		return "refused by the other Deck"
	case peer.CauseRevoked:
		return "revoked"
	}
	return "failing"
}

// peerHint is the hint under a selected peer's row.
func peerHint(p peer.PeerState) string {
	if !p.Pinned {
		return "In the config but not paired: pair again or remove it."
	}
	if p.Link.Status.Permanent {
		switch p.Link.Status.Cause {
		case peer.CauseHostKey:
			return "Its key changed: pair again only if it really has a new key."
		case peer.CauseRefused:
			return "It no longer accepts this Deck: pair again on both sides."
		case peer.CauseRevoked:
			return "No longer paired here: pair again or remove it."
		}
	}
	return "Enter: ping, role, auto-connect, remove."
}

// peerDetails is the value of a peer's row: state, latency, address, role
// and when it was last seen.
func peerDetails(p peer.PeerState, state string, now time.Time) string {
	parts := []string{state}
	connected := p.Link.Inbound > 0 || (p.Link.Supervised &&
		(p.Link.Status.State == peer.StateConnected || p.Link.Status.State == peer.StateDegraded))
	if connected && p.Link.Status.Latency > 0 {
		parts = append(parts, formatLatency(p.Link.Status.Latency))
	}
	addr := p.Addr
	if addr == "" {
		addr = "dials in"
	}
	parts = append(parts, peer.Sanitize(addr), p.Role)
	if !connected && !p.Link.LastSeen.IsZero() {
		parts = append(parts, "seen "+formatSpan(now.Sub(p.Link.LastSeen))+" ago")
	}
	return strings.Join(parts, " · ")
}

func formatLatency(d time.Duration) string {
	if d < time.Millisecond {
		return "<1ms"
	}
	return d.Round(time.Millisecond).String()
}

// listenLabel says who can reach the listener, then its address.
func listenLabel(l deckconfig.PeerListen) string {
	addr := l.ListenAddr()
	switch {
	case l.AllInterfaces():
		return "all interfaces · " + addr
	case isLoopbackListen(l):
		return "this machine only · " + addr
	}
	host, _, _ := net.SplitHostPort(addr)
	return "only " + peer.Sanitize(host) + " · " + addr
}

func isLoopbackListen(l deckconfig.PeerListen) bool {
	host, _, err := net.SplitHostPort(l.ListenAddr())
	if err != nil {
		return false
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// otherListenAddr is the address ←/→ switches the listener to: every
// interface from this machine only, else this machine only; the port stays.
func otherListenAddr(l deckconfig.PeerListen) string {
	_, port, err := net.SplitHostPort(l.ListenAddr())
	if err != nil {
		port = deckconfig.DefaultPeerPort
	}
	if isLoopbackListen(l) {
		return net.JoinHostPort("0.0.0.0", port)
	}
	return net.JoinHostPort("127.0.0.1", port)
}

// friendlyErr is an error as one short line, without the package prefix.
func friendlyErr(err error) string {
	return strings.TrimPrefix(peer.Sanitize(err.Error()), "peer: ")
}

// renderTeamRow draws a row like renderRow, but a value too long for its
// column (a fingerprint, an address with its reach) goes on over more lines
// under the value column instead of being cut off.
func (s *Settings) renderTeamRow(r settingsRow, selected bool, width int) []string {
	t := s.com.Styles
	text := max(0, width-2)
	labelW := min(settingsLabelWidth, text/2)
	mark := ""
	if r.mark != "" {
		mark = padRight(r.mark, settingsMarkWidth) + " "
	}
	valueW := max(1, text-labelW-ansi.StringWidth(mark))
	if ansi.StringWidth(r.value) <= valueW {
		return []string{s.renderRow(r, selected, width)}
	}
	var values []string
	for _, v := range strings.Split(ansi.Wrap(r.value, valueW, ""), "\n") {
		values = append(values, padRight(v, valueW))
	}
	label := padRight(ansi.Truncate(r.label, labelW, "…"), labelW)
	indent := strings.Repeat(" ", labelW+ansi.StringWidth(mark))
	out := make([]string, 0, len(values))
	for i, v := range values {
		pre := indent
		if i == 0 {
			pre = label + mark
		}
		if selected {
			out = append(out, t.Dialog.SelectedItem.Render(pre+v))
			continue
		}
		if i == 0 {
			pre = t.Dialog.NormalItem.UnsetPaddingRight().Render(label)
			if mark != "" && r.markColor != nil {
				pre += lipgloss.NewStyle().Foreground(r.markColor).Render(mark)
			} else {
				pre += mark
			}
		} else {
			pre = t.Dialog.NormalItem.UnsetPaddingRight().Render(indent)
		}
		valueStyle := t.Dialog.ListItem.InfoFocused
		if r.change == nil {
			valueStyle = t.Dialog.ListItem.InfoBlurred
		}
		out = append(out, pre+valueStyle.Render(v)+" ")
	}
	return out
}

// teamLines renders the Team page, and the line of the selected row.
func (s *Settings) teamLines(width int) ([]string, int) {
	t := s.com.Styles
	muted := t.Dialog.SecondaryText
	wrap := func(str string) []string { return wrapText(muted, str, width) }
	if s.team.link == nil {
		return wrap(teamUnavailable), -1
	}
	if s.team.snap == nil {
		return wrap(teamReading), -1
	}
	rows := s.teamRows()
	sel := min(max(0, s.sel[SettingsTabTeam]), len(rows)-1)
	lines := make([]string, 0, len(rows)+12)
	focus, section := 0, ""
	for i, r := range rows {
		if r.section != section {
			if section != "" {
				lines = append(lines, "")
			}
			section = r.section
			lines = append(lines, t.Dialog.PrimaryText.Render(section))
		}
		if i == sel {
			focus = len(lines)
		}
		if i == sel && s.team.edit != nil {
			lines = append(lines, s.renderEditRow(r, width))
		} else {
			lines = append(lines, s.renderTeamRow(r, i == sel, width)...)
		}
		if r.peer == "" {
			continue
		}
		if i == sel && s.team.menu != nil && s.team.menu.peer == r.peer {
			lines = append(lines, s.menuLines(r.peer, width)...)
		}
		if ping, ok := s.team.pings[r.peer]; ok && i == sel {
			lines = append(lines, wrapText(muted.PaddingLeft(4), pingText(ping), width)...)
		}
	}
	if snap := s.team.snap; snap.Err != "" {
		lines = append(lines, "")
		lines = append(lines, wrapText(t.Dialog.TitleError.PaddingLeft(1), snap.Err, width)...)
	}
	lines = append(lines, "")
	lines = append(lines, wrap(rows[sel].hint)...)
	lines = append(lines, s.teamOutcome(width)...)
	return lines, focus
}

// teamOutcome is the line under the hint: the change being saved, the last
// one's outcome, or whether changes here apply now.
func (s *Settings) teamOutcome(width int) []string {
	t := s.com.Styles
	var str string
	switch {
	case s.team.busy != "":
		str = "Saving: " + s.team.busy + "…"
	case s.team.err != "":
		return wrapText(t.Dialog.TitleError.PaddingLeft(1), "Not saved: "+s.team.err, width)
	case s.team.note != "":
		str = "Saved: " + s.team.note + " · " + badgeAppliesNow
	case s.team.snap != nil && s.team.snap.Pending:
		str = "Edited outside Deck: " + badgeNeedsRestart + ", or r applies it now"
	default:
		str = "Changes here " + badgeAppliesNow + ", no restart"
	}
	return wrapText(t.Dialog.SecondaryText, str, width)
}

// renderEditRow draws a row whose value is being typed.
func (s *Settings) renderEditRow(r settingsRow, width int) string {
	t := s.com.Styles
	text := max(0, width-2)
	labelW := min(settingsLabelWidth, text/2)
	e := s.team.edit
	e.input.SetWidth(max(1, text-labelW-ansi.StringWidth(e.input.Prompt)-1))
	label := padRight(ansi.Truncate(r.label, labelW, "…"), labelW)
	return t.Dialog.NormalItem.UnsetPaddingRight().Render(label) + ansi.Truncate(e.input.View(), text-labelW, "")
}

// menuLines draws the action menu under a peer's row.
func (s *Settings) menuLines(name string, width int) []string {
	t := s.com.Styles
	text := max(0, width-2)
	m, p := s.team.menu, s.findPeer(name)
	if p == nil {
		return nil
	}
	if m.confirm {
		q := fmt.Sprintf("Remove %s? It can no longer connect, and this Deck stops dialing it.", name)
		return append(wrapText(t.Dialog.TitleError.PaddingLeft(4), q, width),
			wrapText(t.Dialog.SecondaryText.PaddingLeft(4), "y remove · n keep", width)...)
	}
	items := s.peerMenuItems(*p)
	sel := min(max(0, m.sel), len(items)-1)
	out := make([]string, 0, len(items))
	for i, it := range items {
		label := padRight(ansi.Truncate("  "+it.label, max(0, text-4), "…"), max(0, text-4))
		switch {
		case i == sel:
			out = append(out, "  "+t.Dialog.SelectedItem.Render(label))
		case it.run == nil:
			out = append(out, "  "+t.Dialog.SecondaryText.Render(label))
		default:
			out = append(out, "  "+t.Dialog.NormalItem.Render(label))
		}
	}
	return out
}

// pingText is a ping's outcome in a few words.
func pingText(p teamPing) string {
	switch {
	case p.busy:
		return "pinging…"
	case p.err != "":
		return "ping failed: " + p.err
	}
	str := "pong in " + formatLatency(p.res.Latency)
	if len(p.res.Tools) > 0 {
		str += " · tools: " + strings.Join(p.res.Tools, ", ")
	}
	return str
}

// teamHelp is the Team page's key help for what is on screen.
func (s *Settings) teamHelp() []key.Binding {
	switch {
	case s.team.edit != nil:
		return []key.Binding{s.keyMap.Save, s.keyMap.Back}
	case s.team.menu != nil && s.team.menu.confirm:
		return []key.Binding{s.keyMap.Yes, s.keyMap.No}
	case s.team.menu != nil:
		return []key.Binding{s.keyMap.UpDown, s.keyMap.Run, s.keyMap.Back}
	case s.team.snap != nil && s.team.snap.Pending:
		return []key.Binding{s.keyMap.UpDown, s.keyMap.Change, s.keyMap.ApplyNow, s.keyMap.AddPeer, s.keyMap.Close}
	}
	return []key.Binding{s.keyMap.UpDown, s.keyMap.Change, s.keyMap.AddPeer, s.keyMap.CopyFingerprint, s.keyMap.NextTab, s.keyMap.Close}
}
