package model

import (
	"github.com/dev-dami/deck/internal/config"
	"github.com/dev-dami/deck/internal/session"
	"github.com/dev-dami/deck/internal/ui/common"
	"github.com/dev-dami/deck/internal/workspace"
)

// contextPercent is how full the session's context is, in percent. With
// Deck's agents it is the fullest agent's, from the roster: the session's
// token counts add up every turn and say nothing about the context. Without
// a roster it is the session's tokens against the configured model's
// window. approx is true when the figure is Deck's estimate rather than a
// CLI's, ok false when neither is known. roster is the workspace's roster,
// nil when it has none.
func contextPercent(com *common.Common, roster workspace.Roster, sess *session.Session) (pct int, approx, ok bool) {
	if sess == nil {
		return 0, false, false
	}
	if roster != nil {
		return rosterContextPercent(roster.Roster(sess.ID))
	}
	cfg := com.Config()
	if cfg == nil {
		return 0, false, false
	}
	agentCfg := cfg.Agents[config.AgentCoder]
	model := cfg.GetModelByType(agentCfg.Model)
	if model == nil || model.ContextWindow <= 0 {
		return 0, false, false
	}
	used := float64(sess.CompletionTokens + sess.PromptTokens)
	return int(used / float64(model.ContextWindow) * 100), sess.EstimatedUsage, true
}

// rosterContextPercent is the fullest context among the agents that have
// reported one. An estimate (agent.ApproxContext) never sets it silently:
// approx is true when the fullest figure is one.
func rosterContextPercent(lines []workspace.AgentLine) (pct int, approx, ok bool) {
	for _, a := range lines {
		p, est, known := a.ContextPct()
		if !known {
			continue
		}
		if !ok || p > pct || (p == pct && approx && !est) {
			pct, approx, ok = p, est, true
		}
	}
	return pct, approx, ok
}
