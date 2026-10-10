package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/dev-dami/deck/internal/agent"
	"github.com/dev-dami/deck/internal/config"
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

func TestCtrlKSwitchesThreadsByName(t *testing.T) {
	r := newRig(t, 120, 30)
	r.start("refactor the store")
	first := r.m.sel
	r.say("/new")
	r.start("fix the parser")
	r.feed(tea.KeyMsg{Type: tea.KeyCtrlK})
	if !r.m.pick.open || !r.has("Switch thread") {
		t.Fatalf("ctrl+k opens the switcher:\n%s", r.view())
	}
	r.typeText("store")
	r.key("enter")
	if r.m.sel != first || r.m.pick.open {
		t.Fatalf("enter picks the match: sel=%q open=%v", r.m.sel, r.m.pick.open)
	}
}

func TestScrolledUpShowsANewPillAndGGoesBack(t *testing.T) {
	r := newRig(t, 100, 20)
	r.start("go")
	run := r.claude.last()
	for i := 0; i < 30; i++ {
		r.event(run, agent.Event{Kind: agent.KindAssistantText, Text: fmt.Sprintf("line %d", i)})
	}
	r.drain()
	r.key("tab", "g") // to the top: no longer following
	r.event(run, agent.Event{Kind: agent.KindAssistantText, Text: "something new"})
	r.drain()
	if !r.has("↓ 1 new") {
		t.Fatalf("a pill counts what arrived below:\n%s", r.view())
	}
	r.key("G")
	r.drain()
	if r.has("↓ 1 new") || r.has("↓ latest") {
		t.Fatalf("G goes back to the latest:\n%s", r.view())
	}
}

func TestATurnEndsWithAFooterLine(t *testing.T) {
	r := newRig(t, 120, 30)
	r.start("go")
	run := r.claude.last()
	r.event(run, agent.Event{Kind: agent.KindAssistantText, Text: "done"})
	r.event(run, agent.Event{Kind: agent.KindFileChange, Diff: &agent.FileDiff{Path: "a.go", Added: 1}})
	r.event(run, agent.Event{Kind: agent.KindUsage, Usage: &agent.Usage{OutputTokens: 1234, CostUSD: 0.04}})
	r.event(run, agent.Event{Kind: agent.KindTurnDone})
	r.pump("footer", func() bool { return r.has("1.2k tokens · 1 file · $0.04") })
}

func TestTitleSaysWhatIsGoingOn(t *testing.T) {
	r := newRig(t, 120, 30)
	r.start("go")
	run := r.claude.last()
	r.event(run, agent.Event{Kind: agent.KindAssistantText, Text: "working", Delta: true})
	r.pump("working title", func() bool { return r.m.winTitle == "deck · 1 working" })
	r.event(run, agent.Event{Kind: agent.KindTurnDone})
	r.pump("idle title", func() bool { return r.m.winTitle == "deck" })
}

func TestNotifiesOnlyWhenTheTerminalIsInTheBackground(t *testing.T) {
	r := newRigWith(t, 120, 30, 0, func(c *config.Config) { c.Bell = true })
	var got []string
	r.m.opts.Notify = func(title, body string) tea.Cmd { got = append(got, title); return nil }
	r.m.opts.Bell = func() tea.Cmd { return nil }
	r.start("go")
	run := r.claude.last()
	r.event(run, agent.Event{Kind: agent.KindTurnDone})
	r.drain()
	if len(got) != 0 {
		t.Fatalf("focused: no notification, got %v", got)
	}
	r.feed(tea.BlurMsg{})
	r.say("again")
	run = r.claude.last()
	r.event(run, agent.Event{Kind: agent.KindTurnDone})
	r.pump("notification", func() bool { return len(got) == 1 })
	if !strings.Contains(got[0], "claude finished") {
		t.Fatalf("notification = %q", got[0])
	}
}

func TestQuestionMarkListsTheKeys(t *testing.T) {
	r := newRig(t, 120, 40)
	r.start("go")
	r.key("tab", "?")
	if !r.has("ctrl+k") || !r.has("switch thread") {
		t.Fatalf("? lists the keys:\n%s", r.view())
	}
	r.key("x")
	if r.m.ov.open {
		t.Fatal("any key closes the list")
	}
}

