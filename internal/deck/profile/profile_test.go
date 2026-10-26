package profile

import (
	"strings"
	"testing"
)

var kinds = []string{"claude", "codex", "opencode", "antigravity"}

func TestValidate(t *testing.T) {
	ok := Profile{Name: "charles", Kind: "claude", Role: "reviewer", Paths: []string{"internal/**", "*.go"}, Color: "#a3e"}
	if err := Validate(ok, kinds, []string{"alice"}); err != nil {
		t.Fatalf("a good profile: %v", err)
	}
	cases := []struct {
		name string
		edit func(*Profile)
		want string
	}{
		{"empty name", func(p *Profile) { p.Name = "" }, "invalid name"},
		{"a space", func(p *Profile) { p.Name = "char les" }, "invalid name"},
		{"@", func(p *Profile) { p.Name = "@charles" }, "invalid name"},
		{"$", func(p *Profile) { p.Name = "char$" }, "invalid name"},
		{"#", func(p *Profile) { p.Name = "char#les" }, "invalid name"},
		{"/", func(p *Profile) { p.Name = "a/b" }, "invalid name"},
		{"*", func(p *Profile) { p.Name = "charles*" }, "invalid name"},
		{"+", func(p *Profile) { p.Name = "charles+" }, "invalid name"},
		{"a dot a mention cannot reach", func(p *Profile) { p.Name = "dr.who" }, "can only hold"},
		{"too long", func(p *Profile) { p.Name = strings.Repeat("a", MaxNameRunes+1) }, "longer than"},
		{"all", func(p *Profile) { p.Name = "All" }, "reserved"},
		{"user", func(p *Profile) { p.Name = "user" }, "reserved"},
		{"deck", func(p *Profile) { p.Name = "DECK" }, "reserved"},
		{"a kind", func(p *Profile) { p.Name = "Codex" }, "agent kind"},
		{"a teammate", func(p *Profile) { p.Name = "ALICE" }, "teammate"},
		{"no kind", func(p *Profile) { p.Kind = "" }, "needs a kind"},
		{"an unknown kind", func(p *Profile) { p.Kind = "gemini" }, "no agent kind"},
		{"a role of two lines", func(p *Profile) { p.Role = "one\ntwo" }, "role is one line"},
		{"a voice of two lines", func(p *Profile) { p.Voice = "dry\nterse" }, "voice is one line"},
		{"a bad glob", func(p *Profile) { p.Paths = []string{"src/["} }, "not a valid glob"},
		{"an empty path", func(p *Profile) { p.Paths = []string{" "} }, "empty path"},
		{"a colour that ends the declaration", func(p *Profile) { p.Color = "red; background: url(x)" }, "not a CSS colour"},
		{"an unknown scope", func(p *Profile) { p.Scope = "team" }, "no scope"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := ok
			c.edit(&p)
			err := Validate(p, kinds, []string{"alice"})
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("Validate(%+v) = %v, want %q", p, err, c.want)
			}
		})
	}
}

func TestMergeAndFind(t *testing.T) {
	global := []Profile{{Name: "charles", Kind: "claude", Scope: ScopeGlobal}, {Name: "ada", Kind: "codex", Scope: ScopeGlobal}}
	project := []Profile{{Name: "Charles", Kind: "codex", Scope: ScopeProject}, {Name: "linus", Kind: "opencode", Scope: ScopeProject}}
	got := Merge(global, project)
	if strings.Join(Names(got), ",") != "Charles,ada,linus" || got[0].Scope != ScopeProject || got[0].Kind != "codex" {
		t.Fatalf("merged = %+v", got)
	}
	if p, ok := Find(got, "CHARLES"); !ok || p.Kind != "codex" {
		t.Fatalf("Find = %+v %v", p, ok)
	}
	if _, ok := Find(got, "nobody"); ok {
		t.Fatal("found nobody")
	}
	if strings.Join(Names(Without(got, []string{"ADA"})), ",") != "Charles,linus" {
		t.Fatal("Without kept a teammate's name")
	}
}

func TestBriefing(t *testing.T) {
	if b := (Profile{Name: "x", Kind: "claude"}).Briefing(); b != "" {
		t.Fatalf("an empty profile briefs %q", b)
	}
	b := Profile{Role: "reviewer.", Instructions: "  Check tests.\nThen code. ", Voice: "dry,  terse", Paths: []string{"a/**", "b.go"}}.Briefing()
	want := "Your role: reviewer.\nStanding instructions:\nCheck tests.\nThen code.\nVoice: dry, terse.\nKeep your changes to these paths: a/**, b.go."
	if b != want {
		t.Fatalf("briefing\n got %q\nwant %q", b, want)
	}
}
