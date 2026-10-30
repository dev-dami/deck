package session

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/dev-dami/deck/internal/deck/agent"
)

type ArtifactExecutor func(ctx context.Context, thread, agent, turn, op string, args json.RawMessage) (string, error)

func (m *Manager) SetArtifacts(run ArtifactExecutor) {
	m.mu.Lock()
	m.artifactAction = run
	m.mu.Unlock()
}
func (m *Manager) runArtifacts(ctx context.Context, a *agentState, op string, args json.RawMessage) (string, error) {
	m.mu.Lock()
	run := m.artifactAction
	m.mu.Unlock()
	if run == nil {
		return "", fmt.Errorf("Artifacts are not available in this workspace")
	}
	if a.thread.gone.Load() {
		return "", fmt.Errorf("this thread is no longer active")
	}
	if (op == "artifact.create" || op == "artifact.update" || op == "artifact.restore") && a.thread.getMode() == agent.ModePlan {
		return "", fmt.Errorf("plan mode permits artifact reads, not source mutations")
	}
	return run(ctx, a.thread.id, a.id, a.highlightTurn(), op, args)
}
