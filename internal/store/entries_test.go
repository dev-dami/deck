package store

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/dev-dami/deck/internal/agent"
)

func TestNewEntryFieldsRoundTripAndEntriesGetIDs(t *testing.T) {
	s, _ := newStore(t)
	th := mustCreate(t, s, Thread{Members: []Member{{ID: "claude", Kind: "claude", ContextTokens: 1234, ContextWindow: 200000}}})
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(s.AppendEntry(th.ID, Entry{Type: EntryUser, ID: "u1", Speaker: "dami", To: []string{"claude"},
		Images: []string{"/tmp/a.png", "/tmp/b.jpg"}, Event: agent.Event{Kind: agent.KindUserText, Text: "look"}}))
	must(s.AppendEntry(th.ID, Entry{Type: EntryEdit, ID: "ed1", Ref: "u1", Old: "look", To: []string{"claude"}, Event: agent.Event{Text: "look closely"}}))
	must(s.AppendEntry(th.ID, Entry{Type: EntryUser, ID: "u2", Steer: true, Ref: "u0", Old: "was", To: []string{"claude"}, Event: agent.Event{Kind: agent.KindUserText, Text: "now"}}))
	must(s.AppendEntry(th.ID, Entry{Type: EntryCancel, ID: "c1", Ref: "u3", To: []string{"codex"}}))
	must(s.AppendEntry(th.ID, Entry{Event: agent.Event{AgentID: "claude", Kind: agent.KindAssistantText, Text: "ok"}}))

	entries, meta, err := s.LoadEntries(th.ID)
	if err != nil || len(entries) != 5 {
		t.Fatalf("entries = %+v, %v", entries, err)
	}
	if e := entries[0]; e.ID != "u1" || len(e.Images) != 2 || e.Images[1] != "/tmp/b.jpg" || e.Steer {
		t.Errorf("user entry = %+v", e)
	}
	if e := entries[1]; e.Type != EntryEdit || e.Ref != "u1" || e.Old != "look" || e.Text != "look closely" || e.ID != "ed1" {
		t.Errorf("edit entry = %+v", e)
	}
	if e := entries[2]; !e.Steer || e.Ref != "u0" || e.Old != "was" {
		t.Errorf("steer entry = %+v", e)
	}
	if e := entries[3]; e.Type != EntryCancel || e.Ref != "u3" || e.To[0] != "codex" {
		t.Errorf("cancel entry = %+v", e)
	}
	if entries[4].ID != "e4" {
		t.Errorf("an entry written without an id reads back as %q, want its position e4", entries[4].ID)
	}
	if len(meta.Members) != 1 || meta.Members[0].ContextTokens != 1234 || meta.Members[0].ContextWindow != 200000 {
		t.Errorf("member = %+v", meta.Members)
	}
	again, _, _ := s.LoadEntries(th.ID)
	if again[4].ID != entries[4].ID {
		t.Error("synthesised id changed between loads")
	}
}

func TestLegacyLogLinesGetStablePositionalIDs(t *testing.T) {
	dir := t.TempDir()
	meta := `{"id":"old3","agent_kind":"claude","name":"claude","title":"x","dir":"C:/w","session_id":"s","created":"2026-01-01T00:00:00Z","updated":"2026-01-01T00:00:00Z"}`
	if err := os.WriteFile(filepath.Join(dir, "old3.meta.json"), []byte(meta), 0o600); err != nil {
		t.Fatal(err)
	}
	log := `{"AgentID":"claude","Kind":"user_text","Text":"q"}` + "\n" + `{"AgentID":"claude","Kind":"assistant_text","Text":"a"}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, "old3.jsonl"), []byte(log), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	entries, _, err := s.LoadEntries("old3")
	if err != nil || len(entries) != 2 || entries[0].ID != "e0" || entries[1].ID != "e1" {
		t.Fatalf("entries = %+v, %v", entries, err)
	}
}
