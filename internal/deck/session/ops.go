package session

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/dev-dami/deck/internal/deck/agent"
	"github.com/dev-dami/deck/internal/deck/hub"
	"github.com/dev-dami/deck/internal/deck/store"
)

// SetActivity installs the provider of the deck.jobs op: given a thread id,
// the commands its agents are running and the shells they left in the
// background, as text. The workspace, which tracks that activity, installs it.
func (m *Manager) SetActivity(f func(threadID string) string) {
	m.mu.Lock()
	m.activity = f
	m.mu.Unlock()
}

// SetTeam installs the peer link the peers.* ops read: the teammates this
// Deck is paired with. The engine installs it when the link exists.
func (m *Manager) SetTeam(t hub.Team) {
	m.mu.Lock()
	m.team = t
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
	env := hub.Env{AgentID: agentID, Tasks: taskBoard{m: m, a: a}, Sub: subBoard{m: m, a: a}, MaxOutput: m.budget(a).output}
	if m.st != nil { // a nil *store.Store must stay a nil interface
		env.Store = m.st
	}
	t.mu.Lock()
	// git and gh run where the agent works: its own worktree when it has
	// one, so an isolated subagent's git.diff is its own changes.
	env.ThreadID, env.Dir = t.id, a.workDir(t.dir)
	env.Roots = []string{t.dir}
	if t.ops != nil {
		env.Ops = opsBoard{m: m, t: t, o: t.ops}
	}
	if t.worktree != nil && t.worktree.Main != "" {
		env.Roots = append(env.Roots, t.worktree.Main)
	}
	// Only skills.used reads the thread's tool calls; gathering them for every
	// op would walk the whole log. It walks a view of it, after the lock.
	var log logView
	skills := strings.TrimSpace(op) == hub.OpSkillsUsed
	if skills {
		log = t.view()
	}
	t.mu.Unlock()
	if skills {
		_ = log.each(0, log.size(), func(_ int, e store.Entry) bool {
			if e.Tool != nil && e.Kind == agent.KindToolCall {
				env.Tools = append(env.Tools, hub.ToolUse{Agent: e.AgentID, Name: e.Tool.Name, Title: e.Tool.Title, Input: e.Tool.Input})
			}
			return true
		})
		t.mu.Lock()
		for i := range env.Tools {
			env.Tools[i].Agent = t.name(env.Tools[i].Agent)
		}
		t.mu.Unlock()
	}

	m.mu.Lock()
	activity := m.activity
	if m.team != nil {
		env.Team = m.team
	}
	m.mu.Unlock()
	if activity != nil {
		tid := env.ThreadID
		env.Activity = func() string { return activity(tid) }
	}

	ctx, cancel := context.WithTimeout(m.ctx, hub.Timeout)
	defer cancel()
	return hub.Execute(ctx, env, op, args)
}
