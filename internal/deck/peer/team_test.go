package peer

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
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

	ops, err := h.Search(ctx, Caller{Name: "vps"}, "post chat")
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

func TestConsoleReadIsCappedAndRedacted(t *testing.T) {
	team := NewTeam("", nil)
	h := &TeamHandler{Base: &StatusHandler{Name: "me"}, Team: team}
	read := func(since uint64) ConsoleFeed {
		t.Helper()
		out, err := h.Execute(context.Background(), Caller{Name: "vps"}, OpConsoleRead, json.RawMessage(`{"since":`+strconv.FormatUint(since, 10)+`}`))
		if err != nil {
			t.Fatal(err)
		}
		var f ConsoleFeed
		if err := json.Unmarshal([]byte(out), &f); err != nil {
			t.Fatal(err)
		}
		return f
	}
	for i := range 150 {
		team.AddChat(ChatMessage{From: "c", Text: "line " + strconv.Itoa(i)})
	}
	f := read(0)
	if len(f.Messages) != maxReadMessages || f.Messages[0].Text != "line 50" || f.Messages[99].Text != "line 149" || f.Seq != 150 {
		t.Fatalf("150 messages read as %d, %q..%q, seq %d", len(f.Messages), f.Messages[0].Text, f.Messages[len(f.Messages)-1].Text, f.Seq)
	}
	if got := read(148); len(got.Messages) != 2 || got.Messages[0].Seq != 149 {
		t.Fatalf("since 148 = %+v", got.Messages)
	}
	if got := read(1 << 40); len(got.Messages) != maxReadMessages {
		t.Fatalf("a since from before a restart must read from the start: %d", len(got.Messages))
	}

	big := strings.Repeat("x", maxChatText)
	for range 20 {
		team.AddChat(ChatMessage{From: "c", Text: big})
	}
	team.AddChat(ChatMessage{From: "me", Text: "key ghp_" + strings.Repeat("a", 30), Mine: true, To: "c, vps", ID: "k1"})
	f = read(150)
	size := 0
	for _, m := range f.Messages {
		size += len(m.Text)
	}
	last := f.Messages[len(f.Messages)-1]
	if size > maxReadBytes || len(f.Messages) >= 21 || strings.Contains(last.Text, "ghp_") || !strings.Contains(last.Text, "[redacted]") {
		t.Fatalf("read %d messages, %d bytes, last %+v", len(f.Messages), size, last)
	}
	if last.To != "" || last.Mine || last.ID != "" {
		t.Fatalf("fields only this Deck needs leak: %+v", last)
	}
}

func TestConsoleReadIsNotADefault(t *testing.T) {
	if (Caller{Name: "b"}).Allowed(OpConsoleRead) {
		t.Fatal("console.read must be opted in per peer")
	}
	if !(Caller{Name: "b", AllowInbound: []string{OpConsoleRead}}).Allowed(OpConsoleRead) {
		t.Fatal("an explicit allow_inbound must allow console.read")
	}
}

func TestDrainRefusesNewWork(t *testing.T) {
	s, err := NewService(ServiceOptions{Dir: t.TempDir(), Logger: quietLog})
	if err != nil {
		t.Fatal(err)
	}
	h := &ContractHandler{Base: &TeamHandler{Base: &StatusHandler{Name: "me"}, Team: s.Team()}, S: s}
	ctx := context.Background()
	s.Drain()
	for _, op := range []string{OpContractPropose, OpThreadPost} {
		if _, err := h.Execute(ctx, Caller{Name: "b"}, op, json.RawMessage(`{}`)); !errors.Is(err, errDraining) {
			t.Fatalf("%s while draining: %v", op, err)
		}
	}
	if _, err := h.Execute(ctx, Caller{Name: "b"}, OpConsolePost, json.RawMessage(`{"text":"bye"}`)); err != nil {
		t.Fatalf("chat must still work while draining: %v", err)
	}
}

// TestPostChatTo: a post to one teammate goes to it alone, naming the agent
// there it is for, and an unknown teammate is refused with the names.
func TestPostChatTo(t *testing.T) {
	leakCheck(t)
	a, b := linkedServices(t, Local{}, Local{})
	other, err := LoadOrCreateIdentity(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := OpenStore(b.dir).Pin(NewPinned("c", "", "", other.PublicKey())); err != nil {
		t.Fatal(err)
	}
	ctx := ctxT(t)
	reached, err := b.PostChatTo(ctx, "claude", "a/codex", "hey now")
	if err != nil || len(reached) != 1 || reached[0] != "a" {
		t.Fatalf("post to a/codex: %v, %v", reached, err)
	}
	got := a.Team().Chat()
	if len(got) != 1 || got[0].For != "codex" || got[0].Author() != "b/claude" || got[0].Text != "hey now" {
		t.Fatalf("a's chat = %+v", got)
	}
	if b.out.len() != 0 {
		t.Fatal("a post to a alone was queued for c")
	}
	if _, err := b.PostChatTo(ctx, "claude", "nobody", "hi"); err == nil || !strings.Contains(err.Error(), "a, c") {
		t.Fatalf("unknown teammate: %v", err)
	}
}
