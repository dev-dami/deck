package session

import (
	"github.com/dev-dami/deck/internal/deck/config"
	"github.com/dev-dami/deck/internal/deck/hub"
)

// Context budgets (docs/context-design.md 4.3): how much of the thread Deck
// puts in one prompt and how long one search or execute result may be, as
// shares of the member's context window. The fixed budgets Deck had before
// (a 40,000-character briefing, 6,000 per message, 20 KB per result) were
// set for windows of 128k-200k tokens; with the default percentages they
// still hold there, grow with larger windows up to a ceiling (a 1M window
// must not make every briefing ten times larger: cost and cache churn), and
// shrink for smaller ones down to a floor.
const (
	// baseWindow is the window the fixed budgets were set for. A member
	// whose window is not known yet is budgeted as if it had this one, which
	// gives exactly the fixed budgets.
	baseWindow = 128_000
	// charsPerToken is the rough size of a token, for budgets in characters.
	charsPerToken = 4

	defaultBriefingPct = 4.0
	minBriefingChars   = 16_000
	maxBriefingCap     = 160_000

	defaultToolOutputPct = 1.5
	minToolOutput        = 8 << 10

	// fullCtxPct is how full a context must be for the briefing to be
	// halved: what the member already holds matters more than the detail of
	// what it missed, which thread.read still shows.
	fullCtxPct = 80
)

// ctxBudget is one member's budgets for a prompt or a tool result.
type ctxBudget struct {
	briefing int // characters (runes) of briefing kept, from its end
	entry    int // characters of one briefed message
	read     int // characters thread.read keeps before the output cap
	output   int // bytes of one search or execute result (hub.Env.MaxOutput)
}

// budgetFor is the budget of a member whose model has a window of window
// tokens (0 = not known) of which used are filled, under the context config c.
func budgetFor(window, used int, c config.Context) ctxBudget {
	known := window > 0
	if !known {
		window = baseWindow
	}
	// The fixed budgets, scaled down for a window smaller than baseWindow:
	// with a default percentage they are the least a member gets.
	scale := min(1, float64(window)/baseWindow)
	share := func(pct float64) float64 { return pct / 100 * float64(window) * charsPerToken }

	brief := share(c.BriefingPct)
	if c.BriefingPct == 0 {
		brief = max(share(defaultBriefingPct), maxBriefingChars*scale)
	}
	out := share(c.ToolOutputPct)
	if c.ToolOutputPct == 0 {
		out = max(share(defaultToolOutputPct), hub.MaxOutput*scale)
	}
	b := ctxBudget{
		briefing: clampInt(int(brief), minBriefingChars, maxBriefingCap),
		output:   clampInt(int(out), minToolOutput, hub.MaxOutputCap),
	}
	b.read = b.briefing
	if known && used*100 > window*fullCtxPct {
		b.briefing /= 2
	}
	// One message may take as much of a briefing as it did before: 6,000
	// of 40,000 characters.
	b.entry = b.briefing * maxEntryChars / maxBriefingChars
	return b
}

func clampInt(v, lo, hi int) int { return max(lo, min(v, hi)) }

// budget is a's current budget. The window is the one its CLI reported,
// else the one its adapter lists for its model (only when the model list is
// already loaded: this never asks the CLI), else unknown.
func (m *Manager) budget(a *agentState) ctxBudget {
	a.mu.Lock()
	window, used, kind, model := a.ctxWindow, a.ctxTokens, a.kind, a.model
	a.mu.Unlock()
	if window == 0 {
		window = m.listedWindow(kind, model)
	}
	return budgetFor(window, used, m.config().Context)
}

// listedWindow is the context window kind's model list gives model, 0 when
// the list is not loaded or does not say.
func (m *Manager) listedWindow(kind, model string) int {
	m.modelMu.Lock()
	defer m.modelMu.Unlock()
	for _, mi := range m.modelCache[kind] {
		if mi.ID == model || (model == "" && mi.Default) {
			return mi.ContextWindow
		}
	}
	return 0
}
