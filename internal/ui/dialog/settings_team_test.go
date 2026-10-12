package dialog

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"

	"github.com/dev-dami/deck/internal/config"
	deckconfig "github.com/dev-dami/deck/internal/deck/config"
	"github.com/dev-dami/deck/internal/deck/peer"
	"github.com/dev-dami/deck/internal/ui/common"
	"github.com/dev-dami/deck/internal/ui/styles"
	"github.com/dev-dami/deck/internal/workspace"
)

// fakePeerLink is a PeerLink that records what the UI asked of it.
type fakePeerLink struct {
	mu    sync.Mutex
	snap  peer.Snapshot
	calls []string

	wait     *peer.PairWait
	startErr error
	dialRes  peer.PairResult
	dialErr  error
	dialArgs []string
	pinErrs  []error // returned by Pin in turn; nil after
	pinned   []peer.PinRequest
}

func (f *fakePeerLink) record(format string, a ...any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, fmt.Sprintf(format, a...))
}

func (f *fakePeerLink) Snapshot() peer.Snapshot { return f.snap }
func (f *fakePeerLink) Wait(ctx context.Context, since uint64) uint64 {
	<-ctx.Done()
	return since
}
func (f *fakePeerLink) Reload() error { f.record("Reload"); return nil }
func (f *fakePeerLink) SetEnabled(on bool) error {
	f.record("SetEnabled %v", on)
	f.snap.Enabled = on
	return nil
}
func (f *fakePeerLink) SetName(name string) error { f.record("SetName %s", name); return nil }
func (f *fakePeerLink) SetListen(enabled bool, addr string) error {
	f.record("SetListen %v %s", enabled, addr)
	return nil
}
func (f *fakePeerLink) SetRole(name, role string) error {
	f.record("SetRole %s %s", name, role)
	return nil
}
func (f *fakePeerLink) SetDial(name string, dial bool) error {
	f.record("SetDial %s %v", name, dial)
	return nil
}
func (f *fakePeerLink) Remove(name string) error { f.record("Remove %s", name); return nil }
func (f *fakePeerLink) Ping(context.Context, string) (peer.PingResult, error) {
	return peer.PingResult{Latency: 12 * time.Millisecond, Tools: []string{"search", "execute"}}, nil
}
func (f *fakePeerLink) StartPairing(string) (*peer.PairWait, error) {
	f.record("StartPairing")
	return f.wait, f.startErr
}
func (f *fakePeerLink) PairDial(_ context.Context, addr, code string) (peer.PairResult, error) {
	f.dialArgs = []string{addr, code}
	return f.dialRes, f.dialErr
}
func (f *fakePeerLink) Pin(r peer.PinRequest) error {
	if len(f.pinErrs) > 0 {
		err := f.pinErrs[0]
		f.pinErrs = f.pinErrs[1:]
		if err != nil {
			return err
		}
	}
	f.pinned = append(f.pinned, r)
	return nil
}

func (f *fakePeerLink) called(call string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.calls {
		if c == call {
			return true
		}
	}
	return false
}

// teamWorkspace is a settings workspace with a peer link.
type teamWorkspace struct {
	*settingsWorkspace
	link *fakePeerLink
}

func (w *teamWorkspace) PeerLink() workspace.PeerLink {
	if w.link == nil {
		return nil
	}
	return w.link
}

var teamNow = time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)

// baseSnapshot is a Deck with the team link on, listening on this machine
// only, and no peers.
func baseSnapshot() peer.Snapshot {
	return peer.Snapshot{
		Version: 1, Enabled: true, Name: "pc", Fingerprint: "SHA256:ownfingerprint",
		Listen:    deckconfig.PeerListen{Enabled: true, Addr: "127.0.0.1:7722"},
		Listening: "127.0.0.1:7722", Running: true,
	}
}

func newTeamComForTest(link *fakePeerLink) *common.Common {
	s := styles.DeckDark()
	ws := &teamWorkspace{
		settingsWorkspace: &settingsWorkspace{
			cfg:  &config.Config{Options: &config.Options{}},
			deck: deckconfig.Config{DefaultAgent: "claude", Motion: "on"},
		},
		link: link,
	}
	return &common.Common{Workspace: ws, Styles: &s}
}

