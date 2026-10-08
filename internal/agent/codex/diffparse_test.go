package codex

import (
	"testing"

	"github.com/dev-dami/deck/internal/agent"
)

func TestParseUnifiedGitNewFile(t *testing.T) {
	diff := "diff --git a/hello.txt b/hello.txt\nnew file mode 100644\nindex 0000000..45b983b\n--- /dev/null\n+++ b/hello.txt\n@@ -0,0 +1 @@\n+hi\n"
	fs := ParseUnified(diff)
	if len(fs) != 1 {
		t.Fatalf("files = %d, want 1", len(fs))
	}
	f := fs[0]
	if f.Path != "hello.txt" || !f.New || f.Deleted || f.Added != 1 || f.Removed != 0 || f.OldPath != "" {
		t.Fatalf("bad file: %+v", f)
	}
	if len(f.Hunks) != 1 || f.Hunks[0].NewStart != 1 || f.Hunks[0].NewLines != 1 || f.Hunks[0].OldLines != 0 {
		t.Fatalf("bad hunk: %+v", f.Hunks)
	}
	if got := f.Hunks[0].Lines; len(got) != 1 || got[0].Op != agent.OpAdd || got[0].Text != "hi" {
		t.Fatalf("bad lines: %+v", got)
	}
}

func TestParseUnifiedModifyMultiHunk(t *testing.T) {
	diff := `diff --git a/main.go b/main.go
index 111..222 100644
--- a/main.go
+++ b/main.go
@@ -1,4 +1,5 @@ package main
 import "fmt"
-var a = 1
+var a = 2
+var b = 3

 func main() {
@@ -20,3 +21,2 @@ func other() {
 	fmt.Println(a)
-	fmt.Println("x")
 }
`
	fs := ParseUnified(diff)
	if len(fs) != 1 {
		t.Fatalf("files = %d", len(fs))
	}
	f := fs[0]
	if f.Path != "main.go" || f.New || f.Deleted {
		t.Fatalf("bad file: %+v", f)
	}
	if f.Added != 2 || f.Removed != 2 {
		t.Fatalf("added/removed = %d/%d, want 2/2", f.Added, f.Removed)
	}
	if len(f.Hunks) != 2 {
		t.Fatalf("hunks = %d, want 2", len(f.Hunks))
	}
	h := f.Hunks[0]
	if h.OldStart != 1 || h.OldLines != 4 || h.NewStart != 1 || h.NewLines != 5 || len(h.Lines) != 6 {
		t.Fatalf("bad hunk0: %+v", h)
	}
	if h.Lines[4].Op != agent.OpContext || h.Lines[4].Text != "" {
		t.Fatalf("blank context line lost: %+v", h.Lines[4])
	}
	if f.Hunks[1].OldStart != 20 || f.Hunks[1].NewStart != 21 {
		t.Fatalf("bad hunk1: %+v", f.Hunks[1])
	}
}

func TestParseUnifiedMultiFileDeleteRename(t *testing.T) {
	diff := `diff --git a/old.txt b/old.txt
deleted file mode 100644
index 111..000
--- a/old.txt
+++ /dev/null
@@ -1,2 +0,0 @@
-one
-two
diff --git a/a.txt b/b.txt
similarity index 90%
rename from a.txt
rename to b.txt
index 222..333 100644
--- a/a.txt
+++ b/b.txt
@@ -1 +1 @@
-x
+y
`
	fs := ParseUnified(diff)
	if len(fs) != 2 {
		t.Fatalf("files = %d, want 2: %+v", len(fs), fs)
	}
	if d := fs[0]; d.Path != "old.txt" || !d.Deleted || d.Removed != 2 || d.Added != 0 {
		t.Fatalf("bad delete: %+v", d)
	}
	if r := fs[1]; r.Path != "b.txt" || r.OldPath != "a.txt" || r.Added != 1 || r.Removed != 1 {
		t.Fatalf("bad rename: %+v", r)
	}
}

func TestParseUnifiedPlainHeaders(t *testing.T) {
	diff := "--- a/x.txt\t2020-01-01\n+++ b/x.txt\t2020-01-02\n@@ -1,2 +1,2 @@\n keep\n-old\n+new\n--- a/y.txt\n+++ b/y.txt\n@@ -1 +1,2 @@\n a\n+b\n"
	fs := ParseUnified(diff)
	if len(fs) != 2 {
		t.Fatalf("files = %d, want 2: %+v", len(fs), fs)
	}
	if fs[0].Path != "x.txt" || fs[0].Added != 1 || fs[0].Removed != 1 {
		t.Fatalf("bad x: %+v", fs[0])
	}
	if fs[1].Path != "y.txt" || fs[1].Added != 1 || fs[1].Removed != 0 {
		t.Fatalf("bad y: %+v", fs[1])
	}
}

// A removed line whose text starts with "-- " looks like a "--- " header; the
// hunk line counts must keep it in the body.
func TestParseUnifiedBodyLooksLikeHeader(t *testing.T) {
	diff := `--- a/q.sql
+++ b/q.sql
@@ -1,2 +1,2 @@
 select 1;
--- comment
+++ changed
`
	fs := ParseUnified(diff)
	if len(fs) != 1 {
		t.Fatalf("files = %d, want 1: %+v", len(fs), fs)
	}
	if fs[0].Added != 1 || fs[0].Removed != 1 || len(fs[0].Hunks) != 1 {
		t.Fatalf("body swallowed as header: %+v", fs[0])
	}
}

func TestParseUnifiedNoCountsAndNoNewline(t *testing.T) {
	diff := "--- a/z.txt\n+++ b/z.txt\n@@\n-a\n+b\n\\ No newline at end of file\n"
	fs := ParseUnified(diff)
	if len(fs) != 1 || fs[0].Added != 1 || fs[0].Removed != 1 {
		t.Fatalf("got %+v", fs)
	}
}

func TestParseUnifiedEmpty(t *testing.T) {
	if fs := ParseUnified(""); len(fs) != 0 {
		t.Fatalf("got %+v", fs)
	}
	if fs := ParseUnified("just some text\n"); len(fs) != 0 {
		t.Fatalf("got %+v", fs)
	}
}
