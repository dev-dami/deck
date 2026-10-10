package session

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/dev-dami/deck/internal/agent"
	"github.com/dev-dami/deck/internal/config"
)

// planDenyReason is what a plan-mode denial tells the agent.
const planDenyReason = "Deck is in plan mode: propose the change instead of making it."

// NextMode is the mode shift+tab switches to: plan -> ask -> yolo -> plan. An
// unknown mode goes to ask.
func NextMode(cur agent.Mode) agent.Mode {
	for i, m := range agent.Modes {
		if m == cur {
			return agent.Modes[(i+1)%len(agent.Modes)]
		}
	}
	return agent.ModeAsk
}

func validMode(m agent.Mode) bool {
	for _, o := range agent.Modes {
		if o == m {
			return true
		}
	}
	return false
}

// startMode is the mode new threads start in. The config's default_mode is
// honoured except yolo, which must be chosen during the session.
func startMode(cfg config.Config) (mode agent.Mode, ignoredYolo bool) {
	s, ignored := cfg.StartMode()
	return agent.Mode(s), ignored
}

func yoloIgnoredNotice() NoticeMsg {
	return NoticeMsg{Text: "default_mode yolo ignored at startup; press shift+tab to switch"}
}

// Mode returns a thread's permission mode. An unknown thread reports the mode a
// new thread would start in (config default_mode, never yolo).
func (m *Manager) Mode(threadID string) agent.Mode {
	if t := m.thread(threadID); t != nil {
		return t.getMode()
	}
	mode, _ := startMode(m.config())
	return mode
}

func (t *thread) getMode() agent.Mode {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.mode
}

// SetMode switches a thread's permission mode. The mode is stored on the thread
// (and persisted), handed to every live member whose run implements
// agent.ModeSetter (a member that does not simply gets it from Deck's own
// enforcement, see below, and as its Spec.Mode if it is started later), and
// announced with a ModeChangedMsg. Setting the mode the thread already has does
// nothing.
//
// Deck enforces the mode itself regardless of what the adapter supports. In
// yolo every approval request is answered Allow at once and reported as an
// AutoApprovedMsg instead of an ApprovalMsg; switching into yolo answers the
// requests already waiting the same way (ApprovalGoneMsg, then AutoApprovedMsg
// for each). In plan, edit and command requests are denied and reported as an
// AutoDeniedMsg; other kinds still ask. Each member's next prompt starts with
// "[deck] Permission mode is now <mode>." once.
func (m *Manager) SetMode(threadID string, mode agent.Mode) error {
	if !validMode(mode) {
		return fmt.Errorf("session: unknown mode %q", mode)
	}
	t := m.thread(threadID)
	if t == nil {
		return fmt.Errorf("session: no thread %q", threadID)
	}
	if m.isClosed() {
		return errClosed
	}
	t.mu.Lock()
	prev := t.mode
	if prev == mode {
		t.mu.Unlock()
		return nil
	}
	t.mode = mode
	members := append([]*agentState(nil), t.members...)
	stored := len(t.log) > 0
	t.mu.Unlock()
	if stored {
		m.persistMeta(t)
	}
	m.emit(ModeChangedMsg{ThreadID: t.id, Mode: mode, Prev: prev})

	for _, a := range members {
		if a.status() == StatusExited {
			continue
		}
		run := a.getRun()
		if run == nil {
			continue // starts later with Spec.Mode
		}
		if ms, ok := run.(agent.ModeSetter); ok {
			if err := ms.SetMode(mode); err != nil {
				m.emit(NoticeMsg{ThreadID: t.id, Text: fmt.Sprintf("%s could not switch to %s mode (%v); Deck enforces it anyway", a.getName(), mode, err)})
			}
		}
	}
	if mode == agent.ModeYolo {
		for _, a := range members {
			m.drainPending(t, a)
		}
	}
	return nil
}

// drainPending answers every approval a member has waiting with Allow.
func (m *Manager) drainPending(t *thread, a *agentState) {
	for _, p := range a.takePending() {
		p.respond(agent.Decision{Allow: true})
		m.emit(ApprovalGoneMsg{ThreadID: t.id, AgentID: a.id, ID: p.id})
		m.emit(AutoApprovedMsg{ThreadID: t.id, AgentID: a.id, Summary: p.summary})
	}
}

// enforce applies the thread's mode to an approval request the member just
// raised (it is already in a's pending list). It reports whether Deck answered
// the request itself.
func (m *Manager) enforce(t *thread, a *agentState, req agent.ApprovalRequest) bool {
	mode := t.getMode()
	var d agent.Decision
	switch {
	case mode == agent.ModeYolo:
		d = agent.Decision{Allow: true}
	case mode == agent.ModePlan && (req.Kind == agent.ApproveEdit || req.Kind == agent.ApproveCommand):
		d = agent.Decision{Allow: false, Reason: planDenyReason}
	default:
		return false
	}
	// Whoever removes the request from the pending list answers it; a concurrent
	// SetMode(yolo) may have done so already.
	if !a.claimPending(req.ID) {
		return true
	}
	req.Respond(d)
	if d.Allow {
		m.emit(AutoApprovedMsg{ThreadID: t.id, AgentID: a.id, Summary: summaryOf(req)})
	} else {
		m.emit(AutoDeniedMsg{ThreadID: t.id, AgentID: a.id, Summary: summaryOf(req)})
	}
	return true
}

func summaryOf(req agent.ApprovalRequest) string {
	if s := strings.TrimSpace(req.Summary); s != "" {
		return s
	}
	return req.ToolName
}

