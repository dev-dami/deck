package ui

import "testing"

func TestSplitFences(t *testing.T) {
	src := "# Plan\n\nSome **bold** text.\n```go\nfunc x() {}\n\n```\ntail\n```\nunclosed"
	got := splitFences(src)
	want := []segment{
		{text: "# Plan\n\nSome **bold** text."},
		{code: true, lang: "go", text: "func x() {}\n"},
		{text: "tail"},
		{code: true, text: "unclosed"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d segments: %+v", len(got), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("segment %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestCapLines(t *testing.T) {
	if s, more := capLines("a\nb\nc", 2); s != "a\nb" || more != 1 {
		t.Fatalf("capLines = %q, %d", s, more)
	}
	if s, more := capLines("a", 2); s != "a" || more != 0 {
		t.Fatalf("capLines short = %q, %d", s, more)
	}
}
