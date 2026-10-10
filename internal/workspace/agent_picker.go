package workspace

import (
	"context"

	deckconfig "github.com/dev-dami/deck/internal/deck/config"
)

// EffortChoice is one reasoning effort a model takes.
type EffortChoice struct {
	Level       string
	Description string
}

// ModelChoice is one model an agent CLI offers, as the model picker shows it.
type ModelChoice struct {
	Kind        string // "claude", "codex", "opencode"
	ID          string // passed to the CLI; "" = the CLI's default
	Name        string
	Description string
	Default     bool // the CLI's own default
	Current     bool // what the session's agent of this kind runs now

	Efforts       []EffortChoice
	DefaultEffort string
	CurrentEffort string // set on the Current choice
}

// AgentPicker lists the models of the user's agent CLIs and switches them.
// The UI type-asserts its Workspace to it.
type AgentPicker interface {
	// AgentModelChoices asks every installed CLI for its models. It can
	// take a few seconds: call it off the UI loop.
	AgentModelChoices(ctx context.Context, sessionID string) ([]ModelChoice, error)
	// SetAgentModel makes model (and effort; "" = the model's default) what
	// agents of kind run: in the session now, and in new sessions.
	SetAgentModel(sessionID, kind, model, effort string) error
}

var _ AgentPicker = (*DeckWorkspace)(nil)

// AgentModelChoices implements [AgentPicker].
func (w *DeckWorkspace) AgentModelChoices(ctx context.Context, sessionID string) ([]ModelChoice, error) {
	th, _ := w.threadOf(sessionID)
	var out []ModelChoice
	var firstErr error
	for _, kind := range w.mgr().Kinds() {
		models, err := w.mgr().Models(ctx, kind)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		cur, curEffort := w.eng.Config.Models.For(kind), w.eng.Config.Efforts.For(kind)
		for _, a := range th.Members {
			if a.Kind == kind {
				cur, curEffort = a.Model, a.Effort
				break
			}
		}
		for _, mi := range models {
			c := ModelChoice{
				Kind:          kind,
				ID:            mi.ID,
				Name:          mi.Name,
				Description:   mi.Description,
				Default:       mi.Default,
				DefaultEffort: mi.DefaultEffort,
			}
			for _, e := range mi.Efforts {
				c.Efforts = append(c.Efforts, EffortChoice{Level: e.Level, Description: e.Description})
			}
			if mi.ID == cur || (cur == "" && mi.Default) {
				c.Current, c.CurrentEffort = true, curEffort
			}
			out = append(out, c)
		}
	}
	if len(out) == 0 && firstErr != nil {
		return nil, firstErr
	}
	return out, nil
}

// SetAgentModel implements [AgentPicker].
func (w *DeckWorkspace) SetAgentModel(sessionID, kind, model, effort string) error {
	if th, ok := w.threadOf(sessionID); ok {
		for _, a := range th.Members {
			if a.Kind != kind {
				continue
			}
			if err := w.mgr().SetModel(a.ID, model); err != nil {
				return err
			}
			if err := w.mgr().SetEffort(a.ID, effort); err != nil {
				return err
			}
		}
	}
	w.mgr().SetDefaultModel(kind, model)
	w.mgr().SetDefaultEffort(kind, effort)
	cfg := w.eng.Config
	cfg.Models.Set(kind, model)
	cfg.Efforts.Set(kind, effort)
	w.eng.Config = cfg
	w.mgr().SetConfig(cfg)
	return deckconfig.Save(cfg)
}

// AgentLine is one agent as the sidebar shows it.
type AgentLine struct {
	ID, Kind      string
	Model, Effort string
	Status        string // "idle", "working", "tool", "approval", "error", "exited", "ready"
	ContextTokens int
	ContextWindow int
	CostUSD       float64
	Pending       int // approvals waiting
}

// Roster lists the agents in a session, or, before anyone has joined, the
// agent CLIs that are installed and what they would run.
type Roster interface {
	Roster(sessionID string) []AgentLine
}

var _ Roster = (*DeckWorkspace)(nil)

// Roster implements [Roster].
func (w *DeckWorkspace) Roster(sessionID string) []AgentLine {
	var out []AgentLine
	if th, ok := w.threadOf(sessionID); ok {
		for _, a := range th.Members {
			model := a.RunningModel
			if model == "" {
				model = a.Model
			}
			out = append(out, AgentLine{
				ID: a.Name, Kind: a.Kind, Model: model, Effort: a.Effort, Status: string(a.Status),
				ContextTokens: a.ContextTokens, ContextWindow: a.ContextWindow, CostUSD: a.CostUSD, Pending: a.Pending,
			})
		}
	}
	if len(out) > 0 {
		return out
	}
	for _, kind := range w.mgr().Kinds() {
		out = append(out, AgentLine{
			ID: kind, Kind: kind, Status: "ready",
			Model: w.eng.Config.Models.For(kind), Effort: w.eng.Config.Efforts.For(kind),
		})
	}
	return out
}
