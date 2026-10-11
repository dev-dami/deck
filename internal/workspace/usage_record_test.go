package workspace

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	deckagent "github.com/dev-dami/deck/internal/deck/agent"
	"github.com/dev-dami/deck/internal/usage"
)

func TestTraceBuilderLaysOutATurn(t *testing.T) {
	t0 := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
	at := func(ms int) time.Time { return t0.Add(time.Duration(ms) * time.Millisecond) }
	b := newTraceBuilder(t0)
	for _, ev := range []deckagent.Event{
		{Kind: deckagent.KindUsage, Time: at(100), Usage: &deckagent.Usage{ContextTokens: 5}}, // not a span
		{Kind: deckagent.KindReasoning, Time: at(1000), Text: "abcd", Delta: true},
		{Kind: deckagent.KindReasoning, Time: at(1500), Text: "efghijkl", Delta: true},
		{Kind: deckagent.KindReasoning, Time: at(1600), Text: "abcdefghijkl"}, // the whole thought again
		{Kind: deckagent.KindToolCall, Time: at(1700), Tool: &deckagent.Tool{ID: "t1", Name: "Bash", Title: "rm -rf secret"}},
		{Kind: deckagent.KindToolResult, Time: at(4700), Tool: &deckagent.Tool{ID: "t1", Name: "Bash"}},
		{Kind: deckagent.KindAssistantText, Time: at(5000), Text: "Done, ", Delta: true},
		{Kind: deckagent.KindAssistantText, Time: at(5400), Text: "all good", Delta: true},
		{Kind: deckagent.KindToolCall, Time: at(5500), Tool: &deckagent.Tool{ID: "t2", Name: "Read"}}, // never finishes
	} {
		b.observe(ev)
	}
	require.Equal(t, time.Second, b.ttft())

	u := deckagent.Usage{OutputTokens: 100, ReasoningTokens: 40}
	spans := b.finish(at(6000), u, "claude")
	require.Equal(t, []usage.Span{
		{Kind: usage.SpanWait, Start: 0, Dur: time.Second},
		{Kind: usage.SpanThink, Start: time.Second, Dur: 600 * time.Millisecond, Tokens: 40},
		{Kind: usage.SpanTool, Name: "Bash", Start: 1700 * time.Millisecond, Dur: 3 * time.Second},
		{Kind: usage.SpanText, Start: 4700 * time.Millisecond, Dur: 700 * time.Millisecond, Tokens: 60},
		{Kind: usage.SpanTool, Name: "Read", Start: 5500 * time.Millisecond, Dur: 500 * time.Millisecond},
	}, spans)
	for _, s := range spans {
		require.NotContains(t, s.Name, "secret", "only tool names, never their input")
	}
}

func TestTraceBuilderTokenSplit(t *testing.T) {
	t0 := time.Now()
	b := newTraceBuilder(t0)
	b.observe(deckagent.Event{Kind: deckagent.KindAssistantText, Time: t0.Add(time.Second), Text: "aaaa", Delta: true})
	b.observe(deckagent.Event{Kind: deckagent.KindToolCall, Time: t0.Add(2 * time.Second), Tool: &deckagent.Tool{ID: "x", Name: "Edit"}})
	b.observe(deckagent.Event{Kind: deckagent.KindAssistantText, Time: t0.Add(3 * time.Second), Text: "aaaaaaaaaaaa", Delta: true})
	// agy counts thinking apart from output: all 80 output tokens are text.
	spans := b.finish(t0.Add(4*time.Second), deckagent.Usage{OutputTokens: 80, ReasoningTokens: 30}, "antigravity")
	var text []int
	for _, s := range spans {
		if s.Kind == usage.SpanText {
			text = append(text, s.Tokens)
		}
	}
	require.Equal(t, []int{20, 60}, text, "split by how much text each span carried")

	// No usage at all (an interrupted turn): estimated from the text.
	b = newTraceBuilder(t0)
	b.observe(deckagent.Event{Kind: deckagent.KindAssistantText, Time: t0.Add(time.Second), Text: "12345678"})
	spans = b.finish(t0.Add(2*time.Second), deckagent.Usage{}, "codex")
	require.Equal(t, 2, spans[1].Tokens)

	// A turn with no token at all has no wait span and no TTFT.
	b = newTraceBuilder(t0)
	require.Zero(t, b.ttft())
	require.Empty(t, b.finish(t0.Add(time.Second), deckagent.Usage{}, "claude"))
}

