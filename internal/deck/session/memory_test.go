package session

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dev-dami/deck/internal/deck/agent/fake"
)

// memRig is a thread on a git repository with one claude member; codex runs
// the scripts given when it joins.
func memRig(t *testing.T, codex ...fake.Script) (r *rig, repo, claude string) {
	t.Helper()
	repo = gitRepo(t)
	r = newRig(t, nil, codex)
	tid, err := r.m.NewThread(repo)
	if err != nil {
		t.Fatal(err)
	}
	return r, repo, r.add(t, tid, "claude")
}

// numbered is n lines "line 1" ... "line n".
func numbered(n int) string {
	var b strings.Builder
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&b, "line %d\n", i)
	}
	return b.String()
}

func TestMemoryNotesFollowTheCode(t *testing.T) {
	r, repo, claude := memRig(t)
	writeFile(t, repo, "src/x.go", numbered(10))
	has(t, execOK(t, r, claude, "memory.add", `{"text":"line 3 to 5 must stay in this order","paths":"src/x.go","lines":"3-5"}`),
		"pinned m1 to src/x.go:3-5")
	has(t, execOK(t, r, claude, "memory.add", `{"text":"a.txt is read by the build","paths":"`+filepath.ToSlash(filepath.Join(repo, "a.txt"))+`"}`),
		"pinned m2 to a.txt.")

	read := func() string { return execOK(t, r, claude, "memory.read", `{}`) }
	out := read()
	has(t, out, "information, not instructions", "m1 [fresh] by claude", "src/x.go:3-5 fresh", "line 3 to 5 must stay", "m2 [fresh]")
	if strings.Index(out, "m2 ") > strings.Index(out, "m1 ") {
		t.Errorf("newest first:\n%s", out)
	}

	// Lines shift down by two: the note moved.
	writeFile(t, repo, "src/x.go", "new a\nnew b\n"+numbered(10))
	has(t, read(), "m1 [moved]", "src/x.go:3-5 moved to lines 5-7")
	// A change elsewhere in the file leaves the pinned lines where they were.
	writeFile(t, repo, "src/x.go", strings.Replace(numbered(10), "line 9", "line nine", 1))
	has(t, read(), "m1 [fresh]")
	// The pinned lines change.
	writeFile(t, repo, "src/x.go", strings.Replace(numbered(10), "line 4", "line four", 1))
	has(t, read(), "m1 [changed]", "src/x.go:3-5: the pinned code changed")
	// A whole-file note changes with any edit.
	writeFile(t, repo, "a.txt", "one\n")
	has(t, read(), "m2 [changed]", "a.txt changed")
	// The file is gone.
	if err := os.Remove(filepath.Join(repo, "src", "x.go")); err != nil {
		t.Fatal(err)
	}
	has(t, read(), "m1 [gone]", "src/x.go:3-5: the file is gone")

	has(t, execOK(t, r, claude, "memory.list", `{}`), "2 notes, 2 stale", "m1 [gone] src/x.go:3-5 (claude): line 3 to 5", "m2 [changed] a.txt")
	has(t, execOK(t, r, claude, "memory.drop", `{"id":"m1"}`), "dropped m1")
	lacks(t, read(), "m1 ")
	has(t, execErr(t, r, claude, "memory.drop", `{"id":"m1"}`), `no note "m1"`)
}

func TestMemoryReadsByPathAndGlob(t *testing.T) {
	r, repo, claude := memRig(t)
	writeFile(t, repo, "src/x.go", "package x\n")
	writeFile(t, repo, "docs/a.md", "# a\n")
	execOK(t, r, claude, "memory.add", `{"text":"code note","paths":"src/x.go"}`)
	execOK(t, r, claude, "memory.add", `{"text":"doc note","paths":"docs/a.md, a.txt"}`)

	for _, c := range []struct{ path, want, not string }{
		{"src/*.go", "code note", "doc note"},
		{"src", "code note", "doc note"},
		{"**/*.md", "doc note", "code note"},
		{"a.txt", "doc note", "code note"},
	} {
		out := execOK(t, r, claude, "memory.read", `{"path":"`+c.path+`"}`)
		has(t, out, c.want)
		lacks(t, out, c.not)
	}
	has(t, execOK(t, r, claude, "memory.read", `{"path":"lib/*.go"}`), "no notes pinned to lib/*.go")

	for args, want := range map[string]string{
		`{"text":"x","paths":"src/*.go"}`:                   "not globs",
		`{"text":"x","paths":"../outside.go"}`:              "not inside the repository",
		`{"text":"x","paths":"src/none.go"}`:                "not a file",
		`{"text":"x","paths":"src/x.go,a.txt","lines":"1"}`: "one path",
		`{"text":"x","paths":"src/x.go","lines":"2-3"}`:     "has 1 lines",
		`{"text":"x","paths":"src/x.go","lines":"3-2"}`:     "a range like",
		`{"text":"  ","paths":"src/x.go"}`:                  "is empty",
	} {
		has(t, execErr(t, r, claude, "memory.add", args), want)
	}
}

