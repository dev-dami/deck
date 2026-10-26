package store

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/dev-dami/deck/internal/deck/agent"
)

func TestLoadEntriesFromReadsOnlyTheRest(t *testing.T) {
	s, dir := newStore(t)
	th := mustCreate(t, s, Thread{})
	say := func(text string) {
		t.Helper()
		if err := s.Append(th.ID, agent.Event{Kind: agent.KindAssistantText, Text: text}); err != nil {
			t.Fatal(err)
		}
	}
	say("one")
	say("two")
	head, end, _, err := s.LoadEntriesFrom(th.ID, 0, 0)
	if err != nil || len(head) != 2 {
		t.Fatalf("head = %+v, %v", head, err)
	}
	if st, _ := os.Stat(filepath.Join(dir, th.ID+".jsonl")); end != st.Size() {
		t.Fatalf("end = %d, log is %d bytes", end, st.Size())
	}
	say("three")
	rest, end2, _, err := s.LoadEntriesFrom(th.ID, end, len(head))
	if err != nil || len(rest) != 1 || rest[0].Text != "three" || end2 <= end {
		t.Fatalf("rest = %+v, end %d, %v", rest, end2, err)
	}
	all, _, _ := s.LoadEntries(th.ID)
	if !reflect.DeepEqual(append(head, rest...), all) {
		t.Fatalf("head+rest = %+v\nall = %+v", append(head, rest...), all)
	}
	if rest[0].ID != all[2].ID {
		t.Fatalf("id %q, want %q", rest[0].ID, all[2].ID)
	}
	if none, end3, _, err := s.LoadEntriesFrom(th.ID, end2, 3); err != nil || len(none) != 0 || end3 != end2 {
		t.Fatalf("at the end: %+v, %d, %v", none, end3, err)
	}
}
