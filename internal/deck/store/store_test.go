package store

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dev-dami/deck/internal/deck/agent"
)

func newStore(t *testing.T) (*Store, string) {
	t.Helper()
	dir := t.TempDir()
	s, err := New(dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return s, dir
}

func mustCreate(t *testing.T, s *Store, th Thread) Thread {
	t.Helper()
	out, err := s.Create(th)
	if err != nil {
		t.Fatalf("Create(%+v): %v", th, err)
	}
	return out
}

func TestCreateFillsDefaultsAndPersists(t *testing.T) {
	s, dir := newStore(t)
	got := mustCreate(t, s, Thread{AgentKind: "claude", Name: "claude1", Title: "  fix the\n  build  ", Dir: "C:/work"})

	if got.ID == "" {
		t.Fatal("ID not generated")
	}
	if got.Title != "fix the build" {
		t.Errorf("Title = %q, want one-line collapsed title", got.Title)
	}
	if got.Created.IsZero() || !got.Updated.Equal(got.Created) {
		t.Errorf("Created/Updated not set: %v / %v", got.Created, got.Updated)
	}
	if _, err := os.Stat(filepath.Join(dir, got.ID+".jsonl")); err != nil {
		t.Errorf("log file missing: %v", err)
	}

	// A fresh store on the same directory sees the thread.
	s2, err := New(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	threads, err := s2.List()
	if err != nil || len(threads) != 1 || threads[0].ID != got.ID || threads[0].Name != "claude1" {
		t.Fatalf("reopened List = %+v, %v", threads, err)
	}
}

func TestCreateRejectsDuplicateAndBadID(t *testing.T) {
	s, _ := newStore(t)
	mustCreate(t, s, Thread{ID: "abc", AgentKind: "codex"})
	if _, err := s.Create(Thread{ID: "abc"}); !errors.Is(err, ErrExists) {
		t.Fatalf("duplicate Create err = %v, want ErrExists", err)
	}
	for _, bad := range []string{"../escape", "a/b", ".hidden", "with space"} {
		if _, err := s.Create(Thread{ID: bad}); err == nil {
			t.Errorf("Create(%q) succeeded, want error", bad)
		}
	}
}

func TestTitleTruncatesTo60Runes(t *testing.T) {
	long := strings.Repeat("é", 100) // multi-byte: byte length != rune length
	got := truncateTitle(long)
	if n := len([]rune(got)); n != maxTitleRunes {
		t.Fatalf("truncated title has %d runes, want %d", n, maxTitleRunes)
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("truncated title %q lacks ellipsis", got)
	}
	short := "short prompt"
	if truncateTitle(short) != short {
		t.Errorf("short title changed: %q", truncateTitle(short))
	}
	exact := strings.Repeat("x", maxTitleRunes)
	if truncateTitle(exact) != exact {
		t.Errorf("60-rune title should be kept whole")
	}
}

func TestAppendDropsDeltasAndLoadRoundTrips(t *testing.T) {
	s, _ := newStore(t)
	th := mustCreate(t, s, Thread{AgentKind: "claude", Name: "claude1"})

	events := []agent.Event{
		{Kind: agent.KindSessionStarted, SessionID: "sess-9", AgentID: "claude1"},
		{Kind: agent.KindAssistantText, Text: "partial", Delta: true},
		{Kind: agent.KindAssistantText, Text: "full answer"},
		{Kind: agent.KindToolCall, Tool: &agent.Tool{ID: "t1", Name: "Read", Input: json.RawMessage(`{"file_path":"a.go"}`), Status: agent.ToolRunning}},
		{Kind: agent.KindUsage, Usage: &agent.Usage{InputTokens: 5, CostUSD: 0.12}},
	}
	for _, ev := range events {
		if err := s.Append(th.ID, ev); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}

	got, meta, err := s.Load(th.ID)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if meta.ID != th.ID || meta.Name != "claude1" {
		t.Errorf("meta = %+v", meta)
	}
	if len(got) != 4 {
		t.Fatalf("loaded %d events, want 4 (delta dropped)", len(got))
	}
	if got[0].SessionID != "sess-9" || got[0].Kind != agent.KindSessionStarted {
		t.Errorf("event 0 = %+v", got[0])
	}
	if got[1].Text != "full answer" || got[1].Delta {
		t.Errorf("event 1 = %+v", got[1])
	}
	if got[2].Tool == nil || got[2].Tool.Name != "Read" || string(got[2].Tool.Input) != `{"file_path":"a.go"}` {
		t.Errorf("tool event = %+v", got[2].Tool)
	}
	if got[3].Usage == nil || got[3].Usage.CostUSD != 0.12 {
		t.Errorf("usage event = %+v", got[3].Usage)
	}
}

func TestAppendSanitizesInvalidRawJSON(t *testing.T) {
	s, _ := newStore(t)
	th := mustCreate(t, s, Thread{})
	bad := agent.Event{Kind: agent.KindToolCall, Raw: json.RawMessage("{not json"),
		Tool: &agent.Tool{ID: "x", Input: json.RawMessage("oops")}}
	if err := s.Append(th.ID, bad); err != nil {
		t.Fatalf("Append with invalid raw JSON: %v", err)
	}
	got, _, err := s.Load(th.ID)
	if err != nil || len(got) != 1 {
		t.Fatalf("Load = %d events, %v", len(got), err)
	}
	if got[0].Tool == nil || got[0].Tool.ID != "x" {
		t.Errorf("tool lost: %+v", got[0].Tool)
	}
	// The caller's event must not be mutated.
	if string(bad.Tool.Input) != "oops" {
		t.Errorf("caller's tool input was modified")
	}
}

func TestAppendUnknownThread(t *testing.T) {
	s, _ := newStore(t)
	if err := s.Append("nope", agent.Event{Kind: agent.KindTurnDone}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Append unknown = %v, want ErrNotFound", err)
	}
	if _, _, err := s.Load("nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Load unknown = %v, want ErrNotFound", err)
	}
	if err := s.UpdateMeta(Thread{ID: "nope"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("UpdateMeta unknown = %v, want ErrNotFound", err)
	}
}

func TestConcurrentAppendsStayWholeLines(t *testing.T) {
	s, _ := newStore(t)
	th := mustCreate(t, s, Thread{})

	const writers, each = 10, 20
	var wg sync.WaitGroup
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < each; i++ {
				ev := agent.Event{Kind: agent.KindAssistantText, Text: strings.Repeat("y", 200+w)}
				if err := s.Append(th.ID, ev); err != nil {
					t.Errorf("Append: %v", err)
				}
			}
		}(w)
	}
	wg.Wait()

	got, _, err := s.Load(th.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != writers*each {
		t.Fatalf("loaded %d events, want %d (lost or merged lines)", len(got), writers*each)
	}
}

func TestLoadSkipsTornLine(t *testing.T) {
	s, dir := newStore(t)
	th := mustCreate(t, s, Thread{})
	if err := s.Append(th.ID, agent.Event{Kind: agent.KindAssistantText, Text: "ok"}); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(filepath.Join(dir, th.ID+".jsonl"), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(`{"Kind":"assistant_te`); err != nil { // torn write, no newline
		t.Fatal(err)
	}
	f.Close()

	got, _, err := s.Load(th.ID)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got) != 1 || got[0].Text != "ok" {
		t.Fatalf("Load = %+v, want the one good event", got)
	}
}

func TestUpdateMetaPersistsAndKeepsCreated(t *testing.T) {
	s, dir := newStore(t)
	th := mustCreate(t, s, Thread{AgentKind: "codex", Name: "codex1", Title: "old"})

	upd := th
	upd.Members = []Member{{ID: "codex1", Kind: "codex", Name: "codex2", Model: "gpt-x", SessionID: "thr-7", CostUSD: 0.5}}
	upd.Created = time.Time{} // must be ignored
	if err := s.UpdateMeta(upd); err != nil {
		t.Fatalf("UpdateMeta: %v", err)
	}

	s2, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	list, _ := s2.List()
	if len(list) != 1 {
		t.Fatalf("list = %d threads", len(list))
	}
	got := list[0]
	if got.Name != "codex2" || got.Model != "gpt-x" || got.SessionID != "thr-7" || got.CostUSD != 0.5 {
		t.Errorf("persisted meta = %+v", got)
	}
	if !got.Created.Equal(th.Created) {
		t.Errorf("Created changed: %v -> %v", th.Created, got.Created)
	}
}

func TestListSortedByUpdatedDesc(t *testing.T) {
	s, _ := newStore(t)
	a := mustCreate(t, s, Thread{ID: "a"})
	time.Sleep(5 * time.Millisecond)
	b := mustCreate(t, s, Thread{ID: "b"})
	time.Sleep(5 * time.Millisecond)
	_ = b

	list, _ := s.List()
	if len(list) != 2 || list[0].ID != "b" {
		t.Fatalf("order = %v, want b first", ids(list))
	}

	time.Sleep(5 * time.Millisecond)
	if err := s.Append(a.ID, agent.Event{Kind: agent.KindTurnDone}); err != nil {
		t.Fatal(err)
	}
	list, _ = s.List()
	if list[0].ID != "a" {
		t.Fatalf("after append order = %v, want a first", ids(list))
	}
}

func TestIndexRebuiltWhenMissing(t *testing.T) {
	s, dir := newStore(t)
	mustCreate(t, s, Thread{ID: "one", Name: "claude1"})
	mustCreate(t, s, Thread{ID: "two", Name: "codex1"})

	if err := os.Remove(filepath.Join(dir, "index.json")); err != nil {
		t.Fatal(err)
	}
	s2, err := New(dir)
	if err != nil {
		t.Fatalf("New after index removed: %v", err)
	}
	list, err := s2.List()
	if err != nil || len(list) != 2 {
		t.Fatalf("rebuilt list = %v, %v", ids(list), err)
	}
	if _, err := os.Stat(filepath.Join(dir, "index.json")); err != nil {
		t.Errorf("index.json not rewritten: %v", err)
	}
}

func TestIndexRebuiltWhenCorrupt(t *testing.T) {
	s, dir := newStore(t)
	mustCreate(t, s, Thread{ID: "keep"})
	if err := os.WriteFile(filepath.Join(dir, "index.json"), []byte("{broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	s2, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	if list, _ := s2.List(); len(list) != 1 || list[0].ID != "keep" {
		t.Fatalf("list after corrupt index = %v", ids(list))
	}
}

func TestNoTempFilesLeftBehind(t *testing.T) {
	s, dir := newStore(t)
	th := mustCreate(t, s, Thread{})
	for i := 0; i < 10; i++ {
		_ = s.Append(th.ID, agent.Event{Kind: agent.KindTurnDone})
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Errorf("temp file left behind: %s", e.Name())
		}
	}
}

func TestDefaultDirUnderHome(t *testing.T) {
	dir, err := DefaultDir()
	if err != nil {
		t.Skipf("no home dir: %v", err)
	}
	if filepath.Base(dir) != "threads" || filepath.Base(filepath.Dir(dir)) != ".deck" {
		t.Errorf("DefaultDir = %q, want .../.deck/threads", dir)
	}
}

func ids(ts []Thread) []string {
	out := make([]string, len(ts))
	for i, t := range ts {
		out[i] = t.ID
	}
	return out
}

func TestMembersRoundTripAndLegacyMirror(t *testing.T) {
	s, dir := newStore(t)
	th := mustCreate(t, s, Thread{Title: "duo", Dir: "C:/work", Members: []Member{
		{ID: "claude", Kind: "claude", Name: "claude", SessionID: "s1", CostUSD: 0.25, Cursor: 3},
		{ID: "codex", Kind: "codex", Name: "reviewer", SessionID: "s2", CostUSD: 0.5},
	}})
	if th.Version != CurrentVersion || th.CostUSD != 0.75 || th.AgentKind != "claude" || th.Name != "claude+reviewer" {
		t.Fatalf("created = %+v", th)
	}
	s2, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	_, got, err := s2.Load(th.ID)
	if err != nil || len(got.Members) != 2 || got.Members[0].Cursor != 3 || got.Members[1].Name != "reviewer" {
		t.Fatalf("loaded = %+v, %v", got, err)
	}
}

func TestLegacySingleAgentThreadMigratesOnLoad(t *testing.T) {
	dir := t.TempDir()
	meta := `{"id":"old1","agent_kind":"codex","name":"codex2","title":"rebuild","dir":"C:/w","model":"gpt-x","session_id":"thr-9","created":"2026-01-01T00:00:00Z","updated":"2026-01-01T00:00:00Z","cost_usd":0.04}`
	if err := os.WriteFile(filepath.Join(dir, "old1.meta.json"), []byte(meta), 0o600); err != nil {
		t.Fatal(err)
	}
	log := `{"AgentID":"codex2","Time":"2026-01-01T00:00:00Z","Kind":"user_text","Text":"rebuild dist"}` + "\n" +
		`{"AgentID":"codex2","Time":"2026-01-01T00:00:01Z","Kind":"assistant_text","Text":"done"}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, "old1.jsonl"), []byte(log), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := New(dir) // no index: rebuilt from the meta files
	if err != nil {
		t.Fatal(err)
	}
	entries, th, err := s.LoadEntries("old1")
	if err != nil {
		t.Fatal(err)
	}
	if th.Version != 0 {
		t.Errorf("Version = %d, want 0 so the caller can see it was migrated", th.Version)
	}
	if len(th.Members) != 1 {
		t.Fatalf("members = %+v", th.Members)
	}
	m := th.Members[0]
	if m.ID != "codex2" || m.Kind != "codex" || m.Name != "codex2" || m.Model != "gpt-x" || m.SessionID != "thr-9" || m.CostUSD != 0.04 {
		t.Errorf("migrated member = %+v", m)
	}
	if len(entries) != 2 || entries[0].Type != EntryEvent || entries[0].Text != "rebuild dist" || entries[1].AgentID != "codex2" {
		t.Errorf("entries = %+v", entries)
	}
	// Saving migrates the file to the new format.
	if err := s.UpdateMeta(th); err != nil {
		t.Fatal(err)
	}
	_, th2, _ := s.Load("old1")
	if th2.Version != CurrentVersion || len(th2.Members) != 1 {
		t.Errorf("after save = %+v", th2)
	}
}

func TestAppendEntryKeepsSpeakerAndStaysReadableAsEvents(t *testing.T) {
	s, _ := newStore(t)
	th := mustCreate(t, s, Thread{})
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(s.AppendEntry(th.ID, Entry{Type: EntryUser, Speaker: "dami", To: []string{"claude"}, Event: agent.Event{Kind: agent.KindUserText, Text: "plan it"}}))
	must(s.AppendEntry(th.ID, Entry{Type: EntryRelay, Speaker: "claude", To: []string{"codex"}, Via: "tool", Event: agent.Event{Text: "review"}}))
	must(s.Append(th.ID, agent.Event{AgentID: "codex", Kind: agent.KindAssistantText, Text: "ok"}))
	must(s.AppendEntry(th.ID, Entry{Event: agent.Event{Kind: agent.KindAssistantText, Delta: true, Text: "x"}}))

	entries, _, err := s.LoadEntries(th.ID)
	if err != nil || len(entries) != 3 {
		t.Fatalf("entries = %+v, %v", entries, err)
	}
	if e := entries[1]; e.Type != EntryRelay || e.Speaker != "claude" || e.Via != "tool" || len(e.To) != 1 || e.To[0] != "codex" || e.Text != "review" {
		t.Errorf("relay entry = %+v", e)
	}
	evs, _, _ := s.Load(th.ID)
	if len(evs) != 3 || evs[2].AgentID != "codex" {
		t.Errorf("events = %+v", evs)
	}
}

func TestModePersistsAndOldThreadsReadAsAsk(t *testing.T) {
	s, dir := newStore(t)

	// A new thread without a mode reads as ask.
	plain := mustCreate(t, s, Thread{ID: "plain", AgentKind: "claude", Name: "claude"})
	if plain.Mode != "ask" {
		t.Errorf("new thread Mode = %q, want ask", plain.Mode)
	}

	// A mode survives Create, UpdateMeta and a reopen.
	th := mustCreate(t, s, Thread{ID: "planner", Mode: "plan", Members: []Member{{ID: "claude", Kind: "claude"}}})
	if th.Mode != "plan" {
		t.Fatalf("Create dropped Mode: %q", th.Mode)
	}
	th.Mode = "yolo"
	if err := s.UpdateMeta(th); err != nil {
		t.Fatalf("UpdateMeta: %v", err)
	}
	s2, err := New(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	_, got, err := s2.LoadEntries("planner")
	if err != nil || got.Mode != "yolo" {
		t.Fatalf("reopened Mode = %q, %v; want yolo", got.Mode, err)
	}

	// An unknown value is migrated, not kept.
	bad := got
	bad.Mode = "bogus"
	if err := s.UpdateMeta(bad); err != nil {
		t.Fatalf("UpdateMeta: %v", err)
	}
	if _, m, _ := s.LoadEntries("planner"); m.Mode != "ask" {
		t.Errorf("unknown Mode kept as %q, want ask", m.Mode)
	}
}

func TestMetaWithoutModeMigratesToAsk(t *testing.T) {
	dir := t.TempDir()
	// A thread file written before modes existed, with no index.json.
	old := `{"id":"legacy1","version":2,"title":"old","dir":"C:/x","members":[{"id":"claude","kind":"claude","name":"claude"}],"created":"2026-01-01T00:00:00Z","updated":"2026-01-01T00:00:00Z"}`
	if err := os.WriteFile(filepath.Join(dir, "legacy1.meta.json"), []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := New(dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, th, err := s.LoadEntries("legacy1")
	if err != nil {
		t.Fatalf("LoadEntries: %v", err)
	}
	if th.Mode != "ask" {
		t.Errorf("legacy thread Mode = %q, want ask", th.Mode)
	}
	if th.Version != 2 {
		t.Errorf("Version changed to %d; migration must not bump it", th.Version)
	}
}
