package model

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"

	deckconfig "github.com/dev-dami/deck/internal/deck/config"
	"github.com/dev-dami/deck/internal/deck/peer"
	"github.com/dev-dami/deck/internal/deck/sub"
	"github.com/dev-dami/deck/internal/ui/common"
	"github.com/dev-dami/deck/internal/workspace"
)

func TestTeamSnapshotNotifiesNewChatAndRequests(t *testing.T) {
	m := newBusyUI(&countingWorkspace{})
	old := peer.Snapshot{Chat: []peer.ChatMessage{{Seq: 1, From: "vps", Text: "old news"}}}
	m.applyTeamSnap(teamSnapMsg{snap: old})
	require.Zero(t, m.notices.store.Len(), "what was there at the first read is not news")

	cap := sub.ID("blake3:" + "ab12cd34ef" + "00000000000000000000000000000000000000000000000000000000")
	next := peer.Snapshot{
		Chat: []peer.ChatMessage{
			{Seq: 1, From: "vps", Text: "old news"},
			{Seq: 2, From: "vps", Text: "can you share your fix?"},
			{Seq: 3, From: "pc", Text: "mine", Mine: true},
		},
		Requests: []peer.CapsuleRequest{
			{Seq: 4, From: "vps", ID: cap},
			{Seq: 5, From: "vps", ID: cap, Served: true},
		},
	}
	m.applyTeamSnap(teamSnapMsg{snap: next})

	list := m.notices.store.List() // newest first
	require.Len(t, list, 3, "one message (not my own), one private request, one quiet fetch")
	var share *capsuleShareMsg
	for _, n := range list {
		if n.Action != nil {
			if s, ok := n.Action.Msg.(capsuleShareMsg); ok {
				share = &s
			}
		}
	}
	require.NotNil(t, share, "a private capsule's request offers to share it")
	require.Equal(t, capsuleShareMsg{id: string(cap), from: "vps", public: true}, *share)

	m.applyTeamSnap(teamSnapMsg{snap: next})
	require.Len(t, m.notices.store.List(), 3, "the same snapshot again tells nothing new")
}

func TestChatPaneTyping(t *testing.T) {
	m := newBusyUI(&countingWorkspace{})
	p := &chatPane{}
	for _, r := range "hi team" {
		handled, _ := p.key(m, tea.KeyPressMsg{Code: r, Text: string(r)})
		require.True(t, handled)
	}
	require.Equal(t, "hi team", p.draft)
	handled, _ := p.key(m, tea.KeyPressMsg{Code: tea.KeyBackspace})
	require.True(t, handled)
	require.Equal(t, "hi tea", p.draft)

	// ctrl keys and arrows stay with the sidebar.
	handled, _ = p.key(m, tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl})
	require.False(t, handled)
	handled, _ = p.key(m, tea.KeyPressMsg{Code: tea.KeyUp})
	require.False(t, handled)

	handled, cmd := p.key(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	require.True(t, handled)
	require.NotNil(t, cmd, "enter sends (here: reports that there is no peer link)")
	require.Empty(t, p.draft)
}

func TestTeamPaneShowsPresenceAndChatShowsAgents(t *testing.T) {
	ps := peer.PeerState{Name: "vps", Presence: &peer.Presence{
		Agents: []peer.AgentPresence{{Name: "claude2", Kind: "claude", Status: "working", Task: "fix the flaky test", ContextPct: 40}},
		Leases: []peer.LeasePresence{{Agent: "claude2", Paths: []string{"a.go", "b/**"}, Exclusive: true}},
		Limits: []peer.LimitPresence{{Kind: "claude", Window: "5h", UsedPct: 80}},
	}}
	require.Equal(t, []string{
		"vps/claude2 ▸ working · fix the flaky test · 40%",
		"claude2 holds a.go, b/** (exclusive)",
		"plans: claude 5h 80%",
	}, presenceLines(ps))
	require.Nil(t, presenceLines(peer.PeerState{Name: "pc"}), "no link, nothing said")

	m := newBusyUI(&countingWorkspace{})
	m.teamSnap = &peer.Snapshot{Chat: []peer.ChatMessage{
		{Seq: 1, From: "vps", Agent: "claude2", Text: "build is green"},
		{Seq: 2, From: "pc", Agent: "codex", Text: "thanks", Mine: true},
	}}
	out := ansi.Strip(strings.Join((&chatPane{}).lines(m, 60), "\n"))
	require.Contains(t, out, "vps/claude2")
	require.Contains(t, out, "your codex")
}

// contractWorkspace has a peer link that records answers to contracts, and
// two agent kinds, codex the default.
type contractWorkspace struct {
	*countingWorkspace
	link *answerLink
}

func (w *contractWorkspace) PeerLink() workspace.PeerLink { return w.link }
func (w *contractWorkspace) DeckSettings() deckconfig.Config {
	return deckconfig.Config{DefaultAgent: "codex"}
}
func (w *contractWorkspace) SetDeckSettings(func(*deckconfig.Config)) error { return nil }
func (w *contractWorkspace) AgentKinds() []string                           { return []string{"claude", "codex"} }

type answerLink struct {
	workspace.PeerLink
	answers []string
}

func (l *answerLink) AnswerContract(name, id string, accept bool, agent string) error {
	l.answers = append(l.answers, fmt.Sprintf("%s %s %v %s", name, id, accept, agent))
	return nil
}

func TestTeamPaneConfirmsAContract(t *testing.T) {
	link := &answerLink{}
	m := newBusyUI(&countingWorkspace{})
	m.com = common.DefaultCommon(&contractWorkspace{countingWorkspace: &countingWorkspace{}, link: link})
	m.applyTeamSnap(teamSnapMsg{snap: peer.Snapshot{Name: "me"}})
	snap := peer.Snapshot{Name: "me", Contracts: []peer.Contract{
		{ID: "k2", Peer: "pc", Title: "add docs", State: peer.ContractAccepted, Agent: "codex"},
		{ID: "k1", Peer: "vps", Inbound: true, Title: "fix the parser", Brief: "it drops the last line\nsee issue 4", Files: 12, State: peer.ContractProposed},
	}}
	m.applyTeamSnap(teamSnapMsg{snap: snap})
	require.Len(t, m.notices.store.List(), 1, "the proposal is news")
	require.Equal(t, "vps proposes a contract", m.notices.store.List()[0].Title)

	p := &teamPane{}
	text := func() string {
		var lines []string
		for _, r := range p.rows(m, 100) {
			lines = append(lines, r.lines...)
		}
		return ansi.Strip(strings.Join(lines, "\n"))
	}
	out := text()
	require.Contains(t, out, "vps asks: fix the parser")
	require.Contains(t, out, "it drops the last line")
	require.Contains(t, out, "(12 files)")
	require.Contains(t, out, "y run with codex · a other agent · n decline")
	require.Contains(t, out, "→ pc · add docs · accepted by codex")

	p.list.cursor = 1 // the card
	handled, _ := p.key(m, tea.KeyPressMsg{Code: 'a', Text: "a"})
	require.True(t, handled)
	require.Contains(t, text(), "y run with claude")
	handled, cmd := p.key(m, tea.KeyPressMsg{Code: 'y', Text: "y"})
	require.True(t, handled)
	require.NotNil(t, cmd)
	cmd()
	require.Equal(t, []string{"vps k1 true claude"}, link.answers)

	m.teamSnap.Contracts[1].State = peer.ContractAccepted // no card waits: y is not the pane's
	handled, _ = p.key(m, tea.KeyPressMsg{Code: 'y', Text: "y"})
	require.False(t, handled)
}
