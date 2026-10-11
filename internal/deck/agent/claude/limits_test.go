package claude

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dev-dami/deck/internal/deck/agent"
)

func limitsEvents(evs []agent.Event) []agent.Event {
	var out []agent.Event
	for _, e := range evs {
		if e.Kind == agent.KindLimits {
			out = append(out, e)
		}
	}
	return out
}

func windowByID(l *agent.Limits, id string) (agent.LimitWindow, bool) {
	for _, w := range l.Windows {
		if w.ID == id {
			return w, true
		}
	}
	return agent.LimitWindow{}, false
}

func TestRateLimitEventsAndUsageProbe(t *testing.T) {
	evs := replay(t, filepath.Join("testdata", "limits.jsonl"))
	lim := limitsEvents(evs)
	// two usable rate_limit_events (the one without utilization says
	// nothing), then Deck's own probe answer; another request's answer is not
	// Deck's business.
	if len(lim) != 3 {
		t.Fatalf("limits events = %d, want 3: %v", len(lim), kinds(evs))
	}

	first := lim[0].Limits
	w, ok := windowByID(first, "five_hour")
	if !ok || len(first.Windows) != 1 {
		t.Fatalf("first limits %+v", first)
	}
	if w.Kind != agent.WindowSession || w.Label != "5h" || w.DurationMins != 300 || w.UsedPct < 41.99 || w.UsedPct > 42.01 || !w.ResetsAt.Equal(time.Unix(1760000000, 0)) {
		t.Errorf("five_hour from event %+v", w)
	}
	if first.Unavailable != "" || first.Plan != "" {
		t.Errorf("first limits %+v", first)
	}

	second := lim[1].Limits
	if len(second.Windows) != 2 || second.Windows[0].ID != "five_hour" || second.Windows[1].ID != "seven_day" {
		t.Fatalf("windows are kept and ordered: %+v", second.Windows)
	}
	if w := second.Windows[1]; w.Kind != agent.WindowWeekly || w.DurationMins != 10080 || w.UsedPct < 81.49 || w.UsedPct > 81.51 {
		t.Errorf("seven_day from event %+v", w)
	}

	probe := lim[2].Limits
	if probe.Plan != "max" || probe.Unavailable != "" {
		t.Errorf("probe limits %+v", probe)
	}
	if len(probe.Windows) != 3 {
		t.Fatalf("probe windows %+v", probe.Windows)
	}
	w, _ = windowByID(probe, "five_hour")
	if w.UsedPct != 45 || !w.ResetsAt.Equal(time.Date(2026, 10, 11, 18, 0, 0, 0, time.UTC)) {
		t.Errorf("five_hour from probe %+v", w)
	}
	w, _ = windowByID(probe, "seven_day")
	if w.UsedPct != 82.5 || !w.ResetsAt.Equal(time.Date(2026, 10, 15, 9, 0, 0, 0, time.UTC)) {
		t.Errorf("seven_day from probe %+v", w)
	}
	w, ok = windowByID(probe, "seven_day_sonnet")
	if !ok || w.UsedPct != 3 || !w.ResetsAt.IsZero() || w.Label != "Weekly · Sonnet" {
		t.Errorf("seven_day_sonnet from probe %+v", w)
	}
	if _, ok := windowByID(probe, "seven_day_opus"); ok {
		t.Error("a null window is not a window")
	}

	// Nothing about the account leaves the adapter.
	for _, e := range lim {
		if len(e.Raw) != 0 {
			t.Errorf("limits event keeps its raw line: %s", e.Raw)
		}
		b, _ := json.Marshal(e)
		if strings.Contains(string(b), "example.com") {
			t.Errorf("account email in event: %s", b)
		}
	}

	// The result line keeps what used to be dropped.
	var u *agent.Usage
	for _, e := range evs {
		if e.Kind == agent.KindUsage && e.Usage.OutputTokens > 0 {
			u = e.Usage
		}
	}
	if u == nil {
		t.Fatal("no usage")
	}
	if u.InputTokens != 4 || u.OutputTokens != 153 || u.CachedTokens != 67466 || u.CacheWriteTokens != 12425 || u.ReasoningTokens != 39 {
		t.Errorf("tokens %+v", u)
	}
	if u.TTFTms != 5462 || u.APIms != 6204 || u.DurationMs != 7961 {
		t.Errorf("times %+v", u)
	}
}

