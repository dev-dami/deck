package peer

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/dev-dami/deck/internal/deck/sub"
)

func TestTeamChatPostsAndPersists(t *testing.T) {
	dir := t.TempDir()
	changes := 0
	team := NewTeam(dir, func() { changes++ })
	h := &TeamHandler{Base: &StatusHandler{Name: "me"}, Team: team}
	ctx := context.Background()

	ops, err := h.Search(ctx, Caller{Name: "vps"}, "chat")
	if err != nil || len(ops) != 1 || ops[0].Name != OpConsolePost {
		t.Fatalf("search chat = %+v, %v", ops, err)
	}
	if _, err := h.Execute(ctx, Caller{Name: "vps"}, OpConsolePost, json.RawMessage(`{"text":"  "}`)); err == nil {
		t.Fatal("an empty message must be refused")
	}
	// Control and bidi characters a peer could garble the screen with go.
	if _, err := h.Execute(ctx, Caller{Name: "vps"}, OpConsolePost, json.RawMessage(`{"text":"hi ‮evil\u001b[2J there"}`)); err != nil {
		t.Fatal(err)
	}
	chat := team.Chat()
	if len(chat) != 1 || chat[0].From != "vps" || chat[0].Text != "hi evil[2J there" || chat[0].Mine || changes != 1 {
		t.Fatalf("chat = %+v (changes %d)", chat, changes)
	}

	// The chat survives a restart; sequence numbers carry on.
	again := NewTeam(dir, nil)
	if got := again.Chat(); len(got) != 1 || got[0].Text != chat[0].Text {
		t.Fatalf("reloaded chat = %+v", got)
	}
	m := again.AddChat(ChatMessage{From: "me", Text: "hello", Mine: true})
	if m.Seq <= chat[0].Seq-1 || len(again.Chat()) != 2 {
		t.Fatalf("after reload: %+v", again.Chat())
	}

	long := strings.Repeat("x", maxChatText+50)
	if got := team.AddChat(ChatMessage{From: "vps", Text: long}); len(got.Text) > maxChatText+len("…") {
		t.Fatalf("a long message is kept at %d bytes", len(got.Text))
	}
}

func TestPrivateCapsuleRequestIsRecorded(t *testing.T) {
	s, id, base := subCapsule(t)
	team := NewTeam("", nil)
	h := &SubHandler{Base: &StatusHandler{Name: "me"}, Store: func() *sub.Store { return s },
		OnRequest: func(from Caller, id sub.ID, served bool) { team.AddRequest(from.Name, id, served) }}
	ctx := context.Background()
	closure := func(id sub.ID) error {
		b, _ := json.Marshal(map[string]any{"id": id})
		_, err := h.Execute(ctx, Caller{Name: "vps"}, OpSubClosure, b)
		return err
	}

	// Private: refused, and the person hears of it once per minute.
	if err := closure(id); !errors.Is(err, ErrUnknownOp) || !strings.Contains(err.Error(), "private") {
		t.Fatalf("private capsule: %v", err)
	}
	_ = closure(id)
	reqs := team.Requests()
	if len(reqs) != 1 || reqs[0].From != "vps" || reqs[0].ID != id || reqs[0].Served {
		t.Fatalf("requests = %+v", reqs)
	}
	// Something that is not a capsule (the base snapshot) is no request.
	_ = closure(base)
	if len(team.Requests()) != 1 {
		t.Fatalf("a non-capsule id was recorded: %+v", team.Requests())
	}

	// Shared: served, and recorded as served.
	if _, err := s.Publish(id); err != nil {
		t.Fatal(err)
	}
	if err := closure(id); err != nil {
		t.Fatal(err)
	}
	if reqs := team.Requests(); len(reqs) != 2 || !reqs[1].Served {
		t.Fatalf("requests after sharing = %+v", reqs)
	}

	// Private again: refused again.
	if err := s.Unpublish(id); err != nil || s.IsPublished(id) {
		t.Fatalf("unpublish: %v", err)
	}
	if err := closure(id); !errors.Is(err, ErrUnknownOp) {
		t.Fatalf("closure after unpublish: %v", err)
	}
}
