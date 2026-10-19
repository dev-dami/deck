package model

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"

	"github.com/dev-dami/deck/internal/deck/peer"
	"github.com/dev-dami/deck/internal/ui/common"
)

func (l *answerLink) ProposeContract(_ context.Context, name string, c peer.Contract) (peer.Contract, error) {
	l.answers = append(l.answers, fmt.Sprintf("ops %s %s %v %v", name, c.Title, c.Scope, c.Targets))
	return c, nil
}

// o on a teammate asks it for a report: the request shows as a card, and
// only y proposes it. Its result shows as a table.
func TestTeamPaneAsksForAReport(t *testing.T) {
	link := &answerLink{}
	m := newBusyUI(&countingWorkspace{})
	m.com = common.DefaultCommon(&contractWorkspace{countingWorkspace: &countingWorkspace{}, link: link})
	m.applyTeamSnap(teamSnapMsg{snap: peer.Snapshot{Name: "me", Peers: []peer.PeerState{{Name: "vps"}}}})
	p := &teamPane{}
	text := func() string {
		var lines []string
		for _, r := range p.rows(m, 120) {
			lines = append(lines, r.lines...)
		}
		return ansi.Strip(strings.Join(lines, "\n"))
	}
	typeText := func(s string) {
		for _, r := range s {
			p.key(m, tea.KeyPressMsg{Code: r, Text: string(r)})
		}
	}
	p.list.cursor = 1 // vps
	handled, _ := p.key(m, tea.KeyPressMsg{Code: 'o', Text: "o"})
	require.True(t, handled)
	typeText("podman containers: name, state for web")
	require.Contains(t, text(), "report: ask vps to report podman containers: name, state for web")

	p.key(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	out := text()
	require.Contains(t, out, "▸ ask vps to report podman containers")
	require.Contains(t, out, "scope: podman-read · targets: web")
	require.Contains(t, out, "fields: name, state")
	require.Contains(t, out, "y proposes · n drops")
	require.Empty(t, link.answers, "nothing goes before y")
	handled, cmd := p.key(m, tea.KeyPressMsg{Code: 'y', Text: "y"})
	require.True(t, handled)
	require.Contains(t, fmt.Sprint(cmd()), "Asked vps to report podman containers")
	require.Equal(t, []string{"ops vps report podman containers [podman-read] [web]"}, link.answers)
	require.Nil(t, p.card)

	// A request Deck cannot read stays in the input; n drops a card.
	p.key(m, tea.KeyPressMsg{Code: 'o', Text: "o"})
	typeText("disks: name")
	_, cmd = p.key(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	require.Contains(t, fmt.Sprint(cmd()), "is not something Deck can report")
	require.Equal(t, "vps", p.opsFor)
	p.key(m, tea.KeyPressMsg{Code: tea.KeyEscape})
	p.key(m, tea.KeyPressMsg{Code: 'o', Text: "o"})
	typeText("services: name")
	p.key(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	require.NotNil(t, p.card)
	p.key(m, tea.KeyPressMsg{Code: 'n', Text: "n"})
	require.Nil(t, p.card)
	require.Len(t, link.answers, 1)

	// The result comes, and a report runs here: both are news.
	schema := []peer.Field{{Name: "name", Type: peer.FieldString}, {Name: "restarts", Type: peer.FieldNumber}}
	snap := peer.Snapshot{Name: "me", Peers: []peer.PeerState{{Name: "vps"}}, Contracts: []peer.Contract{
		{ID: "k3", Peer: "vps", Kind: peer.KindOps, Title: "report podman containers", State: peer.ContractDone, Schema: schema,
			Result: json.RawMessage(`{"rows":[{"name":"web","restarts":2},{"name":"database-primary","restarts":null}]}`)},
		{ID: "k4", Peer: "vps", Inbound: true, Kind: peer.KindOps, Title: "report services", State: peer.ContractAccepted, Scope: []string{"systemd-read"}},
	}}
	before := m.notices.store.Len()
	snap.Contracts[0].State = peer.ContractAccepted
	m.applyTeamSnap(teamSnapMsg{snap: snap})
	snap.Contracts = append([]peer.Contract(nil), snap.Contracts...)
	snap.Contracts[0].State = peer.ContractDone
	m.applyTeamSnap(teamSnapMsg{snap: snap})
	var titles []string
	for _, n := range m.notices.store.List()[before:] {
		titles = append(titles, n.Title)
	}
	require.Contains(t, titles, "vps runs a report here: report services")
	require.Contains(t, titles, "vps reported: report podman containers")

	out = text()
	require.Contains(t, out, "→ vps · report podman containers · done (enter shows the rows)")
	require.Contains(t, out, "← vps · report services · accepted")
	require.NotContains(t, out, "y run with", "a report that runs here is no card")
	p.list.cursor = 1 // the report
	handled, _ = p.key(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	require.True(t, handled)
	out = text()
	require.Contains(t, out, "name              restarts")
	require.Contains(t, out, "web               2")
	require.Contains(t, out, "database-primary")
	p.key(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	require.NotContains(t, text(), "database-primary")
}
