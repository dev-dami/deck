package dialog

import (
	"context"
	"image"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"

	deckagent "github.com/dev-dami/deck/internal/deck/agent"
	"github.com/dev-dami/deck/internal/ui/common"
	"github.com/dev-dami/deck/internal/ui/styles"
	"github.com/dev-dami/deck/internal/usage"
	"github.com/dev-dami/deck/internal/workspace"
)

// fakeUsage is a usage.Service with nothing recorded.
type fakeUsage struct{}

func (fakeUsage) Record(context.Context, usage.Record) error           { return nil }
func (fakeUsage) RecordBehavior(context.Context, usage.Behavior) error { return nil }
func (fakeUsage) RecordLimits(context.Context, []usage.LimitSnapshot) (int, error) {
	return 0, nil
}

func (fakeUsage) Report(context.Context, time.Time, int) (usage.Report, error) {
	return usage.Report{}, nil
}

func (fakeUsage) Overview(context.Context, time.Time) (usage.Overview, error) {
	return usage.Overview{}, nil
}

func (fakeUsage) Series(context.Context, time.Time, usage.Granularity) (usage.Series, error) {
	return usage.Series{}, nil
}
func (fakeUsage) Heat(context.Context, int) ([]usage.Day, error) { return nil, nil }
func (fakeUsage) Behavior(context.Context, time.Time) (usage.BehaviorReport, error) {
	return usage.BehaviorReport{}, nil
}
func (fakeUsage) Limits(context.Context) ([]usage.LimitSnapshot, error) { return nil, nil }
func (fakeUsage) LimitHistory(context.Context, string, string, time.Time) ([]usage.LimitSnapshot, error) {
	return nil, nil
}
func (fakeUsage) Traces(context.Context, int) ([]usage.Turn, error)            { return nil, nil }
func (fakeUsage) Trace(context.Context, string) ([]usage.Span, error)          { return nil, nil }
func (fakeUsage) Models(context.Context, time.Time) ([]usage.ModelStat, error) { return nil, nil }
func (fakeUsage) Cost(context.Context, time.Time) ([]usage.KindCost, error)    { return nil, nil }

// usageWorkspace is a Workspace with a usage record and live limits.
type usageWorkspace struct {
	workspace.Workspace
	live map[string]workspace.LiveLimits
}

func (*usageWorkspace) Usage(context.Context, int) (workspace.UsageReport, error) {
	return workspace.UsageReport{}, nil
}
func (*usageWorkspace) UsageService() usage.Service { return fakeUsage{} }
func (w *usageWorkspace) LiveLimits() map[string]workspace.LiveLimits {
	return w.live
}

var usageNow = time.Date(2026, 10, 11, 14, 0, 0, 0, time.Local)

func newUsageForTest(t *testing.T) (*Usage, *usageWorkspace) {
	t.Helper()
	s := styles.DeckDark()
	ws := &usageWorkspace{live: map[string]workspace.LiveLimits{
		"claude": {Limits: deckagent.Limits{Plan: "max", Windows: []deckagent.LimitWindow{
			{ID: "five_hour", Label: "5h", UsedPct: 38, ResetsAt: usageNow.Add(2 * time.Hour), DurationMins: 300},
		}}},
		"opencode": {Limits: deckagent.Limits{Unavailable: deckagent.LimitsUnsupported}},
	}}
	u := NewUsage(&common.Common{Workspace: ws, Styles: &s})
	u.now = func() time.Time { return usageNow }
	return u, ws
}

