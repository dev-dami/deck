package store

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/dev-dami/deck/internal/deck/agent"
)

func TestAppendEntriesAtSaysWhereTheLinesWent(t *testing.T) {
	s, dir := newStore(t)
	th := mustCreate(t, s, Thread{})
	var ends []int64
	for i := range 3 {
		start, end, err := s.AppendEntriesAt(th.ID, []Entry{{Event: agent.Event{Kind: agent.KindAssistantText, Text: fmt.Sprint(i)}}})
		if err != nil {
			t.Fatal(err)
		}
		if want := int64(0); len(ends) > 0 {
			want = ends[len(ends)-1]
			if start != want {
				t.Fatalf("append %d starts at %d, want %d", i, start, want)
			}
		} else if start != want {
			t.Fatalf("the first append starts at %d", start)
		}
		ends = append(ends, end)
	}
	if st, _ := os.Stat(filepath.Join(dir, th.ID+".jsonl")); ends[2] != st.Size() {
		t.Fatalf("end = %d, log is %d bytes", ends[2], st.Size())
	}
	// A scan from the end of the first append reads the other two, and hands
	// over the same offsets.
	var texts []string
	var nexts []int64
	if _, _, err := s.ScanEntries(th.ID, ends[0], 1, 0, func(e Entry, next int64) bool {
		texts, nexts = append(texts, e.Text), append(nexts, next)
		return true
	}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(texts, []string{"1", "2"}) || !reflect.DeepEqual(nexts, ends[1:]) {
		t.Fatalf("scan: %q at %v, want ends %v", texts, nexts, ends[1:])
	}
}

func TestScanEntriesReadsHeadsBeforeFull(t *testing.T) {
	s, _ := newStore(t)
	th := mustCreate(t, s, Thread{})
	var es []Entry
	for i := range 4 {
		es = append(es, Entry{Type: EntryUser, Speaker: "u", To: []string{"claude"}, Event: agent.Event{Kind: agent.KindUserText, Text: fmt.Sprint("ask ", i)}},
			Entry{Event: agent.Event{AgentID: "claude", Kind: agent.KindToolResult, Text: "ran",
				Tool: &agent.Tool{Name: "Bash", Output: strings.Repeat("o", 100)}}})
	}
	if err := s.AppendEntries(th.ID, es); err != nil {
		t.Fatal(err)
	}
	whole, _, _ := s.LoadEntries(th.ID)
	var full int64 // where the fifth entry starts
	var got []Entry
	n := 0
	if _, _, err := s.ScanEntries(th.ID, 0, 0, 0, func(_ Entry, next int64) bool {
		if n++; n == 4 {
			full = next
		}
		return true
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.ScanEntries(th.ID, 0, 0, full, func(e Entry, _ int64) bool { got = append(got, e); return true }); err != nil {
		t.Fatal(err)
	}
	if len(got) != len(whole) {
		t.Fatalf("%d entries, want %d", len(got), len(whole))
	}
	for i := range got {
		want := whole[i]
		if i < 4 {
			want.Tool = nil // a head: everything but the payload
		}
		if !reflect.DeepEqual(got[i], want) {
			t.Fatalf("entry %d = %+v, want %+v", i, got[i], want)
		}
	}
}

func TestALineWithABrokenPayloadStillCounts(t *testing.T) {
	s, dir := newStore(t)
	th := mustCreate(t, s, Thread{})
	log := `{"AgentID":"claude","Kind":"tool_result","Tool":"not an object"}` + "\n" +
		`{"AgentID":"claude","Kind":"assistant_text","Text":"after"}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, th.ID+".jsonl"), []byte(log), 0o600); err != nil {
		t.Fatal(err)
	}
	whole, _, err := s.LoadEntries(th.ID)
	if err != nil {
		t.Fatal(err)
	}
	var heads []Entry
	if _, _, err := s.ScanEntries(th.ID, 0, 0, 1<<20, func(e Entry, _ int64) bool { heads = append(heads, e); return true }); err != nil {
		t.Fatal(err)
	}
	// Read whole or as heads, the log has the same entries with the same ids.
	if len(whole) != 2 || len(heads) != 2 || whole[0].Tool != nil || whole[1].ID != "e1" || heads[1].ID != "e1" || whole[1].Text != "after" {
		t.Fatalf("whole %+v\nheads %+v", whole, heads)
	}
}

// entryHead must stay Entry without the event's payload.
func TestEntryHeadMirrorsEntry(t *testing.T) {
	payload := map[string]bool{"Tool": true, "Diff": true, "Usage": true, "Limits": true, "Raw": true}
	want := map[string]reflect.StructField{}
	var walk func(reflect.Type)
	walk = func(rt reflect.Type) {
		for i := range rt.NumField() {
			f := rt.Field(i)
			if f.Anonymous {
				walk(f.Type)
				continue
			}
			if !payload[f.Name] {
				want[f.Name] = f
			}
		}
	}
	walk(reflect.TypeFor[Entry]())
	ht := reflect.TypeFor[entryHead]()
	if ht.NumField() != len(want) {
		t.Fatalf("entryHead has %d fields, Entry %d besides the payload", ht.NumField(), len(want))
	}
	for i := range ht.NumField() {
		f := ht.Field(i)
		w, ok := want[f.Name]
		if !ok || w.Type != f.Type || w.Tag != f.Tag {
			t.Fatalf("entryHead.%s %v %q, Entry has %v %q", f.Name, f.Type, f.Tag, w.Type, w.Tag)
		}
	}
}

func TestCreateFromWritesALongLogInParts(t *testing.T) {
	s, _ := newStore(t)
	var es []Entry
	for i := range 2000 {
		es = append(es, Entry{ID: fmt.Sprint("x", i), Event: agent.Event{Kind: agent.KindAssistantText, Text: strings.Repeat("w", 300)}})
	}
	th, err := s.CreateFrom(Thread{}, func(yield func(Entry) bool) {
		for _, e := range es {
			if !yield(e) {
				return
			}
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	got, _, err := s.LoadEntries(th.ID)
	if err != nil || len(got) != len(es) || got[1999].ID != "x1999" {
		t.Fatalf("%d entries, %v", len(got), err)
	}
}

// A log cut just before a line's newline: that line is still an entry, the
// scan says the log ends after it, and the next append goes on a line of its
// own, starting where the scan ended. A cut inside a line leaves it unread,
// the scan says the log is unfinished, and the next append still reads.
func TestAppendAfterACutLine(t *testing.T) {
	for _, tc := range []struct {
		name, tail string
		whole      bool
	}{
		{"whole entry", `{"type":"user","id":"cut","Text":"whole"}`, true},
		{"half entry", `{"type":"user","id":"cut","Text":"ha`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, dir := newStore(t)
			th := mustCreate(t, s, Thread{})
			if err := s.AppendEntries(th.ID, []Entry{{Type: EntryUser, Event: agent.Event{Kind: agent.KindUserText, Text: "first"}}}); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, th.ID+".jsonl")
			f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := f.WriteString(tc.tail); err != nil {
				t.Fatal(err)
			}
			f.Close()
			fi, _ := os.Stat(path)

			end, _, err := s.ScanEntries(th.ID, 0, 0, 0, func(Entry, int64) bool { return true })
			if err != nil {
				t.Fatal(err)
			}
			if want := map[bool]int64{true: fi.Size(), false: -1}[tc.whole]; end != want {
				t.Fatalf("scan ends at %d, want %d", end, want)
			}

			start, _, err := s.AppendEntriesAt(th.ID, []Entry{{Type: EntryUser, Event: agent.Event{Kind: agent.KindUserText, Text: "after"}}})
			if err != nil {
				t.Fatal(err)
			}
			if start != fi.Size() {
				t.Fatalf("the append starts at %d, want the old end %d", start, fi.Size())
			}
			var texts []string
			if _, _, err := s.ScanEntries(th.ID, 0, 0, 0, func(e Entry, _ int64) bool {
				texts = append(texts, e.Text)
				return true
			}); err != nil {
				t.Fatal(err)
			}
			want := []string{"first", "after"}
			if tc.whole {
				want = []string{"first", "whole", "after"}
			}
			if !reflect.DeepEqual(texts, want) {
				t.Fatalf("log = %q, want %q", texts, want)
			}
			// A scan from the old end reads only the new entry.
			texts = nil
			if _, _, err := s.ScanEntries(th.ID, fi.Size(), 0, 0, func(e Entry, _ int64) bool {
				texts = append(texts, e.Text)
				return true
			}); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(texts, []string{"after"}) {
				t.Fatalf("from the old end: %q", texts)
			}
		})
	}
}

func TestScanOfAMissingLogIsEmpty(t *testing.T) {
	s, dir := newStore(t)
	th := mustCreate(t, s, Thread{})
	_ = os.Remove(filepath.Join(dir, th.ID+".jsonl"))
	if end, _, err := s.ScanEntries(th.ID, 0, 0, 0, func(Entry, int64) bool { return true }); err != nil || end != 0 {
		t.Fatalf("end %d, err %v, want an empty log", end, err)
	}
	if end, _, _ := s.ScanEntries(th.ID, 10, 0, 0, func(Entry, int64) bool { return true }); end != -1 {
		t.Fatalf("a scan from past the end of a missing log ends at %d", end)
	}
}
