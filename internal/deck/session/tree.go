package session

import (
	"github.com/dev-dami/deck/internal/deck/config"
	"github.com/dev-dami/deck/internal/deck/store"
)

// Subagent trees. A member the user brought in is at depth 0; a subagent is
// one deeper than the member that started it (the scheduler's and the user's
// subagents are at depth 1). A member may spawn while its children would stay
// within MaxDepth, it has fewer than MaxChildren live subagents, and the
// thread has fewer than MaxSubagents. When a member stops (it exits, or a
// subagent is retired) its live subagents are stopped too and their tasks
// cancelled, unless KeepOrphans is set; results of a task whose creator has
// gone go to the creator's nearest live ancestor.

func (m *Manager) maxDepth() int {
	if n := m.config().MaxDepth; n > 0 {
		return n
	}
	return config.DefaultMaxDepth
}

func (m *Manager) maxChildren() int {
	if n := m.config().MaxChildren; n > 0 {
		return n
	}
	return config.DefaultMaxChildren
}

func (m *Manager) maxSubagents() int {
	if n := m.config().MaxSubagents; n > 0 {
		return n
	}
	return config.DefaultMaxSubagents
}

// depth is how many subagent levels a is below the user's agents: 0 for a
// member the user brought in, 1 for its subagents, ... t.mu is held.
func (t *thread) depth(a *agentState) int {
	d := 0
	for i := 0; a != nil && a.isEphemeral() && i < 64; i++ {
		d++
		a = t.member(a.parentID())
	}
	return d
}

// children lists the live subagents parentID started ("" is the user and the
// scheduler); one being retired no longer counts. t.mu is held.
func (t *thread) children(parentID string) []*agentState {
	var out []*agentState
	for _, o := range t.members {
		if o.isEphemeral() && o.parentID() == parentID && o.alive() {
			out = append(out, o)
		}
	}
	return out
}

// liveSubagents counts the thread's live subagents. t.mu is held.
func (t *thread) liveSubagents() int {
	n := 0
	for _, o := range t.members {
		if o.isEphemeral() && o.alive() {
			n++
		}
	}
	return n
}

// ancestor is the nearest live member at or above id: id itself when it is
// running, else its parent, and so on; nil when none is. t.mu is held.
func (t *thread) ancestor(id string) *agentState {
	a := t.member(id)
	for i := 0; a != nil && i < 64; i++ {
		if a.status() != StatusExited {
			return a
		}
		a = t.member(a.parentID())
	}
	return nil
}

// cascade stops a's live subagents after a stopped, cancelling the tasks they
// own: each one's retirement cascades in turn, so the whole subtree goes.
// With KeepOrphans set it does nothing.
func (m *Manager) cascade(t *thread, a *agentState) {
	if m.config().KeepOrphans || m.isClosed() || t.gone.Load() {
		return
	}
	t.mu.Lock()
	kids := t.children(a.id)
	type own struct{ kid, task string }
	var owned []own
	for _, k := range kids {
		for _, tk := range t.tasks {
			if tk.Owner == k.id && tk.Status.Active() {
				owned = append(owned, own{k.id, tk.ID})
			}
		}
	}
	name := a.getName()
	t.mu.Unlock()
	for _, o := range owned {
		_, _ = m.moveTask(t, o.task, store.TaskCancelled, "", name+" stopped, so its subagent was stopped too", nil)
	}
	for _, k := range kids {
		m.retireMember(t, k.id) // a no-op for one the cancel already retired
	}
}

// waitsOnChildren reports, when a's turn has ended, whether a has live
// subagents of its own, or a task result the turn did not answer (a subagent
// can finish during a's turn): the end of the turn is then not the end of its
// task, which waits for those results. answered is a's cursor when the turn
// ended (turnEnd). t.mu is held.
func (t *thread) waitsOnChildren(a *agentState, answered int) bool {
	// A child whose task is over no longer counts, even before it is retired.
	for _, k := range t.children(a.id) {
		for _, tk := range t.tasks {
			if tk.Owner == k.id && tk.Status.Active() {
				return true
			}
		}
	}
	for i := answered; i < len(t.log); i++ {
		if e := t.log[i]; e.Type == store.EntryRelay && e.Via == "task" && !t.inert[i] && contains(e.To, a.id) {
			return true
		}
	}
	return false
}

// alive reports whether a runs and is not being retired.
func (a *agentState) alive() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return !a.retired && a.statusLocked() != StatusExited
}
