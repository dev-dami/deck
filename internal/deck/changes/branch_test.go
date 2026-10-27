package changes

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// branchRepo is a repository on branch main with a worktree on branch
// "line" made from it.
func branchRepo(t *testing.T) (main, wt *Repo, wtDir string) {
	t.Helper()
	dir := newRepo(t)
	gitT(t, dir, "branch", "-M", "main")
	ctx := context.Background()
	m, err := Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	wtDir = filepath.Join(t.TempDir(), "wt")
	if _, err := m.AddWorktreeAt(ctx, wtDir, "line", "main"); err != nil {
		t.Fatal(err)
	}
	w, err := Open(ctx, wtDir)
	if err != nil {
		t.Fatal(err)
	}
	return m, w, wtDir
}

func commit(t *testing.T, dir, msg string) {
	t.Helper()
	gitT(t, dir, "add", "-A")
	gitT(t, dir, "-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-q", "-m", msg)
}

func TestSyncMergesTheBaseAndCountsDivergence(t *testing.T) {
	ctx := context.Background()
	main, wt, wtDir := branchRepo(t)
	if b := wt.Branch(ctx); b != "line" {
		t.Fatalf("branch = %q", b)
	}
	write(t, wtDir, "a.txt", "one\ntwo\nthree\nfour\n")
	write(t, wtDir, "new.txt", "new\n")
	if d, err := wt.Dirty(ctx); err != nil || !slices.Equal(d, []string{"a.txt", "new.txt"}) {
		t.Fatalf("dirty = %v %v", d, err)
	}
	write(t, main.Root, "b.txt", "bee\nbuzz\n")
	commit(t, main.Root, "main moves")

	if err := wt.Sync(ctx, "main", "wip", false); err != nil {
		t.Fatal(err)
	}
	if got := read(t, wtDir, "b.txt"); got != "bee\nbuzz\n" {
		t.Fatalf("the base did not come in: %q", got)
	}
	if d, _ := wt.Dirty(ctx); len(d) != 0 {
		t.Fatalf("still dirty after sync: %v", d)
	}
	ahead, behind, err := wt.Divergence(ctx, "main", "HEAD")
	if err != nil || ahead != 2 || behind != 0 { // the wip commit and the merge
		t.Fatalf("ahead %d behind %d %v", ahead, behind, err)
	}
	cs, err := wt.Commits(ctx, "main", "HEAD")
	if err != nil || len(cs) != 2 || !slices.ContainsFunc(cs, func(c CommitRef) bool { return c.Subject == "wip" }) {
		t.Fatalf("commits = %+v %v", cs, err)
	}
}

func TestSyncConflictLeavesTheWorktreeAsItWas(t *testing.T) {
	ctx := context.Background()
	main, wt, wtDir := branchRepo(t)
	write(t, wtDir, "a.txt", "one\nTWO here\nthree\n")
	write(t, main.Root, "a.txt", "one\ntwo there\nthree\n")
	commit(t, main.Root, "main edits a")
	head, _ := wt.Commit(ctx, "HEAD")

	for _, rebase := range []bool{false, true} {
		err := wt.Sync(ctx, "main", "wip", rebase)
		var ce *ConflictError
		if !errors.As(err, &ce) || !slices.Equal(ce.Paths, []string{"a.txt"}) {
			t.Fatalf("rebase=%v: %v", rebase, err)
		}
		if got := read(t, wtDir, "a.txt"); got != "one\nTWO here\nthree\n" {
			t.Fatalf("rebase=%v: the worktree changed: %q", rebase, got)
		}
		if out := gitT(t, wtDir, "status", "--porcelain"); strings.Contains(out, "UU") {
			t.Fatalf("rebase=%v: left mid-merge: %q", rebase, out)
		}
	}
	// The work was committed on the branch (once), not lost.
	if now, _ := wt.Commit(ctx, "HEAD"); now == head {
		t.Fatal("the uncommitted work was not committed")
	}
	if conflicts, err := main.MergeCheck(ctx, "main", "line"); err != nil || !slices.Equal(conflicts, []string{"a.txt"}) {
		t.Fatalf("merge check = %v %v", conflicts, err)
	}
	if conflicts, err := main.MergeCheck(ctx, "main", "main~1"); err != nil || len(conflicts) != 0 {
		t.Fatalf("clean merge check = %v %v", conflicts, err)
	}
}

func TestChangedAndBlobsReadARange(t *testing.T) {
	ctx := context.Background()
	main, wt, wtDir := branchRepo(t)
	fork, _ := main.Commit(ctx, "main")
	write(t, wtDir, "a.txt", "one\n")
	write(t, wtDir, "dir/ü.txt", "odd\n")
	gitT(t, wtDir, "rm", "-q", "b.txt")
	tree, err := wt.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	chs, err := wt.Changed(ctx, fork, tree)
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	var blobs []string
	for _, c := range chs {
		paths = append(paths, c.Path)
		if c.NewBlob != "" {
			blobs = append(blobs, c.NewBlob)
		}
	}
	if !slices.Equal(paths, []string{"a.txt", "b.txt", "dir/ü.txt"}) {
		t.Fatalf("paths = %q", paths)
	}
	if chs[1].NewBlob != "" || chs[1].OldBlob == "" || chs[0].OldBlob == "" {
		t.Fatalf("delete/modify = %+v", chs)
	}
	var got []string
	if err := wt.Blobs(ctx, blobs, func(_ string, b []byte) error { got = append(got, string(b)); return nil }); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got, []string{"one\n", "odd\n"}) {
		t.Fatalf("blobs = %q", got)
	}
}
