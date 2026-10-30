package rpc

import (
	"testing"

	"github.com/dev-dami/deck/pkg/remote/wire"
)

// A message's turn end crosses the wire, so the desktop shows the state.
func TestMessageCarriesItsTurnEnd(t *testing.T) {
	m := toMessage(wire.Message{ID: "a", Role: wire.RoleAssistant, Finished: true, TurnEnd: wire.TurnEndHandoff})
	if m.GetTurnEnd() != "handoff" || !m.GetFinished() {
		t.Fatalf("message = %+v", m)
	}
}
