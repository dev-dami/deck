package usage

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestRecordKeepsV2FieldsAndSpans(t *testing.T) {
	t.Parallel()
	svc, _ := newTestService(t)
	ctx := t.Context()
	now := time.Now()

	require.NoError(t, svc.Record(ctx, Record{
		ID: "turn-1", SessionID: "s1", AgentID: "claude", Kind: "claude", Model: "opus",
		InputTokens: 100, OutputTokens: 50, CachedTokens: 800, CacheWriteTokens: 100, ReasoningTokens: 20,
		CostUSD: 0.1, Duration: 10 * time.Second, TTFT: 1200 * time.Millisecond, APITime: 8 * time.Second,
		Status: StatusInterrupted, Trigger: TriggerRelay, Mode: "ask", At: now,
		Spans: []Span{
			{Kind: SpanWait, Start: 0, Dur: 1200 * time.Millisecond},
			{Kind: SpanThink, Start: 1200 * time.Millisecond, Dur: time.Second, Tokens: 20},
			{Kind: SpanTool, Name: "Bash", Start: 2200 * time.Millisecond, Dur: 3 * time.Second},
			{Kind: SpanText, Start: 5200 * time.Millisecond, Dur: 2 * time.Second, Tokens: 30},
		},
	}))
	require.NoError(t, svc.Record(ctx, Record{SessionID: "s1", AgentID: "codex", Kind: "codex", InputTokens: 1000, CachedTokens: 600, OutputTokens: 10, At: now.Add(time.Second)}))

	turns, err := svc.Traces(ctx, 10)
	require.NoError(t, err)
	require.Len(t, turns, 2)
	require.Equal(t, "codex", turns[0].Kind, "newest first")
	require.Equal(t, StatusDone, turns[0].Status, "no status means done")
	tr := turns[1]
	require.Equal(t, "turn-1", tr.ID)
	require.Equal(t, int64(100), tr.CacheWriteTokens)
	require.Equal(t, int64(20), tr.ReasoningTokens)
	require.Equal(t, 1200*time.Millisecond, tr.TTFT)
	require.Equal(t, 8*time.Second, tr.APITime)
	require.Equal(t, StatusInterrupted, tr.Status)
	require.Equal(t, TriggerRelay, tr.Trigger)
	require.Equal(t, "ask", tr.Mode)
	require.Equal(t, int64(150), tr.Tokens())

	spans, err := svc.Trace(ctx, "turn-1")
	require.NoError(t, err)
	require.Len(t, spans, 4)
	require.Equal(t, SpanWait, spans[0].Kind)
	require.Equal(t, "Bash", spans[2].Name)
	require.Equal(t, 3*time.Second, spans[2].Dur)
	require.Equal(t, 30, spans[3].Tokens)

	none, err := svc.Trace(ctx, turns[0].ID)
	require.NoError(t, err)
	require.Empty(t, none)

	limited, err := svc.Traces(ctx, 1)
	require.NoError(t, err)
	require.Len(t, limited, 1)
}

