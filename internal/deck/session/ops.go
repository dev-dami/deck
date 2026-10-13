package session

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/dev-dami/deck/internal/deck/agent"
	"github.com/dev-dami/deck/internal/deck/hub"
)

// SetActivity installs the provider of the deck.jobs op: given a thread id,
// the commands its agents are running and the shells they left in the
// background, as text. The workspace, which tracks that activity, installs it.
func (m *Manager) SetActivity(f func(threadID string) string) {
	m.mu.Lock()
	m.activity = f
	m.mu.Unlock()
}

// execOp answers Deck's search tool (op "search") and every execute op other
// than thread.send and thread.read, for the agent that called it. It runs in
// the caller's thread: its folder, its project's history, its tool calls.
func (m *Manager) execOp(agentID, op string, args json.RawMessage) (string, bool) {
	a := m.get(agentID)
	if a == nil {
		return "Deck does not know agent " + agentID, true
	}
	t := a.thread
	env := hub.Env{AgentID: agentID, Tasks: taskBoard{m: m, a: a}}
	if m.st != nil { // a nil *store.Store must stay a nil interface
		env.Store = m.st
	}
	t.mu.Lock()
	env.ThreadID, env.Dir = t.id, t.dir
	env.Roots = []string{t.dir}
	if t.worktree != nil && t.worktree.Main != "" {
		env.Roots = append(env.Roots, t.worktree.Main)
	}
	// Only skills.used reads the thread's tool calls; gathering them for every
	// op would walk the whole log under the thread's lock.
	if strings.TrimSpace(op) == hub.OpSkillsUsed {
		for i := range t.log {
			e := &t.log[i]
			if e.Tool == nil || e.Kind != agent.KindToolCall {
				continue
			}
			env.Tools = append(env.Tools, hub.ToolUse{
				Agent: t.name(e.AgentID), Name: e.Tool.Name, Title: e.Tool.Title, Input: e.Tool.Input,
			})
		}
	}
	t.mu.Unlock()

	m.mu.Lock()
	activity := m.activity
	m.mu.Unlock()
	if activity != nil {
		tid := env.ThreadID
		env.Activity = func() string { return activity(tid) }
	}

	ctx, cancel := context.WithTimeout(m.ctx, hub.Timeout)
	defer cancel()
	return hub.Execute(ctx, env, op, args)
}
