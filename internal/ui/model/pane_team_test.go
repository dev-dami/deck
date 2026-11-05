package model

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"

	"github.com/dev-dami/deck/internal/deck/peer"
	"github.com/dev-dami/deck/internal/deck/sub"
	"github.com/dev-dami/deck/internal/ui/common"
	"github.com/dev-dami/deck/internal/workspace"
)

func TestTeamSnapshotNotifiesNewRequests(t *testing.T) {
	m := newBusyUI(&countingWorkspace{})
	cap := sub.ID("blake3:" + "ab12cd34ef" + "00000000000000000000000000000000000000000000000000000000")
	old := peer.Snapshot{Requests: []peer.CapsuleRequest{{Seq: 1, From: "vps", ID: cap}}}
	m.applyTeamSnap(teamSnapMsg{snap: old})
	require.Zero(t, m.notices.store.Len(), "what was there at the first read is not news")

	next := peer.Snapshot{
		Requests: []peer.CapsuleRequest{
			{Seq: 1, From: "vps", ID: cap},
			{Seq: 2, From: "vps", ID: cap},
			{Seq: 3, From: "vps", ID: cap, Served: true},
		},
	}
	m.applyTeamSnap(teamSnapMsg{snap: next})

	list := m.notices.store.List() // newest first
	require.Len(t, list, 2, "one private request, one quiet fetch")
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
	require.Len(t, m.notices.store.List(), 2, "the same snapshot again tells nothing new")
}

func TestTeamPaneShowsPresence(t *testing.T) {
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
}

// shareWorkspace has a peer link that records what the panes ask of it.
type shareWorkspace struct {
	*countingWorkspace
	link   *answerLink
	shares map[string]string
}

func (w *shareWorkspace) PeerLink() workspace.PeerLink { return w.link }

func (w *shareWorkspace) ShareThread(sid, name, role string) error {
	w.link.answers = append(w.link.answers, "share "+sid+" "+name+" "+role)
	if w.shares == nil {
		w.shares = map[string]string{}
	}
	w.shares[name] = role
	return nil
}
func (w *shareWorkspace) ThreadShares(string) map[string]string { return w.shares }

type answerLink struct {
	workspace.PeerLink
	answers []string
	mirror  peer.Mirror
}

func (l *answerLink) ReadShared(_ context.Context, name, thread string, after int, wait time.Duration) (peer.Mirror, error) {
	l.answers = append(l.answers, fmt.Sprintf("read %s %s %d %v", name, thread, after, wait))
	return l.mirror, nil
}
func (l *answerLink) PostShared(_ context.Context, name, thread, text string) error {
	l.answers = append(l.answers, "post "+name+" "+thread+" "+text)
	return nil
}
func (l *answerLink) AnswerShared(_ context.Context, name, thread, id string, allow bool) error {
	l.answers = append(l.answers, fmt.Sprintf("answer %s %s %s %v", name, thread, id, allow))
	return nil
}

func TestSharedPaneFollowsAThread(t *testing.T) {
	link := &answerLink{}
	ws := &shareWorkspace{countingWorkspace: &countingWorkspace{}, link: link}
	m := newBusyUI(&countingWorkspace{})
	m.com = common.DefaultCommon(ws)
	th := peer.SharedThread{ID: "th1", Title: "fix the parser", Role: "editor", Mode: "ask", Members: []string{"claude"}, Entries: 1000}
	m.applyTeamSnap(teamSnapMsg{snap: peer.Snapshot{Name: "me", Peers: []peer.PeerState{{Name: "vps"}}}})
	m.applyTeamSnap(teamSnapMsg{snap: peer.Snapshot{Name: "me", Peers: []peer.PeerState{{Name: "vps", Presence: &peer.Presence{Threads: []peer.SharedThread{th}}}}}})
	require.Equal(t, "vps shared a thread with you", m.notices.store.List()[0].Title)

	p := &sharedPane{}
	text := func() string {
		var lines []string
		for _, r := range p.rows(m, 100, 30) {
			lines = append(lines, r.lines...)
		}
		return ansi.Strip(strings.Join(lines, "\n"))
	}
	require.Contains(t, text(), "vps · fix the parser")
	_, cmd := p.key(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	require.NotNil(t, cmd)
	link.mirror = peer.Mirror{Thread: th, Lines: []string{"[tester] fix it", "[claude] on it"}, Next: 1002,
		Pending: []peer.PendingApproval{{ID: "a1", Agent: "claude", Summary: "make"}}}
	next := m.applySharedMirror(cmd().(sharedMirrorMsg))
	require.NotNil(t, next, "it keeps following")
	require.Equal(t, "read vps th1 600 0s", link.answers[0], "the first read starts a little before the end")
	out := text()
	require.Contains(t, out, "[claude] on it")
	require.Contains(t, out, "claude asks: make")
	require.Contains(t, out, "you are editor · ask mode · esc back · p posts")

	p.list.cursor = 1 // the approval
	_, cmd = p.key(m, tea.KeyPressMsg{Code: 'y', Text: "y"})
	cmd()
	require.Equal(t, "answer vps th1 a1 true", link.answers[len(link.answers)-1])

	p.key(m, tea.KeyPressMsg{Code: 'p', Text: "p"})
	for _, r := range "thanks" {
		p.key(m, tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	_, cmd = p.key(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	cmd()
	require.Equal(t, "post vps th1 thanks", link.answers[len(link.answers)-1])

	p.key(m, tea.KeyPressMsg{Code: tea.KeyEscape})
	require.Nil(t, m.shared)
	require.Nil(t, m.applySharedMirror(sharedMirrorMsg{gen: 1}), "a read for a thread left behind is dropped")
}

func TestTeamPaneSharesTheThread(t *testing.T) {
	link := &answerLink{}
	ws := &shareWorkspace{countingWorkspace: &countingWorkspace{}, link: link}
	m := newBusyUI(&countingWorkspace{})
	m.com = common.DefaultCommon(ws)
	m.teamSnap = &peer.Snapshot{Name: "me", Peers: []peer.PeerState{{Name: "vps"}}}
	p := &teamPane{}
	p.list.cursor = 1
	for _, want := range []string{"observer", "participant", "editor", ""} {
		_, cmd := p.key(m, tea.KeyPressMsg{Code: 's', Text: "s"})
		cmd()
		require.Equal(t, "share s1 vps "+want, link.answers[len(link.answers)-1])
	}
	ws.shares["vps"] = "participant"
	var lines []string
	for _, r := range p.rows(m, 100) {
		lines = append(lines, r.lines...)
	}
	require.Contains(t, ansi.Strip(strings.Join(lines, "\n")), "sees this thread as participant")
}
