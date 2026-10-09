package tui

import (
	"strings"
	"testing"

	"github.com/dev-dami/deck/internal/agent"
)

// threadChanged waits for the session's ThreadChangedMsg to reach the model.
func (r *rig) threadChanged(what string, cond func() bool) {
	r.t.Helper()
	r.pump(what, cond)
}

func rowIndex(r *rig, id string) int {
	for i, t := range r.m.rows {
		if t.ID == id {
			return i
		}
	}
	return -1
}

func TestEachThreadKeepsItsOwnDraft(t *testing.T) {
	r := newRig(t, 120, 30)
	first, second := idleThreads(r)
	r.typeText("half a thought for two")
	r.m.selectThread(first)
	if v := r.m.comp.Value(); v != "" {
		t.Fatalf("the first thread's composer = %q, want its own (empty) draft", v)
	}
	r.typeText("and one for one")
	r.m.selectThread(second)
	if v := r.m.comp.Value(); v != "half a thought for two" {
		t.Fatalf("back on two: composer = %q", v)
	}
	r.m.selectThread(first)
	if v := r.m.comp.Value(); v != "and one for one" {
		t.Fatalf("back on one: composer = %q", v)
	}
}

func TestPinMovesAThreadToTheTopAndDoneToTheBottom(t *testing.T) {
	r := newRig(t, 120, 30)
	first, second := idleThreads(r) // second is selected and listed after first
	if rowIndex(r, second) < rowIndex(r, first) {
		t.Fatal("setup: second should be listed after first")
	}
	r.say("/pin")
	r.threadChanged("pinned", func() bool { return rowIndex(r, second) == 0 })
	if !r.has(glyphPinned) {
		t.Fatalf("a pinned thread is marked:\n%s", r.view())
	}
	r.say("/pin") // unpin
	r.threadChanged("unpinned", func() bool { return rowIndex(r, second) == 1 })

	r.m.selectThread(first)
	r.say("/done")
	r.threadChanged("resolved", func() bool { return rowIndex(r, first) == len(r.m.rows)-1 })
	if !r.has(glyphResolved) || !strings.Contains(r.m.toast.text, "resolved") {
		t.Fatalf("a resolved thread shows ✓ and says so (toast %q):\n%s", r.m.toast.text, r.view())
	}
}

func TestRenameFromTheTranscriptKey(t *testing.T) {
	r := newRig(t, 120, 30)
	idleThreads(r)
	r.key("tab", "r")
	if !strings.HasPrefix(r.m.comp.Value(), "/rename ") {
		t.Fatalf("r should prefill /rename, composer = %q", r.m.comp.Value())
	}
	r.m.comp.SetValue("/rename the parser fix")
	r.key("enter")
	r.threadChanged("renamed", func() bool { th, _ := r.m.selThread(); return th.Title == "the parser fix" })
	if !r.has("the parser fix") {
		t.Fatalf("the sidebar shows the new title:\n%s", r.view())
	}
}

func TestDeleteAsksTwiceArchiveDoesNot(t *testing.T) {
	r := newRig(t, 120, 30)
	first, second := idleThreads(r)
	r.say("/delete")
	if _, ok := r.m.mgr.Thread(second); !ok {
		t.Fatal("one /delete must not delete")
	}
	if !strings.Contains(r.m.toast.text, "/delete again") {
		t.Fatalf("toast = %q", r.m.toast.text)
	}
	r.say("/delete")
	r.threadChanged("deleted", func() bool { return rowIndex(r, second) < 0 })
	if _, ok := r.m.mgr.Thread(second); ok {
		t.Fatal("the second /delete deletes")
	}

	if r.m.sel != first {
		r.m.selectThread(first)
	}
	r.say("/archive")
	r.threadChanged("archived", func() bool { return rowIndex(r, first) < 0 })
}

func TestNewsInAnotherThreadShowsAnUnreadDot(t *testing.T) {
	r := newRig(t, 120, 30)
	r.start("one")
	first := r.m.sel
	run := r.claude.last()
	r.say("/new")
	r.start("two")
	r.event(run, agent.Event{Kind: agent.KindAssistantText, Text: "done over here"})
	r.event(run, agent.Event{Kind: agent.KindTurnDone})
	r.pump("unread", func() bool { return r.m.unread[first] })
	if !r.has(glyphUnread) {
		t.Fatalf("the other thread shows a dot:\n%s", r.view())
	}
	r.m.selectThread(first)
	if r.m.unread[first] {
		t.Fatal("opening the thread clears its dot")
	}
}

