package sub

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

// put stores s as a file and returns its id.
func put(t *testing.T, s *Store, body string) ID {
	t.Helper()
	id, err := s.PutFile([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestBuildAppliesLikeACapture(t *testing.T) {
	ctx := context.Background()
	s, _, b := trees(t)
	id, err := s.Build([]Op{
		{Path: "edit.txt", Before: put(t, s, "old\n"), After: put(t, s, "new\n")},
		{Path: "gone.txt", Before: put(t, s, "bye\n")},
		{Path: "dir/added.txt", After: put(t, s, "hi\n"), Exec: true},
	}, "", Meta{Source: "codex", Note: "from git"})
	if err != nil || id == "" {
		t.Fatal(id, err)
	}
	c, _ := s.Capsule(id)
	if base, err := s.Snapshot(c.Base); err != nil || len(base.Files) != 2 {
		t.Fatalf("base = %+v %v (only the files touched, as they were)", base, err)
	}
	if err := s.Apply(ctx, b, id); err != nil {
		t.Fatal(err)
	}
	if read(t, b, "edit.txt") != "new\n" || read(t, b, "gone.txt") != "<missing>" || read(t, b, "dir/added.txt") != "hi\n" {
		t.Fatal("not applied")
	}
	if err := s.Revert(ctx, b, id); err != nil || read(t, b, "edit.txt") != "old\n" {
		t.Fatalf("revert: %v", err)
	}
}

func TestRebaseMergesAndReportsConflicts(t *testing.T) {
	ctx := context.Background()
	s, a, b := trees(t)
	base := "one\ntwo\nthree\nfour\nfive\n"
	write(t, a, "code.txt", base)
	write(t, b, "code.txt", base)
	snap, _ := s.Snap(ctx, a)
	write(t, a, "code.txt", "ONE\ntwo\nthree\nfour\nfive\n")
	id, err := s.Capture(ctx, a, snap, "", Meta{})
	if err != nil {
		t.Fatal(err)
	}

	// b moved on other lines: the rebase merges and fits b as is.
	write(t, b, "code.txt", "one\ntwo\nthree\nfour\nFIVE\n")
	nid, prev, err := s.Rebase(ctx, id, s.TreeOnto(b), "", Meta{Note: "rebased"})
	if err != nil || nid == "" || prev[0].State != StateMerge {
		t.Fatalf("rebase = %q %+v %v", nid, prev, err)
	}
	if err := s.Verify(ctx, b, nid); err != nil {
		t.Fatalf("the rebased capsule does not fit its new base: %v", err)
	}
	if err := s.Apply(ctx, b, nid); err != nil || read(t, b, "code.txt") != "ONE\ntwo\nthree\nfour\nFIVE\n" {
		t.Fatalf("apply: %v %q", err, read(t, b, "code.txt"))
	}

	// The same lines changed: nothing is stored, the file is named.
	write(t, b, "code.txt", "uno\ntwo\nthree\nfour\nfive\n")
	nid, prev, err = s.Rebase(ctx, id, s.TreeOnto(b), "", Meta{})
	var ce *ConflictError
	if !errors.As(err, &ce) || nid != "" || ce.Paths[0] != "code.txt" || prev[0].State != StateConflict {
		t.Fatalf("conflict = %q %+v %v", nid, prev, err)
	}
}

func TestStackChainsOntoTheCapsuleBelow(t *testing.T) {
	ctx := context.Background()
	s, a, b := trees(t)
	write(t, a, "code.txt", "1\n2\n3\n4\n5\n6\n")
	write(t, b, "code.txt", "1\n2\n3\n4\n5\n6\n")
	snap, _ := s.Snap(ctx, a)
	write(t, a, "code.txt", "1\n2\n3\n4\n5\nsix\n")
	write(t, a, "edit.txt", "first\n")
	under, _ := s.Capture(ctx, a, snap, "", Meta{})
	// Another capsule made from the same base, on other lines.
	write(t, a, "code.txt", "one\n2\n3\n4\n5\n6\n")
	write(t, a, "edit.txt", "old\n")
	top, _ := s.Capture(ctx, a, snap, "", Meta{})

	nid, _, err := s.Stack(ctx, top, under, Meta{})
	if err != nil {
		t.Fatal(err)
	}
	c, _ := s.Capsule(nid)
	if c.Parent != under {
		t.Fatalf("parent = %s", c.Parent)
	}
	if err := s.Apply(ctx, b, nid); err != nil {
		t.Fatal(err)
	}
	if got := read(t, b, "code.txt"); got != "one\n2\n3\n4\n5\nsix\n" {
		t.Fatalf("stacked = %q", got)
	}
}

// TestLargeCapsuleStaysFast builds a capsule of 3000 files (a chain of 15)
// and reads one page of it: listing must not read every file.
func TestLargeCapsuleStaysFast(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	const n = 3000
	ops := make([]Op, n)
	before, after := put(t, s, "a\nb\nc\n"), put(t, s, "a\nB\nc\nd\n")
	for i := range ops {
		ops[i] = Op{Path: fmt.Sprintf("pkg%02d/file%04d.go", i%40, i), Before: before, After: after}
	}
	start := time.Now()
	id, err := s.Build(ops, "", Meta{})
	if err != nil {
		t.Fatal(err)
	}
	chain, _ := s.Chain(id)
	if len(chain) != n/MaxFiles {
		t.Fatalf("chain of %d", len(chain))
	}
	page, total, err := s.Page(id, 100, 50)
	if err != nil || total != n || len(page) != 50 {
		t.Fatalf("page: %d of %d, %v", len(page), total, err)
	}
	if page[0].Added != 2 || page[0].Removed != 1 || !strings.HasPrefix(page[0].Path, "pkg") {
		t.Fatalf("stat = %+v", page[0])
	}
	if d := time.Since(start); d > 10*time.Second {
		t.Fatalf("a %d-file capsule took %v", n, d)
	}
}
