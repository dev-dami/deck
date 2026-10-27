package session

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dev-dami/deck/internal/deck/agent/fake"
	"github.com/dev-dami/deck/internal/deck/changes"
	"github.com/dev-dami/deck/internal/deck/sub"
)

// workRig is a rig with a thread on a git repository whose first turn
// ended, so the thread is stored.
func workRig(t *testing.T) (*rig, string, string) {
	t.Helper()
	repo := gitRepo(t)
	r := newRig(t, []fake.Script{turn(say("hi"), finish())}, nil)
	tid, err := r.m.NewThread(repo)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.m.Post(tid, "hello"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "first turn", func() bool { return r.c.turnDones("claude") >= 1 })
	return r, tid, repo
}

func commitIn(t *testing.T, dir, msg string) {
	t.Helper()
	gitIn(t, dir, "add", "-A")
	gitIn(t, dir, "-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-q", "-m", msg)
}

func TestWorktreeLifecycle(t *testing.T) {
	r, tid, repo := workRig(t)
	branch := gitOut(t, repo, "rev-parse", "--abbrev-ref", "HEAD")

	in, err := r.m.CreateTree(tid, TreeSpec{Name: "Big Refactor", Task: "t9"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(in.Name, "-big-refactor") || in.Branch != "deck/"+in.Name || in.BaseRef != branch || in.Kind != TreeOwn {
		t.Fatalf("created %+v", in)
	}
	if _, err := r.m.CreateTree(tid, TreeSpec{Name: "big refactor"}); err == nil {
		t.Fatal("a second worktree of the same name")
	}

	// Work in it: an edit committed, one not.
	writeFile(t, in.Path, "a.txt", "one\ntwo\nthree\n")
	commitIn(t, in.Path, "three")
	writeFile(t, in.Path, "b.txt", "bee\n")
	in, _ = r.m.TreeStatus(tid, in.Name)
	if in.Ahead != 1 || in.Behind != 0 || in.Dirty != 1 || !slices.Equal(in.Files, []string{"a.txt", "b.txt"}) {
		t.Fatalf("status %+v", in)
	}

	// The shared branch moves on other lines: sync merges it in.
	writeFile(t, repo, "c.txt", "sea\n")
	commitIn(t, repo, "c")
	if in, err = r.m.SyncTree(tid, in.Name, false, ""); err != nil {
		t.Fatal(err)
	}
	if readFile(t, in.Path, "c.txt") != "sea\n" || in.Dirty != 0 || in.Behind != 0 {
		t.Fatalf("synced %+v", in)
	}

	// Then on the same lines: sync stops and changes nothing.
	writeFile(t, repo, "a.txt", "one\ntwo\nfour\n")
	commitIn(t, repo, "a")
	writeFile(t, in.Path, "b.txt", "bee two\n")
	_, err = r.m.SyncTree(tid, in.Name, true, "")
	var ce *changes.ConflictError
	if !errors.As(err, &ce) || !slices.Equal(ce.Paths, []string{"a.txt"}) {
		t.Fatalf("sync with a conflict: %v", err)
	}
	if readFile(t, in.Path, "a.txt") != "one\ntwo\nthree\n" {
		t.Fatal("the conflicting sync changed the worktree")
	}

	// Landing: the check names the conflict and writes nothing; ours
	// keeps the shared tree's a.txt and lands the rest.
	res, err := r.m.LandTree(tid, in.Name, "", true, sub.Refuse, "")
	if err != nil || !res.Checked || !slices.Equal(res.Conflicts(), []string{"a.txt"}) {
		t.Fatalf("check = %+v %v", res, err)
	}
	if _, err := os.Stat(filepath.Join(repo, "b.txt")); err == nil {
		t.Fatal("the check wrote")
	}
	if _, err := r.m.LandTree(tid, in.Name, "", false, sub.Refuse, ""); err == nil {
		t.Fatal("landed over a conflict")
	}
	res, err = r.m.LandTree(tid, in.Name, "", false, sub.Ours, "")
	if err != nil {
		t.Fatal(err)
	}
	if readFile(t, repo, "b.txt") != "bee two\n" || readFile(t, repo, "a.txt") != "one\ntwo\nfour\n" {
		t.Fatalf("landed: b=%q a=%q", readFile(t, repo, "b.txt"), readFile(t, repo, "a.txt"))
	}
	if b := gitOut(t, repo, "rev-parse", "--abbrev-ref", "HEAD"); b != branch {
		t.Fatalf("your branch moved: %q", b)
	}
	if staged := gitOut(t, repo, "diff", "--cached", "--name-only"); staged != "" {
		t.Fatalf("your index was touched: %q", staged)
	}
	if res.Capsule != in.Name || res.Version != 1 {
		t.Fatalf("landed as %s v%d (the check made v1, the same capsule)", res.Capsule, res.Version)
	}
	// Landed, unchanged and nobody's: pruned, branch and all.
	if _, err := os.Stat(in.Path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the landed worktree is still there: %v", err)
	}
	if b := gitOut(t, repo, "branch", "--list", in.Branch); b != "" {
		t.Fatalf("its branch is still there: %q", b)
	}
	if len(r.m.Trees(tid)) != 0 {
		t.Fatalf("trees = %+v", r.m.Trees(tid))
	}
	caps := r.m.CapsuleViews(tid)
	if len(caps) != 1 || caps[0].State != "landed" || caps[0].Source != FromWorktree || caps[0].Versions[0].CommitsTotal < 1 {
		t.Fatalf("capsules = %+v", caps)
	}
}

func TestWorktreeDiscardAndRestart(t *testing.T) {
	r, tid, repo := workRig(t)
	keep, err := r.m.CreateTree(tid, TreeSpec{Name: "keep"})
	if err != nil {
		t.Fatal(err)
	}
	drop, err := r.m.CreateTree(tid, TreeSpec{Name: "drop"})
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, keep.Path, "k.txt", "kept\n")
	writeFile(t, drop.Path, "d.txt", "dropped\n")
	if err := r.m.DiscardTree(tid, drop.Name, "someone"); err != nil {
		t.Fatalf("a worktree nobody owns: %v", err)
	}
	if _, err := os.Stat(drop.Path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("discarded worktree still on disk")
	}
	// One removed behind Deck's back, while it is stopped.
	gone, _ := r.m.CreateTree(tid, TreeSpec{Name: "gone"})
	r.m.Close()
	gitIn(t, repo, "worktree", "remove", "--force", gone.Path)

	r2 := newRigIn(t, r.dir, []fake.Script{turn(say("again"), finish())}, nil)
	if _, _, err := r2.m.Resume(tid); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "prune", func() bool { return len(r2.m.Trees(tid)) == 1 })
	in := r2.m.Trees(tid)[0]
	if in.Name != keep.Name || len(in.Files) != 1 || in.Files[0] != "k.txt" {
		t.Fatalf("after restart: %+v", in)
	}
	if th := stored(t, r2, tid); len(th.Trees) != 1 || th.Trees[0].Fork == "" {
		t.Fatalf("stored = %+v", th.Trees)
	}
}