func newTeamSettingsForTest(t *testing.T, snap peer.Snapshot) (*Settings, *fakePeerLink) {
	t.Helper()
	link := &fakePeerLink{snap: snap}
	s := NewSettings(newTeamComForTest(link), SettingsTabTeam)
	s.team.now = func() time.Time { return teamNow }
	s.HandleMsg(teamSnapshotMsg{snap: snap})
	return s, link
}

func teamText(s *Settings, width int) string {
	lines, _ := s.teamLines(width)
	return ansi.Strip(strings.Join(lines, "\n"))
}

// selectRow moves the Team page's selection to row i.
func selectRow(s *Settings, i int) { s.sel[SettingsTabTeam] = i }

// run runs an action's command and returns its message.
func run(t *testing.T, act Action) tea.Msg {
	t.Helper()
	c, ok := act.(ActionCmd)
	require.True(t, ok, "want a command, got %#v", act)
	require.NotNil(t, c.Cmd)
	return c.Cmd()
}

func TestTeamRowsRender(t *testing.T) {
	t.Parallel()
	snap := baseSnapshot()
	snap.Peers = []peer.PeerState{{Name: "vps", Addr: "vps.example.com:7722", Role: "observer", Pinned: true, Configured: true, Dial: true,
		Link: peer.Link{Supervised: true, Status: peer.Status{State: peer.StateConnected, Latency: 12 * time.Millisecond}}}}
	s, _ := newTeamSettingsForTest(t, snap)

	const width = 70
	lines, _ := s.teamLines(width)
	for _, l := range lines {
		require.LessOrEqual(t, ansi.StringWidth(l), width, "line %q overflows", ansi.Strip(l))
	}
	text := teamText(s, 110)
	for _, want := range []string{
		"This Deck", "Team link", "Device name", "pc", "Fingerprint", "SHA256:ownfingerprint",
		"Connections", "Accept connections", "this machine only · 127.0.0.1:7722", "listening on 127.0.0.1:7722",
		"Teammates", "vps", "connected · 12ms · vps.example.com:7722 · observer", teamLabelAdd,
	} {
		require.Contains(t, text, want)
	}
}

func TestTeamUnavailableWithoutLink(t *testing.T) {
	t.Parallel()
	s := NewSettings(newTeamComForTest(nil), SettingsTabTeam)
	require.Contains(t, teamText(s, 70), teamUnavailable)
	require.Nil(t, s.watchTeam(), "no watch without a link")
	require.Nil(t, s.teamRows())
}

func TestTeamToggleWritesConfig(t *testing.T) {
	t.Parallel()
	snap := baseSnapshot()
	snap.Enabled = false
	s, link := newTeamSettingsForTest(t, snap)

	msg := run(t, press(s, tea.KeyEnter)) // Team link
	require.True(t, link.called("SetEnabled true"))
	require.True(t, s.team.snap.Enabled, "the page shows the change at once")
	done, ok := msg.(teamDoneMsg)
	require.True(t, ok)
	s.HandleMsg(done)
	require.Equal(t, "Team link on", s.team.note)
	require.Contains(t, teamText(s, 70), "Saved: Team link on · "+badgeAppliesNow)
}

func TestTeamAcceptConnectionsStartsOnThisMachine(t *testing.T) {
	t.Parallel()
	snap := baseSnapshot()
	snap.Listen, snap.Listening = deckconfig.PeerListen{}, ""
	s, link := newTeamSettingsForTest(t, snap)
	selectRow(s, 3) // Accept connections
	run(t, press(s, tea.KeyEnter))
	require.True(t, link.called("SetListen true "+peer.LocalListenAddr))
}

func TestTeamListenOnSwitchesAndWarns(t *testing.T) {
	t.Parallel()
	s, link := newTeamSettingsForTest(t, baseSnapshot())
	selectRow(s, 4) // Listen on
	run(t, press(s, tea.KeyRight))
	require.True(t, link.called("SetListen true 0.0.0.0:7722"))

	all := baseSnapshot()
	all.Version = 2
	all.Listen.Addr, all.Listening = "0.0.0.0:7722", "0.0.0.0:7722"
	s.HandleMsg(teamSnapshotMsg{snap: all})
	text := teamText(s, 120)
	require.Contains(t, text, "all interfaces · 0.0.0.0:7722")
	require.Contains(t, text, "Warning:")

	run(t, press(s, tea.KeyLeft))
	require.True(t, link.called("SetListen true 127.0.0.1:7722"), "back to this machine only")
}