func TestOverviewModelsAndCost(t *testing.T) {
	t.Parallel()
	svc, _ := newTestService(t)
	ctx := t.Context()
	now := time.Now()
	recs := []Record{
		// claude counts cached tokens apart from input: 100+800+100 prompt, 800 cached.
		{SessionID: "s", AgentID: "claude", Kind: "claude", Model: "opus", InputTokens: 100, CachedTokens: 800, CacheWriteTokens: 100, OutputTokens: 50, ReasoningTokens: 10, CostUSD: 0.5, TTFT: time.Second, Duration: time.Minute, At: now},
		{SessionID: "s", AgentID: "claude", Kind: "claude", Model: "opus", InputTokens: 100, OutputTokens: 50, CostUSD: 0.25, TTFT: 3 * time.Second, Status: StatusInterrupted, At: now},
		// codex counts them in input: 1000 prompt, 600 cached; no cost reported.
		{SessionID: "s", AgentID: "codex", Kind: "codex", Model: "gpt-5", InputTokens: 1000, CachedTokens: 600, OutputTokens: 100, Status: StatusFailed, At: now},
		// Outside the range.
		{SessionID: "s", AgentID: "codex", Kind: "codex", InputTokens: 5, At: now.AddDate(0, 0, -10)},
	}
	for _, r := range recs {
		require.NoError(t, svc.Record(ctx, r))
	}

	o, err := svc.Overview(ctx, Since(7, now))
	require.NoError(t, err)
	require.Equal(t, 3, o.Turns)
	require.Equal(t, 4, o.AllTurns)
	require.Equal(t, int64(1200), o.InputTokens)
	require.Equal(t, int64(1400), o.CachedTokens)
	require.Equal(t, int64(100), o.CacheWriteTokens)
	require.Equal(t, int64(10), o.ReasoningTokens)
	require.InDelta(t, 0.75, o.CostUSD, 1e-9)
	require.InDelta(t, 100*1400.0/(1000+100+1000), o.CacheHitPct, 1e-9)
	require.Equal(t, 2*time.Second, o.TTFTAvg, "over the turns whose TTFT is known")
	require.Equal(t, 3*time.Second, o.TTFTP95)
	require.Equal(t, map[string]int{StatusDone: 1, StatusInterrupted: 1, StatusFailed: 1}, o.ByStatus)

	models, err := svc.Models(ctx, Since(7, now))
	require.NoError(t, err)
	require.Len(t, models, 2)
	require.Equal(t, "gpt-5", models[0].Model, "the most tokens first")
	require.InDelta(t, 60, models[0].CacheHitPct, 1e-9)
	require.Zero(t, models[0].TTFTAvg)
	require.Equal(t, "opus", models[1].Model)
	require.Equal(t, 2, models[1].Turns)
	require.InDelta(t, 100*800.0/(200+800+100), models[1].CacheHitPct, 1e-9)
	require.Equal(t, 2*time.Second, models[1].TTFTAvg)

	costs, err := svc.Cost(ctx, Since(7, now))
	require.NoError(t, err)
	require.Len(t, costs, 2)
	require.Equal(t, "claude", costs[0].Kind)
	require.True(t, costs[0].Reported)
	require.Equal(t, 2, costs[0].CostTurns)
	require.InDelta(t, 0.75, costs[0].CostUSD, 1e-9)
	require.Equal(t, "codex", costs[1].Kind)
	require.False(t, costs[1].Reported, "codex reports no cost: unknown, not free")
}

func TestPercentile(t *testing.T) {
	t.Parallel()
	require.Zero(t, percentile(nil, 0.95))
	vals := make([]int64, 100)
	for i := range vals {
		vals[i] = int64(i + 1)
	}
	require.Equal(t, int64(95), percentile(vals, 0.95))
	require.Equal(t, int64(7), percentile([]int64{7}, 0.95))
}

func TestSeriesFillsBuckets(t *testing.T) {
	t.Parallel()
	svc, _ := newTestService(t)
	ctx := t.Context()
	now := time.Date(2026, 10, 11, 15, 30, 0, 0, time.Local)
	for _, r := range []Record{
		{SessionID: "s", Kind: "claude", InputTokens: 10, OutputTokens: 5, At: now.Add(-2 * time.Hour)},
		{SessionID: "s", Kind: "claude", InputTokens: 1, OutputTokens: 1, At: now.Add(-2 * time.Hour)},
		{SessionID: "s", Kind: "codex", InputTokens: 100, At: now},
	} {
		require.NoError(t, svc.Record(ctx, r))
	}
	impl := svc.(*service)

	hours, err := impl.series(ctx, now.Add(-3*time.Hour), Hourly, now)
	require.NoError(t, err)
	require.Len(t, hours.Buckets, 4, "12:00 through 15:00")
	require.Equal(t, []string{"codex", "claude"}, hours.Kinds, "the most tokens first")
	require.Equal(t, []int64{0, 17, 0, 0}, hours.Tokens["claude"])
	require.Equal(t, []int{0, 2, 0, 0}, hours.Turns["claude"])
	require.Equal(t, []int64{0, 0, 0, 100}, hours.Tokens["codex"])

	days, err := impl.series(ctx, now.AddDate(0, 0, -2), Daily, now)
	require.NoError(t, err)
	require.Len(t, days.Buckets, 3)
	require.Equal(t, []int64{0, 0, 100}, days.Tokens["codex"])
	require.Equal(t, []int64{0, 0, 17}, days.Tokens["claude"])
}

