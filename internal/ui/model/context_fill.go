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
// window. ok is false when neither is known. roster is the workspace's
// roster, nil when it has none.
func contextPercent(com *common.Common, roster workspace.Roster, sess *session.Session) (pct int, ok bool) {
	if sess == nil {
		return 0, false
	}
	if roster != nil {
		return rosterContextPercent(roster.Roster(sess.ID))
	}
	cfg := com.Config()
	if cfg == nil {
		return 0, false
	}
	agentCfg := cfg.Agents[config.AgentCoder]
	model := cfg.GetModelByType(agentCfg.Model)
	if model == nil || model.ContextWindow <= 0 {
		return 0, false
	}
	used := float64(sess.CompletionTokens + sess.PromptTokens)
	return int(used / float64(model.ContextWindow) * 100), true
}

// rosterContextPercent is the fullest context among the agents that have
// reported one.
func rosterContextPercent(lines []workspace.AgentLine) (pct int, ok bool) {
	for _, a := range lines {
		if a.ContextWindow <= 0 || a.ContextTokens <= 0 {
			continue
		}
		pct, ok = max(pct, a.ContextTokens*100/a.ContextWindow), true
	}
	return pct, ok
}
