package state

import (
	"testing"

	"github.com/dev-dami/deck/pkg/rpc/deckv1"
)

func TestApply(t *testing.T) {
	var s Store
	changes := 0
	s.Changed = func() { changes++ }
	s.Add("p", &deckv1.InfoResponse{Project: "proj"})
	s.SetSessions("p", []*deckv1.Session{{Id: "a", UpdatedAt: 1}, {Id: "b", UpdatedAt: 2}})

	if !s.Apply("p", &deckv1.Event{Kind: &deckv1.Event_Ready{Ready: &deckv1.Ready{}}}) {
		t.Fatal("ready must ask for a refetch")
	}
	msg := func(id, text string) *deckv1.Message {
		return &deckv1.Message{Id: id, SessionId: "a", Parts: []*deckv1.Part{{Type: "text", Text: text}}}
	}
	s.Apply("p", &deckv1.Event{Kind: &deckv1.Event_MessageCreated{MessageCreated: msg("m1", "he")}})
	s.Apply("p", &deckv1.Event{Kind: &deckv1.Event_MessageUpdated{MessageUpdated: msg("m1", "hello")}})
	s.Apply("p", &deckv1.Event{Kind: &deckv1.Event_SessionUpdated{SessionUpdated: &deckv1.Session{Id: "a", UpdatedAt: 3}}})
	s.Apply("p", &deckv1.Event{Kind: &deckv1.Event_ApprovalRequested{ApprovalRequested: &deckv1.Approval{Id: "x", SessionId: "a"}}})
	s.Apply("p", &deckv1.Event{Kind: &deckv1.Event_Busy{Busy: &deckv1.Busy{SessionId: "a", Busy: true}}})

	s.View(func(ps []*Project) {
		p := Get(ps, "p")
		if ms := p.Messages["a"]; len(ms) != 1 || ms[0].GetParts()[0].GetText() != "hello" {
			t.Fatalf("messages = %v", ms)
		}
		if p.Sessions[0].GetId() != "a" || !p.Sessions[0].GetBusy() {
			t.Fatalf("sessions = %v", p.Sessions)
		}
		if len(p.Approvals["a"]) != 1 {
			t.Fatalf("approvals = %v", p.Approvals)
		}
	})

	s.Apply("p", &deckv1.Event{Kind: &deckv1.Event_ApprovalResolved{ApprovalResolved: &deckv1.ApprovalResolved{Id: "x", SessionId: "a"}}})
	s.Apply("p", &deckv1.Event{Kind: &deckv1.Event_SessionDeleted{SessionDeleted: &deckv1.Session{Id: "a"}}})
	s.View(func(ps []*Project) {
		p := Get(ps, "p")
		if len(p.Approvals["a"]) != 0 || len(p.Sessions) != 1 || p.Messages["a"] != nil {
			t.Fatalf("after delete: %v %v", p.Sessions, p.Approvals)
		}
	})
	if changes == 0 {
		t.Fatal("Changed was never called")
	}
}
