package model

import (
	"github.com/dev-dami/deck/internal/ui/decklogo"
	"github.com/dev-dami/deck/internal/workspace"
)

// sessionWindow is the plan window each CLI's card edge shows: its session
// window. A CLI missing here, or not reporting it, shows its fullest window.
var sessionWindow = map[string]string{
	"claude": "five_hour",
	"codex":  "primary",
}

// markLevels are the card edges of Deck's mark, one per agent kind: in the
// session, or before anyone joins, the agent CLIs that are installed. Each
// is filled by its plan's session window, or, for a CLI with no plan limits
// or none reported yet, by its fullest context. It reads memory only.
func (m *UI) markLevels() []decklogo.Edge {
	roster, ok := m.roster()
	if !ok {
		return nil
	}
	sessionID := ""
	if m.session != nil {
		sessionID = m.session.ID
	}
	var limits map[string]workspace.LiveLimits
	if r, ok := m.com.Workspace.(workspace.UsageReader); ok {
		limits = r.LiveLimits()
	}
	return markLevels(roster.Roster(sessionID), limits)
}

// markLevels is the edges of the agents in lines, by kind in the order
// they first appear, with what limits says of each kind.
func markLevels(lines []workspace.AgentLine, limits map[string]workspace.LiveLimits) []decklogo.Edge {
	var out []decklogo.Edge
	at := map[string]int{}
	for _, a := range lines {
		kind := a.Kind
		if kind == "" {
			kind = a.ID
		}
		i, seen := at[kind]
		if !seen {
			i = len(out)
			at[kind] = i
			out = append(out, decklogo.Edge{Kind: kind})
		}
		if a.ContextWindow <= 0 || a.ContextTokens <= 0 {
			continue
		}
		pct := float64(a.ContextTokens) * 100 / float64(a.ContextWindow)
		if e := &out[i]; !e.Known || pct > e.Pct {
			e.Pct, e.Known = pct, true
		}
	}
	for i := range out {
		if pct, ok := planPct(out[i].Kind, limits[out[i].Kind]); ok {
			out[i].Pct, out[i].Known = pct, true
		}
	}
	return out
}

// planPct is how much of a kind's plan session window is used, else of its
// fullest window. ok is false when the CLI reports no windows.
func planPct(kind string, l workspace.LiveLimits) (pct float64, ok bool) {
	want := sessionWindow[kind]
	for _, w := range l.Windows {
		if want != "" && w.ID == want {
			return w.UsedPct, true
		}
		if !ok || w.UsedPct > pct {
			pct, ok = w.UsedPct, true
		}
	}
	return pct, ok
}
