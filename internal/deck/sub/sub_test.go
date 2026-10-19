package sub

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"testing"
)

func write(t *testing.T, root, p, s string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(p))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, root, p string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(p)))
	if err != nil {
		return "<missing>"
	}
	return string(b)
}

// trees is a store and two work trees with the same files, as two agents'
// worktrees would start.
func trees(t *testing.T) (*Store, string, string) {
	t.Helper()
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a, b := t.TempDir(), t.TempDir()
	for _, r := range []string{a, b} {
		write(t, r, "keep.txt", "same\n")
		write(t, r, "edit.txt", "old\n")
		write(t, r, "gone.txt", "bye\n")
		write(t, r, "bin.dat", "\x00\x01\x02")
	}
	return s, a, b
}

func TestCaptureApply(t *testing.T) {
	ctx := context.Background()
	s, a, b := trees(t)
	base, err := s.Snap(ctx, a)
	if err != nil {
		t.Fatal(err)
	}
	write(t, a, "edit.txt", "new\n")
	write(t, a, "dir/added.txt", "hi\n")
	write(t, a, "bin.dat", "\x00\xff")
	os.Remove(filepath.Join(a, "gone.txt"))

	id, err := s.Capture(ctx, a, base, "", Meta{Source: "a", Task: "t1"})
	if err != nil || id == "" {
		t.Fatalf("capture: %q %v", id, err)
	}
	c, _ := s.Capsule(id)
	if len(c.Ops) != 4 || c.Meta.Task != "t1" {
		t.Fatalf("ops = %+v", c.Ops)
	}
	if err := s.Apply(ctx, b, id); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"keep.txt", "edit.txt", "dir/added.txt", "bin.dat", "gone.txt"} {
		if read(t, a, p) != read(t, b, p) {
			t.Errorf("%s: %q vs %q", p, read(t, a, p), read(t, b, p))
		}
	}
	// Applying again is a no-op that still fits.
	if err := s.Apply(ctx, b, id); err != nil {
		t.Fatalf("reapply: %v", err)
	}
	if err := s.Revert(ctx, b, id); err != nil {
		t.Fatal(err)
	}
	if read(t, b, "edit.txt") != "old\n" || read(t, b, "gone.txt") != "bye\n" || read(t, b, "dir/added.txt") != "<missing>" {
		t.Fatal("revert did not restore")
	}
}

func TestConflictWritesNothing(t *testing.T) {
	ctx := context.Background()
	s, a, b := trees(t)
	base, _ := s.Snap(ctx, a)
	write(t, a, "edit.txt", "from a\n")
	write(t, a, "keep.txt", "also a\n")
	id, _ := s.Capture(ctx, a, base, "", Meta{})
	write(t, b, "edit.txt", "from b\n")

	err := s.Apply(ctx, b, id)
	var ce *ConflictError
	if !errors.As(err, &ce) || len(ce.Paths) != 1 || ce.Paths[0] != "edit.txt" {
		t.Fatalf("err = %v", err)
	}
	if read(t, b, "keep.txt") != "same\n" || read(t, b, "edit.txt") != "from b\n" {
		t.Fatal("a conflicting apply wrote files")
	}
}

func TestChainSplit(t *testing.T) {
	ctx := context.Background()
	s, a, b := trees(t)
	base, _ := s.Snap(ctx, a)
	for i := range MaxFiles + 7 {
		write(t, a, fmt.Sprintf("many/%03d", i), fmt.Sprint(i))
	}
	id, err := s.Capture(ctx, a, base, "", Meta{})
	if err != nil {
		t.Fatal(err)
	}
	chain, _ := s.Chain(id)
	if len(chain) != 2 || len(chain[0].Ops) != MaxFiles || len(chain[1].Ops) != 7 {
		t.Fatalf("chain = %d", len(chain))
	}
	if err := s.Apply(ctx, b, id); err != nil {
		t.Fatal(err)
	}
	if read(t, b, "many/000") != "0" || read(t, b, "many/206") != "206" {
		t.Fatal("chain not applied")
	}
}