func TestSpawnIntoAWorktree(t *testing.T) {
	r, tid, _ := workRig(t)
	if err := r.m.SetOrchestrate(tid, true); err != nil {
		t.Fatal(err)
	}
	in, err := r.m.CreateTree(tid, TreeSpec{Name: "helper"})
	if err != nil {
		t.Fatal(err)
	}
	res, err := r.m.Spawn(tid, "", SpawnSpec{Kind: "codex", Brief: "work there", Tree: in.Name})
	if err != nil {
		t.Fatal(err)
	}
	if res.Branch != in.Branch {
		t.Fatalf("spawned on %q", res.Branch)
	}
	trees := r.m.Trees(tid)
	if len(trees) != 1 || trees[0].Kind != TreeAgent || trees[0].Owner != res.AgentID || trees[0].Task != res.TaskID {
		t.Fatalf("trees = %+v", trees)
	}
	if isolate, tree := spawnTree(false, json.RawMessage(`"helper"`)); isolate || tree != "helper" {
		t.Fatal("worktree: name")
	}
	if isolate, tree := spawnTree(false, json.RawMessage(`true`)); !isolate || tree != "" {
		t.Fatal("worktree: true")
	}
}

func TestCapsulesFromEachSource(t *testing.T) {
	r, tid, repo := workRig(t)
	// commits: a range, and one commit
	writeFile(t, repo, "x.txt", "x\n")
	commitIn(t, repo, "x")
	writeFile(t, repo, "y.txt", "y\n")
	writeFile(t, repo, "a.txt", "one\ntwo\nmore\n")
	commitIn(t, repo, "y")
	name, ver, err := r.m.CreateCapsule(tid, CapsuleSpec{Name: "Two Commits", Source: FromCommits, Range: "HEAD~2..HEAD"})
	if err != nil || name != "two-commits" || ver != 1 {
		t.Fatalf("%q %d %v", name, ver, err)
	}
	c, _, _ := r.m.thread(tid).findCapsule(name)
	if v := c.Versions[0]; v.Files != 3 || v.Add != 2 || v.Mod != 1 || v.CommitsTotal != 2 {
		t.Fatalf("version = %+v", v)
	}
	one, _, err := r.m.CreateCapsule(tid, CapsuleSpec{Source: FromCommits, Range: "HEAD"})
	if err != nil {
		t.Fatal(err)
	}
	if c, _, _ := r.m.thread(tid).findCapsule(one); c.Versions[0].Files != 2 {
		t.Fatalf("one commit = %+v", c.Versions[0])
	}

	// diff: the uncommitted changes
	writeFile(t, repo, "x.txt", "x changed\n")
	diff, _, err := r.m.CreateCapsule(tid, CapsuleSpec{Name: "wip", Source: FromDiff})
	if err != nil {
		t.Fatal(err)
	}
	files, total, err := r.m.CapsuleFiles(tid, diff, 0, 10)
	if err != nil || total != 1 || files[0].Path != "x.txt" || files[0].Added != 1 || files[0].Removed != 1 {
		t.Fatalf("files = %+v %d %v", files, total, err)
	}
	if d, err := r.m.CapsuleFileDiff(tid, diff, "x.txt"); err != nil || !strings.Contains(d, "+x changed") {
		t.Fatalf("diff = %q %v", d, err)
	}

	// worktree: the whole branch, as versions of one capsule
	in, _ := r.m.CreateTree(tid, TreeSpec{Name: "w"})
	writeFile(t, in.Path, "w.txt", "w\n")
	if _, ver, err := r.m.CreateCapsule(tid, CapsuleSpec{Source: FromWorktree, Worktree: in.Name}); err != nil || ver != 1 {
		t.Fatalf("worktree capsule: %d %v", ver, err)
	}
	writeFile(t, in.Path, "w2.txt", "w2\n")
	if _, ver, _ := r.m.CreateCapsule(tid, CapsuleSpec{Source: FromWorktree, Worktree: in.Name}); ver != 2 {
		t.Fatalf("second version = %d", ver)
	}
	if _, ver, _ := r.m.CreateCapsule(tid, CapsuleSpec{Source: FromWorktree, Worktree: in.Name}); ver != 2 {
		t.Fatal("an unchanged worktree made a new version")
	}

	// review and tests are kept with it
	if err := r.m.ReviewCapsule(tid, in.Name, "", "w.txt", "looks fine", "pass", "go test ./..."); err != nil {
		t.Fatal(err)
	}
	if th := stored(t, r, tid); len(th.Capsules) != 4 || th.Capsules[3].Tests != "pass" || th.Capsules[3].Comments[0].Path != "w.txt" {
		t.Fatalf("stored = %+v", th.Capsules)
	}
}

