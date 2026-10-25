package session

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dev-dami/deck/internal/deck/agent/fake"
)

func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func TestIsolateApplyUnisolate(t *testing.T) {
	repo := gitRepo(t)
	branch := gitOut(t, repo, "rev-parse", "--abbrev-ref", "HEAD")
	r := newRig(t, []fake.Script{turn(say("hi"), finish()), turn(say("ok"), finish())}, nil)
	tid, _ := r.m.NewThread(repo)
	if err := r.m.Post(tid, "hello"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "first turn", func() bool { return r.c.turnDones("claude") >= 1 })

	info, _, err := r.m.Isolate(tid)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(info.Branch, "deck/") || info.Dir == filepath.Clean(repo) {
		t.Fatalf("isolated = %+v", info)
	}
	wt := info.Dir
	if got := readFile(t, wt, "a.txt"); got != "one\ntwo\n" {
		t.Fatalf("worktree a.txt = %q", got)
	}
	if th := stored(t, r, tid); th.Worktree == nil || th.Worktree.Path == "" {
		t.Fatalf("stored = %+v", th)
	}

	// an agent's edit in the worktree comes back as an uncommitted edit
	writeFile(t, wt, "a.txt", "ONE\ntwo\n")
	files, err := r.m.ApplyIsolated(tid)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0] != "a.txt" {
		t.Fatalf("applied %v", files)
	}
	if got := readFile(t, repo, "a.txt"); got != "ONE\ntwo\n" {
		t.Fatalf("shared a.txt = %q", got)
	}
	if b := gitOut(t, repo, "rev-parse", "--abbrev-ref", "HEAD"); b != branch {
		t.Fatalf("your branch moved: %q -> %q", branch, b)
	}
	if staged := gitOut(t, repo, "diff", "--cached", "--name-only"); staged != "" {
		t.Fatalf("your index was touched: %q", staged)
	}
	if files, err := r.m.ApplyIsolated(tid); err != nil || len(files) != 0 {
		t.Fatalf("a second apply has nothing new: %v %v", files, err)
	}

	// unapplied work stops a plain unisolate
	writeFile(t, wt, "b.txt", "new\n")
	var ue UnappliedError
	if _, _, err := r.m.Unisolate(tid, false); !errors.As(err, &ue) || ue.Files != 1 {
		t.Fatalf("unisolate with unapplied work: %v", err)
	}
	info, _, err = r.m.Unisolate(tid, true)
	if err != nil {
		t.Fatal(err)
	}
	if info.Branch != "" || info.Dir != filepath.Clean(repo) {
		t.Fatalf("back = %+v", info)
	}
	if _, err := os.Stat(wt); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the worktree is still there: %v", err)
	}
	if b := gitOut(t, repo, "branch", "--list", "deck/*"); b != "" {
		t.Fatalf("the branch is still there: %q", b)
	}
}

func TestUndoApply(t *testing.T) {
	repo := gitRepo(t)
	r := newRig(t, []fake.Script{turn(say("hi"), finish()), turn(say("ok"), finish())}, nil)
	tid, _ := r.m.NewThread(repo)
	if err := r.m.Post(tid, "hello"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "first turn", func() bool { return r.c.turnDones("claude") >= 1 })
	if _, err := r.m.UndoApply(tid); err == nil {
		t.Fatal("undo before isolating")
	}
	info, _, err := r.m.Isolate(tid)
	if err != nil {
		t.Fatal(err)
	}
	wt := info.Dir
	if _, err := r.m.UndoApply(tid); err == nil || !strings.Contains(err.Error(), "no apply to undo") {
		t.Fatalf("undo with nothing applied: %v", err)
	}

	writeFile(t, wt, "a.txt", "ONE\ntwo\n")
	writeFile(t, wt, "b.txt", "new\n")
	if _, err := r.m.ApplyIsolated(tid); err != nil {
		t.Fatal(err)
	}
	base := stored(t, r, tid).Worktree.Applied.Base
	files, err := r.m.UndoApply(tid)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(files, ",") != "a.txt,b.txt" {
		t.Fatalf("undid %v", files)
	}
	if got := readFile(t, repo, "a.txt"); got != "one\ntwo\n" {
		t.Fatalf("shared a.txt = %q", got)
	}
	if _, err := os.Stat(filepath.Join(repo, "b.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("b.txt is still there: %v", err)
	}
	if th := stored(t, r, tid); th.Worktree.Applied != nil || th.Worktree.Base != base {
		t.Fatalf("stored after undo = %+v", th.Worktree)
	}
	if _, err := r.m.UndoApply(tid); err == nil {
		t.Fatal("one level of undo")
	}

	// The same changes apply again, and a later edit of an applied file
	// blocks the undo.
	if files, err := r.m.ApplyIsolated(tid); err != nil || len(files) != 2 {
		t.Fatalf("apply again: %v %v", files, err)
	}
	writeFile(t, repo, "a.txt", "mine\n")
	if _, err := r.m.UndoApply(tid); err == nil || !strings.Contains(err.Error(), "a.txt changed in your tree") {
		t.Fatalf("undo over a later edit: %v", err)
	}
	if got := readFile(t, repo, "b.txt"); got != "new\n" {
		t.Fatalf("a refused undo touched b.txt: %q", got)
	}
}