// testUsageData is a little of everything, with costs on it that only the
// Cost tab may show.
func testUsageData() usageData {
	days := func(n int) []time.Time {
		out := make([]time.Time, n)
		for i := range out {
			out[i] = usage.Midnight(usageNow).AddDate(0, 0, i-n+1)
		}
		return out
	}
	heat := make([]usage.Day, 0, 182)
	for i, d := range days(182) {
		heat = append(heat, usage.Day{Day: d, Tokens: int64(i * 100), CostUSD: 0.5})
	}
	return usageData{
		overview: usage.Overview{
			Turns: 4, AllTurns: 9, InputTokens: 12000, OutputTokens: 3400, CachedTokens: 8000,
			CacheWriteTokens: 900, ReasoningTokens: 400, CostUSD: 1.25, Duration: 3 * time.Minute,
			TTFTAvg: 900 * time.Millisecond, TTFTP95: 2 * time.Second, CacheHitPct: 40,
			ByStatus: map[string]int{usage.StatusDone: 3, usage.StatusInterrupted: 1},
		},
		series: usage.Series{
			Granularity: usage.Daily, Buckets: days(7), Kinds: []string{"claude", "codex"},
			Tokens: map[string][]int64{"claude": {1, 2, 3, 4, 5, 6, 7000}, "codex": {0, 0, 900, 0, 0, 0, 3000}},
		},
		heat: heat,
		behavior: usage.BehaviorReport{
			Turns: 4, Interrupted: 1, CancelRate: 0.25, DenyRate: 0.1,
			Counts: []usage.BehaviorCount{
				{Kind: usage.BehaviorApproveAllow, Detail: "manual", Count: 3},
				{Kind: usage.BehaviorApproveAllow, Detail: "auto", Count: 2},
				{Kind: usage.BehaviorInterrupt, Count: 1},
			},
			Daily: map[string][]usage.DayCount{usage.BehaviorApproveAllow: {{Count: 1}, {Count: 4}}},
		},
		limits: []usage.LimitSnapshot{
			{AgentKind: "claude", WindowID: "five_hour", Label: "5h", UsedPct: 10, DurationMins: 300},
			{AgentKind: "claude", WindowID: "seven_day", Label: "Weekly", UsedPct: 20, DurationMins: 10080},
		},
		turns: []usage.Turn{
			{ID: "t1", Kind: "claude", Model: "opus", InputTokens: 5000, OutputTokens: 900, CostUSD: 1, Duration: 14 * time.Second, TTFT: time.Second, Status: usage.StatusDone, At: usageNow.Add(-time.Hour)},
			{ID: "t2", Kind: "codex", Model: "gpt-5", InputTokens: 3000, OutputTokens: 400, Duration: 9 * time.Second, Status: usage.StatusInterrupted, At: usageNow.Add(-2 * time.Hour)},
			{ID: "t3", Kind: "claude", Model: "opus", InputTokens: 4000, OutputTokens: 700, CostUSD: 0.25, Duration: 11 * time.Second, Status: usage.StatusDone, At: usageNow.Add(-3 * time.Hour)},
		},
		models: []usage.ModelStat{
			{Kind: "claude", Model: "opus", Turns: 2, InputTokens: 9000, OutputTokens: 1600, CacheHitPct: 52, TTFTAvg: time.Second},
			{Kind: "codex", Model: "gpt-5", Turns: 1, InputTokens: 3000, OutputTokens: 400},
		},
		cost: []usage.KindCost{
			{Kind: "claude", Turns: 3, CostTurns: 3, CostUSD: 1.25, Reported: true},
			{Kind: "codex", Turns: 1},
		},
	}
}

func loadedUsage(t *testing.T) *Usage {
	t.Helper()
	u, _ := newUsageForTest(t)
	u.load()
	u.HandleMsg(usageDataMsg{req: u.req, data: testUsageData()})
	return u
}

// usageView draws the view in a w x h screen and returns its plain text.
func usageView(u *Usage, w, h int) string {
	scr := uv.NewScreenBuffer(w, h)
	u.Draw(scr, image.Rect(0, 0, w, h))
	return ansi.Strip(scr.Render())
}