func TestTeamEditDeviceName(t *testing.T) {
	t.Parallel()
	s, link := newTeamSettingsForTest(t, baseSnapshot())
	selectRow(s, 1)
	require.Nil(t, press(s, tea.KeyEnter))
	require.NotNil(t, s.team.edit)
	require.Equal(t, "pc", s.team.edit.input.Value())

	// Esc cancels without saving, and does not close the dialog.
	require.Nil(t, press(s, tea.KeyEscape))
	require.Nil(t, s.team.edit)

	press(s, tea.KeyEnter)
	s.team.edit.input.SetValue("studio")
	run(t, press(s, tea.KeyEnter))
	require.True(t, link.called("SetName studio"))
	require.Nil(t, s.team.edit)
}

func TestTeamCopyAndAddKeys(t *testing.T) {
	t.Parallel()
	s, _ := newTeamSettingsForTest(t, baseSnapshot())
	_, ok := s.HandleMsg(tea.KeyPressMsg{Code: 'c', Text: "c"}).(ActionCmd)
	require.True(t, ok, "c copies the fingerprint")
	require.Equal(t, ActionOpenDialog{DialogID: PeerPairID}, s.HandleMsg(tea.KeyPressMsg{Code: 'a', Text: "a"}))
}

func TestTeamBadges(t *testing.T) {
	t.Parallel()
	s, link := newTeamSettingsForTest(t, baseSnapshot())
	text := teamText(s, 100)
	require.Contains(t, text, badgeAppliesNow)
	require.NotContains(t, text, badgeNeedsRestart)
	require.Nil(t, s.HandleMsg(tea.KeyPressMsg{Code: 'r', Text: "r"}), "nothing to apply")

	pending := baseSnapshot()
	pending.Version, pending.Pending = 2, true
	s.HandleMsg(teamSnapshotMsg{snap: pending})
	text = teamText(s, 100)
	require.Contains(t, text, "listening on 127.0.0.1:7722 · "+badgeNeedsRestart)
	require.Contains(t, text, "Edited outside Deck: "+badgeNeedsRestart)

	run(t, s.HandleMsg(tea.KeyPressMsg{Code: 'r', Text: "r"}))
	require.True(t, link.called("Reload"))
}

func TestTeamPeerStates(t *testing.T) {
	t.Parallel()
	on := baseSnapshot()
	off := baseSnapshot()
	off.Enabled = false
	sup := func(st peer.Status) peer.Link { return peer.Link{Supervised: true, Status: st} }
	pinned := func(l peer.Link) peer.PeerState {
		return peer.PeerState{Name: "vps", Addr: "vps.example.com:7722", Role: "observer", Pinned: true, Dial: true, Link: l}
	}
	cases := []struct {
		name string
		p    peer.PeerState
		snap peer.Snapshot
		dot  peerDot
		text string
	}{
		{"connected", pinned(sup(peer.Status{State: peer.StateConnected})), on, peerDotOK, "connected"},
		{"degraded", pinned(sup(peer.Status{State: peer.StateDegraded})), on, peerDotWarn, "degraded"},
		{"backoff", pinned(sup(peer.Status{State: peer.StateBackoff, NextRetry: teamNow.Add(12 * time.Second)})), on, peerDotOff, "retry in 12s"},
		{"host key", pinned(sup(peer.Status{State: peer.StateBackoff, Permanent: true, Cause: peer.CauseHostKey})), on, peerDotBad, "host key changed"},
		{"revoked", pinned(sup(peer.Status{State: peer.StateBackoff, Permanent: true, Cause: peer.CauseRevoked})), on, peerDotBad, "revoked"},
		{"inbound", peer.PeerState{Name: "pc2", Role: "observer", Pinned: true, Link: peer.Link{Inbound: 1}}, on, peerDotOK, "connected (it dialed in)"},
		{"waiting", peer.PeerState{Name: "pc2", Role: "observer", Pinned: true}, on, peerDotOff, "waiting for it to connect"},
		{"team off", pinned(peer.Link{}), off, peerDotOff, "team link off"},
		{"not reachable", pinned(peer.Link{}), on, peerDotOff, "not reachable"},
		{"not paired", peer.PeerState{Name: "ghost", Configured: true}, on, peerDotBad, "not paired"},
	}
	for _, c := range cases {
		dot, text := peerStatusText(c.p, c.snap, teamNow)
		require.Equal(t, c.dot, dot, c.name)
		require.Contains(t, text, c.text, c.name)
	}

	seen := pinned(peer.Link{LastSeen: teamNow.Add(-3 * time.Minute)})
	require.Contains(t, peerDetails(seen, "not reachable", teamNow), "seen 3m ago")
}

