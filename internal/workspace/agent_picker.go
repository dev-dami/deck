package workspace

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	deckagent "github.com/dev-dami/deck/internal/deck/agent"
	deckconfig "github.com/dev-dami/deck/internal/deck/config"
	decks "github.com/dev-dami/deck/internal/deck/session"
)

// EffortChoice is one reasoning effort a model takes.
type EffortChoice struct {
	Level       string
	Description string
}

// ModelChoice is one model an agent CLI offers, as the model picker shows it.
type ModelChoice struct {
	Kind string // "claude", "codex", "opencode", "antigravity"
	ID   string // passed to the CLI; "" = the CLI's default
	// Name is the model's slug ("claude-opus-5-5") once a run of ID has
	// said what it is (session/slugs.go); before that ID, or the CLI's own
	// name for its default.
	Name        string
	Description string
	Default     bool // the CLI's own default
	// Current marks what a pick for the session would change: for the
	// lead's kind the lead's model, for another kind the agent that would
	// take over, else what a new agent of the kind runs.
	Current bool
	Video   bool // the model takes video (agent.ModelInfo.Video)

	Efforts       []EffortChoice
	DefaultEffort string
	CurrentEffort string // set on the Current choice
}

// SpecResult is an agent's model and effort after SetMemberSpec, with where
// each came from and what the engine changed on its own.
type SpecResult = decks.SpecResult

// LeadResult says who leads a session after SetLead.
type LeadResult struct {
	Agent string // the lead's name in the session ("claude", "charles-2")
	Kind  string
	// Changed is true when another agent, or none, led before.
	Changed bool
}

// AgentPicker lists the models of the user's agent CLIs and sets what agents
// run. A pick for a session changes that session only: SetDefaults, which
// only Settings calls, is the one way to change what new sessions start
// with. The UI type-asserts its Workspace to it.
type AgentPicker interface {
	// AgentModelChoices waits for the CLIs not listed yet, all at once and
	// each for at most its own timeout (a CLI that failed lately is not
	// asked again), and returns every model. Call it off the UI loop.
	AgentModelChoices(ctx context.Context, sessionID string) ([]ModelChoice, error)
	// CachedAgentModelChoices answers at once from what is known already,
	// without asking the CLIs. A kind not listed yet shows its current
	// model alone.
	CachedAgentModelChoices(sessionID string) []ModelChoice
	// SetLead makes agent (a kind or a named agent) the session's lead,
	// handing the session over when another agent leads. It changes no
	// model or effort.
	SetLead(sessionID, agent string) (LeadResult, error)
	// SetMemberSpec sets the model and effort of one agent of the session,
	// named as the roster names it. A nil value is left as it is; "" is
	// the CLI's default model, or the model's default effort.
	SetMemberSpec(sessionID, member string, model, effort *string) (SpecResult, error)
	// SetDefaults sets the model and effort new agents of kind start with,
	// as SetMemberSpec reads its values, and saves the settings. Only
	// Settings calls it.
	SetDefaults(kind string, model, effort *string) error
	// SetAgentModel is the one-call pick of older clients: agent (a kind or
	// a named agent) leads the session, running model at effort. It changes
	// the session only.
	SetAgentModel(sessionID, agent, model, effort string) error
}

var _ AgentPicker = (*DeckWorkspace)(nil)

// ModelRefresher is an AgentPicker whose model lists can be asked for again.
type ModelRefresher interface {
	// RefreshAgentModels forgets every CLI's model list, so the next
	// AgentModelChoices asks them all afresh: after an install, an upgrade or
	// a login changed what a CLI offers.
	RefreshAgentModels()
}

var _ ModelRefresher = (*DeckWorkspace)(nil)

// RefreshAgentModels implements [ModelRefresher].
func (w *DeckWorkspace) RefreshAgentModels() {
	w.mgr().ForgetModels()
}

// AgentModelChoices implements [AgentPicker].
func (w *DeckWorkspace) AgentModelChoices(ctx context.Context, sessionID string) ([]ModelChoice, error) {
	cat := w.mgr().WaitCatalog(ctx)
	out := w.modelChoices(sessionID, cat)
	listed := false
	var errs []string
	for _, kc := range cat {
		listed = listed || len(kc.Models) > 0
		if kc.Err != "" {
			errs = append(errs, kc.Kind+": "+kc.Err)
		}
	}
	if !listed && len(errs) > 0 {
		return out, errors.New("no agent CLI listed its models (" + strings.Join(errs, "; ") + ")")
	}
	return out, nil
}

// CachedAgentModelChoices implements [AgentPicker].
func (w *DeckWorkspace) CachedAgentModelChoices(sessionID string) []ModelChoice {
	return w.modelChoices(sessionID, w.mgr().Catalog())
}

