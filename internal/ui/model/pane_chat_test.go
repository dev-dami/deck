package model

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"

	"github.com/dev-dami/deck/internal/deck/peer"
	"github.com/dev-dami/deck/internal/deck/sub"
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
