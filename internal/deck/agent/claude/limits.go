package claude

import (
	"encoding/json"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/dev-dami/deck/internal/deck/agent"
)

// Claude Code reports the plan's windows two ways:
//
//   - rate_limit_event lines in the output stream, one window at a time:
//     {"type":"rate_limit_event","rate_limit_info":{"rateLimitType":"five_hour",
//     "utilization":0.42,"resetsAt":1760000000,"status":"allowed",...}}
//     utilization is a 0-1 fraction and resetsAt epoch seconds;
//   - the get_usage control request, which Deck sends once per run. Its answer
//     carries every window at once, on a 0-100 scale with ISO reset times:
//     {"type":"control_response","response":{"subtype":"success","request_id":
//     ...,"response":{"rate_limits":{"five_hour":{"utilization":42,
//     "resets_at":"2026-10-11T18:00:00Z"},...},"subscription_type":"max"}}}
//
// get_usage is experimental: any error or unexpected answer leaves Deck with
// the events only. Only the windows and the plan name are read; nothing about
// the account (its email included) is.

// usageProbeID is the request id of Deck's get_usage control request.
const usageProbeID = "deck_get_usage"

// usageProbe is the get_usage control request line.
func usageProbe() []byte {
	b, _ := json.Marshal(map[string]any{
		"type":       "control_request",
		"request_id": usageProbeID,
		"request":    map[string]any{"subtype": "get_usage", "skip_behaviors": true},
	})
	return b
}

// windowInfo describes a Claude rate limit type as a window.
type windowInfo struct {
	kind  string
	label string
	mins  int
	order int
}

var claudeWindows = map[string]windowInfo{
	"five_hour":        {agent.WindowSession, "5h", 300, 0},
	"seven_day":        {agent.WindowWeekly, "Weekly", 10080, 1},
	"seven_day_opus":   {agent.WindowWeekly, "Weekly · Opus", 10080, 2},
	"seven_day_sonnet": {agent.WindowWeekly, "Weekly · Sonnet", 10080, 3},
	"overage":          {agent.WindowOther, "Extra usage", 0, 4},
}

// window builds the window of a rate limit type; an unknown type keeps its
// name as id and label.
func window(id string) agent.LimitWindow {
	if wi, ok := claudeWindows[id]; ok {
		return agent.LimitWindow{ID: id, Kind: wi.kind, Label: wi.label, DurationMins: wi.mins}
	}
	return agent.LimitWindow{ID: id, Kind: agent.WindowOther, Label: strings.ReplaceAll(id, "_", " ")}
}

func windowOrder(id string) int {
	if wi, ok := claudeWindows[id]; ok {
		return wi.order
	}
	return len(claudeWindows)
}

// rateLimitLine is a rate_limit_event line.
type rateLimitLine struct {
	Info struct {
		Status        string   `json:"status"` // allowed | allowed_warning | rejected
		ResetsAt      *float64 `json:"resetsAt"`
		RateLimitType string   `json:"rateLimitType"`
		Utilization   *float64 `json:"utilization"`
	} `json:"rate_limit_info"`
}

// rateLimit folds a rate_limit_event line into the known windows. It reports
// false when the line says nothing usable.
func (t *translator) rateLimit(line []byte) bool {
	var rl rateLimitLine
	if json.Unmarshal(line, &rl) != nil || rl.Info.RateLimitType == "" {
		return false
	}
	var used float64
	switch {
	case rl.Info.Utilization != nil:
		used = *rl.Info.Utilization * 100
	case rl.Info.Status == "rejected":
		used = 100
	default:
		return false // no figure to show
	}
	w := window(rl.Info.RateLimitType)
	w.UsedPct = clampPct(used)
	if rl.Info.ResetsAt != nil && *rl.Info.ResetsAt > 0 {
		w.ResetsAt = time.Unix(int64(*rl.Info.ResetsAt), 0)
	}
	if rl.Info.Status == "rejected" && rl.Info.RateLimitType != "overage" {
		t.rejected = &agent.LimitHit{Window: w.ID, ResetsAt: w.ResetsAt}
	}
	t.limits[w.ID] = w
	t.limitsFailed = false
	return true
}

// controlResponse is a control_response line.
type controlResponse struct {
	Response struct {
		Subtype   string          `json:"subtype"` // success | error
		RequestID string          `json:"request_id"`
		Response  json.RawMessage `json:"response"`
	} `json:"response"`
}

