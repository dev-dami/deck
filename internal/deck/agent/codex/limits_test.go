package codex

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dev-dami/deck/internal/deck/agent"
)

func rateLimitsNote(t *testing.T, tr *translator, params string) []agent.Event {
	t.Helper()
	return tr.notification(noteRateLimits, json.RawMessage(params), rawOf(`{"method":"`+noteRateLimits+`"}`))
}

func TestRateLimitsPartialMerge(t *testing.T) {
	tr := newTranslator("cx", "")

	// A full snapshot first.
	evs := rateLimitsNote(t, tr, `{"rateLimits":{"limitId":"codex","planType":"plus",
		"primary":{"usedPercent":20,"windowDurationMins":300,"resetsAt":1760000000},
		"secondary":{"usedPercent":55,"windowDurationMins":10080,"resetsAt":1760500000}}}`)
	if len(evs) != 1 || evs[0].Kind != agent.KindLimits {
		t.Fatalf("events %+v", evs)
	}
	l := evs[0].Limits
	if l.Plan != "plus" || len(l.Windows) != 2 || l.Unavailable != "" {
		t.Fatalf("limits %+v", l)
	}
	p, s := l.Windows[0], l.Windows[1]
	if p.ID != "primary" || p.Kind != agent.WindowSession || p.Label != "5h" || p.UsedPct != 20 || p.DurationMins != 300 || !p.ResetsAt.Equal(time.Unix(1760000000, 0)) {
		t.Errorf("primary %+v", p)
	}
	if s.ID != "secondary" || s.Kind != agent.WindowWeekly || s.Label != "Weekly" || s.UsedPct != 55 || !s.ResetsAt.Equal(time.Unix(1760500000, 0)) {
		t.Errorf("secondary %+v", s)
	}
	if len(evs[0].Raw) != 0 {
		t.Error("limits event carries a raw line")
	}

	// A sparse update: only primary's percentage moves; the plan, the reset
	// times and the secondary window stay as they were.
	evs = rateLimitsNote(t, tr, `{"rateLimits":{"limitId":null,"planType":null,"primary":{"usedPercent":23},"secondary":null}}`)
	l = evs[0].Limits
	if l.Plan != "plus" || len(l.Windows) != 2 {
		t.Fatalf("after sparse update %+v", l)
	}
	if p := l.Windows[0]; p.UsedPct != 23 || p.DurationMins != 300 || !p.ResetsAt.Equal(time.Unix(1760000000, 0)) {
		t.Errorf("primary after sparse update %+v", p)
	}
	if s := l.Windows[1]; s.UsedPct != 55 || !s.ResetsAt.Equal(time.Unix(1760500000, 0)) {
		t.Errorf("secondary after sparse update %+v", s)
	}

	// Another bucket is not codex's own and changes nothing.
	if evs := rateLimitsNote(t, tr, `{"rateLimits":{"limitId":"codex_other","primary":{"usedPercent":99}}}`); len(evs) != 0 {
		t.Errorf("other bucket gave %+v", evs)
	}
	if got := tr.limits.limits().Windows[0].UsedPct; got != 23 {
		t.Errorf("other bucket changed primary to %v", got)
	}
}

func TestRateLimitsDefaultWindows(t *testing.T) {
	tr := newTranslator("cx", "")
	evs := rateLimitsNote(t, tr, `{"rateLimits":{"secondary":{"usedPercent":7}}}`)
	l := evs[0].Limits
	if len(l.Windows) != 1 || l.Windows[0].ID != "secondary" || l.Windows[0].DurationMins != 10080 || l.Windows[0].Kind != agent.WindowWeekly || !l.Windows[0].ResetsAt.IsZero() {
		t.Errorf("defaults %+v", l.Windows)
	}
	evs = rateLimitsNote(t, tr, `{"rateLimits":{"primary":{"usedPercent":1}}}`)
	if w := evs[0].Limits.Windows[0]; w.ID != "primary" || w.DurationMins != 300 || w.Label != "5h" {
		t.Errorf("primary default %+v", w)
	}
}

func TestWindowKind(t *testing.T) {
	for mins, want := range map[int][2]string{
		300:   {agent.WindowSession, "5h"},
		90:    {agent.WindowSession, "90m"},
		10080: {agent.WindowWeekly, "Weekly"},
		43200: {agent.WindowMonthly, "Monthly"},
		2880:  {agent.WindowOther, "2d"},
		0:     {agent.WindowOther, "Window"},
	} {
		k, l := windowKind(mins)
		if k != want[0] || l != want[1] {
			t.Errorf("windowKind(%d) = %q %q, want %q", mins, k, l, want)
		}
	}
}

