package gh

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestChangedFiles(t *testing.T) {
	c, _ := newFake(reply{prefix: "git status", out: strings.Join([]string{
		"# branch.oid 1234567890abcdef",
		"# branch.head feature/login",
		"1 .M N... 100644 100644 100644 aaa bbb internal/a.go",
		"1 A. N... 000000 100644 100644 000 ccc internal/b c.go",
		"1 MM N... 100644 100644 100644 aaa bbb both.go",
		"2 R. N... 100644 100644 100644 aaa bbb R100 new.go\told.go",
		"u UU N... 100644 100644 100644 100644 h1 h2 h3 conflict.go",
		"? notes.txt",
		"! build/out",
	}, "\n")})
	got, err := c.ChangedFiles(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := []ChangedFile{
		{Code: "M", Path: "internal/a.go"},
		{Code: "A", Path: "internal/b c.go", Staged: true},
		{Code: "M", Path: "both.go", Staged: true},
		{Code: "R", Path: "new.go", Orig: "old.go", Staged: true},
		{Code: "U", Path: "conflict.go"},
		{Code: "?", Path: "notes.txt"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d files %+v, want %d", len(got), got, len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("file %d: got %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestStatusFilesAgree(t *testing.T) {
	c, f := newFake(reply{prefix: "git status", out: "# branch.head main\n? a.txt\n"})
	st, files, err := c.StatusFiles(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if st.Branch != "main" || st.Dirty != 1 || len(files) != 1 {
		t.Fatalf("got %+v %+v", st, files)
	}
	if len(f.calls) != 1 {
		t.Fatalf("one read should do: %d calls", len(f.calls))
	}
}

func TestLog(t *testing.T) {
	c, f := newFake(reply{prefix: "git log", out: "a94d819\x001791699552\x00devban\x00Sidebar panes: the shared pane contract\n" +
		"40da179\x001791699280\x00devban\x00Drop csync: go vet fails on it\n"})
	cs, err := c.Log(context.Background(), 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(cs) != 2 {
		t.Fatalf("got %+v", cs)
	}
	if cs[0].Hash != "a94d819" || cs[0].Author != "devban" || cs[0].Subject != "Sidebar panes: the shared pane contract" {
		t.Fatalf("got %+v", cs[0])
	}
	if !cs[0].When.Equal(time.Unix(1791699552, 0)) {
		t.Fatalf("when: %v", cs[0].When)
	}
	if cs[1].Subject != "Drop csync: go vet fails on it" {
		t.Fatalf("a colon in the subject: %q", cs[1].Subject)
	}
	if call := f.saw("git log -n 20"); call == nil {
		t.Fatalf("git log not asked for 20: %+v", f.calls)
	}
}

func TestLogNoCommits(t *testing.T) {
	c, _ := newFake(reply{prefix: "git log", err: &ExitError{Cmd: "git log", Code: 128,
		Stderr: "fatal: your current branch 'main' does not have any commits yet"}})
	cs, err := c.Log(context.Background(), 20)
	if err != nil || cs != nil {
		t.Fatalf("got %+v, %v", cs, err)
	}
}

func TestShow(t *testing.T) {
	out := "a94d819\x001791699552\x00devban\x00Sidebar panes\x00Body line one\n\nCo-Authored-By: x\n\x01\n\n" +
		"64\t0\tinternal/ui/model/pane.go\n" +
		"3\t1\tREADME.md\n" +
		"-\t-\tlogo.png\n"
	c, f := newFake(reply{prefix: "git show", out: out})
	d, err := c.Show(context.Background(), "a94d819")
	if err != nil {
		t.Fatal(err)
	}
	if d.Hash != "a94d819" || d.Subject != "Sidebar panes" || !strings.HasPrefix(d.Body, "Body line one") {
		t.Fatalf("got %+v", d)
	}
	if len(d.Files) != 3 || d.Added != 67 || d.Deleted != 1 {
		t.Fatalf("files %+v, +%d -%d", d.Files, d.Added, d.Deleted)
	}
	if !d.Files[2].Binary || d.Files[1] != (FileStat{Path: "README.md", Added: 3, Deleted: 1}) {
		t.Fatalf("files %+v", d.Files)
	}
	if call := f.saw("git show --numstat"); call == nil || call.args[len(call.args)-1] != "--" {
		t.Fatalf("calls %+v", f.calls)
	}
}

func TestShowRejectsBadHash(t *testing.T) {
	c, f := newFake()
	for _, h := range []string{"", "--output=x", "HEAD; rm", "zz12"} {
		if _, err := c.Show(context.Background(), h); err == nil {
			t.Errorf("%q: want an error", h)
		}
	}
	if len(f.calls) != 0 {
		t.Fatalf("nothing should run: %+v", f.calls)
	}
}
