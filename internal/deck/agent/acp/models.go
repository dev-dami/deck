package acp

import (
	"context"
	"os"
	"strings"
	"time"

	"github.com/dev-dami/deck/internal/deck/agent"
)

// modelListTimeout bounds the whole listing; effortBudget is how long of it
// may go to asking each model for its efforts.
var (
	modelListTimeout = 30 * time.Second
	effortBudget     = 12 * time.Second
)

// Models lists every model the agent offers, across all the providers it is
// configured for, as the "model" option of a throwaway session. Each model is
// then selected once in that session to read its "thought_level" option, as
// long as the budget lasts. The session is deleted afterwards when the agent
// allows it. A successful result is cached for the adapter's lifetime, a
// failure for modelsRetryAfter. Concurrent callers share one listing, and one
// that gives up does not stop it.
func (a *Adapter) Models(ctx context.Context) ([]agent.ModelInfo, error) {
	return a.models.Get(ctx, func(ctx context.Context) ([]agent.ModelInfo, error) {
		ctx, cancel := context.WithTimeout(ctx, modelListTimeout)
		defer cancel()
		return a.listModels(ctx)
	})
}

// ForgetModels implements agent.ModelForgetter.
func (a *Adapter) ForgetModels() { a.models.Reset() }

// modelsRetryAfter is how long a failed listing is answered from memory
// rather than by starting the agent again.
var modelsRetryAfter = 10 * time.Second

func (a *Adapter) listModels(ctx context.Context) ([]agent.ModelInfo, error) {
	p, err := a.startProc(ctx, "", os.TempDir(), nil)
	if err != nil {
		return nil, err
	}
	defer p.stop()
	go func() { // nothing here needs the session's updates
		for range p.client.Incoming() {
		}
	}()
	var res sessionResult
	if err := p.client.Call(ctx, methodSessionNew, sessionParams{Cwd: os.TempDir(), MCPServers: []mcpServer{}}, &res); err != nil {
		return nil, p.withTail(a.errorf("session/new: %w", err))
	}
	if p.caps.AgentCapabilities.SessionCapabilities.Delete != nil {
		defer func() {
			dctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = p.client.Call(dctx, methodSessionDelete, map[string]string{"sessionId": res.SessionID}, nil)
		}()
	}
	out := modelsOf(res.ConfigOptions)
	mopt, _ := findOption(res.ConfigOptions, catModel)
	deadline := time.Now().Add(effortBudget)
	for i := range out {
		if time.Now().After(deadline) || ctx.Err() != nil {
			break
		}
		var set setOptionResult
		err := p.client.Call(ctx, methodSessionSetOpt, setOptionParams{SessionID: res.SessionID, ConfigID: mopt.ID, Value: out[i].ID}, &set)
		if err != nil {
			continue
		}
		a.effortsOf(&out[i], set.ConfigOptions)
	}
	return out, nil
}

// effortsOf fills in m's efforts from the session's options with m selected.
// The agent's "default" value is not a level; a model whose only level is
// "off" (Pi's for a model that does not reason) has none.
func (a *Adapter) effortsOf(m *agent.ModelInfo, opts []configOption) {
	o, ok := findOption(opts, catEffort)
	if !ok {
		return
	}
	var levels []agent.EffortInfo
	for _, v := range o.Options {
		if v.Value == effortDefault || v.Value == "" {
			continue
		}
		level := a.fromAgentEffort(v.Value)
		desc := v.Description
		if desc == "" {
			desc = effortDescriptions[level]
		}
		levels = append(levels, agent.EffortInfo{Level: level, Description: desc})
	}
	if len(levels) == 1 && levels[0].Level == a.fromAgentEffort("off") {
		return
	}
	m.Efforts = levels
	if o.CurrentValue != effortDefault {
		m.DefaultEffort = a.fromAgentEffort(o.CurrentValue)
	}
}

// effortDescriptions describe the levels an agent lists without one.
var effortDescriptions = map[string]string{
	"none":    "No extra reasoning",
	"off":     "No extra reasoning",
	"minimal": "The least reasoning, fastest",
	"low":     "Quick answers, light reasoning",
	"medium":  "Balanced speed and reasoning depth",
	"high":    "Deeper reasoning for complex work",
	"xhigh":   "Extra reasoning depth for hard problems",
	"max":     "The most reasoning, slowest",
}

// modelsOf reads the models out of a session's options: "provider/model"
// ids, named after the model, described by their provider. The session's
// current model is the agent's default.
func modelsOf(opts []configOption) []agent.ModelInfo {
	o, ok := findOption(opts, catModel)
	if !ok {
		return nil
	}
	var out []agent.ModelInfo
	for _, v := range o.Options {
		if v.Value == "" {
			continue
		}
		provider, _, _ := strings.Cut(v.Value, "/")
		name := v.Name
		if p, n, ok := strings.Cut(name, "/"); ok && p == provider {
			name = n
		}
		desc := v.Description
		if desc == "" {
			desc = provider
		}
		out = append(out, agent.ModelInfo{ID: v.Value, Name: name, Description: desc, Default: v.Value == o.CurrentValue})
	}
	return out
}