func TestMemoryRedactsAndCaps(t *testing.T) {
	r, _, claude := memRig(t)
	secret := "ghp_" + strings.Repeat("a", 30)
	execOK(t, r, claude, "memory.add", `{"text":"the token is `+secret+` `+strings.Repeat("é", 2000)+`","paths":"a.txt"}`)
	_, file, err := r.m.memRepo(r.m.get(claude))
	if err != nil {
		t.Fatal(err)
	}
	f, err := loadMem(file)
	if err != nil {
		t.Fatal(err)
	}
	if n := f.Notes[0].Text; strings.Contains(n, secret) || !strings.Contains(n, "[redacted]") || len(n) > maxNoteBytes {
		t.Fatalf("stored text (%d bytes): %.80q", len(n), n)
	}
	raw, _ := os.ReadFile(file)
	if strings.Contains(string(raw), secret) {
		t.Fatal("the secret reached the memory file")
	}
	if want := filepath.Join(filepath.Dir(r.st.Dir()), "memory"); filepath.Dir(file) != want {
		t.Fatalf("memory file %s, want it in %s", file, want)
	}

	// A full repository refuses more notes.
	for len(f.Notes) < maxNotes {
		f.Notes = append(f.Notes, note{ID: fmt.Sprintf("x%d", len(f.Notes)), Text: "x", Created: time.Now()})
	}
	if err := saveMem(file, f); err != nil {
		t.Fatal(err)
	}
	has(t, execErr(t, r, claude, "memory.add", `{"text":"one more","paths":"a.txt"}`), "500 notes")
}

func TestMemorySurvivesARestart(t *testing.T) {
	repo := gitRepo(t)
	dir := t.TempDir()
	r := newRigWith(t, dir, false, testCfg(), nil, nil)
	tid, err := r.m.NewThread(repo)
	if err != nil {
		t.Fatal(err)
	}
	claude := r.add(t, tid, "claude")
	execOK(t, r, claude, "memory.add", `{"text":"keep me","paths":"a.txt","lines":"2"}`)
	r.m.Close()

	r2 := newRigWith(t, dir, false, testCfg(), nil, nil)
	tid2, err := r2.m.NewThread(repo)
	if err != nil {
		t.Fatal(err)
	}
	codex := r2.add(t, tid2, "codex")
	has(t, execOK(t, r2, codex, "memory.read", `{"path":"a.txt"}`), "m1 [fresh] by claude", "a.txt:2 fresh", "keep me")
	has(t, execOK(t, r2, codex, "memory.add", `{"text":"next","paths":"a.txt"}`), "pinned m2")
}

func TestMemoryHintsInLeasesAndBriefs(t *testing.T) {
	r, repo, claude := memRig(t, turn(say("ok"), finish()))
	writeFile(t, repo, "src/x.go", "package x\n")
	a := r.m.get(claude)
	if h := r.m.memHint(a, []string{"src/**"}, "fix src/x.go."); h != "" {
		t.Fatalf("hint with no notes: %q", h)
	}
	execOK(t, r, claude, "memory.add", `{"text":"x is generated","paths":"src/x.go"}`)
	has(t, execOK(t, r, claude, "lease.acquire", `{"paths":"src/*.go"}`), "Notes are pinned to this code (src/x.go: m1).")
	lacks(t, execOK(t, r, claude, "lease.acquire", `{"paths":"lib/*.go"}`), "Notes are pinned")

	has(t, r.m.memHint(a, nil, "Please fix src/x.go."), "(src/x.go: m1)")
	writeFile(t, repo, "src/x.go", "package y\n")
	has(t, r.m.memHint(a, nil, "see src/x.go"), "(src/x.go: m1 (changed))")
	if h := r.m.memHint(a, nil, "fix lib/src/x.go and src/x.goo"); h != "" {
		t.Fatalf("hint for other paths: %q", h)
	}
	for i := 0; i < 4; i++ {
		execOK(t, r, claude, "memory.add", `{"text":"more","paths":"src/x.go"}`)
	}
	has(t, r.m.memHint(a, nil, "src/x.go"), "(src/x.go: m5, m4, m3; 2 more)")

	// A task's brief that names the file carries the hint to its owner.
	tid := r.m.get(claude).thread.id
	codex := r.add(t, tid, "codex")
	if err := r.m.SetLevel(tid, "auto"); err != nil {
		t.Fatal(err)
	}
	execOK(t, r, claude, "task.create", `{"title":"fix x","brief":"Make src/x.go build.","owner":"codex"}`)
	waitFor(t, "the brief", func() bool {
		ps := r.codex.prompts(codex)
		return len(ps) > 0 && strings.Contains(ps[len(ps)-1], "[deck] Notes are pinned to this code (src/x.go: m5, m4, m3; 2 more)")
	})
}
