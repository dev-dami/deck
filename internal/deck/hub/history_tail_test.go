package hub

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/dev-dami/deck/internal/deck/agent"
	"github.com/dev-dami/deck/internal/deck/store"
)

// tailSpy records where each history read of the log started.
type tailSpy struct {
	*store.Store
	offs []int64
}

func (s *tailSpy) LoadEntriesFrom(id string, off int64, n int) ([]store.Entry, int64, store.Thread, error) {
	s.offs = append(s.offs, off)
	return s.Store.LoadEntriesFrom(id, off, n)
}

func TestHistoryCacheReadsOnlyAppends(t *testing.T) {
	env := testEnv(t)
	dir := filepath.Join(t.TempDir(), "threads")
	st, err := store.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	spy := &tailSpy{Store: st}
	env.Store, env.Roots = spy, []string{env.Dir}
	th, err := st.Create(store.Thread{Title: "tail", Dir: env.Dir, Members: []store.Member{{ID: "claude", Kind: "claude", Name: "Claude"}}})
	if err != nil {
		t.Fatal(err)
	}
	add := func(es ...store.Entry) {
		t.Helper()
		if err := st.AppendEntries(th.ID, es); err != nil {
			t.Fatal(err)
		}
	}
	say := func(text string) store.Entry {
		return store.Entry{Event: agent.Event{AgentID: "claude", Kind: agent.KindAssistantText, Text: text, Time: time.Now()}}
	}
	// get is what history ops see; it must match a fresh read of the log.
	get := func(what string) *threadLines {
		t.Helper()
		ts, err := projectThreads(&env)
		if err != nil || len(ts) != 1 {
			t.Fatalf("%s: threads %v %v", what, ts, err)
		}
		tl, err := threadReadable(&env, ts[0])
		if err != nil {
			t.Fatalf("%s: %v", what, err)
		}
		entries, meta, err := st.LoadEntries(th.ID)
		if err != nil {
			t.Fatal(err)
		}
		if want := readable(entries, meta); !reflect.DeepEqual(tl.lines, want) {
			t.Fatalf("%s: lines\n%+v\nwant\n%+v", what, tl.lines, want)
		}
		for i, l := range tl.lines {
			if tl.low[i] != strings.ToLower(l.text) {
				t.Fatalf("%s: low[%d] = %q", what, i, tl.low[i])
			}
		}
		return tl
	}
	lastOff := func() int64 { return spy.offs[len(spy.offs)-1] }

	add(store.Entry{Type: store.EntryUser, Speaker: "dami", Event: agent.Event{Text: "Find the WALRUS", Time: time.Now()}},
		say("looking"),
		store.Entry{Event: agent.Event{AgentID: "claude", Kind: agent.KindToolCall, Tool: &agent.Tool{Name: "Bash", Title: "grep walrus"}}})
	first := get("first read")
	if lastOff() != 0 || first.end <= 0 || first.entries != 3 {
		t.Fatalf("first read from %d, end %d, %d entries", lastOff(), first.end, first.entries)
	}
	firstLines := append([]line(nil), first.lines...)

	add(say("found the narwhal"), store.Entry{Type: store.EntryNotice, Event: agent.Event{Text: "  noted  "}})
	second := get("after append")
	if lastOff() != first.end {
		t.Fatalf("after append read from %d, want %d (only what was added)", lastOff(), first.end)
	}
	if second.entries != 5 || !reflect.DeepEqual(first.lines, firstLines) {
		t.Fatalf("entries %d; first read changed: %+v", second.entries, first.lines)
	}
	n := len(spy.offs)
	if again := get("unchanged"); again != second || len(spy.offs) != n {
		t.Fatal("an unchanged log was read again")
	}
	if got, _ := run(t, env, "history.search", `{"query":"narwhal walrus"}`); !strings.Contains(got, "found the narwhal") || !strings.Contains(got, "Find the WALRUS") {
		t.Fatalf("search = %q", got)
	}

	// A torn write: the unfinished line is skipped, and the next read starts over.
	f, err := os.OpenFile(filepath.Join(dir, th.ID+".jsonl"), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString(`{"Kind":"assistant_te`)
	f.Close()
	if torn := get("torn write"); torn.end != -1 {
		t.Fatalf("torn log end = %d, want -1", torn.end)
	}
	add(say("after the tear"))
	get("append after a tear")
	if lastOff() != 0 {
		t.Fatalf("a torn log was extended from %d", lastOff())
	}

	// Renaming a member renames the speaker of every line: read it all again.
	add(say("before rename"))
	get("before rename")
	th.Members[0].Name = "Claude Code"
	if err := st.UpdateMeta(th); err != nil {
		t.Fatal(err)
	}
	add(say("after rename"))
	renamed := get("after rename")
	if lastOff() != 0 || renamed.lines[1].speaker != "Claude Code" {
		t.Fatalf("rename read from %d, speaker %q", lastOff(), renamed.lines[1].speaker)
	}
}
