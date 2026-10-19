package workspace

import (
	"context"
	"strings"
	"time"

	decks "github.com/dev-dami/deck/internal/deck/session"
)

// EffortChoice is one reasoning effort a model takes.
type EffortChoice struct {
	Level       string
	Description string
}

// ModelChoice is one model an agent CLI offers, as the model picker shows it.
type ModelChoice struct {
	Kind        string // "claude", "codex", "opencode", "antigravity"
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
	// AgentModelChoices asks every installed CLI for its models (once per
	// run). It can take a few seconds: call it off the UI loop.
	AgentModelChoices(ctx context.Context, sessionID string) ([]ModelChoice, error)
	// CachedAgentModelChoices answers at once from what is known already.
	CachedAgentModelChoices(sessionID string) []ModelChoice
	// SetAgentModel makes model (and effort; "" = the model's default) what
	// agents of kind run: in the session now, and in new sessions.
	SetAgentModel(sessionID, kind, model, effort string) error
}

var _ AgentPicker = (*DeckWorkspace)(nil)

// AgentModelChoices implements [AgentPicker]. Each CLI is asked once; its
// list is kept for the rest of the run.
func (w *DeckWorkspace) AgentModelChoices(ctx context.Context, sessionID string) ([]ModelChoice, error) {
	var firstErr error
	for _, kind := range w.mgr().Kinds() {
		w.mu.Lock()
		_, have := w.models[kind]
		w.mu.Unlock()
		if have {
			continue
		}
		models, err := w.mgr().Models(ctx, kind)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		w.mu.Lock()
		w.models[kind] = models
		w.mu.Unlock()
	}
	out := w.CachedAgentModelChoices(sessionID)
	if len(out) == 0 && firstErr != nil {
		return nil, firstErr
	}
	return out, nil
}

// CachedAgentModelChoices is the model list as far as the CLIs have been
// asked already, without asking them: instant, for the $ popup. A CLI not
// asked yet is listed with just its current model.
func (w *DeckWorkspace) CachedAgentModelChoices(sessionID string) []ModelChoice {
	th, _ := w.threadOf(sessionID)
	cfg := w.DeckSettings()
	var out []ModelChoice
	for _, kind := range w.mgr().Kinds() {
		cur, curEffort := cfg.Models.For(kind), cfg.Efforts.For(kind)
		for _, a := range th.Members {
			if a.Kind == kind {
				cur, curEffort = a.Model, a.Effort
				break
			}
		}
		w.mu.Lock()
		models, have := w.models[kind]
		w.mu.Unlock()
		if !have {
			name := cur
			if name == "" {
				name = "default model"
			}
			out = append(out, ModelChoice{Kind: kind, ID: cur, Name: name, Current: true, CurrentEffort: curEffort})
			continue
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
	return out
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
	w.cfgMu.Lock()
	defer w.cfgMu.Unlock()
	cfg := w.eng.Config
	cfg.Models.Set(kind, model)
	cfg.Efforts.Set(kind, effort)
	w.eng.Config = cfg
	w.mgr().SetConfig(cfg)
	return saveDeckConfig(cfg)
}

// AgentLine is one agent as the sidebar shows it.
type AgentLine struct {
	ID, Kind      string
	Model, Effort string
	Status        string // "idle", "working", "tool", "approval", "error", "exited", "ready"
	ContextTokens int
	ContextWindow int
	CostUSD       float64
	Pending       int       // approvals waiting
	Since         time.Time // when its current turn began; zero when idle
	// Depth is how many subagent levels it is below the user's agents (0 for
	// those); Parent the name of the member that started it; Branch its own
	// worktree's branch, when it has one.
	Depth  int
	Parent string
	Branch string
	// Leases are the files of the shared tree it claimed (lease.acquire),
	// "(exclusive)" after the paths of an exclusive one, "; " between leases.
	// A string, not a slice: the agents pane compares lines with ==.
	Leases string
}

// Roster lists the agents in a session, or, before anyone has joined, the
// agent CLIs that are installed and what they would run.
type Roster interface {
	Roster(sessionID string) []AgentLine
}

var _ Roster = (*DeckWorkspace)(nil)

// Roster implements [Roster]. Members are listed as a tree: each subagent
// right under the member that started it. A subagent that has stopped is left
// out unless its worktree still holds work to apply or discard.
func (w *DeckWorkspace) Roster(sessionID string) []AgentLine {
	var out []AgentLine
	if th, ok := w.threadOf(sessionID); ok {
		names := map[string]string{}
		kids := map[string][]decks.AgentInfo{}
		leases := map[string]string{}
		for _, l := range w.mgr().Leases(th.ID) {
			s := strings.Join(l.Paths, ", ")
			if l.Exclusive {
				s += " (exclusive)"
			}
			if leases[l.Member] != "" {
				s = leases[l.Member] + "; " + s
			}
			leases[l.Member] = s
		}
		for _, a := range th.Members {
			names[a.ID] = a.Name
			if a.Status == decks.StatusExited && a.Ephemeral && a.Branch == "" {
				continue
			}
			parent := a.Parent
			if !a.Ephemeral {
				parent = ""
			}
			kids[parent] = append(kids[parent], a)
		}
		var walk func(parent string, depth int)
		walk = func(parent string, depth int) {
			for _, a := range kids[parent] {
				model := a.RunningModel
				if model == "" {
					model = a.Model
				}
				w.mu.Lock()
				since := w.since[a.ID]
				w.mu.Unlock()
				out = append(out, AgentLine{
					ID: a.Name, Kind: a.Kind, Model: model, Effort: a.Effort, Status: string(a.Status),
					ContextTokens: a.ContextTokens, ContextWindow: a.ContextWindow, CostUSD: a.CostUSD, Pending: a.Pending,
					Since: since, Depth: depth, Parent: names[a.Parent], Branch: a.Branch, Leases: leases[a.ID],
				})
				if depth < 16 {
					walk(a.ID, depth+1)
				}
			}
		}
		walk("", 0)
		// Live subagents of a parent that is not listed (it stopped and was
		// left out) go last, in join order.
		shown := map[string]bool{}
		for _, l := range out {
			shown[l.ID] = true
		}
		for _, a := range th.Members {
			if !shown[a.Name] && len(kids[a.ID]) > 0 && !shown[kids[a.ID][0].Name] {
				walk(a.ID, 1)
			}
		}
	}
	if len(out) > 0 {
		return out
	}
	cfg := w.DeckSettings()
	for _, kind := range w.mgr().Kinds() {
		out = append(out, AgentLine{
			ID: kind, Kind: kind, Status: "ready",
			Model: cfg.Models.For(kind), Effort: cfg.Efforts.For(kind),
		})
	}
	return out
}