// modeLine is the one line that tells a member the mode changed.
func modeLine(mode agent.Mode) string {
	s := fmt.Sprintf("[deck] Permission mode is now %s.", mode)
	if mode == agent.ModePlan {
		s += " Investigate and propose; don't change files."
	}
	return s
}

// claimPending removes the pending approval id and reports whether it was there.
func (a *agentState) claimPending(id string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	for i, p := range a.pending {
		if p.id == id {
			a.pending = append(a.pending[:i], a.pending[i+1:]...)
			return true
		}
	}
	return false
}

// ---- models ----

// SetDefaultModel sets the model members of kind get when they are started from
// now on without an explicit one. It lives in memory only; config.Models stays
// the persisted default. An empty model means the CLI's own default.
func (m *Manager) SetDefaultModel(kind, model string) {
	m.modelMu.Lock()
	if m.defModels == nil {
		m.defModels = map[string]string{}
	}
	m.defModels[kind] = model
	m.modelMu.Unlock()
}

// defaultModel is the model a new member of kind starts with.
func (m *Manager) defaultModel(cfg config.Config, kind string) string {
	m.modelMu.Lock()
	model, ok := m.defModels[kind]
	m.modelMu.Unlock()
	if ok {
		return model
	}
	return modelFor(cfg, kind)
}

// Models lists the models the kind's CLI offers, through its adapter when it
// implements agent.ModelLister. A successful answer is cached per kind for the
// life of the Manager. An adapter that cannot list models yields nil, nil.
func (m *Manager) Models(ctx context.Context, kind string) ([]agent.ModelInfo, error) {
	ad, ok := m.adapters[kind]
	if !ok {
		return nil, fmt.Errorf("session: unknown agent kind %q", kind)
	}
	m.modelMu.Lock()
	cached, ok := m.modelCache[kind]
	m.modelMu.Unlock()
	if ok {
		return append([]agent.ModelInfo(nil), cached...), nil
	}
	ml, ok := ad.(agent.ModelLister)
	if !ok {
		return nil, nil
	}
	list, err := ml.Models(ctx)
	if err != nil {
		return nil, err
	}
	m.modelMu.Lock()
	if m.modelCache == nil {
		m.modelCache = map[string][]agent.ModelInfo{}
	}
	m.modelCache[kind] = append([]agent.ModelInfo(nil), list...)
	m.modelMu.Unlock()
	return list, nil
}

// SetModel switches a member to another model ("" = the CLI's default). The run
// must implement agent.ModelSetter; otherwise the error says to start a new
// agent. A member whose process has not started yet simply starts with the
// model. The choice is persisted with the member and announced with a
// ModelChangedMsg.
func (m *Manager) SetModel(agentID, model string) error {
	a := m.get(agentID)
	if a == nil {
		return fmt.Errorf("session: no agent %q", agentID)
	}
	if a.status() == StatusExited {
		return fmt.Errorf("session: %s has exited", agentID)
	}
	model = strings.TrimSpace(model)
	if run := a.getRun(); run != nil {
		ms, ok := run.(agent.ModelSetter)
		if !ok {
			return errors.New("this agent can't switch model mid-session; start a new one")
		}
		if err := ms.SetModel(model); err != nil {
			return err
		}
	}
	a.mu.Lock()
	a.model = model
	a.mu.Unlock()
	m.persistMeta(a.thread)
	m.emit(ModelChangedMsg{ThreadID: a.thread.id, AgentID: a.id, Model: model, Effort: a.getEffort()})
	return nil
}

// SetEffort switches a member's reasoning effort ("" = the model's default).
// The run must implement agent.EffortSetter; a member whose process has not
// started yet simply starts with it. The choice is persisted with the member
// and announced with a ModelChangedMsg.
func (m *Manager) SetEffort(agentID, effort string) error {
	a := m.get(agentID)
	if a == nil {
		return fmt.Errorf("session: no agent %q", agentID)
	}
	if a.status() == StatusExited {
		return fmt.Errorf("session: %s has exited", agentID)
	}
	effort = strings.ToLower(strings.TrimSpace(effort))
	if run := a.getRun(); run != nil {
		es, ok := run.(agent.EffortSetter)
		if !ok {
			return errors.New("this agent can't change effort mid-session; start a new one")
		}
		if err := es.SetEffort(effort); err != nil {
			return err
		}
	}
	a.mu.Lock()
	a.effort = effort
	model := a.model
	a.mu.Unlock()
	m.persistMeta(a.thread)
	m.emit(ModelChangedMsg{ThreadID: a.thread.id, AgentID: a.id, Model: model, Effort: effort})
	return nil
}

func (a *agentState) getEffort() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.effort
}

// SetDefaultEffort sets the effort members of kind get when they are started
// from now on ("" = the model's default). Like SetDefaultModel it lives in
// memory; config.Efforts stays the persisted default.
func (m *Manager) SetDefaultEffort(kind, effort string) {
	m.modelMu.Lock()
	if m.defEfforts == nil {
		m.defEfforts = map[string]string{}
	}
	m.defEfforts[kind] = effort
	m.modelMu.Unlock()
}

// defaultEffort is the effort a new member of kind starts with.
func (m *Manager) defaultEffort(cfg config.Config, kind string) string {
	m.modelMu.Lock()
	e, ok := m.defEfforts[kind]
	m.modelMu.Unlock()
	if ok {
		return e
	}
	switch kind {
	case "claude":
		return cfg.Efforts.Claude
	case "codex":
		return cfg.Efforts.Codex
	}
	return ""
}
