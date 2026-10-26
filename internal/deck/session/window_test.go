package session

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dev-dami/deck/internal/deck/agent"
	"github.com/dev-dami/deck/internal/deck/agent/fake"
	"github.com/dev-dami/deck/internal/deck/store"
)

// allLog is th's whole log, read back from the store where it is no longer
// in memory. th.mu is held.
func allLog(th *thread) []store.Entry {
	var out []store.Entry
	v := th.view()
	if err := v.each(0, v.size(), func(_ int, e store.Entry) bool { out = append(out, e); return true }); err != nil {
		panic(err)
	}
	return out
}

// windowed is a manager over the store in dir whose threads keep about
// window bytes of their logs in memory.
func windowed(t *testing.T, dir string, window int64, claude ...fake.Script) (*Manager, *store.Store, *spyAdapter, *collector) {
	t.Helper()
	st, err := store.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	spy := newSpy(fake.NewTurns(0, fake.NewHub(), claude...))
	m := NewManager(map[string]agent.Adapter{"claude": spy}, st, testCfg())
	m.window = window
	t.Cleanup(m.Close)
	return m, st, spy, collect(t, m)
}

// held is how many entries of tid's log are in memory, and where they start.
func held(m *Manager, tid string) (n, base int) {
	th := m.thread(tid)
	th.mu.Lock()
	defer th.mu.Unlock()
	return len(th.log), th.base
}

func TestResumeKeepsOnlyTheNewestPartOfALongLog(t *testing.T) {
	dir := t.TempDir()
	m, st, _, _ := windowed(t, dir, 256<<10)
	tid := longLog(t, st, t.TempDir(), 400) // about 2 MB
	stored, _, err := st.LoadEntries(tid)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := m.Resume(tid); err != nil {
		t.Fatal(err)
	}
	n, base := held(m, tid)
	if base == 0 || n < logKeep || n+base != len(stored) {
		t.Fatalf("in memory: %d entries from %d of %d", n, base, len(stored))
	}
	th := m.thread(tid)
	th.mu.Lock()
	cost := th.disk.end - th.disk.baseOff + entryCost*int64(th.disk.n-th.base)
	got := allLog(th)
	idx := th.index
	th.mu.Unlock()
	if cost > m.window {
		t.Fatalf("the memory part costs %d, over the window of %d", cost, m.window)
	}
	// Read back, the log is the stored one, payload and all.
	if len(got) != len(stored) {
		t.Fatalf("read back %d entries, stored %d", len(got), len(stored))
	}
	for i := range got {
		if got[i].ID != stored[i].ID || got[i].Text != stored[i].Text || (got[i].Tool == nil) != (stored[i].Tool == nil) ||
			got[i].Tool != nil && got[i].Tool.Output != stored[i].Tool.Output {
			t.Fatalf("entry %d = %+v, stored %+v", i, got[i], stored[i])
		}
	}
	// The index covers the whole log, not just the part in memory.
	if len(idx.at) != len(stored) || idx.at["u0"] != 0 || len(idx.heads) != 400 || idx.firstText != "message 0" {
		t.Fatalf("index: %d ids, u0 at %d, %d heads, first %q", len(idx.at), idx.at["u0"], len(idx.heads), idx.firstText)
	}
	// The history is all of it, rendered from the store and memory alike.
	var users int
	for _, h := range m.History(tid) {
		if em, ok := h.(EventMsg); ok && em.FromUser() {
			if em.Text != fmt.Sprintf("message %d", users) {
				t.Fatalf("history message %d = %q", users, em.Text)
			}
			users++
		}
	}
	if users != 400 {
		t.Fatalf("history has %d user messages, want 400", users)
	}
}

