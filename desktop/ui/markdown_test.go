package ui

import "testing"

func TestParseMarkdown(t *testing.T) {
	src := "# Plan\n\nFirst **bold** line\ncontinues here.\n\n- one\n  - nested\n2. two\n> quoted\n```go\nfunc x() {}\n\n```\ntail"
	got := parseMarkdown(src)
	want := []block{
		{kind: bHeading, level: 1, text: "Plan"},
		{kind: bPara, text: "First bold line continues here."},
		{kind: bBullet, level: 0, text: "one"},
		{kind: bBullet, level: 1, text: "nested"},
		{kind: bBullet, level: -1, text: "2. two"},
		{kind: bQuote, text: "quoted"},
		{kind: bCode, lang: "go", text: "func x() {}\n"},
		{kind: bPara, text: "tail"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d blocks: %+v", len(got), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("block %d = %+v, want %+v", i, got[i], want[i])
		}
	}
	// An unclosed fence runs to the end instead of losing text.
	if b := parseMarkdown("```\nstill code"); len(b) != 1 || b[0].kind != bCode || b[0].text != "still code" {
		t.Fatalf("unclosed fence: %+v", b)
	}
}