// usageAnswer is the part of a get_usage answer Deck reads. rate_limits mixes
// the windows (five_hour, seven_day, ...) with other kinds of entry (a limits
// list, spend, flags), so each entry is decoded on its own and anything that is
// not a window is skipped.
type usageAnswer struct {
	RateLimits       map[string]json.RawMessage `json:"rate_limits"`
	SubscriptionType string                     `json:"subscription_type"`
}

// usageWindow is one window in a get_usage answer.
type usageWindow struct {
	Utilization *float64        `json:"utilization"` // 0–100
	ResetsAt    json.RawMessage `json:"resets_at"`
}

// probeAnswer handles the answer to Deck's get_usage request. ok is false when
// the line is not that answer. Otherwise the windows it carries are taken in;
// a failed probe is noted and only shows when nothing else is known.
func (t *translator) probeAnswer(line []byte) (ok bool) {
	var cr controlResponse
	if json.Unmarshal(line, &cr) != nil || cr.Response.RequestID != usageProbeID {
		return false
	}
	var ans usageAnswer
	if cr.Response.Subtype != "success" || json.Unmarshal(cr.Response.Response, &ans) != nil {
		t.limitsFailed = true
		return true
	}
	n := 0
	for id, raw := range ans.RateLimits {
		var rl usageWindow
		if json.Unmarshal(raw, &rl) != nil || rl.Utilization == nil {
			continue
		}
		w := window(id)
		w.UsedPct = clampPct(*rl.Utilization)
		w.ResetsAt = parseReset(rl.ResetsAt)
		t.limits[id] = w
		n++
	}
	if ans.SubscriptionType != "" {
		t.plan = ans.SubscriptionType
	}
	if n == 0 && t.plan == "" {
		t.limitsFailed = true
	} else {
		t.limitsFailed = false
	}
	return true
}

// limitsEvent is the KindLimits event for what is known now.
func (t *translator) limitsEvent(mk func(agent.Kind) agent.Event) []agent.Event {
	l := &agent.Limits{Plan: t.plan}
	for _, w := range t.limits {
		l.Windows = append(l.Windows, w)
	}
	sort.Slice(l.Windows, func(i, j int) bool {
		oi, oj := windowOrder(l.Windows[i].ID), windowOrder(l.Windows[j].ID)
		if oi != oj {
			return oi < oj
		}
		return l.Windows[i].ID < l.Windows[j].ID
	})
	if len(l.Windows) == 0 && t.limitsFailed {
		l.Unavailable = agent.LimitsProbeFailed
	}
	ev := mk(agent.KindLimits)
	ev.Raw = nil // a control response may carry more about the account than the windows
	ev.Limits = l
	return []agent.Event{ev}
}

// parseReset reads a reset time given as an ISO string or epoch seconds.
func parseReset(raw json.RawMessage) time.Time {
	var s string
	if json.Unmarshal(raw, &s) == nil && s != "" {
		if ts, err := time.Parse(time.RFC3339Nano, s); err == nil {
			return ts
		}
		if n, err := strconv.ParseFloat(s, 64); err == nil && n > 0 {
			return time.Unix(int64(n), 0)
		}
		return time.Time{}
	}
	var n float64
	if json.Unmarshal(raw, &n) == nil && n > 0 {
		return time.Unix(int64(n), 0)
	}
	return time.Time{}
}

func clampPct(p float64) float64 {
	return min(100, max(0, p))
}

// limitHit says whether the turn that ends with a result line of text ran
// out of plan: the text says so, or the turn failed after a rate_limit_event
// of it said "rejected" (one that went on regardless, on another model, did
// not run out). Claude Code writes "5-hour limit reached ∙ resets 3pm", or,
// in older versions, "Claude AI usage limit reached|<epoch>". The event's
// reset time is exact and wins over one read from the text.
func (t *translator) limitHit(text string, failed bool) *agent.LimitHit {
	rej := t.rejected
	t.rejected = nil
	hit := agent.ParseLimitHit(text, t.now())
	if rej == nil || !failed && hit == nil {
		return hit
	}
	if rej.ResetsAt.IsZero() && hit != nil {
		rej.ResetsAt = hit.ResetsAt
	}
	return rej
}