func TestCapsuleLandRebaseRevert(t *testing.T) {
	r, tid, repo := workRig(t)
	base := "1\n2\n3\n4\n5\n6\n"
	writeFile(t, repo, "f.txt", base)
	commitIn(t, repo, "f")
	in, _ := r.m.CreateTree(tid, TreeSpec{Name: "w"})
	writeFile(t, in.Path, "f.txt", "one\n2\n3\n4\n5\n6\n")
	name, _, err := r.m.CreateCapsule(tid, CapsuleSpec{Source: FromWorktree, Worktree: in.Name})
	if err != nil {
		t.Fatal(err)
	}

	// The shared tree changed the same line: a conflict, reported per file.
	writeFile(t, repo, "f.txt", "uno\n2\n3\n4\n5\n6\n")
	res, err := r.m.LandCapsule(tid, name, "", true, sub.Refuse, "")
	if err != nil || len(res.Conflicts()) != 1 {
		t.Fatalf("check = %+v %v", res, err)
	}
	if _, err := r.m.RebaseCapsule(tid, name, "main", ""); err == nil {
		t.Fatal("rebased over a conflict")
	}

	// Other lines: it merges three-way, and a rebase makes a version that
	// fits the shared tree as is.
	writeFile(t, repo, "f.txt", "1\n2\n3\n4\n5\nsix\n")
	res, err = r.m.RebaseCapsule(tid, name, "main", "")
	if err != nil || res.Version != 2 {
		t.Fatalf("rebase = %+v %v", res, err)
	}
	if err := r.m.subStore().Verify(t.Context(), repo, sub.ID(res.ID)); err != nil {
		t.Fatalf("the rebased version does not fit: %v", err)
	}
	if _, err := r.m.LandCapsule(tid, name, "", false, sub.Refuse, ""); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, repo, "f.txt"); got != "one\n2\n3\n4\n5\nsix\n" {
		t.Fatalf("landed f.txt = %q", got)
	}
	if err := r.m.RevertCapsule(tid, name, "", ""); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, repo, "f.txt"); got != "1\n2\n3\n4\n5\nsix\n" {
		t.Fatalf("reverted f.txt = %q", got)
	}
	if v := r.m.CapsuleViews(tid); v[0].State != "reverted" || len(v[0].Landings) != 2 {
		t.Fatalf("views = %+v", v)
	}

	// Stacked on another capsule, it chains onto it.
	writeFile(t, repo, "g.txt", "g\n")
	under, _, _ := r.m.CreateCapsule(tid, CapsuleSpec{Name: "under", Source: FromDiff})
	res, err = r.m.RebaseCapsule(tid, name+"@1", "capsule:"+under, "")
	if err != nil {
		t.Fatal(err)
	}
	if c, _ := r.m.subStore().Capsule(sub.ID(res.ID)); c.Parent == "" {
		t.Fatal("the stacked version does not chain onto the capsule under it")
	}
}