func TestTeamPermanentFailureHint(t *testing.T) {
	t.Parallel()
	snap := baseSnapshot()
	snap.Peers = []peer.PeerState{{Name: "vps", Addr: "vps.example.com:7722", Role: "observer", Pinned: true, Dial: true,
		Link: peer.Link{Supervised: true, Status: peer.Status{State: peer.StateBackoff, Permanent: true, Cause: peer.CauseHostKey}}}}
	s, _ := newTeamSettingsForTest(t, snap)
	selectRow(s, 6)
	text := teamText(s, 100)
	require.Contains(t, text, "host key changed")
	require.Contains(t, text, "Its key changed")

	press(s, tea.KeyEnter)
	items := s.peerMenuItems(snap.Peers[0])
	labels := make([]string, len(items))
	for i, it := range items {
		labels[i] = it.label
	}
	require.Contains(t, labels, "Pair again…")
}

func TestTeamPeerMenu(t *testing.T) {
	t.Parallel()
	snap := baseSnapshot()
	snap.Peers = []peer.PeerState{{Name: "vps", Addr: "vps.example.com:7722", Role: "observer", Pinned: true, Configured: true, Dial: true}}
	s, link := newTeamSettingsForTest(t, snap)
	selectRow(s, 6)

	require.Nil(t, press(s, tea.KeyEnter))
	require.NotNil(t, s.team.menu)
	text := teamText(s, 100)
	require.Contains(t, text, "Ping")
	require.Contains(t, text, "Role: observer (make contractor)")
	require.Contains(t, text, "Connect automatically: on")
	require.Contains(t, text, "Remove…")

	// Ping.
	msg := run(t, press(s, tea.KeyEnter))
	s.HandleMsg(msg)
	require.Contains(t, teamText(s, 100), "pong in 12ms · tools: search, execute")

	// Change role.
	press(s, tea.KeyDown)
	run(t, press(s, tea.KeyEnter))
	require.True(t, link.called("SetRole vps contractor"))

	// Remove, after a confirmation; n keeps it.
	press(s, tea.KeyDown)
	press(s, tea.KeyDown)
	require.Nil(t, press(s, tea.KeyEnter))
	require.True(t, s.team.menu.confirm)
	require.Contains(t, teamText(s, 100), "Remove vps?")
	s.HandleMsg(tea.KeyPressMsg{Code: 'n', Text: "n"})
	require.False(t, s.team.menu.confirm)
	require.False(t, link.called("Remove vps"))

	press(s, tea.KeyEnter)
	run(t, s.HandleMsg(tea.KeyPressMsg{Code: 'y', Text: "y"}))
	require.True(t, link.called("Remove vps"))
	require.Nil(t, s.team.menu)

	// Esc closes the menu, not the dialog.
	press(s, tea.KeyEnter)
	require.Nil(t, press(s, tea.KeyEscape))
	require.Nil(t, s.team.menu)
}

func TestTeamStaleSnapshotIgnored(t *testing.T) {
	t.Parallel()
	snap := baseSnapshot()
	snap.Version = 5
	s, _ := newTeamSettingsForTest(t, snap)
	old := baseSnapshot()
	old.Version, old.Name = 4, "old"
	s.HandleMsg(teamSnapshotMsg{snap: old})
	require.Equal(t, "pc", s.team.snap.Name)
}

// The Add a teammate wizard.

func newPairForTest(link *fakePeerLink) *PeerPair {
	p := NewPeerPair(newTeamComForTest(link))
	p.now = func() time.Time { return teamNow }
	return p
}

func pairKey(p *PeerPair, code rune) Action {
	return p.HandleMsg(tea.KeyPressMsg{Code: code})
}

func pairText(p *PeerPair) string {
	return ansi.Strip(strings.Join(p.lines(64), "\n"))
}

func testPairWait() (*peer.PairWait, chan peer.PairOutcome) {
	ch := make(chan peer.PairOutcome, 1)
	return &peer.PairWait{
		Code: "ABCDE-23456", Addr: "0.0.0.0:7722", Fingerprint: "SHA256:own", Name: "pc",
		Expires: teamNow.Add(5 * time.Minute), Done: ch,
	}, ch
}

