package session

import (
	"testing"

	"github.com/dev-dami/deck/internal/deck/agent"
)

// TestRestartStatus covers an idle agent showing "restarting" while its CLI
// restarts to apply a setting, without counting as busy, and a prompt sent
// meanwhile keeping it working when the restart ends.
func TestRestartStatus(t *testing.T) {
	a := &agentState{base: StatusIdle}
	a.apply(agent.Event{Kind: agent.KindRestarting, Text: "model"})
	if s := a.status(); s != StatusRestarting {
		t.Fatalf("status %s want restarting", s)
	}
	if a.busy() {
		t.Fatal("a restarting agent must take a prompt (the adapter holds it)")
	}
	a.apply(agent.Event{Kind: agent.KindRestarted})
	if s := a.status(); s != StatusIdle {
		t.Fatalf("status %s want idle", s)
	}

	a.apply(agent.Event{Kind: agent.KindRestarting})
	if _, ok, _ := a.tryBegin(); !ok {
		t.Fatal("tryBegin refused a restarting agent")
	}
	a.apply(agent.Event{Kind: agent.KindRestarted})
	if s := a.status(); s != StatusWorking {
		t.Fatalf("status %s want working", s)
	}
}
