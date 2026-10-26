package hub

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dev-dami/deck/internal/deck/agent"
	"github.com/dev-dami/deck/internal/deck/store"
)

func TestSkillCacheSeesChanges(t *testing.T) {
	env := testEnv(t)
	writeFile(t, filepath.Join(env.Dir, ".claude", "skills", "one", "SKILL.md"), "---\nname: one\ndescription: First.\n---\n")
	names := func() string {
		var ns []string
		for _, s := range discoverSkills(&env) {
			if !s.Builtin {
				ns = append(ns, s.Name+"="+s.Description)
			}
		}
		return strings.Join(ns, ",")
	}
	if got := names(); got != "one=First." {
		t.Fatalf("first = %q", got)
	}
	if got := names(); got != "one=First." {
		t.Fatalf("cached = %q", got)
	}
	// A skill in a new group folder, and a new home folder.
	writeFile(t, filepath.Join(env.Dir, ".claude", "skills", "grp", "two", "SKILL.md"), "---\nname: two\ndescription: Second.\n---\n")
	writeFile(t, filepath.Join(env.Home, ".codex", "skills", "three", "SKILL.md"), "---\nname: three\ndescription: Third.\n---\n")
	if got := names(); got != "two=Second.,one=First.,three=Third." { // paths sort: grp/two before one
		t.Fatalf("after adding = %q", got)
	}
	// An edited SKILL.md (a different size) is read again.
	writeFile(t, filepath.Join(env.Dir, ".claude", "skills", "one", "SKILL.md"), "---\nname: one\ndescription: First, edited.\n---\n")
	if got := names(); !strings.Contains(got, ",one=First, edited.,") {
		t.Fatalf("after editing = %q", got)
	}
	// A removed skill goes.
	if err := os.RemoveAll(filepath.Join(env.Dir, ".claude", "skills", "grp")); err != nil {
		t.Fatal(err)
	}
	if got := names(); got != "one=First, edited.,three=Third." {
		t.Fatalf("after removing = %q", got)
	}
}

func TestHistoryCacheSeesAppends(t *testing.T) {
	env := testEnv(t)
	st, err := store.New(filepath.Join(t.TempDir(), "threads"))
	if err != nil {
		t.Fatal(err)
	}
	env.Store, env.Roots = st, []string{env.Dir}
	th, err := st.Create(store.Thread{Title: "cache", Dir: env.Dir})
	if err != nil {
		t.Fatal(err)
	}
	add := func(text string) {
		t.Helper()
		if err := st.AppendEntry(th.ID, store.Entry{Type: store.EntryUser, Speaker: "dami", Event: agent.Event{Text: text, Time: time.Now()}}); err != nil {
			t.Fatal(err)
		}
	}
	add("the walrus sleeps")
	if got, _ := run(t, env, "history.search", `{"query":"walrus"}`); !strings.Contains(got, "walrus sleeps") {
		t.Fatalf("first search = %q", got)
	}
	add("the narwhal swims")
	if got, _ := run(t, env, "history.search", `{"query":"narwhal"}`); !strings.Contains(got, "narwhal swims") {
		t.Fatalf("search after append = %q", got)
	}
	if got, _ := run(t, env, "history.read", `{"thread":"`+th.ID+`"}`); !strings.Contains(got, "walrus") || !strings.Contains(got, "narwhal") {
		t.Fatalf("read after append = %q", got)
	}
}

func TestCapOutCountsDroppedOutput(t *testing.T) {
	big := strings.Repeat("x", 300<<10)
	got := capOut(big, 0, MaxOutput)
	if !strings.Contains(got, "truncated at 20 KB of 300 KB") {
		t.Fatalf("note: %q", got[len(got)-100:])
	}
	got = capOut(strings.Repeat("y", 84<<10), 216<<10, MaxOutput)
	if !strings.Contains(got, "truncated at 20 KB of 300 KB") {
		t.Fatalf("note with dropped: %q", got[len(got)-100:])
	}
	// Short output is untouched but still redacted.
	if got := capOut("token ghp_abcdefghijklmnopqrstuvwxyz0123", 0, MaxOutput); strings.Contains(got, "ghp_") {
		t.Fatalf("not redacted: %q", got)
	}
	// A secret near the cut is redacted in what is shown.
	s := strings.Repeat("a", MaxOutput-30) + " ghp_abcdefghijklmnopqrstuvwxyz0123 " + strings.Repeat("b", 200<<10)
	if got := capOut(s, 0, MaxOutput); strings.Contains(got, "ghp_") {
		t.Fatalf("secret at the cut leaked")
	}
}

// TestEnvMaxOutput covers an agent's own output budget: 0 is the default,
// and no budget goes past MaxOutputCap.
func TestEnvMaxOutput(t *testing.T) {
	for in, want := range map[int]int{0: MaxOutput, -1: MaxOutput, 40 << 10: 40 << 10, 1 << 20: MaxOutputCap} {
		if got := (&Env{MaxOutput: in}).maxOut(); got != want {
			t.Errorf("maxOut(%d) = %d, want %d", in, got, want)
		}
	}
	got := capOut(strings.Repeat("z", 100<<10), 0, (&Env{MaxOutput: 48 << 10}).maxOut())
	if len(got) > 48<<10+200 || !strings.Contains(got, "truncated at 48 KB of 100 KB") {
		t.Fatalf("48 KB budget: %d bytes, note %q", len(got), got[len(got)-80:])
	}
}

func TestGitDiffInARealRepo(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	dir := t.TempDir()
	g := func(args ...string) {
		t.Helper()
		c := exec.Command("git", args...)
		c.Dir = dir
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	g("init", "-q")
	writeFile(t, filepath.Join(dir, "f.txt"), "a\nb\nc\n")
	g("add", ".")
	g("-c", "user.email=t@example.com", "-c", "user.name=t", "commit", "-qm", "init")
	env := Env{Dir: dir}
	if got, isErr := Execute(t.Context(), env, "git.diff", nil); isErr || !strings.Contains(got, "No unstaged changes") {
		t.Fatalf("clean diff = %q %v", got, isErr)
	}
	writeFile(t, filepath.Join(dir, "f.txt"), "a\nB\nc\n")
	got, isErr := Execute(t.Context(), env, "git.diff", nil)
	if isErr || !strings.Contains(got, " f.txt | 2 +-\n") || !strings.Contains(got, "(-)\n\ndiff --git a/f.txt b/f.txt") || !strings.Contains(got, "+B\n") {
		t.Fatalf("diff = %q %v", got, isErr)
	}
	// A huge diff is cut, and the note still gives its full size.
	writeFile(t, filepath.Join(dir, "f.txt"), strings.Repeat("0123456789abcdef\n", 20000))
	got, isErr = Execute(t.Context(), env, "git.diff", json.RawMessage(`{}`))
	if isErr || !strings.Contains(got, "truncated at 20 KB of") || len(got) > MaxOutput+200 {
		t.Fatalf("huge diff: %d bytes, %v, tail %q", len(got), isErr, got[max(len(got)-120, 0):])
	}
	var kb int
	tail := got[strings.LastIndex(got, " of ")+4:]
	if _, err := fmt.Sscan(tail, &kb); err != nil || kb < 300 {
		t.Fatalf("total %q", tail)
	}
}