// TestLargeWorktreeCapsule captures a worktree that changed 600 files
// (three capsules of a chain) and lands it: big work stays a few seconds.
func TestLargeWorktreeCapsule(t *testing.T) {
	r, tid, repo := workRig(t)
	for i := range 600 {
		writeFile(t, repo, fmt.Sprintf("pkg/f%03d.txt", i), "a\nb\nc\n")
	}
	commitIn(t, repo, "many")
	in, _ := r.m.CreateTree(tid, TreeSpec{Name: "big"})
	for i := range 600 {
		writeFile(t, in.Path, fmt.Sprintf("pkg/f%03d.txt", i), "a\nB\nc\n")
	}
	start := time.Now()
	res, err := r.m.LandTree(tid, in.Name, "", false, sub.Refuse, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Files) != 600 || readFile(t, repo, "pkg/f599.txt") != "a\nB\nc\n" {
		t.Fatalf("landed %d files", len(res.Files))
	}
	if d := time.Since(start); d > 20*time.Second {
		t.Fatalf("600 files took %v", d)
	}
	page, total, err := r.m.CapsuleFiles(tid, res.Capsule, 590, 50)
	if err != nil || total != 600 || len(page) != 10 {
		t.Fatalf("last page: %d of %d %v", len(page), total, err)
	}
}

// TestWorkOps runs the worktree.* and capsule.* ops as an agent calls them.
func TestWorkOps(t *testing.T) {
	r, tid, _ := workRig(t)
	th := r.m.thread(tid)
	th.mu.Lock()
	a := th.members[0]
	th.mu.Unlock()
	b := subBoard{m: r.m, a: a}
	ctx := t.Context()
	out, err := b.SubOp(ctx, "worktree.create", map[string]any{"name": "mine"})
	if err != nil || !strings.Contains(out, "agent.spawn {worktree:") {
		t.Fatalf("create: %q %v", out, err)
	}
	in := r.m.Trees(tid)[0]
	if in.Owner != a.id || in.OwnerName != a.getName() {
		t.Fatalf("owner = %+v", in)
	}
	writeFile(t, in.Path, "a.txt", "one\ntwo\nmine\n")
	if out, err := b.SubOp(ctx, "worktree.list", nil); err != nil || !strings.Contains(out, in.Name) || !strings.Contains(out, "1 files changed") {
		t.Fatalf("list: %q %v", out, err)
	}
	if out, err := b.SubOp(ctx, "worktree.merge", map[string]any{"name": in.Name, "check": true}); err != nil || !strings.Contains(out, "fits") {
		t.Fatalf("check: %q %v", out, err)
	}
	if out, err := b.SubOp(ctx, "capsule.create", map[string]any{"from": "worktree", "worktree": in.Name}); err != nil || !strings.Contains(out, "thread.send {capsule:") {
		t.Fatalf("capsule from the worktree: %q %v", out, err)
	}
	if out, err := b.SubOp(ctx, "capsule.review", map[string]any{"name": in.Name, "tests": "fail", "test_note": "1 failing"}); err != nil {
		t.Fatalf("review: %q %v", out, err)
	}
	if out, _ := b.SubOp(ctx, "capsule.list", nil); !strings.Contains(out, "tests fail") {
		t.Fatalf("list: %q", out)
	}
	if _, err := b.SubOp(ctx, "worktree.discard", map[string]any{"name": in.Name}); err != nil {
		t.Fatal(err)
	}
}
