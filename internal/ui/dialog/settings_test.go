package dialog

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"

	"github.com/dev-dami/deck/internal/config"
	deckconfig "github.com/dev-dami/deck/internal/deck/config"
	"github.com/dev-dami/deck/internal/ui/common"
	"github.com/dev-dami/deck/internal/ui/styles"
	"github.com/dev-dami/deck/internal/usage"
	"github.com/dev-dami/deck/internal/workspace"
)

// settingsWorkspace is a Workspace with just Deck settings and usage.
type settingsWorkspace struct {
	workspace.Workspace
	cfg    *config.Config
	deck   deckconfig.Config
	report workspace.UsageReport
}

func (w *settingsWorkspace) Config() *config.Config          { return w.cfg }
func (w *settingsWorkspace) DeckSettings() deckconfig.Config { return w.deck }
func (w *settingsWorkspace) AgentKinds() []string            { return []string{"claude", "codex"} }
func (w *settingsWorkspace) SetDeckSettings(edit func(*deckconfig.Config)) error {
	edit(&w.deck)
	return nil
}

func (w *settingsWorkspace) Usage(context.Context, int) (workspace.UsageReport, error) {
	return w.report, nil
}
func (*settingsWorkspace) UsageService() usage.Service                 { return nil }
func (*settingsWorkspace) LiveLimits() map[string]workspace.LiveLimits { return nil }

func newSettingsForTest(t *testing.T, tab SettingsTab) (*Settings, *settingsWorkspace) {
	t.Helper()
	s := styles.DeckDark()
	ws := &settingsWorkspace{
		cfg:  &config.Config{Options: &config.Options{}},
		deck: deckconfig.Config{UserName: "tester", DefaultAgent: "claude", Motion: "on", Bell: true, DefaultMode: "ask", TrackChanges: true},
	}
	com := &common.Common{Workspace: ws, Styles: &s}
	return NewSettings(com, tab), ws
}

func press(s *Settings, code rune) Action {
	return s.HandleMsg(tea.KeyPressMsg{Code: code})
}

func TestSettingsThemeRowOpensThemePicker(t *testing.T) {
	t.Parallel()
	s, _ := newSettingsForTest(t, SettingsTabGeneral)
	require.Equal(t, ActionOpenDialog{DialogID: ThemeID}, press(s, tea.KeyEnter))
}

func TestSettingsToggleBellSavesDeckSetting(t *testing.T) {
	t.Parallel()
	s, ws := newSettingsForTest(t, SettingsTabGeneral)

	// Theme, Motion, Bell.
	press(s, tea.KeyDown)
	press(s, tea.KeyDown)
	act, ok := press(s, tea.KeyEnter).(ActionSetDeckSettings)
	require.True(t, ok)
	require.Equal(t, "Bell off", act.Label)
	require.False(t, s.deck.Bell, "the dialog shows the change at once")

	require.NoError(t, ws.SetDeckSettings(act.Edit))
	require.False(t, ws.deck.Bell, "the edit sets the value the dialog shows")
}

func TestSettingsDefaultModeCyclesBothWays(t *testing.T) {
	t.Parallel()
	s, _ := newSettingsForTest(t, SettingsTabGeneral)
	for range 3 { // Theme, Motion, Bell, Default mode
		press(s, tea.KeyDown)
	}
	act := press(s, tea.KeyRight).(ActionSetDeckSettings)
	require.Equal(t, "yolo", s.deck.DefaultMode)
	require.Equal(t, "Default mode yolo", act.Label)
	press(s, tea.KeyLeft)
	press(s, tea.KeyLeft)
	require.Equal(t, "plan", s.deck.DefaultMode)
}

func TestSettingsTUITogglesUseExistingActions(t *testing.T) {
	t.Parallel()
	s, _ := newSettingsForTest(t, SettingsTabGeneral)
	for range 4 { // Compact mode
		press(s, tea.KeyDown)
	}
	require.Equal(t, ActionToggleCompactMode{}, press(s, tea.KeyEnter))
	require.True(t, s.compact)

	press(s, tea.KeyDown)
	require.Equal(t, ActionToggleTransparentBackground{}, press(s, tea.KeyEnter))
	press(s, tea.KeyDown)
	require.Equal(t, ActionToggleMouseSupport{}, press(s, tea.KeyEnter))
	require.False(t, s.mouse)

	press(s, tea.KeyDown)
	act := press(s, tea.KeyEnter).(ActionSetTUIOption)
	require.Equal(t, "options.tui.exit_banner", act.Key)
	require.Equal(t, "compact", act.Value)
}

func TestSettingsDefaultAgentCyclesInstalledKinds(t *testing.T) {
	t.Parallel()
	s, _ := newSettingsForTest(t, SettingsTabAgents)
	act := press(s, tea.KeyEnter).(ActionSetDeckSettings)
	require.Equal(t, "codex", s.deck.DefaultAgent)
	require.Equal(t, "Default agent codex", act.Label)
	press(s, tea.KeyEnter)
	require.Equal(t, "claude", s.deck.DefaultAgent, "wraps around the installed kinds")
}

