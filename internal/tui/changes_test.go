package tui

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/dev-dami/deck/internal/agent"
	"github.com/dev-dami/deck/internal/config"
)

// gitRepo makes a repository with one commit holding a.txt.
func gitRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q")
	run("config", "core.autocrlf", "false")
	put(t, dir, "a.txt", "one\ntwo\n")
	run("add", "-A")
	run("-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-q", "-m", "init")
	return dir
}

func put(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// changesRig is a rig whose new threads start in dir, with change tracking on.
// Commands that run git outlast exec, so their results arrive through late.
func changesRig(t *testing.T, dir string) *rig {
	t.Helper()
	r := newRigWith(t, 120, 40, 0, func(c *config.Config) { c.TrackChanges = true })
	r.late = make(chan tea.Msg, 64)
	r.m.newDir = dir
	return r
}

// gitWait is pump with time for git, which can take a second a call on Windows.
func (r *rig) gitWait(what string, cond func() bool) {
	r.t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			r.t.Fatalf("timed out waiting for %s\n%s", what, r.view())
		}
		select {
		case msg := <-r.mgr.Updates():
			r.feed(msg)
		case msg := <-r.late:
			r.feed(msg)
		case <-time.After(10 * time.Millisecond):
		}
	}
}

// begin starts a thread with text and waits until claude has it, which is
// after the message's snapshot was taken.
func (r *rig) begin(text string) *stubRun {
	r.t.Helper()
	r.say(text)
	r.gitWait("claude to get the message", func() bool {
		r.claude.mu.Lock()
		defer r.claude.mu.Unlock()
		return len(r.claude.runs) > 0
	})
	return r.claude.last()
}

func (r *rig) panelReady() {
	r.t.Helper()
	r.gitWait("the changes panel", func() bool { return r.m.chg.loaded && !r.m.chg.busy })
}

func TestChangesPanelShowsWhatChangedAndUndoes(t *testing.T) {
	repo := gitRepo(t)
	r := changesRig(t, repo)
	run := r.begin("fix a")

	// claude edits a.txt; a script makes new.txt
	put(t, repo, "a.txt", "ONE\ntwo\n")
	put(t, repo, "new.txt", "new\n")
	r.event(run, agent.Event{Kind: agent.KindFileChange, Diff: &agent.FileDiff{Path: filepath.Join(repo, "a.txt"), Added: 1, Removed: 1}})
	r.event(run, agent.Event{Kind: agent.KindTurnDone})
	r.gitWait("the header summary", func() bool { return r.has("2 files +2 −1") })

	r.key("tab", "d")
	if r.m.ov.kind != ovChanges {
		t.Fatalf("d should open the panel:\n%s", r.view())
	}
	r.panelReady()
	if !r.has("changes · since “fix a” · 2 files · +2 −1") {
		t.Fatalf("title:\n%s", r.view())
	}
	row := r.find("a.txt")
	if !strings.Contains(row, "M a.txt") || !strings.Contains(row, "◆") || !strings.Contains(row, "+1 −1") {
		t.Fatalf("a.txt row = %q", row)
	}
	if row := r.find("new.txt"); !strings.Contains(row, "A new.txt") || strings.Contains(row, "◆") {
		t.Fatalf("new.txt row = %q (no agent reported it)", row)
	}
	if !r.has("ONE") {
		t.Fatalf("a.txt's diff is not shown:\n%s", r.view())
	}

	// u twice puts the selected file back
	r.key("j", "u")
	if !r.has("u again puts back new.txt as before “fix a”") {
		t.Fatalf("the first u should ask for a second:\n%s", r.view())
	}
	if _, err := os.Stat(filepath.Join(repo, "new.txt")); err != nil {
		t.Fatal("one u must not revert anything")
	}
	r.key("u")
	r.gitWait("the revert", func() bool { return r.m.chg.undoID != "" })
	if _, err := os.Stat(filepath.Join(repo, "new.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("new.txt is still there: %v", err)
	}
	if !strings.Contains(r.m.toast.text, "put back 1 file") {
		t.Fatalf("toast = %q", r.m.toast.text)
	}
	r.panelReady()
	if len(r.m.chg.set.Files) != 1 || !r.has("1 file · +1 −1") {
		t.Fatalf("the panel should reload to one file:\n%s", r.view())
	}
	r.gitWait("the revert in the transcript", func() bool {
		for _, it := range r.tv().items {
			if it.kind == kNotice && strings.Contains(it.text, "you put back new.txt as before “fix a”") {
				return true
			}
		}
		return false
	})

	// z brings it back
	r.key("z")
	r.gitWait("the undo", func() bool { return r.m.chg.undoID == "" && !r.m.chg.busy })
	if b, err := os.ReadFile(filepath.Join(repo, "new.txt")); err != nil || string(b) != "new\n" {
		t.Fatalf("new.txt after z = %q, %v", b, err)
	}
	r.key("esc")
	if r.m.ov.open {
		t.Fatal("esc should close the panel")
	}
}

func TestChangesPanelAnyOtherKeyDisarmsUndo(t *testing.T) {
	repo := gitRepo(t)
	r := changesRig(t, repo)
	r.begin("fix a")
	put(t, repo, "a.txt", "changed\n")
	r.typeText("/changes")
	r.key("enter")
	r.panelReady()
	if r.m.ov.kind != ovChanges || len(r.m.chg.set.Files) != 1 {
		t.Fatalf("panel:\n%s", r.view())
	}
	r.key("u", "j", "u")
	time.Sleep(200 * time.Millisecond)
	if got, _ := os.ReadFile(filepath.Join(repo, "a.txt")); string(got) != "changed\n" {
		t.Fatalf("a.txt = %q: u, j, u must not revert", got)
	}
}

func TestChangesPanelOutsideGitShowsReportsWithoutUndo(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	r := changesRig(t, dir)
	run := r.begin("go")
	r.event(run, agent.Event{Kind: agent.KindFileChange, Diff: &agent.FileDiff{
		Path: filepath.Join(dir, "x.go"), Added: 1,
		Hunks: []agent.Hunk{{NewStart: 1, NewLines: 1, Lines: []agent.DiffLine{{Op: agent.OpAdd, Text: "package x"}}}},
	}})
	r.done(run, "claude")
	r.key("tab", "d")
	r.panelReady()
	if r.has("1 file +1") {
		t.Fatal("the header summary is for git-tracked changes only")
	}
	if !r.has("not a git repository") || r.find("x.go") == "" || !r.has("package x") {
		t.Fatalf("panel:\n%s", r.view())
	}
	if r.has("u undo") {
		t.Fatalf("no undo hint without git:\n%s", r.view())
	}
	r.key("u")
	if !strings.Contains(r.m.toast.text, "undo needs git") {
		t.Fatalf("u should explain; toast = %q", r.m.toast.text)
	}
}
