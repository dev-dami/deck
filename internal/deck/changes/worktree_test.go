package changes

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestSplitRaw(t *testing.T) {
	out := ":100644 100644 bdc955b 8352675 M\x00bin\x00" +
		":000000 100644 0000000 8ba3a16 A\x00new\x00" +
		":100644 100644 9405325 d5e10a2 R090\x00x.txt\x00y.txt\x00" +
		"\x00diff --git a/bin b/bin\nGIT binary patch\n"
	files, patch := splitRaw(out)
	if want := []string{"bin", "new", "y.txt"}; !reflect.DeepEqual(files, want) {
		t.Fatalf("files = %q, want %q", files, want)
	}
	if patch != "diff --git a/bin b/bin\nGIT binary patch\n" {
		t.Fatalf("patch = %q", patch)
	}
	if files, patch := splitRaw(""); files != nil || patch != "" {
		t.Fatalf("empty: %q %q", files, patch)
	}
}

// TestPendingRenamesBinariesAndOddNames runs Pending against real git: a
// rename, a binary file, a delete and a path git would quote all come back
// as file names, and the patch applies to the main tree byte for byte.
func TestPendingRenamesBinariesAndOddNames(t *testing.T) {
	dir := newRepo(t)
	ctx := context.Background()
	main, err := Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	wtDir := filepath.Join(t.TempDir(), "wt")
	base, err := main.AddWorktree(ctx, wtDir, "deck-test")
	if err != nil {
		t.Fatal(err)
	}
	wt, err := Open(ctx, wtDir)
	if err != nil {
		t.Fatal(err)
	}

	if err := os.Rename(filepath.Join(wtDir, "a.txt"), filepath.Join(wtDir, "moved.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(wtDir, "b.txt")); err != nil {
		t.Fatal(err)
	}
	bin := string([]byte{0, 1, 2, 0xff, 0, 'x', '\n', 0})
	write(t, wtDir, "bin.dat", bin)
	odd := "dir/ü file.txt"
	write(t, wtDir, odd, "odd\n")

	patch, files, now, err := main.Pending(ctx, wt, base)
	if err != nil {
		t.Fatal(err)
	}
	if now == "" {
		t.Fatal("no tree id")
	}
	slices.Sort(files)
	if want := []string{"b.txt", "bin.dat", odd, "moved.txt"}; !reflect.DeepEqual(files, want) {
		t.Fatalf("files = %q, want %q", files, want)
	}
	for _, s := range []string{"rename from a.txt", "rename to moved.txt", "GIT binary patch"} {
		if !strings.Contains(patch, s) {
			t.Fatalf("patch lacks %q:\n%s", s, patch)
		}
	}

	if err := main.Apply(ctx, patch); err != nil {
		t.Fatal(err)
	}
	if got := read(t, dir, "moved.txt"); got != "one\ntwo\nthree\n" {
		t.Fatalf("moved.txt = %q", got)
	}
	if got := read(t, dir, "bin.dat"); got != bin {
		t.Fatalf("bin.dat = %q", got)
	}
	if got := read(t, dir, odd); got != "odd\n" {
		t.Fatalf("odd = %q", got)
	}
	for _, gone := range []string{"a.txt", "b.txt"} {
		if _, err := os.Stat(filepath.Join(dir, gone)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("%s still there: %v", gone, err)
		}
	}

	// Nothing changed since: empty.
	patch, files, _, err = main.Pending(ctx, wt, now)
	if err != nil || patch != "" || len(files) != 0 {
		t.Fatalf("second Pending = %q %q %v", patch, files, err)
	}
}