func TestUsageProbeFailureFallsBackToEvents(t *testing.T) {
	tr := newTranslator("c1")
	evs := tr.Line([]byte(`{"type":"control_response","response":{"subtype":"error","request_id":"deck_get_usage","error":"unknown subtype"}}`))
	if len(evs) != 1 || evs[0].Kind != agent.KindLimits || evs[0].Limits.Unavailable != agent.LimitsProbeFailed {
		t.Fatalf("failed probe %+v", evs)
	}
	// A later event brings the window, and the failure no longer shows.
	evs = tr.Line([]byte(`{"type":"rate_limit_event","rate_limit_info":{"status":"rejected","rateLimitType":"five_hour","resetsAt":1760000000}}`))
	if len(evs) != 1 || evs[0].Limits.Unavailable != "" || len(evs[0].Limits.Windows) != 1 || evs[0].Limits.Windows[0].UsedPct != 100 {
		t.Fatalf("after event %+v", evs[0].Limits)
	}
	// An answer Deck cannot read counts as a failure too, but known windows stay.
	evs = tr.Line([]byte(`{"type":"control_response","response":{"subtype":"success","request_id":"deck_get_usage","response":"weird"}}`))
	if len(evs) != 1 || evs[0].Limits.Unavailable != "" || len(evs[0].Limits.Windows) != 1 {
		t.Fatalf("unreadable answer %+v", evs[0].Limits)
	}
	// An unknown window type is kept under its own name.
	evs = tr.Line([]byte(`{"type":"rate_limit_event","rate_limit_info":{"rateLimitType":"thirty_day_fable","utilization":0.5}}`))
	w, ok := windowByID(evs[0].Limits, "thirty_day_fable")
	if !ok || w.Kind != agent.WindowOther || w.UsedPct != 50 {
		t.Errorf("unknown window %+v", evs[0].Limits)
	}
}

func TestUsageProbeLine(t *testing.T) {
	var req struct {
		Type      string `json:"type"`
		RequestID string `json:"request_id"`
		Request   struct {
			Subtype       string `json:"subtype"`
			SkipBehaviors bool   `json:"skip_behaviors"`
		} `json:"request"`
	}
	if err := json.Unmarshal(usageProbe(), &req); err != nil {
		t.Fatal(err)
	}
	if req.Type != "control_request" || req.RequestID != usageProbeID || req.Request.Subtype != "get_usage" || !req.Request.SkipBehaviors {
		t.Errorf("probe %+v", req)
	}
}

// TestProbeAnswerRealShape: a real get_usage answer mixes the windows with a
// limits list, spend, flags and null windows under rate_limits; the windows
// must still come through.
func TestProbeAnswerRealShape(t *testing.T) {
	tr := newTranslator("claude")
	line := `{"type":"control_response","response":{"subtype":"success","request_id":"deck_get_usage","response":{` +
		`"session":{"total_cost_usd":0,"model_usage":{}},"subscription_type":"max","rate_limits_available":true,"rate_limits":{` +
		`"five_hour":{"utilization":4,"resets_at":"2026-10-11T14:39:59.798808+00:00","limit_dollars":null,"locked_reason":null},` +
		`"seven_day":{"utilization":23,"resets_at":"2026-10-13T00:59:59.798833+00:00"},` +
		`"seven_day_opus":null,"tangelo":null,` +
		`"extra_usage":{"is_enabled":false,"utilization":null,"currency":null},` +
		`"limits":[{"kind":"session","group":"session","percent":4,"severity":"normal","resets_at":"2026-10-11T14:39:59.798808+00:00","scope":null,"is_active":false}],` +
		`"spend":{"used":{"amount_minor":0,"currency":"USD","exponent":2},"percent":0,"enabled":false},` +
		`"member_dashboard_available":false,` +
		`"seven_day_breakdown":{"as_of":"2026-10-11T13:08:14Z","rows":[{"key":"claude_code","percent":20}]}}}}}`
	evs := tr.Line([]byte(line))
	if len(evs) != 1 || evs[0].Kind != agent.KindLimits {
		t.Fatalf("events %+v", evs)
	}
	l := evs[0].Limits
	if l.Plan != "max" || l.Unavailable != "" || len(l.Windows) != 2 {
		t.Fatalf("limits %+v", l)
	}
	if w, _ := windowByID(l, "five_hour"); w.UsedPct != 4 || w.ResetsAt.IsZero() {
		t.Errorf("five_hour %+v", w)
	}
	if w, _ := windowByID(l, "seven_day"); w.UsedPct != 23 {
		t.Errorf("seven_day %+v", w)
	}
}
