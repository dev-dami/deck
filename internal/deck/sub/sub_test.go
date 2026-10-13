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

	id, err := s.Capture(ctx, a, base, Meta{Source: "a", Task: "t1"})
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
	id, _ := s.Capture(ctx, a, base, Meta{})
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
	id, err := s.Capture(ctx, a, base, Meta{})
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
	if err := s.Restore(ctx, a, snap, []string{"../x"}); err == nil {
		t.Fatal("bad path accepted")
	}
}