// modelChoices lists the catalog's models with the session's current ones
// marked.
func (w *DeckWorkspace) modelChoices(sessionID string, cat []decks.KindCatalog) []ModelChoice {
	th, have := w.threadOf(sessionID)
	cfg := w.DeckSettings()
	var out []ModelChoice
	for _, kc := range cat {
		kind := kc.Kind
		cur, curEffort := currentSpec(th, have, cfg, kind)
		if len(kc.Models) == 0 {
			name := cmp.Or(w.mgr().ModelSlug(kind, cur), cur, "default model")
			c := ModelChoice{Kind: kind, ID: cur, Name: name, Current: true, CurrentEffort: curEffort}
			if kc.Err != "" {
				c.Description = "can't list models: " + kc.Err
			}
			out = append(out, c)
			continue
		}
		for _, mi := range kc.Models {
			c := ModelChoice{
				Kind:          kind,
				ID:            mi.ID,
				Name:          cmp.Or(mi.Slug, mi.ID, mi.Name),
				Description:   mi.Description,
				Default:       mi.Default,
				DefaultEffort: mi.DefaultEffort,
				Video:         mi.Video,
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

// currentSpec is the model and effort a pick of kind would start from in
// the thread: the lead's when it is an agent of kind (not a named one);
// else the agent of kind SetLead would hand the thread to; else what a new
// agent of kind runs.
func currentSpec(th decks.ThreadInfo, have bool, cfg deckconfig.Config, kind string) (model, effort string) {
	if have {
		for _, a := range th.Members {
			if a.ID == th.Lead && a.Kind == kind && a.Profile == "" {
				return a.Model, a.Effort
			}
		}
		for _, a := range th.Members {
			if a.Kind == kind && a.Profile == "" && !a.Ephemeral && a.Status != decks.StatusExited {
				return a.Model, a.Effort
			}
		}
	}
	return cfg.Models.For(kind), cfg.Efforts.For(kind)
}

// errNoSession refuses a pick that names no session: a pick is for one
// thread, and defaults change only in Settings.
var errNoSession = errors.New("pick an agent in a session; the defaults for new sessions are in Settings")

// SetLead implements [AgentPicker].
func (w *DeckWorkspace) SetLead(sessionID, agent string) (LeadResult, error) {
	if sessionID == "" {
		return LeadResult{}, errNoSession
	}
	tid, err := w.thread(sessionID)
	if err != nil {
		return LeadResult{}, err
	}
	res, err := w.mgr().SetLead(tid, agent)
	if err != nil {
		return LeadResult{}, err
	}
	out := LeadResult{Agent: res.AgentID, Changed: res.Changed}
	if a, ok := w.mgr().Agent(res.AgentID); ok {
		out.Agent, out.Kind = a.Name, a.Kind
	}
	return out, nil
}

// SetMemberSpec implements [AgentPicker].
func (w *DeckWorkspace) SetMemberSpec(sessionID, member string, model, effort *string) (SpecResult, error) {
	if sessionID == "" {
		return SpecResult{}, errNoSession
	}
	tid, err := w.thread(sessionID)
	if err != nil {
		return SpecResult{}, err
	}
	return w.mgr().SetMemberSpec(tid, member, model, effort)
}

// SetDefaults implements [AgentPicker]. An effort the model does not take
// is refused; a new model that does not take the kind's effort resets it
// to the model's default.
func (w *DeckWorkspace) SetDefaults(kind string, model, effort *string) error {
	if !slices.Contains(w.mgr().Kinds(), kind) {
		return fmt.Errorf("no agent kind %q", kind)
	}
	cur := w.DeckSettings()
	m, e := cur.Models.For(kind), cur.Efforts.For(kind)
	if model != nil {
		m = strings.TrimSpace(*model)
	}
	if effort != nil {
		e = strings.ToLower(strings.TrimSpace(*effort))
		if err := w.mgr().CheckEffort(kind, m, e); err != nil {
			return err
		}
	} else if w.mgr().CheckEffort(kind, m, e) != nil {
		e = ""
	}
	return w.SetDeckSettings(func(c *deckconfig.Config) {
		c.Models.Set(kind, m)
		c.Efforts.Set(kind, e)
	})
}

// SetAgentModel implements [AgentPicker]: SetLead, then SetMemberSpec on the
// lead. When the pick hands the session to another agent and names neither
// a model nor an effort, the agent keeps its own (its profile's, or the
// defaults).
func (w *DeckWorkspace) SetAgentModel(sessionID, agent, model, effort string) error {
	lead, err := w.SetLead(sessionID, agent)
	if err != nil {
		return err
	}
	if lead.Changed && model == "" && effort == "" {
		return nil
	}
	_, err = w.SetMemberSpec(sessionID, lead.Agent, &model, &effort)
	return err
}

// AgentLine is one agent as the sidebar shows it.
type AgentLine struct {
	ID, Kind      string
	Model, Effort string
	Status        string // "idle", "working", "tool", "approval", "restarting", "error", "exited", "ready"
	ContextTokens int
	ContextWindow int
	// ContextSource is where the two came from (agent.ContextCLI, ...).
	ContextSource string
	// ContextBaseline of ContextTokens is what the agent's session started
	// with and DeckTokens what Deck's own text takes (estimates; 0 = not
	// known).
	ContextBaseline int
	DeckTokens      int
	CostUSD         float64
	Pending         int       // approvals waiting
	Since           time.Time // when its current turn began; zero when idle
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
	// Profile is the named agent it was started from ("" for none) and Role
	// that profile's one-line role; ID is then the profile's name
	// ("charles", "charles-2"). Color is the profile's CSS colour, "" for
	// the kind's hue.
	Profile, Role, Color string
}

// ContextPct is how full the agent's context is, in percent. approx is true
// when the figure is Deck's estimate or assumption rather than the CLI's
// (the UI marks it "~"), and ok false when it is not known.
func (l AgentLine) ContextPct() (pct int, approx, ok bool) {
	if l.ContextWindow <= 0 || l.ContextTokens <= 0 {
		return 0, false, false
	}
	return l.ContextTokens * 100 / l.ContextWindow, deckagent.ApproxContext(l.ContextSource), true
}

// ContextParts splits the agent's context into what its session started
// with, the conversation, Deck's own text and what is free, in tokens. The
// parts add up to the window; baseline and deck are 0 when not known, and
// ok is false when the fill or the window is not known.
func (l AgentLine) ContextParts() (baseline, conversation, deck, free int, ok bool) {
	if l.ContextWindow <= 0 || l.ContextTokens <= 0 {
		return 0, 0, 0, 0, false
	}
	used := min(l.ContextTokens, l.ContextWindow)
	baseline = min(max(0, l.ContextBaseline), used)
	deck = min(max(0, l.DeckTokens), used-baseline)
	return baseline, used - baseline - deck, deck, l.ContextWindow - used, true
}

// Roster lists the agents in a session, or, before anyone has joined, the
// agent CLIs that are installed and what they would run.
type Roster interface {
	Roster(sessionID string) []AgentLine
}

var _ Roster = (*DeckWorkspace)(nil)

// AgentKind is an installed agent CLI as a session sees it: the model a new
// member of it would run, and whether one is in the session already.
type AgentKind struct {
	Kind, Model string
	Joined      bool
}

// AgentAdder adds agents to a session from outside the TUI (the phone).
type AgentAdder interface {
	JoinableKinds(sessionID string) []AgentKind
	// AddAgent starts an agent of kind in the session without sending it
	// anything and returns its roster id. An empty model means the one
	// configured for the kind.
	AddAgent(sessionID, kind, model string) (string, error)
}

var _ AgentAdder = (*DeckWorkspace)(nil)

// JoinableKinds implements [AgentAdder].
func (w *DeckWorkspace) JoinableKinds(sessionID string) []AgentKind {
	th, _ := w.threadOf(sessionID)
	cfg := w.DeckSettings()
	var out []AgentKind
	for _, kind := range w.mgr().Kinds() {
		k := AgentKind{Kind: kind, Model: cfg.Models.For(kind)}
		for _, a := range th.Members {
			if a.Kind == kind && !a.Ephemeral && a.Status != decks.StatusExited {
				k.Joined = true
			}
		}
		out = append(out, k)
	}
	return out
}

// AddAgent implements [AgentAdder]. It opens the session's thread first when
// nobody has spoken in it yet.
func (w *DeckWorkspace) AddAgent(sessionID, kind, model string) (string, error) {
	tid, err := w.thread(sessionID)
	if err != nil {
		return "", err
	}
	id, err := w.mgr().AddMember(tid, kind, model)
	if err != nil {
		return "", err
	}
	// The roster names members by their name in the thread, not their id.
	if th, ok := w.mgr().Thread(tid); ok {
		for _, a := range th.Members {
			if a.ID == id {
				return a.Name, nil
			}
		}
	}
	return id, nil
}

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
					ContextTokens: a.ContextTokens, ContextWindow: a.ContextWindow, ContextSource: a.ContextSource,
					ContextBaseline: a.ContextBaseline, DeckTokens: a.DeckTokens,
					CostUSD: a.CostUSD, Pending: a.Pending,
					Since: since, Depth: depth, Parent: names[a.Parent], Branch: a.Branch, Leases: leases[a.ID],
					Profile: a.Profile, Role: a.Role, Color: a.Color,
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
