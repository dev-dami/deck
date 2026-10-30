package session

import (
	"strings"
)

// Model slugs. A CLI takes an alias ("opus", "sonnet[1m]") or nothing for its
// default, and says which model that is only when a session starts (Claude
// Code's system/init names "claude-opus-5-5"). Deck remembers what each pick
// of each kind turned out to be, so the clients name every model by its slug,
// also for a member that has not started yet and in the model menus.

// Slug is a model name as the clients show it: the CLI's report without the
// annotations it adds ("claude-opus-5-5[1m]" is "claude-opus-5-5").
func Slug(model string) string {
	model = strings.TrimSpace(model)
	if i := strings.IndexByte(model, '['); i > 0 {
		model = model[:i]
	}
	return model
}

func slugKey(kind, pick string) string {
	return strings.ToLower(kind) + "\x00" + strings.ToLower(strings.TrimSpace(pick))
}

// learnSlug records that pick ("" for the CLI's default) of kind ran as model.
func (m *Manager) learnSlug(kind, pick, model string) {
	if model = Slug(model); model == "" {
		return
	}
	m.modelMu.Lock()
	defer m.modelMu.Unlock()
	if m.slugs == nil {
		m.slugs = map[string]string{}
	}
	m.slugs[slugKey(kind, pick)] = model
}

// ModelSlug is the slug pick ("" for the default) of kind runs as: what a
// member with that pick last reported, or "" when none has. A pick that is a
// slug already is its own answer once a member has run it.
func (m *Manager) ModelSlug(kind, pick string) string {
	m.modelMu.Lock()
	defer m.modelMu.Unlock()
	return m.slugs[slugKey(kind, pick)]
}

// runningSlug is the slug a member runs: what its CLI reported, else what its
// pick ran as before, else "".
func (m *Manager) runningSlug(a *agentState) string {
	a.mu.Lock()
	running, kind, pick := a.running, a.kind, a.model
	a.mu.Unlock()
	if running != "" {
		return Slug(running)
	}
	return m.ModelSlug(kind, pick)
}
