package session

import (
	"reflect"
	"testing"
)

func TestParseAddressing(t *testing.T) {
	kinds := []string{"claude", "codex"}
	duo := []MemberRef{{ID: "claude", Name: "claude", Kind: "claude"}, {ID: "codex", Name: "codex", Kind: "codex"}}
	solo := []MemberRef{{ID: "claude", Name: "claude", Kind: "claude"}}
	renamed := []MemberRef{{ID: "claude2", Name: "auth", Kind: "claude"}, {ID: "codex", Name: "reviewer", Kind: "codex"}}
	gone := []MemberRef{{ID: "claude", Name: "claude", Kind: "claude", Exited: true}}

	cases := []struct {
		name    string
		text    string
		members []MemberRef
		want    Addressing
	}{
		{"no mention", "hello there", duo, Addressing{Body: "hello there"}},
		{"member", "$claude hello", duo, Addressing{Members: []string{"claude"}, Body: "hello"}},
		{"case-insensitive", "$Codex hello", duo, Addressing{Members: []string{"codex"}, Body: "hello"}},
		{"mention only", "$claude", duo, Addressing{Members: []string{"claude"}}},
		{"colon form", "$codex: review it", duo, Addressing{Members: []string{"codex"}, Body: "review it"}},
		{"kind without member adds one", "$codex review this", solo, Addressing{NewKinds: []string{"codex"}, Body: "review this"}},
		{"kind alone adds one", "$codex", solo, Addressing{NewKinds: []string{"codex"}}},
		{"both leading", "$claude $codex both of you", duo, Addressing{Members: []string{"claude", "codex"}, Body: "both of you"}},
		{"comma list", "$claude, $codex both of you", duo, Addressing{Members: []string{"claude", "codex"}, Body: "both of you"}},
		{"all", "$all status please", duo, Addressing{All: true, Body: "status please"}},
		{"later clause after then", "$claude plan it, then $codex review it", duo,
			Addressing{Members: []string{"claude", "codex"}, Body: "plan it, then $codex review it"}},
		{"later clause on a new line", "$claude plan it\n$codex review it", duo,
			Addressing{Members: []string{"claude", "codex"}, Body: "plan it\n$codex review it"}},
		{"later clause after a sentence", "Plan it. $codex review it", duo,
			Addressing{Members: []string{"codex"}, Body: "Plan it. $codex review it"}},
		{"mid-clause is plain text", "please ask $codex to look", duo, Addressing{Body: "please ask $codex to look"}},
		{"email is plain text", "mail me$claude.com", duo, Addressing{Body: "mail me$claude.com"}},
		{"file name is plain text", "open $codex.go", duo, Addressing{Body: "open $codex.go"}},
		{"unknown name is plain text", "$foo hi", duo, Addressing{Body: "$foo hi"}},
		{"shell variable is plain text", "$HOME is wrong", duo, Addressing{Body: "$HOME is wrong"}},
		{"shell variable mid-clause", "echo $PATH", duo, Addressing{Body: "echo $PATH"}},
		{"inside a word is plain text", "foo$bar", duo, Addressing{Body: "foo$bar"}},
		{"at sign is not a mention", "@codex review this", duo, Addressing{Body: "@codex review this"}},
		{"kind to review", "$codex review this", duo, Addressing{Members: []string{"codex"}, Body: "review this"}},
		{"unknown name after a member stays in the body", "$claude $HOME is wrong", duo,
			Addressing{Members: []string{"claude"}, Body: "$HOME is wrong"}},
		{"renamed by name", "$auth hi", renamed, Addressing{Members: []string{"claude2"}, Body: "hi"}},
		{"renamed by id", "$claude2 hi", renamed, Addressing{Members: []string{"claude2"}, Body: "hi"}},
		{"kind resolves to renamed member", "$claude hi", renamed, Addressing{Members: []string{"claude2"}, Body: "hi"}},
		{"exited member of the kind: add a new one", "$codex hi", gone, Addressing{NewKinds: []string{"codex"}, Body: "hi"}},
		{"duplicates collapse", "$claude $claude hi", duo, Addressing{Members: []string{"claude"}, Body: "hi"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ParseAddressing(c.text, c.members, kinds)
			if !reflect.DeepEqual(got, c.want) {
				t.Fatalf("ParseAddressing(%q)\n got %+v\nwant %+v", c.text, got, c.want)
			}
		})
	}
}

func TestTrimSigil(t *testing.T) {
	cases := map[string]string{
		"$codex":  "codex",
		"@codex":  "codex",
		"  $all ": "all",
		"codex":   "codex",
		"":        "",
	}
	for in, want := range cases {
		if got := TrimSigil(in); got != want {
			t.Errorf("TrimSigil(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParseMentions(t *testing.T) {
	cases := []struct {
		name  string
		reply string
		want  []mention
	}{
		{"none", "All done, nothing to relay.", nil},
		{"one line", "Done.\n\n$claude add a test", []mention{{Names: []string{"claude"}, Text: "add a test"}}},
		{"continues until a blank line", "$codex please check\nthe lock order\n\nThanks", []mention{{Names: []string{"codex"}, Text: "please check\nthe lock order"}}},
		{"all", "$all heads up", []mention{{Names: []string{"all"}, Text: "heads up"}}},
		{"two targets", "$claude $codex sync up", []mention{{Names: []string{"claude", "codex"}, Text: "sync up"}}},
		{"inside a code fence is ignored", "```\n$claude not me\n```\ndone", nil},
		{"mid-line is ignored", "I told $claude already", nil},
		{"empty text is dropped", "$claude", nil},
		{"same target merges", "$codex one\n\n$codex two", []mention{{Names: []string{"codex"}, Text: "one\n\ntwo"}}},
		{"email is ignored", "$claude.com is a domain", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := parseMentions(c.reply)
			if !reflect.DeepEqual(got, c.want) {
				t.Fatalf("parseMentions(%q)\n got %+v\nwant %+v", c.reply, got, c.want)
			}
		})
	}
}
