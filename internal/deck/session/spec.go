package session

import (
	"cmp"
	"fmt"
	"strings"
)

// A member's spec: the model and effort it runs, and where each came from
// (docs/design/v3/03-agents-and-delegation.md §3). Three operations change
// it, each with its own scope: SetLead picks who leads a thread, SetMemberSpec
// sets one member's model and effort in its thread, and the defaults change
// only from Settings (the workspace's SetDefaults). Nothing that picks for
// one thread writes the config. An empty model is the CLI's own default and
// an empty effort the model's, wherever they appear.

// SpecSource says where a member's model or effort came from.
type SpecSource string

const (
	SourceCLI        SpecSource = "cli"        // nothing set it: the CLI's own default
	SourceDefaults   SpecSource = "defaults"   // Settings: the kind's default
	SourceProfile    SpecSource = "profile"    // the named agent's profile
	SourceParent     SpecSource = "parent"     // inherited from the member that delegated
	SourceDelegation SpecSource = "delegation" // set on the spawn, the task or the sub-thread
	SourceThread     SpecSource = "thread"     // picked in this thread (SetMemberSpec, AddMember)
)

// SpecResult is a member's spec after a change.
type SpecResult struct {
	AgentID, Name, Kind   string
	Model, Effort         string
	ModelFrom, EffortFrom SpecSource
	// Warnings say what the engine changed on its own, such as an effort
	// the new model does not take.
	Warnings []string
}

// specResult is a's spec now.
func (a *agentState) specResult() SpecResult {
	a.mu.Lock()
	defer a.mu.Unlock()
	return SpecResult{
		AgentID: a.id, Name: a.name, Kind: a.kind, Model: a.model, Effort: a.effort,
		ModelFrom: a.modelFrom, EffortFrom: a.effortFrom,
	}
}

// SetMemberSpec sets the model and effort of one member of a thread, named
// by its id or its name in the thread. A nil value is left as it is; ""
// is the CLI's default model, or the model's default effort. It never
// changes the lead, adds a member or writes the config. An effort the
// model does not take is refused; a new model that does not take the
// member's effort resets it to the model's default, with a warning.
func (m *Manager) SetMemberSpec(threadID, member string, model, effort *string) (SpecResult, error) {
	t := m.thread(threadID)
	if t == nil {
		return SpecResult{}, fmt.Errorf("session: no thread %q", threadID)
	}
	a := t.findMember(member)
	if a == nil {
		return SpecResult{}, fmt.Errorf("session: no agent %q in this thread", member)
	}
	if a.status() == StatusExited {
		return SpecResult{}, fmt.Errorf("session: %s has exited", a.getName())
	}
	a.mu.Lock()
	kind, curModel, curEffort := a.kind, a.model, a.effort
	a.mu.Unlock()
	newModel := curModel
	if model != nil {
		newModel = strings.TrimSpace(*model)
	}
	newEffort, warn := curEffort, ""
	if effort != nil {
		newEffort = strings.ToLower(strings.TrimSpace(*effort))
		if err := m.checkEffort(kind, newModel, newEffort); err != nil {
			return SpecResult{}, err
		}
	} else if m.checkEffort(kind, newModel, curEffort) != nil {
		newEffort = ""
		warn = fmt.Sprintf("%s isn't an effort %s takes; it runs at the model's default", curEffort, m.modelLabel(kind, newModel))
	}
	if model != nil {
		if newModel != curModel {
			if err := m.SetModel(a.id, newModel); err != nil {
				return SpecResult{}, err
			}
		}
		a.mu.Lock()
		a.modelFrom = SourceThread
		a.mu.Unlock()
	}
	if newEffort != curEffort {
		if err := m.SetEffort(a.id, newEffort); err != nil {
			return SpecResult{}, err
		}
	}
	if effort != nil || warn != "" {
		a.mu.Lock()
		a.effortFrom = SourceThread
		a.mu.Unlock()
	}
	m.persistMeta(t)
	res := a.specResult()
	if warn != "" {
		res.Warnings = append(res.Warnings, warn)
		m.notice(t, a.getName()+": "+warn)
	}
	return res, nil
}

// findMember is the thread's member with id or, failing that, name.
func (t *thread) findMember(idOrName string) *agentState {
	t.mu.Lock()
	defer t.mu.Unlock()
	if a := t.member(idOrName); a != nil {
		return a
	}
	for _, a := range t.members {
		if strings.EqualFold(a.getName(), idOrName) {
			return a
		}
	}
	return nil
}

// checkEffort refuses an effort that kind's model does not take, as far as
// the kept model list says: a model the list does not name, or a list not
// loaded yet, takes any effort (the CLI then decides). "" is always taken.
func (m *Manager) checkEffort(kind, model, effort string) error {
	if effort == "" {
		return nil
	}
	mi, ok := m.listedModel(kind, model)
	if !ok || len(mi.Efforts) == 0 {
		return nil
	}
	levels := make([]string, 0, len(mi.Efforts))
	for _, e := range mi.Efforts {
		if strings.EqualFold(e.Level, effort) {
			return nil
		}
		levels = append(levels, e.Level)
	}
	return fmt.Errorf("%s isn't an effort %s takes (it takes %s)", effort, m.modelLabel(kind, model), strings.Join(levels, ", "))
}

// CheckEffort is checkEffort for the workspace's SetDefaults.
func (m *Manager) CheckEffort(kind, model, effort string) error {
	return m.checkEffort(kind, model, strings.ToLower(strings.TrimSpace(effort)))
}

// modelLabel names kind's model for a person.
func (m *Manager) modelLabel(kind, model string) string {
	if model == "" {
		return kind + "'s default model"
	}
	return cmp.Or(m.ModelSlug(kind, model), model)
}

// inherit gives a delegate a, made by addMember and not started yet, the
// model and effort of the member from that delegated to it, where neither
// the delegation nor a's profile set them: the model when both run the same
// kind, the effort when a's model takes it. It records that they were
// inherited and returns a warning when the effort could not be.
func (m *Manager) inherit(a, from *agentState) string {
	if from == nil {
		return ""
	}
	from.mu.Lock()
	pKind, pModel, pEffort := from.kind, from.model, from.effort
	from.mu.Unlock()
	a.mu.Lock()
	defer a.mu.Unlock()
	own := func(s SpecSource) bool { return s == SourceDelegation || s == SourceProfile }
	if !own(a.modelFrom) && a.kind == pKind {
		a.model, a.modelFrom = pModel, SourceParent
		a.running = m.ModelSlug(a.kind, pModel)
	}
	if own(a.effortFrom) || pEffort == "" {
		return ""
	}
	if a.kind != pKind {
		mi, known := m.listedModel(a.kind, a.model)
		if !known || len(mi.Efforts) == 0 {
			return "" // whether another CLI takes the effort is not known
		}
		if m.checkEffort(a.kind, a.model, pEffort) != nil {
			return fmt.Sprintf("%s runs at its model's default effort: %s isn't one %s takes", a.name, pEffort, m.modelLabel(a.kind, a.model))
		}
	}
	a.effort, a.effortFrom = pEffort, SourceParent
	return ""
}

// specFrom is where a value set as v came from when it was not picked:
// from (the profile or the defaults) when v is set, else the CLI.
func specFrom(v string, from SpecSource) SpecSource {
	if v == "" {
		return SourceCLI
	}
	return from
}

// storedSource is the source a stored member records, or, for a member
// stored before sources were, the one its value implies.
func storedSource(stored, v string) SpecSource {
	if stored != "" {
		return SpecSource(stored)
	}
	return specFrom(v, SourceDefaults)
}
