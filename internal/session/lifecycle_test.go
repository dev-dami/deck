package session

import (
	"errors"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

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
