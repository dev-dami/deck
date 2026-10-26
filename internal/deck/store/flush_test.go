package store

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dev-dami/deck/internal/deck/agent"
)

func readMeta(t *testing.T, dir, id string) Thread {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, id+".meta.json"))
	if err != nil {
		t.Fatal(err)
	}
	var th Thread
	if err := json.Unmarshal(data, &th); err != nil {
		t.Fatal(err)
	}
	return th
}

func TestAppendHoldsMetaUntilFlush(t *testing.T) {
	s, dir := newStore(t)
	s.delay = time.Hour // only Flush writes
	th := mustCreate(t, s, Thread{ID: "a"})
	time.Sleep(5 * time.Millisecond)
	if err := s.Append(th.ID, agent.Event{Kind: agent.KindAssistantText, Text: "hi"}); err != nil {
		t.Fatal(err)
	}
	list, _ := s.List()
	if !list[0].Updated.After(th.Updated) {
		t.Fatalf("Updated not bumped in memory: %v", list[0].Updated)
	}
	if got := readMeta(t, dir, th.ID); !got.Updated.Equal(th.Updated) {
		t.Fatalf("meta file rewritten on append: %v", got.Updated)
	}
	if err := s.Flush(); err != nil {
		t.Fatal(err)
	}
	if got := readMeta(t, dir, th.ID); !got.Updated.Equal(list[0].Updated) {
		t.Fatalf("after Flush meta Updated = %v, want %v", got.Updated, list[0].Updated)
	}
	s2, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	if l2, _ := s2.List(); !l2[0].Updated.Equal(list[0].Updated) {
		t.Fatalf("reopened Updated = %v, want %v", l2[0].Updated, list[0].Updated)
	}
}