var testPairResult = peer.PairResult{
	PeerName: "vps", PeerFingerprint: "SHA256:theirs", OwnFingerprint: "SHA256:own", RemoteAddr: "203.0.113.5:50000",
}

// toWaitConfirm walks wait mode up to the fingerprints.
func toWaitConfirm(t *testing.T, p *PeerPair, link *fakePeerLink) {
	t.Helper()
	started := run(t, pairKey(p, tea.KeyEnter)) // wait mode is the default
	require.True(t, link.called("StartPairing"))
	require.Equal(t, pairWaiting, p.step)
	act := p.HandleMsg(started)
	require.NotNil(t, act)
	text := pairText(p)
	require.Contains(t, text, "A B C D E - 2 3 4 5 6", "the code shows big")
	require.Contains(t, text, "expires in 5:00")
	require.Contains(t, text, "SHA256:own")
	require.Contains(t, text, "0.0.0.0:7722 (all interfaces)")

	p.HandleMsg(pairOutcomeMsg{req: p.req, out: peer.PairOutcome{Result: testPairResult}})
	require.Equal(t, pairConfirm, p.step)
	text = pairText(p)
	require.Contains(t, text, "SHA256:theirs")
	require.Contains(t, text, "SHA256:own")
	require.Contains(t, text, "Do these match on the other screen?")
	require.Equal(t, "vps", p.name.Value(), "the name it sent is offered")
}

func TestPairWaitModeFlow(t *testing.T) {
	t.Parallel()
	w, _ := testPairWait()
	link := &fakePeerLink{wait: w}
	p := newPairForTest(link)
	require.Contains(t, pairText(p), "This Deck waits for a code")
	toWaitConfirm(t, p, link)

	msg := run(t, pairKey(p, tea.KeyEnter)) // yes, they match
	require.Equal(t, pairSaving, p.step)
	require.Len(t, link.pinned, 1)
	require.Equal(t, peer.PinRequest{Name: "vps", Result: testPairResult}, link.pinned[0])
	p.HandleMsg(msg)
	require.Equal(t, pairDone, p.step)
	require.Contains(t, pairText(p), "Paired with vps.")
	require.Equal(t, ActionClose{}, pairKey(p, tea.KeyEnter))
}

func TestPairWaitModeMismatchAbortsAndPinsNothing(t *testing.T) {
	t.Parallel()
	w, _ := testPairWait()
	link := &fakePeerLink{wait: w}
	p := newPairForTest(link)
	toWaitConfirm(t, p, link)

	require.Nil(t, pairKey(p, tea.KeyEscape)) // no
	require.Equal(t, pairFailed, p.step)
	require.Contains(t, pairText(p), "Not paired: nothing was saved")
	require.Empty(t, link.pinned)

	// Enter starts again from the first step.
	pairKey(p, tea.KeyEnter)
	require.Equal(t, pairChoose, p.step)
}

func TestPairEscWhileWaitingStopsAndIgnoresLateResult(t *testing.T) {
	t.Parallel()
	w, _ := testPairWait()
	link := &fakePeerLink{wait: w}
	p := newPairForTest(link)
	started := run(t, pairKey(p, tea.KeyEnter))
	p.HandleMsg(started)
	req := p.req

	require.Equal(t, ActionClose{}, pairKey(p, tea.KeyEscape))
	require.Nil(t, p.wait)
	p.HandleMsg(pairOutcomeMsg{req: req, out: peer.PairOutcome{Result: testPairResult}})
	require.NotEqual(t, pairConfirm, p.step, "an abandoned pairing never reaches the fingerprints")
}

func TestPairWaitModeExpired(t *testing.T) {
	t.Parallel()
	w, _ := testPairWait()
	link := &fakePeerLink{wait: w}
	p := newPairForTest(link)
	p.HandleMsg(run(t, pairKey(p, tea.KeyEnter)))
	p.HandleMsg(pairOutcomeMsg{req: p.req, out: peer.PairOutcome{Err: peer.ErrPairExpired}})
	require.Equal(t, pairFailed, p.step)
	require.Contains(t, pairText(p), "The code expired")
}

