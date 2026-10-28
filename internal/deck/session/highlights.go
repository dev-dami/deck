package session

import (
	"context"
	"encoding/json"
	"fmt"
)

type HighlightExecutor func(ctx context.Context, thread, agent, turn, op string, args json.RawMessage) (string, error)

func (m *Manager) SetHighlights(run HighlightExecutor) {
	m.mu.Lock()
	m.highlightAction = run
	m.mu.Unlock()
}
func (a *agentState) highlightTurn() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.turnAt.IsZero() {
		return ""
	}
	return fmt.Sprintf("%s:%d", a.id, a.turnAt.UnixNano())
}
func (m *Manager) runHighlights(ctx context.Context, a *agentState, op string, args json.RawMessage) (string, error) {
	m.mu.Lock()
	run := m.highlightAction
	m.mu.Unlock()
	if run == nil {
		return "", fmt.Errorf("Highlights are not available in this workspace")
	}
	if a.thread.gone.Load() {
		return "", fmt.Errorf("this thread is no longer active")
	}
	return run(ctx, a.thread.id, a.id, a.highlightTurn(), op, args)
}
