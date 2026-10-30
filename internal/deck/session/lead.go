package session

import (
	"cmp"
	"errors"
	"fmt"
	"strings"

	"github.com/dev-dami/deck/internal/deck/agent"
	"github.com/dev-dami/deck/internal/deck/profile"
)

// The lead (docs/design/lead-agent-and-subthreads.md). A thread has one agent
// the person talks to, picked in the clients' agent dropdown: every message
// of theirs goes to it, and "@name" in it is plain text. The lead delegates:
// subagents (agent.spawn), which report to it and cannot be talked to, and
// sub-threads (thread.open), which can. Switching the lead to another kind
// keeps the history: the new member is briefed on the thread so far.

var errNoLead = errors.New("every agent in this thread has exited; pick one in the agent menu")

// errSubagent refuses the person's message to a subagent: it works for the
// agent that started it.
func errSubagent(a *agentState) error {
	return fmt.Errorf("%s is a subagent: it reports to the agent that started it; ask that agent, or have it open a sub-thread you can talk to", a.getName())
}

// leadLocked is the thread's lead: the member it names while that member is
// live and no subagent; else the member who last answered the person; else
// the first live member that is no subagent. It records the choice, so a
// thread from before leads settles on one. t.mu is held.
func (t *thread) leadLocked() *agentState {
	ok := func(a *agentState) bool { return a != nil && !a.isEphemeral() && a.status() != StatusExited }
	if a := t.member(t.lead); ok(a) {
		return a
	}
	pick := t.member(t.lastSpeaker)
	if !ok(pick) {
		pick = nil
		for _, a := range t.members {
			if ok(a) {
				pick = a
				break
			}
		}
	}
	if pick != nil {
		t.lead = pick.id
	}
	return pick
}

// PostWith sends the person's message, with any images, to the thread's
// lead, starting one of the default agent when the thread has none yet. An
// image-only message (empty text) is sent as "See the attached image(s)."
func (m *Manager) PostWith(threadID, text string, images []agent.Image) (PostResult, error) {
	t := m.thread(threadID)
	if t == nil {
		return PostResult{}, fmt.Errorf("session: no thread %q", threadID)
	}
	text = strings.TrimSpace(text)
	if text == "" && len(images) > 0 {
		text = "See the attached image(s)."
	}
	if text == "" {
		return PostResult{}, errors.New("session: empty message")
	}
	m.wake(t)
	t.mu.Lock()
	lead := t.leadLocked()
	empty := len(t.members) == 0
	t.mu.Unlock()
	if lead == nil {
		if !empty {
			return PostResult{}, errNoLead
		}
		kind := m.defaultKind()
		if kind == "" {
			return PostResult{}, errors.New("session: no agent kinds available")
		}
		a, err := m.addMember(t, kind, "", "", profile.Profile{})
		if err != nil {
			return PostResult{}, err
		}
		t.mu.Lock()
		t.lead = a.id
		t.mu.Unlock()
		m.persistMeta(t)
		lead = a
	}
	return m.postTo(t, text, []*agentState{lead}, images)
}

// SetLead makes an agent of kind (an agent kind, or a named agent's
// profile) the thread's lead, with model and effort ("" keeps the current or
// default one). The lead's own kind changes in place; another kind takes
// over: an idle member of it is reused, else one is added, briefed on the
// thread so far with its first message. It returns the lead's member id.
func (m *Manager) SetLead(threadID, kind, model, effort string) (string, error) {
	t := m.thread(threadID)
	if t == nil {
		return "", fmt.Errorf("session: no thread %q", threadID)
	}
	kind = strings.TrimSpace(TrimSigil(kind))
	var prof profile.Profile
	if _, ok := m.adapters[strings.ToLower(kind)]; ok {
		kind = strings.ToLower(kind)
	} else {
		p, ok := profile.Find(m.allProfiles(t), kind)
		if !ok {
			return "", fmt.Errorf("session: no agent kind or named agent %q (kinds: %s)", kind, strings.Join(m.Kinds(), ", "))
		}
		prof, kind = p, p.Kind
	}
	same := func(a *agentState) bool {
		return a.kind == kind && strings.EqualFold(a.prof.Name, prof.Name)
	}
	t.mu.Lock()
	old := t.leadLocked()
	var next *agentState
	if old != nil && same(old) {
		next = old
	} else {
		for _, a := range t.members {
			if same(a) && !a.isEphemeral() && a.status() != StatusExited {
				next = a
				break
			}
		}
	}
	t.mu.Unlock()
	if next == nil {
		a, err := m.addMember(t, kind, model, "", prof)
		if err != nil {
			return "", err
		}
		next = a
	}
	t.mu.Lock()
	t.lead = next.id
	t.mu.Unlock()
	m.persistMeta(t)
	if old != nil && old != next {
		m.notice(t, fmt.Sprintf("%s took over from %s", next.getName(), old.getName()))
	}
	if model != "" || effort != "" {
		if err := m.setModelEffort(next, model, effort); err != nil {
			return next.id, err
		}
	}
	return next.id, nil
}

// setModelEffort sets what a member runs; "" keeps that part.
func (m *Manager) setModelEffort(a *agentState, model, effort string) error {
	a.mu.Lock()
	curModel, curEffort := a.model, a.effort
	a.mu.Unlock()
	if model != "" && model != curModel {
		if err := m.SetModel(a.id, model); err != nil {
			return err
		}
	}
	if effort = cmp.Or(effort, curEffort); effort != curEffort {
		return m.SetEffort(a.id, effort)
	}
	return nil
}

// allProfiles are the named agents a thread's lead can be.
func (m *Manager) allProfiles(t *thread) []profile.Profile {
	return m.profilesFor(t, "@")
}
