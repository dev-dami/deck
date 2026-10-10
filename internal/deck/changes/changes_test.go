package changes

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// newRepo makes a git repository with one commit holding a.txt and b.txt, and
// an ignored build/ directory.
func newRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	gitT(t, dir, "init", "-q")
	gitT(t, dir, "config", "core.autocrlf", "false") // byte-exact content checks on every platform
	write(t, dir, "a.txt", "one\ntwo\nthree\n")
	write(t, dir, "b.txt", "bee\n")
	write(t, dir, ".gitignore", "build/\n")
	gitT(t, dir, "add", "-A")
	gitT(t, dir, "-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-q", "-m", "init")
	return dir
}

func gitT(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

func write(t *testing.T, dir, name, body string) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, dir, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(name)))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestOpenOutsideARepo(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	_, err := Open(context.Background(), t.TempDir())
	if !errors.Is(err, ErrNoRepo) {
		t.Fatalf("err = %v, want ErrNoRepo", err)
	}
}

func TestOpenFromASubdirectory(t *testing.T) {
	dir := newRepo(t)
	write(t, dir, "sub/x.txt", "x\n")
	r, err := Open(context.Background(), filepath.Join(dir, "sub"))
	if err != nil {
		t.Fatal(err)
	}
	want, _ := filepath.EvalSymlinks(dir)
	got, _ := filepath.EvalSymlinks(r.Root)
	if !strings.EqualFold(got, want) {
		t.Fatalf("Root = %q, want %q", r.Root, dir)
	}
}

func TestSnapshotDiffSeesEditsNewFilesAndDeletes(t *testing.T) {
	dir := newRepo(t)
	ctx := context.Background()
	r, err := Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	base, err := r.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}

	write(t, dir, "a.txt", "one\nTWO\nthree\n")
	write(t, dir, "new/c.txt", "see\n")
	write(t, dir, "build/out.bin", "ignored\n")
	if err := os.Remove(filepath.Join(dir, "b.txt")); err != nil {
		t.Fatal(err)
	}
	now, err := r.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	files, err := r.Diff(ctx, base, now)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, f := range files {
		switch {
		case f.New:
			got[f.Path] = "new"
		case f.Deleted:
			got[f.Path] = "deleted"
		default:
			got[f.Path] = "modified"
		}
	}
	want := map[string]string{"a.txt": "modified", "new/c.txt": "new", "b.txt": "deleted"}
	if len(got) != len(want) {
		t.Fatalf("files = %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("files = %v, want %v", got, want)
		}
	}
	for _, f := range files {
		if f.Path == "a.txt" && (f.Added != 1 || f.Removed != 1) {
			t.Fatalf("a.txt +%d -%d, want +1 -1", f.Added, f.Removed)
		}
	}

	// the user's index is exactly as it was: nothing staged, nothing dropped
	if after := gitT(t, dir, "diff", "--cached", "--name-only"); after != "" {
		t.Fatalf("snapshot staged files: %q", after)
	}
}

func TestRestoreBringsFilesBackAndRemovesNewOnes(t *testing.T) {
	dir := newRepo(t)
	ctx := context.Background()
	r, err := Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	base, err := r.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	write(t, dir, "a.txt", "changed\n")
	write(t, dir, "new/c.txt", "see\n")
	if err := os.Remove(filepath.Join(dir, "b.txt")); err != nil {
		t.Fatal(err)
	}

	if err := r.Restore(ctx, base, []string{"a.txt", "new/c.txt", "b.txt"}); err != nil {
		t.Fatal(err)
	}
	if got := read(t, dir, "a.txt"); got != "one\ntwo\nthree\n" {
		t.Fatalf("a.txt = %q", got)
	}
	if got := read(t, dir, "b.txt"); got != "bee\n" {
		t.Fatalf("b.txt = %q", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "new", "c.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("new/c.txt still there: %v", err)
	}
	if after := gitT(t, dir, "diff", "--cached", "--name-only"); after != "" {
		t.Fatalf("restore staged files: %q", after)
	}
	now, _ := r.Snapshot(ctx)
	if files, _ := r.Diff(ctx, base, now); len(files) != 0 {
		t.Fatalf("still differs after restore: %+v", files)
	}
}

// An edit that keeps a file's size and lands in the same second as the last
// index write leaves stat data that matches the index entry; git only knows
// to look at the content because the index file is as new as the entry. The
// snapshot's copy of the index must keep that.
func TestSnapshotSeesASameSizeEditInTheSameSecondAsTheIndex(t *testing.T) {
	dir := newRepo(t)
	ctx := context.Background()
	r, err := Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	a := filepath.Join(dir, "a.txt")
	at := time.Now().Add(-time.Hour).Truncate(time.Second)
	if err := os.Chtimes(a, at, at); err != nil {
		t.Fatal(err)
	}
	gitT(t, dir, "update-index", "--refresh")   // the entry takes that mtime, unsmudged
	write(t, dir, "a.txt", "ONE\nTWO\nthree\n") // same size as "one\ntwo\nthree\n"
	if err := os.Chtimes(a, at, at); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(filepath.Join(dir, ".git", "index"), at, at); err != nil {
		t.Fatal(err)
	}
	if got := gitT(t, dir, "status", "--porcelain"); !strings.Contains(got, "a.txt") {
		t.Skipf("git itself cannot see this edit here (status %q)", got)
	}
	snap, err := r.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	head := strings.TrimSpace(gitT(t, dir, "rev-parse", "HEAD^{tree}"))
	files, err := r.Diff(ctx, head, snap)
	if err != nil || len(files) != 1 || files[0].Path != "a.txt" {
		t.Fatalf("diff = %+v, %v; want a.txt changed", files, err)
	}
}

func TestRestoreOnlyTouchesTheNamedPaths(t *testing.T) {
	dir := newRepo(t)
	ctx := context.Background()
	r, _ := Open(ctx, dir)
	base, _ := r.Snapshot(ctx)
	write(t, dir, "a.txt", "agent\n")
	write(t, dir, "b.txt", "mine\n")
	if err := r.Restore(ctx, base, []string{"a.txt"}); err != nil {
		t.Fatal(err)
	}
	if got := read(t, dir, "b.txt"); got != "mine\n" {
		t.Fatalf("b.txt = %q, want untouched", got)
	}
}

func TestRestoreRefusesPathsOutsideTheTree(t *testing.T) {
	dir := newRepo(t)
	ctx := context.Background()
	r, _ := Open(ctx, dir)
	base, _ := r.Snapshot(ctx)
	for _, p := range []string{"../x", "a/../../x", "/etc/passwd", ""} {
		if err := r.Restore(ctx, base, []string{p}); err == nil {
			t.Fatalf("Restore(%q) succeeded", p)
		}
	}
}

func TestSnapshotInARepoWithoutCommits(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	gitT(t, dir, "init", "-q")
	ctx := context.Background()
	r, err := Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	base, err := r.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	write(t, dir, "first.txt", "hi\n")
	now, err := r.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	files, err := r.Diff(ctx, base, now)
	if err != nil || len(files) != 1 || !files[0].New {
		t.Fatalf("files = %+v, err = %v", files, err)
	}
	if !r.Exists(ctx, base) || r.Exists(ctx, strings.Repeat("0", 40)) {
		t.Fatal("Exists is wrong")
	}
}
