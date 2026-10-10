package session

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/dev-dami/deck/internal/deck/agent"
	"github.com/dev-dami/deck/internal/deck/agent/fake"
	"github.com/dev-dami/deck/internal/deck/config"
)

// gitRepo makes a repository with one commit holding a.txt.
func gitRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q"},
		{"config", "core.autocrlf", "false"},
	} {
		gitIn(t, dir, args...)
	}
	writeFile(t, dir, "a.txt", "one\ntwo\n")
	gitIn(t, dir, "add", "-A")
	gitIn(t, dir, "-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-q", "-m", "init")
	return dir
}

func gitIn(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func writeFile(t *testing.T, dir, name, body string) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, dir, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(name)))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// edited is a step reporting a change to path, as the CLIs do after an edit.
func edited(path string, add, del int) fake.Step {
	return fake.Step{Event: agent.Event{Kind: agent.KindFileChange, Diff: &agent.FileDiff{
		Path: path, Added: add, Removed: del,
		Hunks: []agent.Hunk{{OldStart: 1, OldLines: 1, NewStart: 1, NewLines: 1, Lines: []agent.DiffLine{{Op: agent.OpDel, Text: "one"}, {Op: agent.OpAdd, Text: "ONE"}}}},
	}}}
}

func trackingCfg() (c config.Config) {
	c = testCfg()
	c.TrackChanges = true
	return c
}

func TestChangesComeFromGitAndNameTheAgents(t *testing.T) {
	repo := gitRepo(t)
	r := newRigWith(t, t.TempDir(), false, trackingCfg(), []fake.Script{
		turn(edited(filepath.Join(repo, "a.txt"), 1, 1), say("done"), finish()),
		turn(say("ok"), finish()),
	}, nil)
	tid, err := r.m.NewThread(repo)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.m.Post(tid, "fix a"); err != nil {
		t.Fatal(err)
	}
	// what the agent did, and a file a shell command made
	writeFile(t, repo, "a.txt", "ONE\ntwo\n")
	writeFile(t, repo, "gen/out.txt", "made by a script\n")
	waitFor(t, "claude's turn", func() bool { return r.c.turnDones("claude") >= 1 })

	ctx := context.Background()
	cs, err := r.m.Changes(ctx, tid, "")
	if err != nil {
		t.Fatal(err)
	}
	if !cs.Git || cs.Note != "" {
		t.Fatalf("Git = %v, Note = %q", cs.Git, cs.Note)
	}
	if len(cs.Checkpoints) != 1 || cs.Since != cs.Checkpoints[0].EntryID || cs.Checkpoints[0].Text != "fix a" {
		t.Fatalf("checkpoints = %+v, since %q", cs.Checkpoints, cs.Since)
	}
	got := map[string][]string{}
	for _, f := range cs.Files {
		got[f.Path] = f.Agents
	}
	if len(got) != 2 || strings.Join(got["a.txt"], ",") != "claude" || len(got["gen/out.txt"]) != 0 {
		t.Fatalf("files = %v", got)
	}
	if _, ok := got["gen/out.txt"]; !ok {
		t.Fatalf("files = %v, want the script's file too", got)
	}
	if cs.Added() != 2 || cs.Removed() != 1 {
		t.Fatalf("+%d -%d, want +2 -1", cs.Added(), cs.Removed())
	}
}

func TestRevertPutsFilesBackTellsTheAgentsAndCanBeUndone(t *testing.T) {
	repo := gitRepo(t)
	r := newRigWith(t, t.TempDir(), false, trackingCfg(), []fake.Script{
		turn(edited(filepath.Join(repo, "a.txt"), 1, 1), finish()),
		turn(say("ok"), finish()),
	}, nil)
	tid, _ := r.m.NewThread(repo)
	if err := r.m.Post(tid, "fix a"); err != nil {
		t.Fatal(err)
	}
	writeFile(t, repo, "a.txt", "ONE\ntwo\n")
	writeFile(t, repo, "keep.txt", "not reverted\n")
	waitFor(t, "claude's turn", func() bool { return r.c.turnDones("claude") >= 1 })

	ctx := context.Background()
	cs, err := r.m.Changes(ctx, tid, "")
	if err != nil {
		t.Fatal(err)
	}
	rid, err := r.m.Revert(ctx, tid, cs.Since, []string{"a.txt"})
	if err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, repo, "a.txt"); got != "one\ntwo\n" {
		t.Fatalf("a.txt = %q after revert", got)
	}
	if got := readFile(t, repo, "keep.txt"); got != "not reverted\n" {
		t.Fatalf("keep.txt = %q, want untouched", got)
	}
	waitFor(t, "the revert message", func() bool {
		return r.c.count(func(m tea.Msg) bool {
			rm, ok := m.(RevertedMsg)
			return ok && rm.EntryID == rid && rm.Since == "fix a" && !rm.Undo && len(rm.Files) == 1
		}) == 1
	})

	// the agent hears of it with its next message
	if err := r.m.Post(tid, "carry on"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "claude's second turn", func() bool { return r.c.turnDones("claude") >= 2 })
	ps := r.claude.prompts("claude")
	has(t, ps[len(ps)-1], `the user reverted 1 file to how they were before the message "fix a": a.txt`, "carry on")

	if err := r.m.UndoRevert(ctx, tid, rid); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, repo, "a.txt"); got != "ONE\ntwo\n" {
		t.Fatalf("a.txt = %q after undo", got)
	}

	// both show up when the thread's history is replayed
	n := 0
	for _, m := range r.m.History(tid) {
		if rm, ok := m.(RevertedMsg); ok {
			n++
			if rm.Since != "fix a" {
				t.Fatalf("replayed %+v", rm)
			}
		}
	}
	if n != 2 {
		t.Fatalf("history has %d reverts, want 2", n)
	}
}