func TestDelayedFlushAndTurnEndFlush(t *testing.T) {
	s, dir := newStore(t)
	s.delay = 20 * time.Millisecond
	th := mustCreate(t, s, Thread{ID: "a"})
	time.Sleep(5 * time.Millisecond)
	if err := s.Append(th.ID, agent.Event{Kind: agent.KindAssistantText, Text: "hi"}); err != nil {
		t.Fatal(err)
	}
	// Poll the store, not the file: on Windows reading the file while the
	// flush renames over it fails.
	pending := func() bool {
		s.mu.Lock()
		defer s.mu.Unlock()
		return len(s.dirty) > 0 || s.indexDirty
	}
	deadline := time.Now().Add(2 * time.Second)
	for pending() {
		if time.Now().After(deadline) {
			t.Fatal("held-back meta never flushed")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if readMeta(t, dir, th.ID).Updated.Equal(th.Updated) {
		t.Fatal("flushed meta lacks the append's Updated")
	}

	s.delay = time.Hour
	time.Sleep(5 * time.Millisecond)
	if err := s.Append(th.ID, agent.Event{Kind: agent.KindTurnDone}); err != nil {
		t.Fatal(err)
	}
	list, _ := s.List()
	if got := readMeta(t, dir, th.ID); !got.Updated.Equal(list[0].Updated) {
		t.Fatalf("turn end did not flush: meta %v, memory %v", got.Updated, list[0].Updated)
	}
}

func TestNewTakesMetaNewerThanIndex(t *testing.T) {
	s, dir := newStore(t)
	s.delay = time.Hour
	th := mustCreate(t, s, Thread{ID: "a", Title: "old"})
	time.Sleep(20 * time.Millisecond)
	th.Title = "new"
	if err := s.UpdateMeta(th); err != nil {
		t.Fatal(err)
	}
	// As after a crash: the meta file is written, the index is not.
	s2, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	if l, _ := s2.List(); len(l) != 1 || l[0].Title != "new" {
		t.Fatalf("reopened = %+v, want the newer meta's title", l)
	}
	idx, err := readIndex(filepath.Join(dir, "index.json"))
	if err != nil || len(idx) != 1 || idx[0].Title != "new" {
		t.Fatalf("index not repaired: %+v, %v", idx, err)
	}
}

func TestRawKeptOnlyForErrors(t *testing.T) {
	s, dir := newStore(t)
	th := mustCreate(t, s, Thread{})
	raw := json.RawMessage(`{"type":"x"}`)
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(s.Append(th.ID, agent.Event{Kind: agent.KindAssistantText, Text: "a", Raw: raw}))
	must(s.Append(th.ID, agent.Event{Kind: agent.KindError, Text: "boom", Raw: raw}))
	must(s.Append(th.ID, agent.Event{Kind: "brand_new", Raw: raw}))
	data, _ := os.ReadFile(filepath.Join(dir, th.ID+".jsonl"))
	if n := strings.Count(string(data), `"Raw":{`); n != 2 {
		t.Fatalf("log keeps %d raw lines, want 2 (error and unknown kind):\n%s", n, data)
	}
	// An old log line with a raw text event loads without it.
	old := `{"Kind":"assistant_text","Text":"old","Raw":{"type":"y"}}` + "\n"
	f, _ := os.OpenFile(filepath.Join(dir, th.ID+".jsonl"), os.O_APPEND|os.O_WRONLY, 0)
	_, _ = f.WriteString(old)
	f.Close()
	evs, _, err := s.Load(th.ID)
	if err != nil || len(evs) != 4 {
		t.Fatalf("Load = %d, %v", len(evs), err)
	}
	if evs[0].Raw != nil || evs[3].Raw != nil || evs[3].Text != "old" || len(evs[1].Raw) == 0 || len(evs[2].Raw) == 0 {
		t.Fatalf("raw after load: %q %q %q %q", evs[0].Raw, evs[1].Raw, evs[2].Raw, evs[3].Raw)
	}
}

func TestAppendEntriesWritesInOrder(t *testing.T) {
	s, _ := newStore(t)
	th := mustCreate(t, s, Thread{})
	err := s.AppendEntries(th.ID, []Entry{
		{Event: agent.Event{Kind: agent.KindAssistantText, Text: "1"}},
		{Event: agent.Event{Kind: agent.KindAssistantText, Text: "skip", Delta: true}},
		{Event: agent.Event{Kind: agent.KindAssistantText, Text: "2"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	evs, _, _ := s.Load(th.ID)
	if len(evs) != 2 || evs[0].Text != "1" || evs[1].Text != "2" {
		t.Fatalf("events = %+v", evs)
	}
}

// BenchmarkAppendEntry measures one logged event with a store of many threads,
// the case where rewriting the index on every append used to dominate.
func BenchmarkAppendEntry(b *testing.B) {
	dir := b.TempDir()
	s, err := New(dir)
	if err != nil {
		b.Fatal(err)
	}
	defer s.Close()
	for i := 0; i < 200; i++ {
		if _, err := s.Create(Thread{Title: "thread"}); err != nil {
			b.Fatal(err)
		}
	}
	th, _ := s.Create(Thread{Title: "hot"})
	e := Entry{Event: agent.Event{AgentID: "claude", Kind: agent.KindAssistantText, Text: strings.Repeat("x", 200),
		Raw: json.RawMessage(`{"type":"assistant","message":{"content":"` + strings.Repeat("x", 200) + `"}}`)}}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := s.AppendEntry(th.ID, e); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkAppendEntryThreads is N threads streaming into one store at
// once, one goroutine per thread, as a busy session with several agents
// does; ns/op is per appended event across all of them.
func BenchmarkAppendEntryThreads(b *testing.B) {
	for _, n := range []int{1, 4, 16} {
		b.Run(fmt.Sprintf("threads=%d", n), func(b *testing.B) {
			s, err := New(b.TempDir())
			if err != nil {
				b.Fatal(err)
			}
			defer s.Close()
			ids := make([]string, n)
			for i := range ids {
				th, err := s.Create(Thread{Title: "thread"})
				if err != nil {
					b.Fatal(err)
				}
				ids[i] = th.ID
			}
			e := Entry{Event: agent.Event{AgentID: "claude", Kind: agent.KindAssistantText, Text: strings.Repeat("x", 200),
				Raw: json.RawMessage(`{"type":"assistant","message":{"content":"` + strings.Repeat("x", 200) + `"}}`)}}
			b.ReportAllocs()
			b.ResetTimer()
			var wg sync.WaitGroup
			errs := make(chan error, n)
			for i, id := range ids {
				count := b.N / n
				if i < b.N%n {
					count++
				}
				wg.Add(1)
				go func() {
					defer wg.Done()
					for range count {
						if err := s.AppendEntry(id, e); err != nil {
							errs <- err
							return
						}
					}
				}()
			}
			wg.Wait()
			close(errs)
			for err := range errs {
				b.Fatal(err)
			}
		})
	}
}