func TestHeatStartsOnAMonday(t *testing.T) {
	t.Parallel()
	svc, _ := newTestService(t)
	now := time.Date(2026, 10, 11, 12, 0, 0, 0, time.Local) // a Sunday
	require.NoError(t, svc.Record(t.Context(), Record{SessionID: "s", Kind: "claude", InputTokens: 3, At: now}))
	days, err := svc.(*service).heat(t.Context(), 2, now)
	require.NoError(t, err)
	require.Len(t, days, 14)
	require.Equal(t, time.Monday, days[0].Day.Weekday())
	require.Equal(t, int64(3), days[13].Tokens)
}

func TestBehaviorRates(t *testing.T) {
	t.Parallel()
	svc, _ := newTestService(t)
	ctx := t.Context()
	now := time.Now()
	for _, b := range []Behavior{
		{SessionID: "s", AgentID: "claude", Kind: BehaviorApproveAllow, Detail: "manual"},
		{SessionID: "s", AgentID: "claude", Kind: BehaviorApproveAllow, Detail: "auto"},
		{SessionID: "s", AgentID: "claude", Kind: BehaviorApproveAllow, Detail: "auto"},
		{SessionID: "s", AgentID: "codex", Kind: BehaviorApproveDeny, Detail: "manual"},
		{SessionID: "s", AgentID: "claude", Kind: BehaviorInterrupt},
		{SessionID: "s", Kind: BehaviorHopPause, Value: 6},
		{SessionID: "s", Kind: BehaviorSteer, At: now.AddDate(0, 0, -1)},
	} {
		require.NoError(t, svc.RecordBehavior(ctx, b))
	}
	for _, r := range []Record{
		{SessionID: "s", Kind: "claude", Trigger: TriggerUser, At: now},
		{SessionID: "s", Kind: "claude", Trigger: TriggerRelay, Status: StatusInterrupted, At: now},
		{SessionID: "s", Kind: "codex", Trigger: TriggerMention, At: now},
		{SessionID: "s", Kind: "codex", Trigger: TriggerUser, Status: StatusFailed, At: now},
	} {
		require.NoError(t, svc.Record(ctx, r))
	}

	rep, err := svc.(*service).behavior(ctx, Since(7, now), now)
	require.NoError(t, err)
	require.Equal(t, 3, rep.Count(BehaviorApproveAllow))
	require.Equal(t, 1, rep.Count(BehaviorApproveDeny))
	require.InDelta(t, 0.25, rep.DenyRate, 1e-9)
	require.Equal(t, 4, rep.Turns)
	require.Equal(t, 1, rep.Interrupted)
	require.Equal(t, 1, rep.Failed)
	require.InDelta(t, 0.25, rep.CancelRate, 1e-9)
	require.InDelta(t, 0.5, rep.RelayRate, 1e-9)
	for _, c := range rep.Counts {
		if c.Kind == BehaviorHopPause {
			require.Equal(t, int64(6), c.Value)
		}
		if c.Kind == BehaviorApproveAllow && c.Detail == "auto" {
			require.Equal(t, 2, c.Count)
		}
	}
	require.Len(t, rep.Daily[BehaviorSteer], 7)
	require.Equal(t, 1, rep.Daily[BehaviorSteer][5].Count, "yesterday")
	require.Equal(t, 1, rep.Daily[BehaviorInterrupt][6].Count, "today")

	empty, err := svc.Behavior(ctx, now.Add(time.Hour))
	require.NoError(t, err)
	require.Zero(t, empty.Turns)
	require.Zero(t, empty.CancelRate)
}

