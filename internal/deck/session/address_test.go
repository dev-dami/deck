package session

import (
	"reflect"
	"strings"
	"testing"
)

// withAt is s with every "$" written "@": the same mention with the other
// sigil.
func withAt(s string) string { return strings.ReplaceAll(s, "$", "@") }

// atAddressing is a with its body written as withAt would.
func atAddressing(a Addressing) Addressing {
	a.Body = withAt(a.Body)
	return a
}

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
		{"kind to review", "$codex review this", duo, Addressing{Members: []string{"codex"}, Body: "review this"}},
		{"unknown name after a member stays in the body", "$claude $HOME is wrong", duo,
			Addressing{Members: []string{"claude"}, Body: "$HOME is wrong"}},
		{"renamed by name", "$auth hi", renamed, Addressing{Members: []string{"claude2"}, Body: "hi"}},
		{"renamed by id", "$claude2 hi", renamed, Addressing{Members: []string{"claude2"}, Body: "hi"}},
		{"kind resolves to renamed member", "$claude hi", renamed, Addressing{Members: []string{"claude2"}, Body: "hi"}},
		{"exited member of the kind: add a new one", "$codex hi", gone, Addressing{NewKinds: []string{"codex"}, Body: "hi"}},
		{"duplicates collapse", "$claude $claude hi", duo, Addressing{Members: []string{"claude"}, Body: "hi"}},
		{"a kind suffix is text", "$claude* hi", duo, Addressing{Body: "$claude* hi"}},
		{"another of a kind is text", "$claude+ hi", duo, Addressing{Body: "$claude+ hi"}},
		{"a typed race is text", "$claude×2 fix it", duo, Addressing{Body: "$claude×2 fix it"}},
		{"a path is plain text", "$src/main.go is broken", duo, Addressing{Body: "$src/main.go is broken"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ParseAddressing(c.text, c.members, kinds)
			if !reflect.DeepEqual(got, c.want) {
				t.Fatalf("ParseAddressing(%q)\n got %+v\nwant %+v", c.text, got, c.want)
			}
		})
		// "@" is the same mention as "$", in every case.
		t.Run(c.name+" with @", func(t *testing.T) {
			text, want := withAt(c.text), atAddressing(c.want)
			got := ParseAddressing(text, c.members, kinds)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("ParseAddressing(%q)\n got %+v\nwant %+v", text, got, want)
			}
		})
	}
}

func TestParseAddressingAt(t *testing.T) {
	kinds := []string{"claude", "codex"}
	duo := []MemberRef{{ID: "claude", Name: "claude", Kind: "claude"}, {ID: "codex", Name: "codex", Kind: "codex"}}
	cases := []struct {
		name string
		text string
		want Addressing
	}{
		{"both sigils in one message", "@claude $codex both of you", Addressing{Members: []string{"claude", "codex"}, Body: "both of you"}},
		{"a file reference is plain text", "@src/auth/login.ts is broken", Addressing{Body: "@src/auth/login.ts is broken"}},
		{"a file at the root is plain text", "@README.md is stale", Addressing{Body: "@README.md is stale"}},
		{"a file after a mention stays in the body", "@codex look at @internal/a.go", Addressing{Members: []string{"codex"}, Body: "look at @internal/a.go"}},
		{"an email is plain text", "write to user@example.com", Addressing{Body: "write to user@example.com"}},
		{"an email at the start is plain text", "alice@example.com wrote in", Addressing{Body: "alice@example.com wrote in"}},
		{"a domain after @ is plain text", "@example.com is down", Addressing{Body: "@example.com is down"}},
		{"a decorator is plain text", "@Override is missing", Addressing{Body: "@Override is missing"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ParseAddressing(c.text, duo, kinds); !reflect.DeepEqual(got, c.want) {
				t.Fatalf("ParseAddressing(%q)\n got %+v\nwant %+v", c.text, got, c.want)
			}
		})
	}
}

func TestParseAddressingProfiles(t *testing.T) {
	kinds := []string{"claude", "codex"}
	profiles := []string{"charles", "ada", "auth"}
	none := []MemberRef{{ID: "claude", Name: "claude", Kind: "claude"}}
	charles := []MemberRef{{ID: "claude", Name: "claude", Kind: "claude"}, {ID: "claude2", Name: "charles", Kind: "claude"}}
	auth := []MemberRef{{ID: "codex", Name: "auth", Kind: "codex"}}
	cases := []struct {
		name    string
		text    string
		members []MemberRef
		want    Addressing
	}{
		{"a profile adds a member", "@charles review this", none, Addressing{NewProfiles: []string{"charles"}, Body: "review this"}},
		{"the configured name, whatever the case", "@Charles review this", none, Addressing{NewProfiles: []string{"charles"}, Body: "review this"}},
		{"$ works as well", "$ada build it", none, Addressing{NewProfiles: []string{"ada"}, Body: "build it"}},
		{"a member of that name wins", "@charles again", charles, Addressing{Members: []string{"claude2"}, Body: "again"}},
		{"+ is text", "@charles+ a second look", charles, Addressing{Body: "@charles+ a second look"}},
		{"twice is one", "@charles @charles go", none, Addressing{NewProfiles: []string{"charles"}, Body: "go"}},
		{"a kind comes before a profile's member", "@claude hi", charles, Addressing{Members: []string{"claude"}, Body: "hi"}},
		{"a member's name comes before a profile", "@auth hi", auth, Addressing{Members: []string{"codex"}, Body: "hi"}},
		{"* is text", "@charles* hi", none, Addressing{Body: "@charles* hi"}},
		{"a later clause", "@claude plan it, then @ada build it", none,
			Addressing{Members: []string{"claude"}, NewProfiles: []string{"ada"}, Body: "plan it, then @ada build it"}},
		{"mid-clause is plain text", "ask @charles later", none, Addressing{Body: "ask @charles later"}},
		{"no profiles, plain text", "@nobody hi", none, Addressing{Body: "@nobody hi"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ParseAddressing(c.text, c.members, kinds, profiles...); !reflect.DeepEqual(got, c.want) {
				t.Fatalf("ParseAddressing(%q)\n got %+v\nwant %+v", c.text, got, c.want)
			}
		})
	}
	if ad := ParseAddressing("@charles hi", none, kinds); ad.Mentioned() {
		t.Fatalf("without profiles @charles is text: %+v", ad)
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

func TestDefuse(t *testing.T) {
	cases := map[string]string{
		"$claude fix it":               "＄claude fix it",
		"@claude fix it":               "＠claude fix it",
		"then @codex now":              "then ＠codex now",
		"then @codex×3 make":           "then @codex×3 make", // text, not a mention
		"mail user@example.com":        "mail user@example.com",
		"look at @src/a.go and @b.txt": "look at @src/a.go and @b.txt",
		"echo $HOME":                   "echo ＄HOME",
	}
	for in, want := range cases {
		if got := defuse(in); got != want {
			t.Errorf("defuse(%q) = %q, want %q", in, got, want)
		}
		if ad := ParseAddressing("[vps] "+defuse(in), []MemberRef{{ID: "claude", Name: "claude", Kind: "claude"}}, []string{"claude", "codex"}); ad.Mentioned() {
			t.Errorf("defused %q still addresses: %+v", in, ad)
		}
	}
}