// toConnectConfirm walks connect mode up to the fingerprints.
func toConnectConfirm(t *testing.T, p *PeerPair, link *fakePeerLink) {
	t.Helper()
	pairKey(p, tea.KeyDown)
	require.Equal(t, pairModeConnect, p.mode)
	pairKey(p, tea.KeyEnter)
	require.Equal(t, pairForm, p.step)

	// Nothing typed: Enter asks for the name first.
	require.Nil(t, pairKey(p, tea.KeyEnter))
	require.Contains(t, p.err, "name")

	p.inputs[fieldName].SetValue("vps")
	p.inputs[fieldAddr].SetValue("vps.example.com")
	p.inputs[fieldCode].SetValue("abcde-23456")
	msg := run(t, pairKey(p, tea.KeyEnter))
	require.Equal(t, pairDialing, p.step)
	require.Equal(t, []string{"vps.example.com", "abcde-23456"}, link.dialArgs)
	p.HandleMsg(msg)
}

func TestPairConnectModeFlow(t *testing.T) {
	t.Parallel()
	link := &fakePeerLink{dialRes: testPairResult}
	p := newPairForTest(link)
	toConnectConfirm(t, p, link)
	require.Equal(t, pairConfirm, p.step)
	require.Contains(t, pairText(p), "SHA256:theirs")

	msg := run(t, p.HandleMsg(tea.KeyPressMsg{Code: 'y', Text: "y"}))
	require.Len(t, link.pinned, 1)
	require.Equal(t, peer.PinRequest{Name: "vps", Addr: "vps.example.com:7722", Dial: true, Result: testPairResult}, link.pinned[0])
	p.HandleMsg(msg)
	require.Equal(t, pairDone, p.step)
}

func TestPairConnectModeDialToggle(t *testing.T) {
	t.Parallel()
	link := &fakePeerLink{dialRes: testPairResult}
	p := newPairForTest(link)
	pairKey(p, tea.KeyDown)
	pairKey(p, tea.KeyEnter)
	for range fieldDial {
		pairKey(p, tea.KeyTab)
	}
	require.Equal(t, fieldDial, p.focus)
	pairKey(p, tea.KeySpace)
	require.False(t, p.dial)
}

func TestPairConnectModeWrongCode(t *testing.T) {
	t.Parallel()
	link := &fakePeerLink{dialErr: peer.ErrPairWrong}
	p := newPairForTest(link)
	toConnectConfirm(t, p, link)
	require.Equal(t, pairForm, p.step, "back to the form to fix the code")
	require.Contains(t, pairText(p), "Wrong code")
	require.Equal(t, "abcde-23456", p.inputs[fieldCode].Value(), "what was typed is kept")
}

func TestPairConnectModeUnreachable(t *testing.T) {
	t.Parallel()
	link := &fakePeerLink{dialErr: &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")}}
	p := newPairForTest(link)
	toConnectConfirm(t, p, link)
	require.Contains(t, pairText(p), "Could not reach it")
}

func TestPairConnectModeMismatchAborts(t *testing.T) {
	t.Parallel()
	link := &fakePeerLink{dialRes: testPairResult}
	p := newPairForTest(link)
	toConnectConfirm(t, p, link)
	p.HandleMsg(tea.KeyPressMsg{Code: 'n', Text: "n"})
	require.Equal(t, pairFailed, p.step)
	require.Empty(t, link.pinned)
}

func TestPairNameTakenOffersReplace(t *testing.T) {
	t.Parallel()
	link := &fakePeerLink{dialRes: testPairResult, pinErrs: []error{fmt.Errorf("%w: %q is paired with another key", peer.ErrPinTaken, "vps")}}
	p := newPairForTest(link)
	toConnectConfirm(t, p, link)

	p.HandleMsg(run(t, pairKey(p, tea.KeyEnter)))
	require.Equal(t, pairConfirm, p.step)
	require.Equal(t, "vps", p.taken)
	require.Contains(t, p.err, "Enter again replaces it")
	require.Contains(t, pairText(p), "already paired with a different key")
	require.Empty(t, link.pinned)

	p.HandleMsg(run(t, pairKey(p, tea.KeyEnter)))
	require.Len(t, link.pinned, 1)
	require.True(t, link.pinned[0].Replace)
	require.Equal(t, pairDone, p.step)
}

func TestPairErrText(t *testing.T) {
	t.Parallel()
	require.Contains(t, pairErrText(peer.ErrPairAttempts), "Too many wrong codes")
	require.Contains(t, pairErrText(peer.ErrPairUnproven), "did not accept the code")
	require.Contains(t, pairErrText(context.DeadlineExceeded), "No answer in time")
	require.Equal(t, "something odd", pairErrText(errors.New("peer: something odd")))
}