func TestRecordLimitsDeduplicates(t *testing.T) {
	t.Parallel()
	svc, _ := newTestService(t)
	ctx := t.Context()
	t0 := time.Now().Add(-time.Hour)
	reset := t0.Add(3 * time.Hour).Truncate(time.Second)
	snap := func(used float64, resets time.Time, at time.Time) LimitSnapshot {
		return LimitSnapshot{AgentKind: "claude", WindowID: "five_hour", Kind: "session", Label: "5h", UsedPct: used, ResetsAt: resets, DurationMins: 300, Plan: "max", At: at}
	}

	n, err := svc.RecordLimits(ctx, []LimitSnapshot{snap(10, reset, t0), {AgentKind: "codex", WindowID: "primary", UsedPct: 5, At: t0}})
	require.NoError(t, err)
	require.Equal(t, 2, n, "the first of each window is written")

	n, err = svc.RecordLimits(ctx, []LimitSnapshot{snap(10.6, reset.Add(20*time.Second), t0.Add(time.Minute))})
	require.NoError(t, err)
	require.Zero(t, n, "under 1 point and the same reset: skipped")

	n, err = svc.RecordLimits(ctx, []LimitSnapshot{snap(11, reset, t0.Add(2*time.Minute))})
	require.NoError(t, err)
	require.Equal(t, 1, n, "1 point more is written")

	n, err = svc.RecordLimits(ctx, []LimitSnapshot{snap(11.2, reset.Add(5*time.Hour), t0.Add(3*time.Minute))})
	require.NoError(t, err)
	require.Equal(t, 1, n, "a new reset is written")

	latest, err := svc.Limits(ctx)
	require.NoError(t, err)
	require.Len(t, latest, 2)
	require.Equal(t, "claude", latest[0].AgentKind)
	require.InDelta(t, 11.2, latest[0].UsedPct, 1e-9)
	require.Equal(t, reset.Add(5*time.Hour).Unix(), latest[0].ResetsAt.Unix())
	require.Equal(t, "max", latest[0].Plan)
	require.Equal(t, 300, latest[0].DurationMins)
	require.Equal(t, "codex", latest[1].AgentKind)
	require.True(t, latest[1].ResetsAt.IsZero())

	hist, err := svc.LimitHistory(ctx, "claude", "five_hour", t0.Add(-time.Minute))
	require.NoError(t, err)
	require.Len(t, hist, 3)
	require.InDelta(t, 10, hist[0].UsedPct, 1e-9)
	require.InDelta(t, 11.2, hist[2].UsedPct, 1e-9)

	hist, err = svc.LimitHistory(ctx, "claude", "five_hour", t0.Add(90*time.Second))
	require.NoError(t, err)
	require.Len(t, hist, 2)
}