func TestRevertToALaterCheckpointKeepsEarlierWork(t *testing.T) {
	repo := gitRepo(t)
	r := newRigWith(t, t.TempDir(), false, trackingCfg(), []fake.Script{
		turn(finish()), turn(finish()), turn(finish()),
	}, nil)
	tid, _ := r.m.NewThread(repo)
	_ = r.m.Post(tid, "first")
	waitFor(t, "turn 1", func() bool { return r.c.turnDones("claude") >= 1 })
	writeFile(t, repo, "a.txt", "first\n")
	_ = r.m.Post(tid, "second")
	waitFor(t, "turn 2", func() bool { return r.c.turnDones("claude") >= 2 })
	writeFile(t, repo, "a.txt", "second\n")

	ctx := context.Background()
	cs, _ := r.m.Changes(ctx, tid, "")
	if len(cs.Checkpoints) != 2 {
		t.Fatalf("checkpoints = %+v", cs.Checkpoints)
	}
	later := cs.Checkpoints[1].EntryID
	since, err := r.m.Changes(ctx, tid, later)
	if err != nil || since.Since != later || len(since.Files) != 1 || since.Files[0].Added != 1 || since.Files[0].Removed != 1 {
		t.Fatalf("since second: %+v, %v", since, err)
	}
	if _, err := r.m.Revert(ctx, tid, later, []string{"a.txt"}); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, repo, "a.txt"); got != "first\n" {
		t.Fatalf("a.txt = %q, want the state before the second message", got)
	}
}

func TestChangesOutsideGitFallBackToWhatTheAgentsReported(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	plain := t.TempDir()
	r := newRigWith(t, t.TempDir(), false, trackingCfg(), []fake.Script{
		turn(edited(filepath.Join(plain, "x.go"), 1, 1), edited(filepath.Join(plain, "x.go"), 1, 1), finish()),
	}, nil)
	tid, _ := r.m.NewThread(plain)
	_ = r.m.Post(tid, "go")
	waitFor(t, "claude's turn", func() bool { return r.c.turnDones("claude") >= 1 })

	ctx := context.Background()
	cs, err := r.m.Changes(ctx, tid, "")
	if err != nil {
		t.Fatal(err)
	}
	if cs.Git || !strings.Contains(cs.Note, "not a git repository") {
		t.Fatalf("Git = %v, Note = %q", cs.Git, cs.Note)
	}
	if len(cs.Files) != 1 || cs.Files[0].Path != "x.go" || len(cs.Files[0].Hunks) != 2 || cs.Files[0].Added != 2 {
		t.Fatalf("files = %+v", cs.Files)
	}
	if _, err := r.m.Revert(ctx, tid, cs.Since, []string{"x.go"}); err == nil {
		t.Fatal("Revert outside git succeeded")
	}
}

func TestTrackingOffTakesNoSnapshots(t *testing.T) {
	repo := gitRepo(t)
	r := newRigWith(t, t.TempDir(), false, testCfg(), []fake.Script{turn(finish())}, nil)
	tid, _ := r.m.NewThread(repo)
	_ = r.m.Post(tid, "hi")
	waitFor(t, "claude's turn", func() bool { return r.c.turnDones("claude") >= 1 })
	th := r.m.thread(tid)
	th.mu.Lock()
	defer th.mu.Unlock()
	for _, e := range th.log {
		if e.Snap != "" {
			t.Fatalf("entry %s has a snapshot with tracking off", e.ID)
		}
	}
}