func TestTurnStatus(t *testing.T) {
	require.Equal(t, usage.StatusDone, turnStatus(deckagent.Event{Kind: deckagent.KindTurnDone}, false))
	require.Equal(t, usage.StatusInterrupted, turnStatus(deckagent.Event{Kind: deckagent.KindTurnDone, Text: "interrupted"}, true))
	require.Equal(t, usage.StatusFailed, turnStatus(deckagent.Event{Kind: deckagent.KindTurnDone, Text: "failed"}, false))
	require.Equal(t, usage.StatusFailed, turnStatus(deckagent.Event{Kind: deckagent.KindTurnDone}, true), "an error during the turn")
	require.Equal(t, usage.StatusFailed, turnStatus(deckagent.Event{Kind: deckagent.KindExited}, false))
	require.True(t, retrying("stream disconnected (retrying)"))
	require.False(t, retrying("boom"))
}

func TestMergeLimits(t *testing.T) {
	reset := time.Now().Add(time.Hour)
	got := mergeLimits(deckagent.Limits{}, deckagent.Limits{Unavailable: deckagent.LimitsProbeFailed})
	require.Equal(t, deckagent.LimitsProbeFailed, got.Unavailable)

	got = mergeLimits(got, deckagent.Limits{Plan: "max", Windows: []deckagent.LimitWindow{
		{ID: "five_hour", UsedPct: 10, ResetsAt: reset}, {ID: "seven_day", UsedPct: 50},
	}})
	require.Empty(t, got.Unavailable)
	require.Equal(t, "max", got.Plan)

	// Another agent of the kind reports one window and no plan: the rest stays.
	got = mergeLimits(got, deckagent.Limits{Windows: []deckagent.LimitWindow{{ID: "five_hour", UsedPct: 12}}})
	require.Equal(t, "max", got.Plan)
	require.Len(t, got.Windows, 2)
	require.InDelta(t, 12, got.Windows[0].UsedPct, 1e-9)
	require.InDelta(t, 50, got.Windows[1].UsedPct, 1e-9)

	// A failed probe does not wipe known windows out.
	got = mergeLimits(got, deckagent.Limits{Unavailable: deckagent.LimitsProbeFailed})
	require.Empty(t, got.Unavailable)
	require.Len(t, got.Windows, 2)
}

func TestLiveLimitsByKind(t *testing.T) {
	w := newPickerWorkspace(t, pickerCfg())
	w.limits = map[string]LiveLimits{}
	at := time.Now()
	w.onEvent("", deckagent.Event{AgentID: "codex", Kind: deckagent.KindLimits, Time: at, Limits: &deckagent.Limits{
		Plan: "pro", Windows: []deckagent.LimitWindow{{ID: "primary", Kind: deckagent.WindowSession, UsedPct: 30}},
	}})
	w.onEvent("", deckagent.Event{AgentID: "opencode", Kind: deckagent.KindLimits, Limits: &deckagent.Limits{Unavailable: deckagent.LimitsUnsupported}})

	live := w.LiveLimits()
	require.Len(t, live, 2)
	require.Equal(t, "pro", live["codex"].Plan)
	require.Equal(t, at, live["codex"].At)
	require.InDelta(t, 30, live["codex"].Windows[0].UsedPct, 1e-9)
	require.Equal(t, deckagent.LimitsUnsupported, live["opencode"].Unavailable)

	// The map handed out is a copy.
	live["codex"].Windows[0].UsedPct = 99
	require.InDelta(t, 30, w.LiveLimits()["codex"].Windows[0].UsedPct, 1e-9)
	require.Nil(t, w.UsageService(), "no database here")
}