func TestLimitTimelineLeadsWithTheValueAtTheStart(t *testing.T) {
	t.Parallel()
	svc, _ := newTestService(t)
	ctx := t.Context()
	t0 := time.Now().Add(-10 * time.Hour).Truncate(time.Second)
	snap := func(kind, window string, used float64, at time.Time) LimitSnapshot {
		return LimitSnapshot{AgentKind: kind, WindowID: window, Kind: "session", UsedPct: used, ResetsAt: t0.Add(12 * time.Hour), At: at}
	}
	_, err := svc.RecordLimits(ctx, []LimitSnapshot{snap("claude", "five_hour", 10, t0), snap("codex", "primary", 4, t0)})
	require.NoError(t, err)
	_, err = svc.RecordLimits(ctx, []LimitSnapshot{snap("claude", "five_hour", 20, t0.Add(time.Hour))})
	require.NoError(t, err)
	_, err = svc.RecordLimits(ctx, []LimitSnapshot{snap("claude", "five_hour", 30, t0.Add(3*time.Hour))})
	require.NoError(t, err)

	rows, err := svc.LimitTimeline(ctx, t0.Add(2*time.Hour))
	require.NoError(t, err)
	require.Len(t, rows, 3, "claude's row before the start and the one after, and codex's only row")
	require.Equal(t, "claude", rows[0].AgentKind)
	require.InDelta(t, 20, rows[0].UsedPct, 1e-9)
	require.InDelta(t, 30, rows[1].UsedPct, 1e-9)
	require.Equal(t, "codex", rows[2].AgentKind)
}

func TestPruneKeepsEachWindowsNewestRow(t *testing.T) {
	t.Parallel()
	svc, _ := newTestService(t)
	ctx := t.Context()
	old := time.Now().Add(-limitRetention - 48*time.Hour)
	for i, used := range []float64{10, 20, 30} {
		_, err := svc.RecordLimits(ctx, []LimitSnapshot{{AgentKind: "codex", WindowID: "primary", UsedPct: used, At: old.Add(time.Duration(i) * time.Hour)}})
		require.NoError(t, err)
	}
	all, err := svc.LimitTimeline(ctx, time.Time{})
	require.NoError(t, err)
	require.Len(t, all, 3, "a prune runs at most once a day")

	// A day on, the next write prunes again.
	svc.(*service).pruned.Store(0)
	_, err = svc.RecordLimits(ctx, []LimitSnapshot{{AgentKind: "claude", WindowID: "five_hour", UsedPct: 5}})
	require.NoError(t, err)
	rows, err := svc.LimitTimeline(ctx, time.Time{})
	require.NoError(t, err)
	require.Len(t, rows, 2, "codex keeps its newest row only")
	require.Equal(t, "codex", rows[1].AgentKind)
	require.InDelta(t, 30, rows[1].UsedPct, 1e-9)
}

func TestHourlyByKindAndAgent(t *testing.T) {
	t.Parallel()
	svc, _ := newTestService(t)
	ctx := t.Context()
	h := time.Now().Add(-3 * time.Hour).Truncate(time.Hour)
	for _, r := range []Record{
		{AgentID: "claude", Kind: "claude", InputTokens: 100, OutputTokens: 10, CostUSD: 0.5, At: h.Add(5 * time.Minute)},
		{AgentID: "claude", Kind: "claude", InputTokens: 50, OutputTokens: 5, CostUSD: 0.25, At: h.Add(40 * time.Minute)},
		{AgentID: "reviewer", Kind: "claude", InputTokens: 7, At: h.Add(41 * time.Minute)},
		{AgentID: "claude", Kind: "claude", InputTokens: 1, At: h.Add(time.Hour + time.Minute)},
		{AgentID: "codex", Kind: "codex", InputTokens: 1, At: h.Add(-48 * time.Hour)},
	} {
		require.NoError(t, svc.Record(ctx, r))
	}
	hours, err := svc.Hourly(ctx, h.Add(-time.Hour))
	require.NoError(t, err)
	require.Len(t, hours, 3)
	require.Equal(t, h.Unix(), hours[0].At.Unix())
	require.Equal(t, "claude", hours[0].AgentID)
	require.Equal(t, 2, hours[0].Turns)
	require.Equal(t, int64(165), hours[0].Tokens)
	require.InDelta(t, 0.75, hours[0].CostUSD, 1e-9)
	require.Equal(t, "reviewer", hours[1].AgentID)
	require.Equal(t, h.Add(time.Hour).Unix(), hours[2].At.Unix())
}
