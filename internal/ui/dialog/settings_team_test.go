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
	uv "github.com/charmbracelet/ultraviolet"
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
	moved    *peer.PairWait // what PairListenAll returns
	moveErr  error
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
func (f *fakePeerLink) StartPairListen(context.Context) (*peer.PairWait, error) {
	f.record("StartPairListen")
	return f.wait, f.startErr
}
func (f *fakePeerLink) PairListenAll(*peer.PairWait) (*peer.PairWait, error) {
	f.record("PairListenAll")
	return f.moved, f.moveErr
}
func (f *fakePeerLink) PairDial(_ context.Context, addr, code, expect string) (peer.PairResult, error) {
	f.dialArgs = []string{addr, code, expect}
	return f.dialRes, f.dialErr
}

func (f *fakePeerLink) count(call string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.calls {
		if c == call {
			n++
		}
	}
	return n
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
func (f *fakePeerLink) AnswerContract(name, id string, accept bool, agent string) error {
	f.record("AnswerContract")
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
	return ansi.Strip(strings.Join(p.lines(64, false), "\n"))
}

// Realistic fingerprints: as long as the real ones, so a narrow dialog
// has to wrap them.
const (
	ownFP    = "SHA256:jaSK+FcF9OBTJiSft2QoKvM2UHimsBx3eM7RWdM7RAQ"
	theirsFP = "SHA256:Qm9vLq2X0r8yZ1c4e7HkTnP3sUaWbDfGjJlMoRtVx5E"
)

func testPairWait() (*peer.PairWait, chan peer.PairOutcome) {
	ch := make(chan peer.PairOutcome, 1)
	return &peer.PairWait{
		Code: "ABCDE-23456", Addr: "0.0.0.0:7722", Fingerprint: ownFP, Name: "pc",
		Hosts: []string{"203.0.113.5"}, Reachable: true, Shared: true,
		Expires: teamNow.Add(5 * time.Minute), Done: ch,
	}, ch
}

var testPairResult = peer.PairResult{
	PeerName: "vps", PeerFingerprint: theirsFP, OwnFingerprint: ownFP, RemoteAddr: "203.0.113.5:50000",
}

// squash drops line breaks and spaces, so a fingerprint wrapped over lines
// reads as one again.
func squash(s string) string {
	return strings.NewReplacer("\n", "", " ", "").Replace(s)
}

// toWaitConfirm walks wait mode up to the fingerprints.
func toWaitConfirm(t *testing.T, p *PeerPair, link *fakePeerLink) {
	t.Helper()
	started := run(t, pairKey(p, tea.KeyEnter)) // wait mode is the default
	require.True(t, link.called("StartPairListen"))
	require.Equal(t, pairWaiting, p.step)
	act := p.HandleMsg(started)
	require.NotNil(t, act)
	text := pairText(p)
	require.Contains(t, text, "A B C D E - 2 3 4 5 6", "the code shows big")
	require.Contains(t, text, "expires in 5:00")
	require.Contains(t, squash(text), ownFP)
	require.Contains(t, text, "0.0.0.0:7722 (all interfaces)")
	require.Contains(t, text, "203.0.113.5:7722", "the address to type")
	require.NotContains(t, text, "cannot reach")

	p.HandleMsg(pairOutcomeMsg{done: p.wait.Done, out: peer.PairOutcome{Result: testPairResult}})
	require.Equal(t, pairConfirm, p.step)
	text = pairText(p)
	require.Contains(t, squash(text), theirsFP)
	require.Contains(t, squash(text), ownFP)
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

	require.Equal(t, ActionClose{}, pairKey(p, tea.KeyEscape))
	require.Nil(t, p.wait)
	p.HandleMsg(pairOutcomeMsg{done: w.Done, out: peer.PairOutcome{Result: testPairResult}})
	require.NotEqual(t, pairConfirm, p.step, "an abandoned pairing never reaches the fingerprints")
}

func TestPairWaitModeExpired(t *testing.T) {
	t.Parallel()
	w, _ := testPairWait()
	link := &fakePeerLink{wait: w}
	p := newPairForTest(link)
	p.HandleMsg(run(t, pairKey(p, tea.KeyEnter)))
	p.HandleMsg(pairOutcomeMsg{done: w.Done, out: peer.PairOutcome{Err: peer.ErrPairExpired}})
	require.Equal(t, pairExpired, p.step, "not a dead screen: a new code is a key away")
	require.Contains(t, pairText(p), "The code expired")

	w2, _ := testPairWait()
	link.wait = w2
	started := run(t, pairKey(p, 'n'))
	require.Equal(t, 2, link.count("StartPairListen"))
	require.Equal(t, pairWaiting, p.step)
	p.HandleMsg(started)
	require.Same(t, w2, p.wait)
}

// runFirst runs an action's command and returns the first message to come
// back: of a batch, the one that answers at once (a progress tick is left
// to fire on its own).
func runFirst(t *testing.T, act Action) tea.Msg {
	t.Helper()
	msg := run(t, act)
	b, ok := msg.(tea.BatchMsg)
	if !ok {
		return msg
	}
	ch := make(chan tea.Msg, len(b))
	for _, c := range b {
		if c != nil {
			go func() { ch <- c() }()
		}
	}
	select {
	case m := <-ch:
		return m
	case <-time.After(500 * time.Millisecond):
		t.Fatal("no message from the batch")
	}
	return nil
}

func TestPairWaitCountdownExpiresAndOffersNewCode(t *testing.T) {
	t.Parallel()
	w, _ := testPairWait()
	link := &fakePeerLink{wait: w}
	p := newPairForTest(link)
	now := teamNow
	p.now = func() time.Time { return now }
	p.HandleMsg(run(t, pairKey(p, tea.KeyEnter)))
	now = now.Add(4*time.Minute + 30*time.Second)
	require.Contains(t, pairText(p), "expires in 0:30")
	p.HandleMsg(pairTickMsg{req: p.req})
	require.Equal(t, pairWaiting, p.step)

	now = w.Expires.Add(time.Second)
	require.Nil(t, p.HandleMsg(pairTickMsg{req: p.req}))
	require.Equal(t, pairExpired, p.step)
	require.Nil(t, p.wait, "the expired code is disarmed")
	text := pairText(p)
	require.Contains(t, text, "The code expired")
	require.Contains(t, text, "n gets a new code")
	run(t, pairKey(p, 'n'))
	require.Equal(t, pairWaiting, p.step)
	require.Equal(t, 2, link.count("StartPairListen"))
}

func TestPairWaitUnreachableOffersListenAll(t *testing.T) {
	t.Parallel()
	w, ch := testPairWait()
	w.Addr, w.Reachable = "127.0.0.1:7722", false
	moved := *w
	moved.Addr, moved.Reachable = "0.0.0.0:7722", true
	link := &fakePeerLink{wait: w, moved: &moved}
	p := newPairForTest(link)
	p.HandleMsg(run(t, pairKey(p, tea.KeyEnter)))

	text := pairText(p)
	require.Contains(t, text, "Listening on")
	require.Contains(t, text, "127.0.0.1:7722 (this machine only)", "the real port shows")
	require.Contains(t, squash(text), squash("The other Deck cannot reach 127.0.0.1 — it only listens on this machine"))
	require.Contains(t, text, "Open port 7722 in your firewall")
	require.False(t, link.called("PairListenAll"), "never widened without the key")

	msg := run(t, pairKey(p, 'l'))
	require.True(t, link.called("PairListenAll"))
	require.True(t, p.moving)
	p.HandleMsg(msg)
	require.False(t, p.moving)
	require.Equal(t, "0.0.0.0:7722", p.wait.Addr)
	require.Equal(t, w.Code, p.wait.Code, "the code stays armed")
	require.NotContains(t, pairText(p), "cannot reach")
	require.Nil(t, pairKey(p, 'l'), "nothing to widen any more")

	// The kept code still pairs.
	ch <- peer.PairOutcome{Result: testPairResult}
	p.HandleMsg(pairOutcomeMsg{done: p.wait.Done, out: <-ch})
	require.Equal(t, pairConfirm, p.step)
}

func TestPairWaitListenAllWithNewCode(t *testing.T) {
	t.Parallel()
	w, _ := testPairWait()
	w.Addr, w.Reachable, w.Shared = "127.0.0.1:7722", false, false
	fresh, _ := testPairWait()
	fresh.Code = "FGHJK-23456"
	link := &fakePeerLink{wait: w, moved: fresh}
	p := newPairForTest(link)
	p.HandleMsg(run(t, pairKey(p, tea.KeyEnter)))
	msg := run(t, pairKey(p, 'l'))
	// The old listener's cancellation lands first: it is not a failure.
	p.HandleMsg(pairOutcomeMsg{done: w.Done, out: peer.PairOutcome{Err: context.Canceled}})
	require.Equal(t, pairWaiting, p.step)
	p.HandleMsg(msg)
	require.Same(t, fresh, p.wait)
	require.Contains(t, pairText(p), "F G H J K - 2 3 4 5 6")

	// A move that fails says why.
	link.moved, link.moveErr = nil, errors.New("listen tcp 0.0.0.0:7722: bind: address already in use")
	w3, _ := testPairWait()
	w3.Reachable = false
	link.wait = w3
	p.HandleMsg(run(t, pairKey(p, 'n')))
	p.HandleMsg(run(t, pairKey(p, 'l')))
	require.Equal(t, pairFailed, p.step)
	require.Contains(t, pairText(p), "Could not listen on all interfaces")
}

func TestPairCopyInvite(t *testing.T) {
	t.Parallel()
	w, _ := testPairWait()
	link := &fakePeerLink{wait: w}
	p := newPairForTest(link)
	p.HandleMsg(run(t, pairKey(p, tea.KeyEnter)))
	require.Contains(t, pairText(p), "5-minute, single use")
	_, ok := p.HandleMsg(tea.KeyPressMsg{Code: 'c', Text: "c"}).(ActionCmd)
	require.True(t, ok, "c copies")
	require.True(t, p.copied)
	// The invite shows in full, for a terminal where the copy did not land.
	text := squash(pairText(p))
	at := strings.Index(text, "deck://")
	require.GreaterOrEqual(t, at, 0, "the invite shows")
	inv, err := peer.ParseInvite(text[at:])
	require.NoError(t, err)
	require.Equal(t, "203.0.113.5:7722", inv.Addr())
	require.Equal(t, w.Code, inv.Code)
	require.Equal(t, ownFP, inv.Fingerprint)
	_, ok = p.HandleMsg(tea.KeyPressMsg{Code: 'C', Text: "C"}).(ActionCmd)
	require.True(t, ok, "C copies the code alone")
}

// testInviteLine is an invite from the waiting Deck of testPairWait.
func testInviteLine() string {
	return peer.Invite{Host: "203.0.113.5", Port: "7722", Code: "ABCDE-23456", Fingerprint: theirsFP, Name: "vps"}.Encode()
}

func TestPairPasteInviteFillsEveryField(t *testing.T) {
	t.Parallel()
	for _, focus := range []int{fieldName, fieldAddr, fieldCode, fieldDial} {
		link := &fakePeerLink{dialRes: testPairResult}
		p := newPairForTest(link)
		pairKey(p, tea.KeyDown)
		pairKey(p, tea.KeyEnter)
		p.setFocus(focus)
		require.Nil(t, p.HandleMsg(tea.PasteMsg{Content: testInviteLine() + "\n"}))
		require.Equal(t, pairForm, p.step)
		require.Equal(t, "vps", p.inputs[fieldName].Value(), "focus %d", focus)
		require.Equal(t, "203.0.113.5:7722", p.inputs[fieldAddr].Value())
		require.Equal(t, "ABCDE-23456", p.inputs[fieldCode].Value())
		require.Equal(t, theirsFP, p.expect)
		require.Contains(t, squash(pairText(p)), theirsFP)

		msg := runFirst(t, pairKey(p, tea.KeyEnter))
		require.Equal(t, []string{"203.0.113.5:7722", "ABCDE-23456", theirsFP}, link.dialArgs, "the invite's fingerprint is checked")
		p.HandleMsg(msg)
		require.Equal(t, pairConfirm, p.step)
		require.Contains(t, pairText(p), "✓ matches the invite")
	}
}

func TestPairPasteInviteOnFirstStep(t *testing.T) {
	t.Parallel()
	p := newPairForTest(&fakePeerLink{})
	require.Equal(t, pairChoose, p.step)
	p.HandleMsg(tea.PasteMsg{Content: testInviteLine()})
	require.Equal(t, pairForm, p.step, "straight to the filled form")
	require.Equal(t, pairModeConnect, p.mode)
	require.Equal(t, "203.0.113.5:7722", p.inputs[fieldAddr].Value())
	require.Contains(t, pairText(p), "Invite pasted")

	// v reads the clipboard; what it holds goes the same way.
	q := newPairForTest(&fakePeerLink{})
	q.HandleMsg(pairClipReadMsg{req: q.req, text: "hello"})
	require.Equal(t, pairChoose, q.step)
	require.Contains(t, q.err, "That is not a Deck invite")
	q.HandleMsg(pairClipReadMsg{req: q.req, text: testInviteLine()})
	require.Equal(t, pairForm, q.step)

	// No system clipboard: the terminal is asked, and its answer is used.
	r := newPairForTest(&fakePeerLink{})
	_, ok := r.HandleMsg(pairClipReadMsg{req: r.req, err: errors.New("unsupported")}).(ActionCmd)
	require.True(t, ok)
	require.True(t, r.askClip)
	r.HandleMsg(PairClipboardMsg{Content: testInviteLine()})
	require.Equal(t, pairForm, r.step)
	r.HandleMsg(PairClipboardMsg{Content: "deck://pair?v=1"})
	require.Empty(t, r.err, "an answer nobody asked for is ignored")
}

func TestPairPastePlainCodeStillWorks(t *testing.T) {
	t.Parallel()
	p := newPairForTest(&fakePeerLink{})
	pairKey(p, tea.KeyDown)
	pairKey(p, tea.KeyEnter)
	p.setFocus(fieldCode)
	p.HandleMsg(tea.PasteMsg{Content: "ABCDE-23456"})
	require.Equal(t, "ABCDE-23456", p.inputs[fieldCode].Value())
	require.Empty(t, p.inputs[fieldAddr].Value())
	require.Empty(t, p.expect)

	p.HandleMsg(tea.PasteMsg{Content: "deck://pair?v=1&host=x"})
	require.Contains(t, p.err, "That is not a Deck invite")
	require.Equal(t, "ABCDE-23456", p.inputs[fieldCode].Value(), "a bad invite changes nothing")
}

func TestPairInviteFingerprintMismatchPinsNothing(t *testing.T) {
	t.Parallel()
	link := &fakePeerLink{dialErr: peer.ErrPairFingerprint}
	p := newPairForTest(link)
	pairKey(p, tea.KeyDown)
	pairKey(p, tea.KeyEnter)
	p.HandleMsg(tea.PasteMsg{Content: testInviteLine()})
	p.HandleMsg(runFirst(t, pairKey(p, tea.KeyEnter)))
	require.Equal(t, pairForm, p.step)
	require.Contains(t, squash(pairText(p)), squash("The fingerprint does not match the invite: someone may be intercepting this connection"))
	require.Empty(t, link.pinned)
}

func TestPairConnectTimeoutMessages(t *testing.T) {
	t.Parallel()
	cases := map[error]string{
		fmt.Errorf("%w from 203.0.113.5:7722 in 12s", peer.ErrPairNoAnswer): "No answer from vps.example.com:7722. Is Deck running there, is Accept connections on, and is port 7722 open in its firewall?",
		fmt.Errorf("%w with x: ssh: EOF", peer.ErrPairHandshake):            "Host key / handshake failed",
		peer.ErrPairNotWaiting: "Wrong or expired code",
		peer.ErrPairUnproven:   "Wrong or expired code",
		peer.ErrPairWrong:      "Wrong code",
	}
	for err, want := range cases {
		link := &fakePeerLink{dialErr: err}
		p := newPairForTest(link)
		toConnectConfirm(t, p, link)
		require.Equal(t, pairForm, p.step)
		require.Contains(t, squash(pairText(p)), squash(want), "%v", err)
	}
}

func TestPairDialingShowsProgressAndEscCancels(t *testing.T) {
	t.Parallel()
	link := &fakePeerLink{dialRes: testPairResult}
	p := newPairForTest(link)
	now := teamNow
	p.now = func() time.Time { return now }
	pairKey(p, tea.KeyDown)
	pairKey(p, tea.KeyEnter)
	p.inputs[fieldName].SetValue("vps")
	p.inputs[fieldAddr].SetValue("vps.example.com")
	p.inputs[fieldCode].SetValue("abcde-23456")
	act := pairKey(p, tea.KeyEnter)
	require.Equal(t, pairDialing, p.step)
	now = now.Add(3 * time.Second)
	require.Contains(t, pairText(p), "Checking the code with vps.example.com:7722… 0:03 of 0:12")
	req := p.req
	require.NotNil(t, p.HandleMsg(pairTickMsg{req: req}), "the progress keeps ticking")

	require.Nil(t, pairKey(p, tea.KeyEscape))
	require.Equal(t, pairForm, p.step)
	require.Nil(t, p.cancel, "the dial was cancelled")
	// Its late answer is ignored.
	p.HandleMsg(runFirst(t, act))
	require.Equal(t, pairForm, p.step)
	require.Empty(t, link.pinned)
}

// pairInner is the wizard's inner width in a terminal cols wide, as Draw
// works it out.
func pairInner(p *PeerPair, cols int) int {
	t := p.com.Styles
	width := max(0, min(peerPairMaxWidth, cols-t.Dialog.View.GetHorizontalBorderSize()))
	return max(0, width-t.Dialog.View.GetHorizontalFrameSize())
}

// requireUncut fails when a line is wider than width or cut off with an
// ellipsis.
func requireUncut(t *testing.T, lines []string, width int, where string) {
	t.Helper()
	for _, l := range lines {
		for _, sub := range strings.Split(l, "\n") {
			require.LessOrEqual(t, ansi.StringWidth(sub), width, "%s: %q overflows", where, ansi.Strip(sub))
			require.NotContains(t, ansi.Strip(sub), "…", "%s: %q is cut off", where, ansi.Strip(sub))
		}
	}
}

func TestPairNothingCutAtCommonWidths(t *testing.T) {
	t.Parallel()
	for _, cols := range []int{60, 80, 100} {
		w, ch := testPairWait()
		w.Addr, w.Reachable = "127.0.0.1:7722", false
		link := &fakePeerLink{wait: w}
		p := newPairForTest(link)
		p.HandleMsg(run(t, pairKey(p, tea.KeyEnter)))
		inner := pairInner(p, cols)
		where := fmt.Sprintf("wait at %d", cols)
		for _, compact := range []bool{false, true} {
			lines := p.lines(inner, compact)
			requireUncut(t, lines, inner, where)
			require.Contains(t, squash(ansi.Strip(strings.Join(lines, "\n"))), ownFP, where)
		}
		requireUncut(t, []string{p.renderHelp(inner)}, inner, where+" help")
		p.HandleMsg(tea.KeyPressMsg{Code: 'c', Text: "c"})
		requireUncut(t, p.lines(inner, false), inner, where+" copied")

		ch <- peer.PairOutcome{Result: testPairResult}
		p.HandleMsg(pairOutcomeMsg{done: w.Done, out: <-ch})
		where = fmt.Sprintf("confirm at %d", cols)
		lines := p.lines(inner, false)
		requireUncut(t, lines, inner, where)
		text := squash(ansi.Strip(strings.Join(lines, "\n")))
		require.Contains(t, text, ownFP, where)
		require.Contains(t, text, theirsFP, where)
		requireUncut(t, []string{p.renderHelp(inner)}, inner, where+" help")

		// Connect mode, filled from an invite.
		q := newPairForTest(&fakePeerLink{})
		q.HandleMsg(tea.PasteMsg{Content: testInviteLine()})
		requireUncut(t, q.lines(inner, false), inner, fmt.Sprintf("form at %d", cols))
		requireUncut(t, []string{q.renderHelp(inner)}, inner, fmt.Sprintf("form help at %d", cols))
	}
}

func TestPairWaitFitsA24RowTerminal(t *testing.T) {
	t.Parallel()
	w, _ := testPairWait()
	w.Addr, w.Reachable = "127.0.0.1:7722", false
	p := newPairForTest(&fakePeerLink{wait: w})
	p.HandleMsg(run(t, pairKey(p, tea.KeyEnter)))
	area := uv.Rect(0, 0, 80, 24)
	inner := pairInner(p, 80)
	help := p.renderHelp(inner)
	maxLines := p.maxBody(area, help)
	body := p.fit(inner, maxLines)
	require.LessOrEqual(t, len(body), maxLines)
	text := ansi.Strip(strings.Join(body, "\n"))
	require.Contains(t, text, "A B C D E - 2 3 4 5 6", "the code is never what gets dropped")
	require.Contains(t, text, "203.0.113.5:7722")
}

func TestTeamPageNothingCutAtCommonWidths(t *testing.T) {
	t.Parallel()
	snap := baseSnapshot()
	snap.Fingerprint = ownFP
	snap.Peers = []peer.PeerState{{Name: "vps", Addr: "vps.example.com:7722", Role: "observer", Pinned: true, Configured: true, Dial: true,
		Link: peer.Link{Supervised: true, Status: peer.Status{State: peer.StateConnected, Latency: 12 * time.Millisecond}}}}
	for _, cols := range []int{60, 80, 100} {
		s, _ := newTeamSettingsForTest(t, snap)
		t2 := s.com.Styles
		width := max(0, min(settingsDialogMaxWidth, cols-t2.Dialog.View.GetHorizontalBorderSize()))
		inner := max(0, width-t2.Dialog.View.GetHorizontalFrameSize())
		for row := 0; row < len(s.teamRows()); row++ {
			selectRow(s, row)
			lines, _ := s.teamLines(inner)
			where := fmt.Sprintf("team at %d, row %d", cols, row)
			requireUncut(t, lines, inner, where)
			require.Contains(t, squash(ansi.Strip(strings.Join(lines, "\n"))), ownFP, where)
		}
		helpView := renderFittedHelp(t2, &s.help, s.ShortHelp(), &s.keyMap.More, false, inner)
		requireUncut(t, []string{helpView}, inner, fmt.Sprintf("team help at %d", cols))
		if cols == 60 {
			require.Contains(t, ansi.Strip(helpView), "? more", "what does not fit goes behind ? more")
		}
		s.HandleMsg(tea.KeyPressMsg{Code: '?', Text: "?"})
		all := renderFittedHelp(t2, &s.help, s.ShortHelp(), &s.keyMap.More, s.moreHelp, inner)
		requireUncut(t, []string{all}, inner, fmt.Sprintf("team full help at %d", cols))
		require.Contains(t, ansi.Strip(all), "copy fingerprint")
	}
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
	msg := runFirst(t, pairKey(p, tea.KeyEnter))
	require.Equal(t, pairDialing, p.step)
	require.Equal(t, []string{"vps.example.com", "abcde-23456", ""}, link.dialArgs)
	p.HandleMsg(msg)
}

func TestPairConnectModeFlow(t *testing.T) {
	t.Parallel()
	link := &fakePeerLink{dialRes: testPairResult}
	p := newPairForTest(link)
	toConnectConfirm(t, p, link)
	require.Equal(t, pairConfirm, p.step)
	require.Contains(t, squash(pairText(p)), theirsFP)

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
	refused := &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")}
	link := &fakePeerLink{dialErr: fmt.Errorf("%w from vps.example.com:7722: %v", peer.ErrPairNoAnswer, refused)}
	p := newPairForTest(link)
	toConnectConfirm(t, p, link)
	require.Contains(t, squash(pairText(p)), squash("No answer from vps.example.com:7722."))
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
	require.Contains(t, pairErrText(peer.ErrPairUnproven), "Wrong or expired code")
	require.Contains(t, pairErrText(context.DeadlineExceeded), "No answer from the other Deck")
	require.Equal(t, "something odd", pairErrText(errors.New("peer: something odd")))
}
