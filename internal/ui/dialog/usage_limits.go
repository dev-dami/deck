package dialog

import (
	"slices"
	"sort"
	"time"

	deckagent "github.com/dev-dami/deck/internal/deck/agent"
	"github.com/dev-dami/deck/internal/usage"
	"github.com/dev-dami/deck/internal/workspace"
)

// limitsNeverReported are the agent CLIs that report no plan limits Deck
// may read (see their adapters), so the view says so before they first run.
var limitsNeverReported = map[string]bool{"opencode": true, "antigravity": true, "pi": true}

// PlanWindow is one plan window as the Usage view and the Usage pane show
// it.
type PlanWindow struct {
	Kind, ID, Label string
	UsedPct         float64 // 0-100
	ResetsAt        time.Time
	Mins            int // the window's length; 0 when not known
}

// Name is the window's label, or its id when it has none.
func (w PlanWindow) Name() string {
	if w.Label != "" {
		return w.Label
	}
	return w.ID
}

// AgentLimits is one agent kind's plan and its windows.
type AgentLimits struct {
	Kind, Plan  string
	Unavailable string // "", deckagent.LimitsUnsupported or LimitsProbeFailed
	Windows     []PlanWindow
}

// Unsupported reports whether the agent's CLI reports no plan limits at all.
func (a AgentLimits) Unsupported() bool {
	return len(a.Windows) == 0 && a.Unavailable == deckagent.LimitsUnsupported
}

// MergeLimits is each agent kind's plan limits: the live ones (fresher)
// merged window by window over the newest stored ones. Kinds come in the
// order given, then any others with limits by name. The shortest window
// comes first; one of unknown length last.
func MergeLimits(kinds []string, stored []usage.LimitSnapshot, live map[string]workspace.LiveLimits) []AgentLimits {
	byKind := map[string][]usage.LimitSnapshot{}
	for _, s := range stored {
		byKind[s.AgentKind] = append(byKind[s.AgentKind], s)
	}
	kinds = slices.Clone(kinds)
	var extra []string
	for k := range byKind {
		if !slices.Contains(kinds, k) && !slices.Contains(extra, k) {
			extra = append(extra, k)
		}
	}
	for k := range live {
		if !slices.Contains(kinds, k) && !slices.Contains(extra, k) {
			extra = append(extra, k)
		}
	}
	sort.Strings(extra)
	kinds = append(kinds, extra...)

	out := make([]AgentLimits, 0, len(kinds))
	for _, k := range kinds {
		a := AgentLimits{Kind: k}
		byID := map[string]int{}
		put := func(w PlanWindow) {
			if i, ok := byID[w.ID]; ok {
				a.Windows[i] = w
				return
			}
			byID[w.ID] = len(a.Windows)
			a.Windows = append(a.Windows, w)
		}
		for _, s := range byKind[k] {
			if s.Plan != "" {
				a.Plan = s.Plan
			}
			put(PlanWindow{Kind: k, ID: s.WindowID, Label: s.Label, UsedPct: s.UsedPct, ResetsAt: s.ResetsAt, Mins: s.DurationMins})
		}
		if l, ok := live[k]; ok {
			if l.Plan != "" {
				a.Plan = l.Plan
			}
			a.Unavailable = l.Unavailable
			for _, w := range l.Windows {
				put(PlanWindow{Kind: k, ID: w.ID, Label: w.Label, UsedPct: w.UsedPct, ResetsAt: w.ResetsAt, Mins: w.DurationMins})
			}
		}
		if len(a.Windows) == 0 && a.Unavailable == "" && limitsNeverReported[k] {
			a.Unavailable = deckagent.LimitsUnsupported
		}
		sort.SliceStable(a.Windows, func(i, j int) bool {
			wi, wj := a.Windows[i].Mins, a.Windows[j].Mins
			if (wi == 0) != (wj == 0) {
				return wj == 0
			}
			return wi < wj
		})
		out = append(out, a)
	}
	return out
}

// LimitsText is what to say for an agent with no window to show: that its
// CLI does not report limits, or that they have not come in yet.
func LimitsText(a AgentLimits) string {
	if a.Unsupported() {
		return "not reported by " + a.Kind
	}
	return "no limits reported yet; they arrive with " + a.Kind + "'s next turn"
}
