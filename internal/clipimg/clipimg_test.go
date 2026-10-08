package clipimg

import (
	"context"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var pngBytes = []byte("\x89PNG\r\n\x1a\nfake-image-data")

// fakeTools makes every clipboard tool look installed.
func fakeTools(t *testing.T) {
	t.Helper()
	old := lookPath
	lookPath = func(string) (string, error) { return "/fake/bin", nil }
	t.Cleanup(func() { lookPath = old })
}

// noTools makes every clipboard tool look missing.
func noTools(t *testing.T) {
	t.Helper()
	old := lookPath
	lookPath = func(name string) (string, error) { return "", errors.New(name + ": not found") }
	t.Cleanup(func() { lookPath = old })
}

// fakeRunner replaces the command runner for the duration of the test.
func fakeRunner(t *testing.T, fn runner) {
	t.Helper()
	old := runCommand
	runCommand = fn
	t.Cleanup(func() { runCommand = old })
}

// writingRunner pretends the tool succeeded and wrote image bytes to dest.
func writingRunner(ctx context.Context, inv invocation) (int, error) {
	if err := os.WriteFile(inv.dest, pngBytes, 0o600); err != nil {
		return -1, err
	}
	return 0, nil
}

func TestCandidatePath(t *testing.T) {
	now := time.Date(2026, 1, 2, 3, 4, 5, 123_000_000, time.UTC)
	dir := filepath.Join("some", "dir")

	got := candidatePath(dir, now, 0)
	want := filepath.Join(dir, "deck-clip-20260102-030405.123.png")
	if got != want {
		t.Fatalf("candidatePath(0) = %q, want %q", got, want)
	}
	got = candidatePath(dir, now, 2)
	want = filepath.Join(dir, "deck-clip-20260102-030405.123-2.png")
	if got != want {
		t.Fatalf("candidatePath(2) = %q, want %q", got, want)
	}
}

func TestCreateUniqueNeverReusesFile(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

	p1, f1, err := createUnique(dir, now)
	if err != nil {
		t.Fatal(err)
	}
	f1.Close()
	p2, f2, err := createUnique(dir, now)
	if err != nil {
		t.Fatal(err)
	}
	f2.Close()
	if p1 == p2 {
		t.Fatalf("both calls returned %q", p1)
	}
	if filepath.Dir(p1) != dir || filepath.Ext(p1) != ".png" {
		t.Fatalf("unexpected path %q", p1)
	}
}

func TestSaveWritesImage(t *testing.T) {
	fakeTools(t)
	fakeRunner(t, writingRunner)
	dir := t.TempDir()

	path, err := Save(context.Background(), dir)
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if filepath.Dir(path) != dir || !strings.HasPrefix(filepath.Base(path), "deck-clip-") {
		t.Fatalf("path %q is not a deck-clip file in %q", path, dir)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(pngBytes) {
		t.Fatalf("file content = %q", got)
	}
}

func TestSaveDefaultDirUsesTempDeckImages(t *testing.T) {
	fakeTools(t)
	fakeRunner(t, writingRunner)
	// Point the temp dir at a test directory so nothing lands in the real one.
	t.Setenv("TMPDIR", t.TempDir())
	t.Setenv("TMP", os.Getenv("TMPDIR"))
	t.Setenv("TEMP", os.Getenv("TMPDIR"))

	path, err := Save(context.Background(), "")
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	t.Cleanup(func() { os.Remove(path) })
	if filepath.Base(filepath.Dir(path)) != "deck-images" {
		t.Fatalf("default dir: got %q", path)
	}
}

func TestSaveNoImageExitCode(t *testing.T) {
	fakeTools(t)
	fakeRunner(t, func(ctx context.Context, inv invocation) (int, error) {
		return noImageCode, nil
	})
	dir := t.TempDir()

	_, err := Save(context.Background(), dir)
	if !errors.Is(err, ErrNoImage) {
		t.Fatalf("err = %v, want ErrNoImage", err)
	}
	assertEmptyDir(t, dir)
}

func TestSaveEmptyOutputIsNoImage(t *testing.T) {
	fakeTools(t)
	fakeRunner(t, func(ctx context.Context, inv invocation) (int, error) {
		return 0, nil // success but nothing written
	})
	dir := t.TempDir()

	_, err := Save(context.Background(), dir)
	if !errors.Is(err, ErrNoImage) {
		t.Fatalf("err = %v, want ErrNoImage", err)
	}
	assertEmptyDir(t, dir)
}

func TestSaveRunnerErrorIsNotNoImage(t *testing.T) {
	fakeTools(t)
	boom := errors.New("boom")
	fakeRunner(t, func(ctx context.Context, inv invocation) (int, error) {
		return -1, boom
	})
	dir := t.TempDir()

	_, err := Save(context.Background(), dir)
	if err == nil || errors.Is(err, ErrNoImage) || !errors.Is(err, boom) {
		t.Fatalf("err = %v, want wrapped boom and not ErrNoImage", err)
	}
	assertEmptyDir(t, dir)
}

func TestSaveUnavailable(t *testing.T) {
	noTools(t)
	fakeRunner(t, func(ctx context.Context, inv invocation) (int, error) {
		t.Fatal("runner must not be called without a tool")
		return 0, nil
	})

	_, err := Save(context.Background(), t.TempDir())
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("err = %v, want ErrUnavailable", err)
	}
	if Available() {
		t.Fatal("Available() = true with no tools")
	}
}

func TestAvailableWithTools(t *testing.T) {
	fakeTools(t)
	if !Available() {
		t.Fatal("Available() = false with tools present")
	}
}

func TestSaveTimeoutPropagates(t *testing.T) {
	fakeTools(t)
	fakeRunner(t, func(ctx context.Context, inv invocation) (int, error) {
		<-ctx.Done()
		return -1, ctx.Err()
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := Save(ctx, t.TempDir())
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

func assertEmptyDir(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("dir %q has %d leftover files", dir, len(entries))
	}
}

// makeImage writes a small file and returns its path.
func makeImage(t *testing.T, dir, name string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, pngBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// fileURL builds a file:// URL for a local path, as terminals and file
// managers produce them.
func fileURL(p string) string {
	s := filepath.ToSlash(p)
	if !strings.HasPrefix(s, "/") {
		s = "/" + s
	}
	return (&url.URL{Scheme: "file", Path: s}).String()
}

func TestDetectImagePath(t *testing.T) {
	dir := t.TempDir()
	plain := makeImage(t, dir, "shot.png")
	spaced := makeImage(t, dir, "my shot.PNG")
	jpg := makeImage(t, dir, "photo.jpeg")
	gif := makeImage(t, dir, "anim.gif")
	webp := makeImage(t, dir, "pic.webp")
	txt := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(txt, []byte("hi"), 0o600); err != nil {
		t.Fatal(err)
	}
	asDir := filepath.Join(dir, "folder.png")
	if err := os.Mkdir(asDir, 0o700); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(dir, "gone.png")

	ok := []struct {
		name, in, want string
	}{
		{"plain path", plain, plain},
		{"surrounding spaces", "  " + plain + "\t", plain},
		{"double quotes", `"` + spaced + `"`, spaced},
		{"single quotes", `'` + spaced + `'`, spaced},
		{"powershell drag and drop", "& '" + spaced + "'", spaced},
		{"powershell drag with spaces", "&  \"" + plain + "\" ", plain},
		{"file url", fileURL(spaced), spaced},
		{"file url with quotes", `"` + fileURL(plain) + `"`, plain},
		{"jpg extension", jpg, jpg},
		{"gif extension", gif, gif},
		{"webp extension", webp, webp},
		{"uppercase extension", spaced, spaced},
		{"trailing newline", plain + "\r\n", plain},
	}
	for _, c := range ok {
		t.Run(c.name, func(t *testing.T) {
			got, found := DetectImagePath(c.in)
			if !found {
				t.Fatalf("DetectImagePath(%q) not found", c.in)
			}
			if got != filepath.Clean(c.want) {
				t.Fatalf("DetectImagePath(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}

	bad := []struct {
		name, in string
	}{
		{"empty", ""},
		{"whitespace", "   "},
		{"plain text", "hello world"},
		{"wrong extension", txt},
		{"missing file", missing},
		{"directory named like image", asDir},
		{"multi-line paste", plain + "\n" + spaced},
	}
	for _, c := range bad {
		t.Run(c.name, func(t *testing.T) {
			if got, found := DetectImagePath(c.in); found {
				t.Fatalf("DetectImagePath(%q) = %q, want not found", c.in, got)
			}
		})
	}
}
