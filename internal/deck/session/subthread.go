package session

import (
	"cmp"
	"errors"
	"fmt"
	"strings"

	"github.com/dev-dami/deck/internal/deck/profile"
	"github.com/dev-dami/deck/internal/deck/store"
)

// Sub-threads (docs/design/lead-agent-and-subthreads.md). A thread's lead can
// open a thread of its own for a piece of work: it has its own lead, the
// person can open it and talk to that agent, and the clients list it under
// its parent. Unlike a subagent it stays: it is a conversation, not a call.
//
// The parent agent's brief (thread.open) and follow-ups (thread.post) are
// posted into the sub-thread under that agent's name; each is an ask. When the
// sub-thread's lead ends a turn that answered an ask, its final reply goes to
// the parent agent as a relay with via "subthread", like a subagent's report,
// and is batched with them (batch.go).
// What the person says in the sub-thread stays there.
//
// A sub-thread costs its parent: opening one and each follow-up count a turn
// against the parent thread's turn budget (chargeTurnLocked), and each report
// starts a turn of the parent agent, which counts too. So a lead cannot loop
// through its sub-threads; the level caps how many it may open
// (Level.MaxSubthreads).

const viaSubthread = "subthread"

// ViaSubthread is the Via of a sub-thread's report to its parent agent.
const ViaSubthread = viaSubthread

// SubThreadMsg says an agent opened a sub-thread: the clients list it under
// its parent.
type SubThreadMsg struct {
	ThreadID, ParentID, Title string
}

var errNotLead = errors.New("only a thread's lead opens or writes to sub-threads; a subagent reports to the agent that started it")

// openSubThread is thread.open for member from of thread parent: a new thread
// in the same folder, led by an agent of kind (from's kind by default), with
// brief as its first message. It returns the sub-thread's id.
func (m *Manager) openSubThread(parent *thread, from *agentState, title, brief, kind, model, effort string) (string, error) {
	brief = strings.TrimSpace(brief)
	if brief == "" {
		return "", errors.New("brief is required: what the sub-thread's agent should do")
	}
	if from.isEphemeral() {
		return "", errNotLead
	}
	kind, prof, err := m.leadKind(parent, cmp.Or(strings.TrimSpace(kind), from.kind))
	if err != nil {
		return "", err
	}
	parent.mu.Lock()
	lv := m.levelLocked(parent)
	if lv.AskFirst && from.promptUser == 0 {
		parent.mu.Unlock()
		return "", fmt.Errorf("orchestration is off: open a sub-thread only in a turn where the user asked you to")
	}
	parent.mu.Unlock()
	if n := len(m.childrenOf(parent.id, from.id)); lv.MaxSubthreads > 0 && n >= lv.MaxSubthreads {
		return "", fmt.Errorf("you have %d sub-threads, the most this thread's level allows; thread.post to one of them instead", n)
	}
	if err := m.chargeAsk(parent); err != nil {
		return "", err
	}
	if title = strings.TrimSpace(title); title == "" {
		title = placeholderTitle(brief)
	}
	t, err := m.newSubThread(parent, from.id, title, kind, model, effort, prof)
	if err != nil {
		return "", err
	}
	id := t.id
	parent.mu.Lock()
	ptitle := parent.title
	parent.mu.Unlock()
	intro := fmt.Sprintf("[deck] %s, the agent of the thread %q, opened this sub-thread for you. Your final reply to this goes back to it; the user may also talk to you here.", from.getName(), clip(ptitle, 80))
	if err := m.askSub(t, from, intro+"\n\n"+brief); err != nil {
		return id, err
	}
	return id, nil
}

// newSubThread makes a sub-thread of parent in its folder and mode, opened by
// member agentID ("" for the person), led by an agent of kind, and announces
// it to the clients.
func (m *Manager) newSubThread(parent *thread, agentID, title, kind, model, effort string, prof profile.Profile) (*thread, error) {
	parent.mu.Lock()
	dir, mode := parent.dir, parent.mode
	parent.mu.Unlock()
	id, err := m.NewThread(dir)
	if err != nil {
		return nil, err
	}
	t := m.thread(id)
	t.mu.Lock()
	t.parent, t.parentAgent = parent.id, agentID
	t.title, t.mode = title, mode
	t.mu.Unlock()
	lead, err := m.addMember(t, kind, model, "", prof)
	if err != nil {
		return nil, err
	}
	t.mu.Lock()
	t.lead = lead.id
	t.mu.Unlock()
	if effort != "" {
		_ = m.SetEffort(lead.id, effort)
	}
	m.persistMeta(t)
	m.emit(SubThreadMsg{ThreadID: id, ParentID: parent.id, Title: title})
	return t, nil
}