func TestChunksDedup(t *testing.T) {
	s, _ := Open(t.TempDir())
	big := make([]byte, 1<<20)
	rand.New(rand.NewSource(1)).Read(big)
	f1, _ := s.PutFile(big)
	edited := append(append(append([]byte{}, big[:500000]...), "inserted"...), big[500000:]...)
	f2, _ := s.PutFile(edited)
	c1, _ := s.Chunks(f1)
	c2, _ := s.Chunks(f2)
	have := map[ID]bool{}
	for _, c := range c1 {
		have[c] = true
	}
	var fresh int
	for _, c := range c2 {
		if !have[c] {
			fresh++
		}
	}
	if fresh > 2 {
		t.Fatalf("an insert changed %d of %d chunks", fresh, len(c2))
	}
	got, _ := s.ReadFile(f2)
	if !bytes.Equal(got, edited) || FileID(edited) != f2 {
		t.Fatal("round trip")
	}
}

func TestRestore(t *testing.T) {
	ctx := context.Background()
	s, a, _ := trees(t)
	snap, _ := s.Snap(ctx, a)
	write(t, a, "edit.txt", "changed\n")
	write(t, a, "extra.txt", "x")
	if err := s.Restore(ctx, a, snap, []string{"edit.txt", "extra.txt"}); err != nil {
		t.Fatal(err)
	}
	if read(t, a, "edit.txt") != "old\n" || read(t, a, "extra.txt") != "<missing>" {
		t.Fatal("restore")
	}
	for _, p := range []string{"../x", ".git/config", "a/.GIT/hooks/x", ".git./config", ".g\u200cit/config", "GIT~1/config"} {
		if err := s.Restore(ctx, a, snap, []string{p}); err == nil {
			t.Fatalf("bad path %q accepted", p)
		}
	}
	if err := validPath(".github/workflows/ci.yml"); err != nil {
		t.Fatal(err)
	}
}

func TestMerge3(t *testing.T) {
	base := "a\nb\nc\nd\ne\n"
	got, ok := Merge3(base, "A\nb\nc\nd\ne\n", "a\nb\nc\nd\nE\n")
	if !ok || got != "A\nb\nc\nd\nE\n" {
		t.Fatalf("clean merge: %q %v", got, ok)
	}
	if _, ok := Merge3(base, "a\nX\nc\nd\ne\n", "a\nY\nc\nd\ne\n"); ok {
		t.Fatal("same line changed both ways merged")
	}
	if _, ok := Merge3(base, base+"ours\n", base+"theirs\n"); ok {
		t.Fatal("two insertions at one point merged")
	}
}

func TestMergeApply(t *testing.T) {
	ctx := context.Background()
	s, a, b := trees(t)
	lines := "1\n2\n3\n4\n5\n6\n7\n8\n"
	write(t, a, "m.txt", lines)
	write(t, b, "m.txt", lines)
	base, _ := s.Snap(ctx, a)
	write(t, a, "m.txt", "one\n2\n3\n4\n5\n6\n7\n8\n")
	write(t, a, "edit.txt", "a's\n")
	id, _ := s.Capture(ctx, a, base, "", Meta{})
	write(t, b, "m.txt", "1\n2\n3\n4\n5\n6\n7\neight\n")
	write(t, b, "edit.txt", "b's\n")

	if _, err := s.MergeApply(ctx, b, id, Refuse); err == nil {
		t.Fatal("conflict on edit.txt not refused")
	}
	if read(t, b, "m.txt") != "1\n2\n3\n4\n5\n6\n7\neight\n" {
		t.Fatal("refused merge wrote")
	}
	prev, err := s.MergeApply(ctx, b, id, Ours)
	if err != nil {
		t.Fatal(err)
	}
	if read(t, b, "m.txt") != "one\n2\n3\n4\n5\n6\n7\neight\n" || read(t, b, "edit.txt") != "b's\n" {
		t.Fatalf("ours: %q %q", read(t, b, "m.txt"), read(t, b, "edit.txt"))
	}
	states := map[string]State{}
	for _, p := range prev {
		states[p.Path] = p.State
	}
	if states["m.txt"] != StateMerge || states["edit.txt"] != StateConflict {
		t.Fatalf("states = %v", states)
	}
}