func TestFOnAMessageForksThereWithTheMessageInTheComposer(t *testing.T) {
	r := newRig(t, 120, 30)
	r.late = make(chan tea.Msg, 16)
	r.start("first question")
	run := r.claude.last()
	r.event(run, agent.Event{Kind: agent.KindAssistantText, Text: "first answer"})
	r.done(run, "claude") // the answer is in the thread's log before the next message
	r.say("second question")
	r.event(run, agent.Event{Kind: agent.KindTurnDone})
	r.drain()
	orig := r.m.sel

	r.key("tab")
	for i := 0; i < 20; i++ { // walk the cursor to the second question
		if it := r.tv().cursorItem(); it != nil && it.kind == kUser && it.text == "second question" {
			break
		}
		r.key("[")
	}
	r.key("f")
	r.pump("the fork", func() bool {
		select {
		case msg := <-r.late:
			r.feed(msg)
		default:
		}
		return r.m.sel != orig
	})
	if v := r.m.comp.Value(); v != "second question" {
		t.Fatalf("composer = %q, want the forked message", v)
	}
	if !r.has("first answer") || r.has("second question\n") {
		t.Fatalf("the fork shows the history before the message:\n%s", r.view())
	}
	if !strings.Contains(r.m.toast.text, "forked") {
		t.Fatalf("toast = %q", r.m.toast.text)
	}
}

func TestAtFileMentionsComplete(t *testing.T) {
	dir := t.TempDir()
	for _, f := range []string{"internal/store/store.go", "README.md", "Makefile"} {
		p := filepath.Join(dir, filepath.FromSlash(f))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		put(t, filepath.Dir(p), filepath.Base(p), "x")
	}
	r := newRig(t, 120, 30)
	r.late = make(chan tea.Msg, 16)
	r.m.newDir = dir
	r.typeText("look at @stor")
	r.gitWait("file list", func() bool { _, ok := r.m.files[dir]; return ok })
	r.typeText("e") // any key re-syncs the menu with the list in
	if !r.m.menu.open() || len(r.m.menu.items) == 0 || r.m.menu.items[0].label != "internal/store/store.go" {
		t.Fatalf("menu = %+v", r.m.menu.items)
	}
	r.key("tab")
	if v := r.m.comp.Value(); v != "look at @internal/store/store.go " {
		t.Fatalf("composer = %q", v)
	}
	if r.m.menu.open() {
		t.Fatal("the menu closes after picking")
	}

	r.m.comp.Reset()
	r.typeText("@Makef")
	r.key("enter")
	if v := r.m.comp.Value(); v != "@./Makefile " {
		t.Fatalf("a bare top-level name gets ./ so it is not an agent: %q", v)
	}
}

func TestIsolateShowsTheBranchAndUnisolateAsksAboutUnappliedWork(t *testing.T) {
	repo := gitRepo(t)
	r := changesRig(t, repo)
	r.begin("fix a")
	r.say("/isolate")
	r.gitWait("isolated", func() bool { th, _ := r.m.selThread(); return th.Branch != "" })
	th, _ := r.m.selThread()
	if !r.has(glyphBranch + " " + th.Branch) {
		t.Fatalf("the chip line shows the branch:\n%s", r.view())
	}
	put(t, th.Dir, "a.txt", "ONE\ntwo\n")

	r.say("/unisolate")
	r.gitWait("the refusal", func() bool { return strings.Contains(r.m.toast.text, "not applied") })
	if th, _ := r.m.selThread(); th.Branch == "" {
		t.Fatal("one /unisolate with unapplied work must not drop it")
	}

	r.say("/apply")
	r.gitWait("applied", func() bool { return strings.Contains(r.m.toast.text, "applied 1 file") })
	if b, _ := os.ReadFile(filepath.Join(repo, "a.txt")); string(b) != "ONE\ntwo\n" {
		t.Fatalf("shared a.txt = %q", b)
	}

	r.say("/unisolate")
	r.gitWait("back", func() bool { return strings.Contains(r.m.toast.text, "shared tree") })
	if th, _ := r.m.selThread(); th.Branch != "" || th.Dir != filepath.Clean(repo) {
		t.Fatalf("back = %+v", th)
	}
}