func TestOldEntriesAreReadBackOnDemand(t *testing.T) {
	dir := t.TempDir()
	m, st, spy, c := windowed(t, dir, 64<<10, turn(say("on it"), finish()), turn(say("again"), finish()))
	tid := longLog(t, st, t.TempDir(), 200)
	if _, _, err := m.Resume(tid); err != nil {
		t.Fatal(err)
	}
	if _, base := held(m, tid); base <= 5 {
		t.Fatalf("base = %d: the start of the log should be on disk", base)
	}

	// Editing a message long out of memory finds it, and tells claude,
	// which had it, with the old text.
	if err := m.Edit(tid, "u1", "message one, edited"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the edit's turn", func() bool { return c.turnDones("claude") == 1 })
	p := spy.prompts("claude")
	if len(p) != 1 || !strings.Contains(p[0], "Before: message 1") || !strings.Contains(p[0], "After: message one, edited") {
		t.Fatalf("prompts = %q", p)
	}
	// A checkpoint far back still has its text, as edited.
	cs, err := m.Changes(t.Context(), tid, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(cs.Checkpoints) != 200 || cs.Checkpoints[1].Text != "message one, edited" || cs.Checkpoints[0].Text != "message 0" {
		t.Fatalf("checkpoints: %d, %+v", len(cs.Checkpoints), cs.Checkpoints[:2])
	}

	// A member that has seen nothing is briefed from the start of the log,
	// which comes back from the store.
	th := m.thread(tid)
	th.mu.Lock()
	a := th.member("claude")
	a.cursor = 0
	th.mu.Unlock()
	if err := m.Post(tid, "$claude catch up"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the catch-up turn", func() bool { return c.turnDones("claude") == 2 })
	p = spy.prompts("claude")
	brief := p[len(p)-1]
	has(t, brief, "[tester] message 0\n", "[tester] message one, edited", "[tester] message 199", "[tester] catch up")
	lacks(t, brief, "[tester] message 1\n")
}

func TestLiveLogStaysWithinTheWindow(t *testing.T) {
	var turns []fake.Script
	out := strings.Repeat("x", 4<<10)
	for range 60 {
		turns = append(turns, turn(fake.Step{Event: agent.Event{Kind: agent.KindToolResult,
			Tool: &agent.Tool{ID: "t", Name: "Bash", Title: "make", Output: out}}}, say("done"), finish()))
	}
	m, _, _, c := windowed(t, t.TempDir(), 256<<10, turns...)
	tid, err := m.NewThread(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for i := range 60 {
		if err := m.Post(tid, fmt.Sprintf("step %d", i)); err != nil {
			t.Fatal(err)
		}
		waitFor(t, "the turn", func() bool { return c.turnDones("claude") == i+1 })
	}
	m.Flush()
	n, base := held(m, tid)
	th := m.thread(tid)
	th.mu.Lock()
	size, synced := th.size(), th.disk.synced
	cost := th.disk.end - th.disk.baseOff + entryCost*int64(th.disk.n-th.base)
	got := allLog(th)
	th.mu.Unlock()
	// Everything is in the store: memory holds the window at most.
	if !synced || base == 0 || cost > m.window || size != len(got) {
		t.Fatalf("in memory %d from %d of %d, costing %d (synced %v)", n, base, size, cost, synced)
	}
	var steps int
	for _, e := range got {
		if e.Type == store.EntryUser {
			if e.Text != fmt.Sprintf("step %d", steps) {
				t.Fatalf("user entry %d = %q", steps, e.Text)
			}
			steps++
		}
		if e.Kind == agent.KindToolResult && (e.Tool == nil || len(e.Tool.Output) != len(out)) {
			t.Fatalf("a tool result read back without its output: %+v", e)
		}
	}
	if steps != 60 {
		t.Fatalf("%d user entries, want 60", steps)
	}
}

func TestForkOfALongLogCopiesAllOfIt(t *testing.T) {
	dir := t.TempDir()
	m, st, _, _ := windowed(t, dir, 64<<10)
	tid := longLog(t, st, t.TempDir(), 300)
	if _, _, err := m.Resume(tid); err != nil {
		t.Fatal(err)
	}
	info, hist, text, err := m.Fork(tid, "u250")
	if err != nil {
		t.Fatal(err)
	}
	if text != "message 250" {
		t.Fatalf("text = %q", text)
	}
	forked, _, err := st.LoadEntries(info.ID)
	if err != nil {
		t.Fatal(err)
	}
	stored, _, _ := st.LoadEntries(tid)
	if len(forked) != 250*5 {
		t.Fatalf("the fork has %d entries, want %d", len(forked), 250*5)
	}
	for i := range forked {
		if forked[i].ID != stored[i].ID || forked[i].Text != stored[i].Text ||
			forked[i].Tool != nil && forked[i].Tool.Output != stored[i].Tool.Output {
			t.Fatalf("fork entry %d = %+v, want %+v", i, forked[i], stored[i])
		}
	}
	var users int
	for msg := range hist.All() {
		if em, ok := msg.(EventMsg); ok && em.FromUser() {
			users++
		}
	}
	if users != 250 {
		t.Fatalf("the fork's history has %d user messages", users)
	}
	if n, _ := held(m, info.ID); n*entryCost > int(m.window) {
		t.Fatalf("the fork holds %d entries", n)
	}
}

// appendRaw adds text, as it is, to the end of thread id's log in dir.
func appendRaw(dir, id, text string) error {
	f, err := os.OpenFile(filepath.Join(dir, id+".jsonl"), os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.WriteString(text); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

func TestATornLogStopsTheWindowAtWhatWasRead(t *testing.T) {
	dir := t.TempDir()
	m, st, _, c := windowed(t, dir, 64<<10, turn(say("ok"), finish()))
	tid := longLog(t, st, t.TempDir(), 100)
	if err := appendRaw(dir, tid, `{"type":"user","id":"torn","Text":"half`); err != nil {
		t.Fatal(err)
	}
	if _, _, err := m.Resume(tid); err != nil {
		t.Fatal(err)
	}
	th := m.thread(tid)
	th.mu.Lock()
	synced, n := th.disk.synced, th.disk.n
	th.mu.Unlock()
	if synced || n != 500 {
		t.Fatalf("synced %v, %d entries in the store", synced, n)
	}
	if err := m.Post(tid, "$claude more"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the turn", func() bool { return c.turnDones("claude") == 1 })
	m.Flush()
	th.mu.Lock()
	defer th.mu.Unlock()
	if th.disk.n != 500 || th.base > 500 {
		t.Fatalf("the window went past the torn line: n %d, base %d", th.disk.n, th.base)
	}
	if got := allLog(th); len(got) != th.size() || got[len(got)-1].Kind != agent.KindTurnDone {
		t.Fatalf("log = %d entries, size %d", len(got), th.size())
	}
}