func usageKey(u *Usage, s string) Action {
	if s == "shift+tab" {
		return u.HandleMsg(tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
	}
	if s == "tab" {
		return u.HandleMsg(tea.KeyPressMsg{Code: tea.KeyTab})
	}
	if s == "enter" {
		return u.HandleMsg(tea.KeyPressMsg{Code: tea.KeyEnter})
	}
	return u.HandleMsg(tea.KeyPressMsg{Code: rune(s[0]), Text: s})
}

func TestUsageTabsSwitch(t *testing.T) {
	t.Parallel()
	u := loadedUsage(t)
	require.Equal(t, UsageTabOverview, u.Tab())
	usageKey(u, "tab")
	require.Equal(t, UsageTabLimits, u.Tab())
	usageKey(u, "shift+tab")
	usageKey(u, "shift+tab")
	require.Equal(t, UsageTabCost, u.Tab(), "wraps around")
	for i, want := range []UsageTab{UsageTabOverview, UsageTabLimits, UsageTabBehaviour, UsageTabTraces, UsageTabModels, UsageTabCost} {
		usageKey(u, string(rune('1'+i)))
		require.Equal(t, want, u.Tab())
	}
	require.Equal(t, ActionClose{}, u.HandleMsg(tea.KeyPressMsg{Code: tea.KeyEscape}))
}

func TestUsagePeriodKeysReload(t *testing.T) {
	t.Parallel()
	u := loadedUsage(t)
	require.Equal(t, defaultUsagePeriod, u.period)
	for i, k := range []string{"t", "w", "m", "a"} {
		before := u.req
		act, ok := usageKey(u, k).(ActionCmd)
		require.True(t, ok)
		require.NotNil(t, act.Cmd, "period %s loads", k)
		require.Equal(t, i, u.period)
		require.Equal(t, before+1, u.req)
		u.HandleMsg(usageDataMsg{req: u.req, data: testUsageData()})
	}

	// A load for a period since left is dropped.
	usageKey(u, "t")
	stale := u.req
	usageKey(u, "w")
	u.HandleMsg(usageDataMsg{req: stale, data: usageData{}})
	require.True(t, u.loading)
	u.HandleMsg(usageDataMsg{req: u.req, data: testUsageData()})
	require.False(t, u.loading)
	require.Equal(t, 9, u.data.overview.AllTurns)
}

func TestUsageDollarsOnlyOnCostTab(t *testing.T) {
	t.Parallel()
	for _, size := range [][2]int{{160, 50}, {80, 24}, {50, 16}} {
		u := loadedUsage(t)
		for tab := UsageTabOverview; tab < usageTabCount; tab++ {
			u.SetTab(tab)
			if tab == UsageTabTraces {
				usageKey(u, "enter")
				u.HandleMsg(usageTraceMsg{turnID: u.traceOpen, spans: []usage.Span{{Kind: usage.SpanWait, Dur: time.Second}}})
			}
			view := usageView(u, size[0], size[1])
			if tab == UsageTabCost {
				require.Contains(t, view, "Reported by each CLI")
				require.Contains(t, view, "$1.25")
				require.Contains(t, view, "—", "a CLI that reports no cost shows a dash")
				require.NotContains(t, view, "$0.00")
				continue
			}
			require.NotContains(t, view, "$", "%s at %v shows dollars", tab, size)
		}
	}
}

func TestUsageLimitsMergeLiveAndSayUnsupported(t *testing.T) {
	t.Parallel()
	u := loadedUsage(t)
	usageKey(u, "2")
	view := usageView(u, 140, 40)
	require.Contains(t, view, "max plan")
	require.Contains(t, view, "62% left", "the live window wins over the stored one")
	require.Contains(t, view, "80% left", "a stored window still shows")
	require.Contains(t, view, "not reported by opencode")
	require.Contains(t, view, "not reported by antigravity", "known not to report before it first runs")
	require.Contains(t, view, "no limits reported yet", "codex reports, but has not yet")

	// j selects the next window and reads its history.
	require.Equal(t, "claude/five_hour", u.historyKey)
	act := usageKey(u, "j").(ActionCmd)
	require.NotNil(t, act.Cmd)
	require.Equal(t, "claude/seven_day", u.historyKey)
}

func TestUsageOverviewCrosshairAndLayout(t *testing.T) {
	t.Parallel()
	u := loadedUsage(t)
	wide := usageView(u, 160, 50)
	require.Contains(t, wide, "Tokens by agent")
	require.Contains(t, wide, "Token types")
	require.Contains(t, wide, "Last 26 weeks")
	require.Contains(t, wide, "TTFT p95")
	require.True(t, u.HandlesHover())

	require.Equal(t, -1, u.chart.Cursor())
	u.HandleMsg(tea.KeyPressMsg{Code: tea.KeyLeft})
	require.GreaterOrEqual(t, u.chart.Cursor(), 0, "← shows the crosshair")

	narrow := usageView(u, 50, 30)
	require.NotContains(t, narrow, "Last 26 weeks", "the heatmap is dropped when narrow")
	for _, l := range strings.Split(narrow, "\n") {
		require.LessOrEqual(t, ansi.StringWidth(l), 50)
	}
}

func TestUsageTraceWaterfall(t *testing.T) {
	t.Parallel()
	u := loadedUsage(t)
	usageKey(u, "4")
	usageKey(u, "j")
	act := usageKey(u, "enter").(ActionCmd)
	require.NotNil(t, act.Cmd)
	require.Equal(t, "t2", u.traceOpen)
	u.HandleMsg(usageTraceMsg{turnID: "t2", spans: []usage.Span{
		{Kind: usage.SpanWait, Dur: 800 * time.Millisecond},
		{Kind: usage.SpanTool, Name: "Bash", Start: time.Second, Dur: 3 * time.Second},
		{Kind: usage.SpanText, Start: 4 * time.Second, Dur: 2 * time.Second, Tokens: 300},
	}})
	view := usageView(u, 120, 40)
	require.Contains(t, view, "span tokens are estimates")
	require.Contains(t, view, "Bash")
	require.Contains(t, view, "interrupted")

	usageKey(u, "enter")
	require.Empty(t, u.traceOpen, "enter again closes the waterfall")
}

func TestUsageModelsTrend(t *testing.T) {
	t.Parallel()
	u := loadedUsage(t)
	usageKey(u, "5")
	view := usageView(u, 120, 40)
	require.Contains(t, view, "opus")
	require.Contains(t, view, "last 2 turns")
	usageKey(u, "j")
	require.Contains(t, usageView(u, 120, 40), "Not enough recent turns")
}

func TestUsageWithoutRecord(t *testing.T) {
	t.Parallel()
	s := styles.DeckDark()
	u := NewUsage(&common.Common{Styles: &s})
	require.Nil(t, u.InitialCmd())
	require.Contains(t, usageView(u, 80, 24), "only counted when Deck runs its agents")
}

func TestFormatCost(t *testing.T) {
	t.Parallel()
	require.Equal(t, "$0.00", formatCost(0))
	require.Equal(t, "<$0.01", formatCost(0.001))
	require.Equal(t, "$12.35", formatCost(12.346))
}
