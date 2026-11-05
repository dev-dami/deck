package rpc

import (
	"testing"

	"github.com/dev-dami/deck/pkg/rpc/deckv1"
)

func streaming(parts ...*deckv1.Part) *deckv1.Message {
	return &deckv1.Message{Id: "m", SessionId: "s", Role: "assistant", Agent: "claude", CreatedAt: 1, UpdatedAt: 1, Order: 3, Parts: parts}
}

func text(s string) *deckv1.Part { return &deckv1.Part{Type: "text", Text: s} }

func TestAppendOf(t *testing.T) {
	tool := &deckv1.Part{Type: "tool_call", Id: "t1", Name: "Bash"}
	base := streaming(tool, text("héllo"))

	grown := streaming(tool, text("héllo wörld"))
	grown.UpdatedAt = 2
	a := appendOf(base, grown)
	if a == nil || a.GetPart() != 1 || a.GetOffset() != uint64(len("héllo")) || a.GetText() != " wörld" || a.GetUpdatedAt() != 2 {
		t.Fatalf("append = %v", a)
	}

	for name, next := range map[string]*deckv1.Message{
		"unchanged":       streaming(tool, text("héllo")),
		"finished":        func() *deckv1.Message { m := streaming(tool, text("héllo!")); m.Finished = true; return m }(),
		"turn end":        func() *deckv1.Message { m := streaming(tool, text("héllo!")); m.TurnEnd = "done"; return m }(),
		"rewritten":       streaming(tool, text("hello")),
		"part added":      streaming(tool, text("héllo"), text("x")),
		"tool changed":    streaming(&deckv1.Part{Type: "tool_call", Id: "t1", Name: "Bash", Summary: "ls"}, text("héllo!")),
		"two parts grown": streaming(&deckv1.Part{Type: "text", Text: "a"}, text("héllo!")),
	} {
		if a := appendOf(base, next); a != nil {
			t.Errorf("%s: got a delta %v; it must go out whole", name, a)
		}
	}
	if appendOf(nil, grown) != nil {
		t.Fatal("no delta without a base")
	}
}

func TestDeltasFollowTheStream(t *testing.T) {
	d := newDeltas()
	ev := func(kind string, m *deckv1.Message) *deckv1.Event {
		switch kind {
		case "created":
			return &deckv1.Event{Kind: &deckv1.Event_MessageCreated{MessageCreated: m}}
		case "deleted":
			return &deckv1.Event{Kind: &deckv1.Event_MessageDeleted{MessageDeleted: &deckv1.MessageDeleted{Id: m.GetId()}}}
		}
		return &deckv1.Event{Kind: &deckv1.Event_MessageUpdated{MessageUpdated: m}}
	}
	if d.observe(ev("created", streaming(text("a")))) != nil {
		t.Fatal("a created message goes out whole")
	}
	if got := d.observe(ev("updated", streaming(text("ab")))).GetMessageAppendText(); got.GetText() != "b" || got.GetOffset() != 1 {
		t.Fatalf("first append = %v", got)
	}
	if got := d.observe(ev("updated", streaming(text("abc")))).GetMessageAppendText(); got.GetText() != "c" || got.GetOffset() != 2 {
		t.Fatalf("second append = %v", got)
	}
	done := streaming(text("abcd"))
	done.Finished = true
	if d.observe(ev("updated", done)) != nil {
		t.Fatal("finishing goes out whole")
	}
	if d.observe(ev("updated", streaming(text("abcde")))) != nil {
		t.Fatal("after finishing nothing is remembered, so the next update is whole")
	}
	d.observe(ev("deleted", streaming()))
	if len(d.last) != 0 {
		t.Fatalf("a deleted message is forgotten: %v", d.last)
	}
}
