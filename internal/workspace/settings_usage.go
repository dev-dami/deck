package workspace

import (
	"context"
	"log/slog"
	"time"

	deckagent "github.com/dev-dami/deck/internal/deck/agent"
	deckconfig "github.com/dev-dami/deck/internal/deck/config"
	"github.com/dev-dami/deck/internal/usage"
)

// UsageReport is what the agents used over a range of days: totals, per
// day, per agent kind, per model and the costliest sessions.
type UsageReport = usage.Report

// usageTopSessions is how many sessions a usage report lists.
const usageTopSessions = 5

// UsageReader reads the local record of what the agents used. The UI
// type-asserts its Workspace to it.
type UsageReader interface {
	// Usage sums the turns of the last sinceDays days, today included
	// (1 = today; 0 = all time). It reads the database: call it off the UI
	// loop.
	Usage(ctx context.Context, sinceDays int) (UsageReport, error)
}

// SettingsEditor reads and changes Deck's own settings (~/.deck/config.json).
// The UI type-asserts its Workspace to it.
type SettingsEditor interface {
	// DeckSettings is a copy of the settings in effect.
	DeckSettings() deckconfig.Config
	// SetDeckSettings applies edit to a copy of the settings, validates it,
	// puts it in effect and saves it. It writes a file: call it off the UI
	// loop.
	SetDeckSettings(edit func(*deckconfig.Config)) error
	// AgentKinds are the agent CLIs Deck found, in the order it offers them.
	AgentKinds() []string
}

var (
	_ UsageReader    = (*DeckWorkspace)(nil)
	_ SettingsEditor = (*DeckWorkspace)(nil)
)

// saveDeckConfig writes the settings; tests stub it so they never touch the
// user's ~/.deck.
var saveDeckConfig = deckconfig.Save

// Usage implements [UsageReader].
func (w *DeckWorkspace) Usage(ctx context.Context, sinceDays int) (UsageReport, error) {
	if w.AppWorkspace == nil || w.app == nil || w.app.Usage == nil {
		return UsageReport{}, nil
	}
	return w.app.Usage.Report(ctx, usage.Since(sinceDays, time.Now()), usageTopSessions)
}

// DeckSettings implements [SettingsEditor].
func (w *DeckWorkspace) DeckSettings() deckconfig.Config {
	w.cfgMu.Lock()
	defer w.cfgMu.Unlock()
	return w.eng.Config
}

// SetDeckSettings implements [SettingsEditor]. Like [DeckWorkspace.SetAgentModel]
// it updates the engine's copy, hands it to the manager and saves it.
func (w *DeckWorkspace) SetDeckSettings(edit func(*deckconfig.Config)) error {
	w.cfgMu.Lock()
	defer w.cfgMu.Unlock()
	cfg := w.eng.Config
	edit(&cfg)
	if err := cfg.Validate(); err != nil {
		return err
	}
	if err := saveDeckConfig(cfg); err != nil {
		return err
	}
	w.eng.Config = cfg
	w.mgr().SetConfig(cfg)
	return nil
}

// AgentKinds implements [SettingsEditor].
func (w *DeckWorkspace) AgentKinds() []string { return w.mgr().Kinds() }

// recordUsage writes one finished turn to the local usage table, under the
// agent's kind and the model it ran.
func (w *DeckWorkspace) recordUsage(ctx context.Context, sid, agentID string, u deckagent.Usage, took time.Duration) {
	if w.AppWorkspace == nil || w.app == nil || w.app.Usage == nil {
		return
	}
	kind, model := agentID, ""
	if a, ok := w.mgr().Agent(agentID); ok {
		kind, model = a.Kind, a.RunningModel
		if model == "" {
			model = a.Model
		}
	}
	err := w.app.Usage.Record(ctx, usage.Record{
		SessionID:    sid,
		AgentID:      agentID,
		Kind:         kind,
		Model:        model,
		InputTokens:  u.InputTokens,
		OutputTokens: u.OutputTokens,
		CachedTokens: u.CachedTokens,
		CostUSD:      u.CostUSD,
		Duration:     took,
	})
	if err != nil {
		slog.Warn("deck: record usage", "error", err)
	}
}