func TestPackUnpack(t *testing.T) {
	ctx := context.Background()
	s, a, _ := trees(t)
	base, _ := s.Snap(ctx, a)
	write(t, a, "edit.txt", "new\n")
	id, _ := s.Capture(ctx, a, base, "", Meta{})

	far, _ := Open(t.TempDir())
	ids, err := s.Closure(id)
	if err != nil {
		t.Fatal(err)
	}
	want := far.Missing(ids)
	var buf bytes.Buffer
	if err := s.Pack(&buf, want); err != nil {
		t.Fatal(err)
	}
	got, err := far.Unpack(&buf, 1<<20)
	if err != nil || len(got) != len(want) {
		t.Fatalf("unpack %d of %d: %v", len(got), len(want), err)
	}
	if len(far.Missing(ids)) != 0 {
		t.Fatal("still missing objects")
	}
	b := t.TempDir()
	write(t, b, "edit.txt", "old\n")
	if err := far.Apply(ctx, b, id); err != nil || read(t, b, "edit.txt") != "new\n" {
		t.Fatalf("apply from unpacked: %v", err)
	}

	if s.Shareable(id) {
		t.Fatal("unpublished capsule shareable")
	}
	if _, err := s.Publish(id); err != nil {
		t.Fatal(err)
	}
	if !s.Shareable(id) || s.Shareable(base) || len(s.Published()) != 1 {
		t.Fatal("publish")
	}
}

// A contract's base: a snapshot's closure carries the whole tree, and a
// store that got it checks the tree out into an empty folder.
func TestSnapshotTravels(t *testing.T) {
	ctx := context.Background()
	s, a, _ := trees(t)
	write(t, a, "dir/new.txt", "x\n")
	snap, _ := s.Snap(ctx, a)
	ids, err := s.Closure(snap)
	if err != nil || len(ids) < 3 || ids[0] != snap {
		t.Fatalf("closure = %v, %v", ids, err)
	}
	far, _ := Open(t.TempDir())
	var buf bytes.Buffer
	if err := s.Pack(&buf, ids); err != nil {
		t.Fatal(err)
	}
	if _, err := far.Unpack(&buf, 1<<20); err != nil {
		t.Fatal(err)
	}
	b := t.TempDir()
	if err := far.Checkout(ctx, b, snap); err != nil {
		t.Fatal(err)
	}
	if read(t, b, "edit.txt") != read(t, a, "edit.txt") || read(t, b, "dir/new.txt") != "x\n" {
		t.Fatal("the checkout differs from the tree")
	}
	if again, _ := far.Snap(ctx, b); again != snap {
		t.Fatalf("snapshot of the checkout = %s, want %s", again, snap)
	}
}

func TestGC(t *testing.T) {
	ctx := context.Background()
	s, a, _ := trees(t)
	keep, _ := s.Snap(ctx, a)
	write(t, a, "edit.txt", "garbage\n")
	drop, _ := s.Snap(ctx, a)
	n, err := s.GC([]ID{keep}, 0)
	if err != nil || n == 0 {
		t.Fatalf("gc removed %d: %v", n, err)
	}
	if s.Has(drop) || !s.Has(keep) {
		t.Fatal("gc kept the wrong objects")
	}
	if err := s.Restore(ctx, a, keep, []string{"edit.txt"}); err != nil || read(t, a, "edit.txt") != "old\n" {
		t.Fatalf("restore after gc: %v", err)
	}
}

// A reviewer applies a's capsule, fixes the same file and captures on top:
// the fix chains onto a's capsule, so applying it to an untouched tree
// brings both, and a tree that already has a's change takes just the fix.
func TestParentChain(t *testing.T) {
	ctx := context.Background()
	s, a, b := trees(t)
	main := t.TempDir()
	write(t, main, "edit.txt", "old\n")
	base, _ := s.Snap(ctx, a)
	write(t, a, "edit.txt", "v1\n")
	first, _ := s.Capture(ctx, a, base, "", Meta{Source: "a"})

	if err := s.Apply(ctx, b, first); err != nil {
		t.Fatal(err)
	}
	rbase, _ := s.Snap(ctx, b)
	write(t, b, "edit.txt", "v2\n")
	fix, err := s.Capture(ctx, b, rbase, first, Meta{Source: "reviewer"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Apply(ctx, main, fix); err != nil || read(t, main, "edit.txt") != "v2\n" {
		t.Fatalf("fresh tree: %v %q", err, read(t, main, "edit.txt"))
	}
	if err := s.Apply(ctx, a, fix); err != nil || read(t, a, "edit.txt") != "v2\n" {
		t.Fatalf("tree with the first capsule: %v", err)
	}
	if err := s.Revert(ctx, main, fix); err != nil || read(t, main, "edit.txt") != "old\n" {
		t.Fatalf("revert chain: %v %q", err, read(t, main, "edit.txt"))
	}
}