func TestTokenUsageReadsReasoningAndCacheWrite(t *testing.T) {
	tr := newTranslator("cx", "")
	evs := tr.notification(noteTokenUsage, json.RawMessage(`{"turnId":"t1","tokenUsage":{
		"total":{"inputTokens":1000,"cachedInputTokens":600,"cacheWriteInputTokens":50,"outputTokens":200,"reasoningOutputTokens":80,"totalTokens":1200},
		"last":{"inputTokens":1000,"cachedInputTokens":600,"outputTokens":200,"reasoningOutputTokens":80,"totalTokens":1200},
		"modelContextWindow":200000}}`), nil)
	u := evs[0].Usage
	if u.ReasoningTokens != 80 || u.CacheWriteTokens != 50 || u.CachedTokens != 600 {
		t.Errorf("usage %+v", u)
	}
}

func TestAdapterReadsLimitsAfterThreadStart(t *testing.T) {
	fe := newFakeEnv(t, "testdata/session1.jsonl")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	run, err := New(os.Args[0]).Start(ctx, agent.Spec{ID: "cx3", Dir: t.TempDir(), Env: fe.env})
	if err != nil {
		t.Fatal(err)
	}
	defer run.Close()
	evs, mu := drain(run)
	waitFor(t, "limits", func() bool { return hasKind(evs, mu, agent.KindLimits) })

	l := limitsOf(evs, mu)
	if l.Plan != "pro" || len(l.Windows) != 2 || l.Windows[0].UsedPct != 12 || l.Windows[1].UsedPct != 40 || l.Windows[1].DurationMins != 10080 {
		t.Errorf("limits %+v", l)
	}
	out := fe.read(t)
	if !strings.Contains(out, `rateLimits={"excludeResetCreditDetails":true}`) || !strings.Contains(out, "account/read") {
		t.Errorf("reads not sent:\n%s", out)
	}
	mu.Lock()
	defer mu.Unlock()
	for _, e := range *evs {
		b, _ := json.Marshal(e)
		if strings.Contains(string(b), "example.com") || strings.Contains(string(b), "acct-1") {
			t.Errorf("account details in event: %s", b)
		}
	}
}

func limitsOf(evs *[]agent.Event, mu *sync.Mutex) *agent.Limits {
	mu.Lock()
	defer mu.Unlock()
	for _, e := range *evs {
		if e.Kind == agent.KindLimits {
			return e.Limits
		}
	}
	return nil
}

func TestTurnFailedOnUsageLimitIsALimitHit(t *testing.T) {
	tr := newTranslator("cx", "")
	now := time.Unix(1760000000, 0)
	tr.now = func() time.Time { return now }
	rateLimitsNote(t, tr, `{"rateLimits":{"limitId":"codex","primary":{"usedPercent":100,"windowDurationMins":300,"resetsAt":1760007200},
		"secondary":{"usedPercent":40,"windowDurationMins":10080,"resetsAt":1760500000}}}`)
	done := func(params string) []agent.Event {
		return tr.notification(noteTurnCompleted, json.RawMessage(params), rawOf(`{"method":"`+noteTurnCompleted+`"}`))
	}
	evs := done(`{"threadId":"th","turn":{"id":"t1","status":"failed","error":{"message":"You've hit your usage limit.","codexErrorInfo":"usageLimitExceeded"}}}`)
	if len(evs) != 2 || evs[0].Kind != agent.KindError || evs[1].Kind != agent.KindTurnDone {
		t.Fatalf("events %+v", evs)
	}
	h := evs[0].LimitHit
	if h == nil || h.Window != "primary" || !h.ResetsAt.Equal(time.Unix(1760007200, 0)) {
		t.Errorf("hit %+v", h)
	}

	// No snapshot says which window: the message's time stands.
	tr2 := newTranslator("cx", "")
	tr2.now = func() time.Time { return now }
	evs = tr2.notification(noteError, json.RawMessage(`{"error":{"message":"You've hit your usage limit. Try again in 3 hours."},"willRetry":false}`), rawOf(`{"method":"error"}`))
	if h := evs[0].LimitHit; h == nil || !h.ResetsAt.Equal(now.Add(3*time.Hour).Truncate(time.Minute)) {
		t.Errorf("hit from text %+v", h)
	}

	// Any other failure is not.
	evs = done(`{"threadId":"th","turn":{"id":"t2","status":"failed","error":{"message":"stream disconnected","codexErrorInfo":{"responseStreamDisconnected":{}}}}}`)
	if evs[0].LimitHit != nil {
		t.Errorf("not a limit: %+v", evs[0].LimitHit)
	}
}