// OpenSubThread is the person opening a sub-thread of thread parentID (the
// desktop's side chat): a thread of its own under the parent, led by an
// agent of kind (the parent's lead's by default), that nobody reports from.
// It returns the sub-thread's id.
func (m *Manager) OpenSubThread(parentID, title, kind string) (string, error) {
	parent := m.thread(parentID)
	if parent == nil {
		return "", fmt.Errorf("session: no thread %q", parentID)
	}
	if strings.TrimSpace(kind) == "" {
		parent.mu.Lock()
		if a := parent.leadLocked(); a != nil {
			kind = a.kind
		}
		parent.mu.Unlock()
	}
	kind, prof, err := m.leadKind(parent, cmp.Or(strings.TrimSpace(kind), m.defaultKind()))
	if err != nil {
		return "", fmt.Errorf("session: %w", err)
	}
	t, err := m.newSubThread(parent, "", cmp.Or(strings.TrimSpace(title), "Side thread"), kind, "", "", prof)
	if err != nil {
		return "", err
	}
	return t.id, nil
}

// postToSubThread is thread.post: a follow-up from member from to one of the
// sub-threads it opened.
func (m *Manager) postToSubThread(parent *thread, from *agentState, id, text string) error {
	text = strings.TrimSpace(text)
	if text == "" {
		return errors.New("empty message")
	}
	if from.isEphemeral() {
		return errNotLead
	}
	t := m.thread(id)
	if t == nil {
		return fmt.Errorf("no sub-thread %q: thread.children lists yours", id)
	}
	t.mu.Lock()
	mine := t.parent == parent.id && t.parentAgent == from.id
	t.mu.Unlock()
	if !mine {
		return fmt.Errorf("%s is not a sub-thread you opened: thread.children lists yours", id)
	}
	if err := m.chargeAsk(parent); err != nil {
		return err
	}
	return m.askSub(t, from, fmt.Sprintf("[deck] From %s, the agent that opened this sub-thread:\n\n%s", from.getName(), text))
}

// askSub posts text into sub-thread t under from's name, to its lead, and
// records it as an ask whose answer goes back to from.
func (m *Manager) askSub(t *thread, from *agentState, text string) error {
	t.mu.Lock()
	lead := t.leadLocked()
	t.mu.Unlock()
	if lead == nil {
		return errNoLead
	}
	// On t's relay worker, where the ends of its lead's turns are settled: a
	// lead that answers at once must find the ask recorded (reportToParent).
	var err error
	done := make(chan struct{})
	m.onThread(t, func() {
		defer close(done)
		var res PostResult
		res, err = m.postFrom(t, from.getName(), text, []*agentState{lead}, nil, false, viaSubthread)
		t.mu.Lock()
		if res.EntryID != "" {
			t.asks = append(t.asks, res.EntryID)
		}
		t.mu.Unlock()
	})
	select {
	case <-done:
	case <-m.done:
		return errClosed
	}
	m.persistMeta(t)
	return err
}

// chargeAsk counts a turn against parent's budget for a brief or follow-up it
// sends a sub-thread; paused or at the limit, the ask is refused.
func (m *Manager) chargeAsk(parent *thread) error {
	parent.mu.Lock()
	pe, ok := m.chargeTurnLocked(parent)
	parent.mu.Unlock()
	if pe != nil {
		m.emitAll(msgFor(parent.id, *pe))
	}
	if !ok {
		return errors.New("held: Deck paused this thread; the user will continue it")
	}
	return nil
}

