package session

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/dev-dami/deck/internal/agent"
	"github.com/dev-dami/deck/internal/agent/fake"
	"github.com/dev-dami/deck/internal/store"
)

// lifeRig is a thread with one finished claude turn, so it is in the store.
func lifeRig(t *testing.T) (*rig, string) {
	t.Helper()
	r := newRig(t, []fake.Script{turn(say("hi"), finish()), turn(say("again"), finish())}, nil)
	tid, err := r.m.NewThread(r.dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.m.Post(tid, "hello"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "claude's turn", func() bool { return r.c.turnDones("claude") >= 1 })
	return r, tid
}

func stored(t *testing.T, r *rig, id string) store.Thread {
	t.Helper()
	r.m.Flush()
	_, th, err := r.st.LoadEntries(id)
	if err != nil {
		t.Fatal(err)
	}
	return th
}

func TestRenamePinResolveAreStoredAndSurviveArchiveAndResume(t *testing.T) {
	r, tid := lifeRig(t)
	if err := r.m.RenameThread(tid, "  the  store refactor "); err != nil {
		t.Fatal(err)
	}
	if err := r.m.SetPinned(tid, true); err != nil {
		t.Fatal(err)
	}
	if err := r.m.Resolve(tid); err != nil {
		t.Fatal(err)
	}
	info, _ := r.m.Thread(tid)
	if info.Title != "the store refactor" || !info.Pinned || !info.Resolved || info.Active.IsZero() {
		t.Fatalf("info = %+v", info)
	}
	waitFor(t, "a ThreadChangedMsg per change", func() bool {
		return r.c.count(func(m tea.Msg) bool { _, ok := m.(ThreadChangedMsg); return ok }) >= 3
	})

	if err := r.m.Archive(tid); err != nil {
		t.Fatal(err)
	}
	if _, live := r.m.Thread(tid); live {
		t.Fatal("an archived thread is not live")
	}
	if th := stored(t, r, tid); !th.Archived || !th.Pinned || !th.Resolved || th.Title != "the store refactor" {
		t.Fatalf("stored = %+v", th)
	}

	info, _, err := r.m.Resume(tid)
	if err != nil {
		t.Fatal(err)
	}
	if !info.Pinned || !info.Resolved || info.Title != "the store refactor" {
		t.Fatalf("resumed = %+v", info)
	}
	if th := stored(t, r, tid); th.Archived {
		t.Fatal("resuming un-archives")
	}
}

func TestANewMessageWakesAResolvedThread(t *testing.T) {
	r, tid := lifeRig(t)
	if err := r.m.Resolve(tid); err != nil {
		t.Fatal(err)
	}
	if err := r.m.Post(tid, "one more thing"); err != nil {
		t.Fatal(err)
	}
	if info, _ := r.m.Thread(tid); info.Resolved {
		t.Fatal("posting wakes the thread")
	}
	if th := stored(t, r, tid); th.Resolved {
		t.Fatal("the wake is stored")
	}
}

func TestDeleteRemovesTheThreadForGood(t *testing.T) {
	r, tid := lifeRig(t)
	if err := r.m.Delete(tid); err != nil {
		t.Fatal(err)
	}
	r.m.Flush()
	if _, _, err := r.st.LoadEntries(tid); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("LoadEntries after delete: %v", err)
	}
	if _, live := r.m.Thread(tid); live {
		t.Fatal("deleted thread still live")
	}
}

func TestForkKeepsTheHistoryBeforeAMessageAndBriefsFreshAgents(t *testing.T) {
	r := newRig(t, []fake.Script{turn(say("hi"), finish()), turn(say("again"), finish()), turn(say("forked reply"), finish())}, nil)
	tid, _ := r.m.NewThread(r.dir)
	if err := r.m.Post(tid, "hello"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "first turn", func() bool { return r.c.turnDones("claude") >= 1 })
	if err := r.m.Post(tid, "second question"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "second turn", func() bool { return r.c.turnDones("claude") >= 2 })

	var at string
	th := r.m.thread(tid)
	th.mu.Lock()
	for _, e := range th.log {
		if e.Type == store.EntryUser && e.Text == "second question" {
			at = e.ID
		}
	}
	th.mu.Unlock()

	info, hist, text, err := r.m.Fork(tid, at)
	if err != nil {
		t.Fatal(err)
	}
	if info.ID == tid || len(info.Members) != 1 || text != "second question" {
		t.Fatalf("fork = %+v, text %q", info, text)
	}
	var said []string
	for _, h := range hist {
		if em, ok := h.(EventMsg); ok && em.Text != "" {
			said = append(said, em.Text)
		}
	}
	joined := strings.Join(said, "|")
	if !strings.Contains(joined, "hello") || !strings.Contains(joined, "hi") || strings.Contains(joined, "second question") {
		t.Fatalf("fork history = %q, want up to before the forked message", joined)
	}
	if _, live := r.m.Thread(tid); !live {
		t.Fatal("the original thread stays")
	}

	fid := info.Members[0].ID
	if err := r.m.Post(info.ID, "try another way"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the fork's agent gets a prompt", func() bool { return len(r.claude.prompts(fid)) > 0 })
	if p := r.claude.prompts(fid)[0]; !strings.Contains(p, "hello") || !strings.Contains(p, "try another way") {
		t.Fatalf("the fresh agent is briefed on the history:\n%s", p)
	}
}

func TestMoveRestartsTheAgentsInTheNewFolderBriefedOnTheThread(t *testing.T) {
	r, tid := lifeRig(t)
	to := t.TempDir()
	info, hist, err := r.m.Move(tid, to)
	if err != nil {
		t.Fatal(err)
	}
	if info.ID != tid || info.Dir != filepath.Clean(to) || len(info.Members) != 1 {
		t.Fatalf("moved = %+v", info)
	}
	for _, h := range hist {
		if em, ok := h.(EventMsg); ok && em.Kind == agent.KindExited {
			t.Fatal("closing the old agents must not land in the thread")
		}
	}
	if err := r.m.Post(tid, "carry on here"); err != nil {
		t.Fatal(err)
	}
	id := info.Members[0].ID
	waitFor(t, "the restarted agent's prompt", func() bool { return len(r.claude.prompts(id)) > 0 })
	r.claude.mu.Lock()
	last := r.claude.specs[len(r.claude.specs)-1]
	r.claude.mu.Unlock()
	if last.Dir != filepath.Clean(to) || last.ResumeID != "" {
		t.Fatalf("restarted with dir %q resume %q, want the new folder and a fresh session", last.Dir, last.ResumeID)
	}
	if p := r.claude.prompts(id)[0]; !strings.Contains(p, "hello") {
		t.Fatalf("the agent is briefed on the thread:\n%s", p)
	}
	if th := stored(t, r, tid); th.Dir != filepath.Clean(to) {
		t.Fatalf("stored dir = %q", th.Dir)
	}
}
