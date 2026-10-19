package session

import (
	"fmt"
	"strings"

	"github.com/dev-dami/deck/internal/deck/store"
)

// roster renders a's thread's members for the thread.roster op, one per line:
//
//	claude2 (you) · claude · opus/high · working · task t3 · subagent of claude1 · branch deck/ab12cd-claude2
//
// then, for each kind with several live members, how to reach them.
func (m *Manager) roster(a *agentState) string {
	t := a.thread
	t.mu.Lock()
	defer t.mu.Unlock()
	var lines []string
	live := map[string]int{}
	for _, o := range t.members {
		ai := o.info()
		parts := []string{ai.Name}
		if o == a {
			parts[0] += " (you)"
		}
		parts = append(parts, ai.Kind)
		if model := ai.Model; model != "" || ai.Effort != "" {
			if model == "" {
				model = "default"
			}
			if ai.Effort != "" {
				model += "/" + ai.Effort
			}
			parts = append(parts, model)
		}
		parts = append(parts, string(ai.Status))
		if ai.Status != StatusExited {
			live[ai.Kind]++
		}
		for _, tk := range t.tasks {
			if tk.Owner == o.id && tk.Status == store.TaskRunning {
				parts = append(parts, "task "+tk.ID)
			}
		}
		if ai.Parent != "" {
			parts = append(parts, "subagent of "+t.name(ai.Parent))
		}
		if wt := o.getWorktree(); wt != nil && wt.Branch != "" {
			parts = append(parts, "branch "+wt.Branch)
		}
		if ai.ContextWindow > 0 && ai.ContextTokens > 0 {
			parts = append(parts, fmt.Sprintf("%d%% ctx", ai.ContextTokens*100/ai.ContextWindow))
		}
		lines = append(lines, strings.Join(parts, " · "))
	}
	if len(lines) == 0 {
		return "nobody is in this thread"
	}
	for _, k := range m.Kinds() {
		if live[k] > 1 {
			lines = append(lines, fmt.Sprintf("%d %s are running: to %q reaches whichever is free, %q all of them.", live[k], k, k, k+"*"))
		}
	}
	return strings.Join(lines, "\n")
}
