package model

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"

	"github.com/dev-dami/deck/internal/config"
	deckagent "github.com/dev-dami/deck/internal/deck/agent"
	"github.com/dev-dami/deck/internal/session"
	"github.com/dev-dami/deck/internal/ui/common"
	"github.com/dev-dami/deck/internal/ui/dialog"
	"github.com/dev-dami/deck/internal/usage"
	"github.com/dev-dami/deck/internal/workspace"
)

// usagePaneWorkspace has a roster and live plan limits, and no database.
type usagePaneWorkspace struct {
	workspace.Workspace
	roster []workspace.AgentLine
	live   map[string]workspace.LiveLimits
}

func (w *usagePaneWorkspace) Config() *config.Config                      { return nil }
func (w *usagePaneWorkspace) Roster(string) []workspace.AgentLine         { return w.roster }
func (w *usagePaneWorkspace) UsageService() usage.Service                 { return nil }
func (w *usagePaneWorkspace) LiveLimits() map[string]workspace.LiveLimits { return w.live }
func (w *usagePaneWorkspace) Usage(context.Context, int) (workspace.UsageReport, error) {
	return workspace.UsageReport{}, nil
}

func newUsagePaneUI(t *testing.T) *UI {
	t.Helper()
	now := time.Now()
	ws := &usagePaneWorkspace{
		roster: []workspace.AgentLine{
			{ID: "claude", Kind: "claude", Status: "working", Since: now.Add(-12 * time.Second), ContextTokens: 45000, CostUSD: 3.5},
			{ID: "opencode", Kind: "opencode", Status: "idle", CostUSD: 1.2},
		},
		live: map[string]workspace.LiveLimits{
			"claude": {Limits: deckagent.Limits{Plan: "max", Windows: []deckagent.LimitWindow{
				{ID: "five_hour", Label: "5h", UsedPct: 38, ResetsAt: now.Add(2 * time.Hour), DurationMins: 300},
				{ID: "seven_day", Label: "Weekly", UsedPct: 70, ResetsAt: now.Add(72 * time.Hour), DurationMins: 10080},
			}}},
			"opencode": {Limits: deckagent.Limits{Unavailable: deckagent.LimitsUnsupported}},
		},
	}
	com := common.DefaultCommon(ws)
	return &UI{
		com:     com,
		status:  NewStatus(com, nil),
		chat:    NewChat(com, config.ScrollbarDefault),
		state:   uiChat,
		width:   160,
		height:  45,
		session: &session.Session{ID: "s1"},
		keyMap:  DefaultKeyMap(),
		dialog:  dialog.NewOverlay(),
	}
}

func TestUsagePaneRegisteredAsItsOwnColumn(t *testing.T) {
	t.Parallel()
	f, ok := paneFactories[paneUsage]
	require.True(t, ok)
	require.Equal(t, paneUsage, f().id())
	require.Equal(t, 44, f().width())
	require.Contains(t, defaultColumns, []paneID{paneUsage})
}

func TestUsagePaneRendersAtItsWidth(t *testing.T) {
	t.Parallel()
	m := newUsagePaneUI(t)
	p := m.usagePane()
	require.NotNil(t, p)
	m.setUsagePane(usagePaneMsg{
		today: usage.Series{Kinds: []string{"claude"}, Tokens: map[string][]int64{"claude": {0, 1200, 300, 9000}}},
		turns: []usage.Turn{{Kind: "claude", InputTokens: 3000, OutputTokens: 200, CostUSD: 0.4, TTFT: 1200 * time.Millisecond, Duration: 14 * time.Second}},
	})

	const width = usagePaneWidth
	h := p.height(m, width)
	require.Positive(t, h)
	out := ansi.Strip(p.render(m, width, h, false))
	for _, l := range strings.Split(out, "\n") {
		require.LessOrEqual(t, ansi.StringWidth(l), width, "line %q overflows", l)
	}
	require.Contains(t, out, "claude · max")
	require.Contains(t, out, "62% left")
	require.Contains(t, out, "today")
	require.Contains(t, out, "9k", "today's sparkline ends on the latest hour")
	require.Contains(t, out, "ctx 45k", "the live turn")
	require.Contains(t, out, "ttft 1.2s", "the last turn")
	require.Contains(t, out, "not reported by opencode")
	require.NotContains(t, out, "$")
	require.Equal(t, "30% left", p.badge(m), "the least left of any window")
}

func TestUsagePaneEnterOpensFullView(t *testing.T) {
	t.Parallel()
	m := newUsagePaneUI(t)
	handled, _ := m.usagePane().key(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	require.True(t, handled)
	require.True(t, m.dialog.ContainsDialog(dialog.UsageID))
}

func TestUsagePaneJoinsSavedLayoutsAndReopens(t *testing.T) {
	t.Parallel()
	// A layout saved before the pane existed gets it as a new column.
	s := newPaneStrip(config.SidebarLayout{Columns: [][]string{{"session", "agents"}, {"git"}}})
	require.True(t, s.isOpen(paneUsage))

	require.True(t, s.focusOn(paneUsage))
	id, ok := s.closeFocused()
	require.True(t, ok)
	require.Equal(t, paneUsage, id)
	require.True(t, slices.Contains(s.reopenList(), paneUsage), "o can reopen it")
	require.True(t, s.open(paneUsage))
	require.True(t, s.isOpen(paneUsage))
}