// reportToParent runs when a member of t ends a turn: when t is a sub-thread,
// a is its lead and the turn answered asks of the parent agent, the reply goes
// to that agent (its thread's lead when it has gone).
func (m *Manager) reportToParent(t *thread, a *agentState, reply string, answered int) {
	// From taking the answered asks off to the batch decision (batch.go);
	// what is to be sent goes out after.
	batchMu.Lock()
	var send func()
	defer func() {
		batchMu.Unlock()
		if send != nil {
			send()
		}
	}()
	t.mu.Lock()
	if t.parent == "" || t.leadLocked() != a || len(t.asks) == 0 {
		t.mu.Unlock()
		return
	}
	var keep []string
	n := 0
	for _, id := range t.asks {
		if i, ok := t.index.at[id]; ok && i < answered {
			n++
			continue
		}
		keep = append(keep, id)
	}
	t.asks = keep
	parentID, agentID, title := t.parent, t.parentAgent, t.title
	t.mu.Unlock()
	if n == 0 {
		return
	}
	m.persistMeta(t)
	p := m.thread(parentID)
	if p == nil {
		return // the parent is not open; its agent reads the sub-thread when it is
	}
	if reply = strings.TrimSpace(reply); reply == "" {
		reply = "(it ended its turn without a reply)"
	}
	subs := m.busySubThreads(parentID, agentID) // before p.mu: it locks the sub-threads
	p.mu.Lock()
	to := p.member(agentID)
	if to == nil || to.status() == StatusExited {
		to = p.leadLocked()
	}
	if to == nil {
		p.mu.Unlock()
		return
	}
	e := store.Entry{Type: store.EntryRelay, Speaker: a.getName(), To: []string{to.id}, Via: viaSubthread, Ref: t.id,
		Event: eventText(fmt.Sprintf("[sub-thread %q] %s", clip(title, 80), reply))}
	idx := m.appendLocked(p, e)
	// Batched with the agent's other reports (batch.go); the lead standing in
	// for an agent that has gone is told at once.
	wake := to.id != agentID || p.holdReportLocked(to, heldReport{idx, fmt.Sprintf("sub-thread %q", clip(title, 60))}, false, "", subs)
	p.mu.Unlock()
	send = func() {
		m.emit(msgFor(p.id, e))
		if wake {
			_, _ = m.deliver(p, to)
		}
	}
}

// subThread is one sub-thread as thread.children and the clients see it.
type subThread struct {
	ID, Title, Agent, Kind string
	Busy                   bool
	Waiting                int // asks not answered yet
}

// childrenOf lists the open sub-threads of thread parentID, those agentID
// opened when it is not "".
func (m *Manager) childrenOf(parentID, agentID string) []subThread {
	m.mu.Lock()
	ts := append([]*thread(nil), m.torder...)
	m.mu.Unlock()
	var out []subThread
	for _, t := range ts {
		if t.gone.Load() {
			continue
		}
		t.mu.Lock()
		if t.parent == parentID && (agentID == "" || t.parentAgent == agentID) {
			s := subThread{ID: t.id, Title: t.title, Waiting: len(t.asks)}
			if a := t.leadLocked(); a != nil {
				s.Agent, s.Kind, s.Busy = a.getName(), a.kind, a.busy()
			}
			out = append(out, s)
		}
		t.mu.Unlock()
	}
	return out
}

// childrenText is thread.children's answer.
func (m *Manager) childrenText(parent *thread, from *agentState) string {
	kids := m.childrenOf(parent.id, from.id)
	if len(kids) == 0 {
		return "you have no sub-threads; thread.open starts one"
	}
	var b strings.Builder
	for _, k := range kids {
		state := "idle"
		switch {
		case k.Busy:
			state = "working"
		case k.Waiting > 0:
			state = fmt.Sprintf("%d asks waiting", k.Waiting)
		}
		fmt.Fprintf(&b, "%s %q: %s (%s), %s\n", k.ID, k.Title, k.Agent, k.Kind, state)
	}
	return strings.TrimSpace(b.String())
}

// leadKind resolves an agent kind or a named agent's profile.
func (m *Manager) leadKind(t *thread, kind string) (string, profile.Profile, error) {
	kind = strings.TrimSpace(TrimSigil(kind))
	if _, ok := m.adapters[strings.ToLower(kind)]; ok {
		return strings.ToLower(kind), profile.Profile{}, nil
	}
	if p, ok := profile.Find(m.allProfiles(t), kind); ok {
		return p.Kind, p, nil
	}
	return "", profile.Profile{}, fmt.Errorf("no agent kind or named agent %q (kinds: %s)", kind, strings.Join(m.Kinds(), ", "))
}