func TestSettingsBinaryRowsAreReadOnly(t *testing.T) {
	t.Parallel()
	s, _ := newSettingsForTest(t, SettingsTabAgents)
	s.HandleMsg(settingsBinariesMsg{binaries: []binaryStatus{
		{Kind: "claude", Command: "claude", Path: "/usr/bin/claude"},
		{Kind: "codex", Command: "codex"},
	}})
	rows := s.agentRows()
	require.Len(t, rows, 5+len(deckconfig.Kinds))
	require.Equal(t, "Deck attribution", rows[3].label)
	require.Equal(t, "Author commits as Deck", rows[4].label)
	require.Equal(t, "/usr/bin/claude", rows[5].value)
	require.Equal(t, "codex not found", rows[6].value)
	require.Nil(t, rows[5].change)

	for range 5 {
		press(s, tea.KeyDown)
	}
	require.Nil(t, press(s, tea.KeyEnter))
}

func TestSettingsTabsSwitch(t *testing.T) {
	t.Parallel()
	s, _ := newSettingsForTest(t, SettingsTabGeneral)
	press(s, tea.KeyTab)
	require.Equal(t, SettingsTabAgents, s.Tab())
	s.HandleMsg(tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
	require.Equal(t, SettingsTabGeneral, s.Tab())
	s.HandleMsg(tea.KeyPressMsg{Code: '3', Text: "3"})
	require.Equal(t, SettingsTabUsage, s.Tab())
	require.True(t, s.loading, "opening Usage loads it")
}

func TestSettingsUsageIgnoresStaleReports(t *testing.T) {
	t.Parallel()
	s, _ := newSettingsForTest(t, SettingsTabUsage)
	s.loadUsage()
	stale := s.req - 1
	s.HandleMsg(settingsUsageMsg{req: stale, report: workspace.UsageReport{Totals: usage.Totals{Turns: 9}}})
	require.Nil(t, s.report)
	require.True(t, s.loading)

	s.HandleMsg(settingsUsageMsg{req: s.req, report: workspace.UsageReport{Totals: usage.Totals{Turns: 2}}})
	require.NotNil(t, s.report)
	require.Equal(t, 2, s.report.Totals.Turns)
	require.False(t, s.loading)
}

func TestSettingsUsageEmptyState(t *testing.T) {
	t.Parallel()
	s, _ := newSettingsForTest(t, SettingsTabUsage)
	s.HandleMsg(settingsUsageMsg{req: s.req, report: workspace.UsageReport{}})
	text := ansi.Strip(strings.Join(s.usageLines(70), "\n"))
	require.Contains(t, text, usageEmptyText)
}

func TestSettingsUsageLinesFitWidth(t *testing.T) {
	t.Parallel()
	s, _ := newSettingsForTest(t, SettingsTabUsage)
	rep := workspace.UsageReport{
		Totals:   usage.Totals{Turns: 3, InputTokens: 12000, OutputTokens: 3400, CostUSD: 1.25},
		AllTurns: 3,
		Kinds:    []usage.Group{{Kind: "claude", Turns: 2, InputTokens: 10000, CostUSD: 1}, {Kind: "codex", Turns: 1, InputTokens: 2000, CostUSD: 0.25}},
		Models:   []usage.Group{{Kind: "claude", Model: "a-rather-long-model-name-that-overflows", Turns: 2, CostUSD: 1}},
		Days:     []usage.Day{{Tokens: 400, CostUSD: 0.25}, {}, {Tokens: 15000, CostUSD: 1}},
		Sessions: []usage.Session{{SessionID: "s1", Title: strings.Repeat("long title ", 20), Turns: 2, CostUSD: 1}},
	}
	s.HandleMsg(settingsUsageMsg{req: s.req, report: rep})
	const width = 60
	for _, l := range s.usageLines(width) {
		require.LessOrEqual(t, ansi.StringWidth(l), width, "line %q overflows", ansi.Strip(l))
	}
	text := ansi.Strip(strings.Join(s.usageLines(width), "\n"))
	require.Contains(t, text, "15.4k tokens")
	require.Contains(t, text, "By agent")
	require.Contains(t, text, "Daily tokens")
	require.Contains(t, text, "alt+u for the full view")
	require.NotContains(t, text, "$", "cost is only on the full view's Cost tab")
}

func TestSettingsUsageAltUOpensFullView(t *testing.T) {
	t.Parallel()
	s, _ := newSettingsForTest(t, SettingsTabUsage)
	act := s.HandleMsg(tea.KeyPressMsg{Code: 'u', Mod: tea.ModAlt})
	require.Equal(t, ActionOpenDialog{DialogID: UsageID}, act)
}

func TestFormatHelpers(t *testing.T) {
	t.Parallel()
	require.Equal(t, "950", formatTokens(950))
	require.Equal(t, "1k", formatTokens(1000))
	require.Equal(t, "12.3k", formatTokens(12_345))
	require.Equal(t, "4.5M", formatTokens(4_500_000))
	require.Equal(t, "1.2B", formatTokens(1_200_000_000))

	require.Equal(t, "plan", cycle(modeValues, "ask", -1))
	require.Equal(t, "plan", cycle(modeValues, "yolo", 1))
	require.Equal(t, "plan", cycle(modeValues, "", 1), "an unknown value starts at the first")
}
