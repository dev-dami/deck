package tui

import (
	"testing"

	"github.com/dev-dami/deck/internal/session"
)

func duoThread() session.ThreadInfo {
	return session.ThreadInfo{
		ID: "t1", LastSpeaker: "claude",
		Members: []session.AgentInfo{
			{ID: "claude", Kind: "claude", Name: "claude", Status: session.StatusIdle},
			{ID: "codex", Kind: "codex", Name: "codex", Status: session.StatusIdle},
		},
	}
}

func soloThread() session.ThreadInfo {
	return session.ThreadInfo{ID: "t2", LastSpeaker: "claude", Members: []session.AgentInfo{
		{ID: "claude", Kind: "claude", Name: "claude", Status: session.StatusIdle},
	}}
}

func TestParseCommand(t *testing.T) {
	name, arg, ok := parseCommand("/cd   ~/some dir ")
	if !ok || name != "cd" || arg != "~/some dir" {
		t.Fatalf("%q %q %v", name, arg, ok)
	}
	name, arg, ok = parseCommand("/new")
	if !ok || name != "new" || arg != "" {
		t.Fatalf("%q %q %v", name, arg, ok)
	}
	if _, _, ok := parseCommand("@claude hi"); ok {
		t.Fatal("a mention is not a command")
	}
	if len(Commands) != 17 {
		t.Fatalf("exactly seventeen commands, have %d", len(Commands))
	}
	for _, n := range []string{"new", "resume", "model", "effort", "cd", "name", "changes", "rename", "pin", "done", "archive", "delete", "fork", "move", "isolate", "apply", "unisolate"} {
		if !isCommand(n) {
			t.Errorf("missing /%s", n)
		}
	}
	if isCommand("quit") {
		t.Error("/quit is not a command")
	}
}

func TestAddressHint(t *testing.T) {
	kinds := []string{"claude", "codex"}
	exited := duoThread()
	exited.Members[1].Status = session.StatusExited
	renamed := duoThread()
	renamed.Members[1].Name = "reviewer"

	cases := []struct {
		name string
		text string
		th   session.ThreadInfo
		want string
	}{
		{"empty text", "", duoThread(), ""},
		{"slash command", "/new", duoThread(), ""},
		{"member", "@codex look at this", duoThread(), "→ codex"},
		{"member by id shows its name", "@codex look", renamed, "→ reviewer"},
		{"member by name", "@reviewer look", renamed, "→ reviewer"},
		{"two members", "@claude do a, @codex do b", duoThread(), "→ claude, codex"},
		{"all", "@all stop", duoThread(), "→ all"},
		{"no mention goes to whoever spoke last", "keep going", duoThread(), "→ claude (last spoke)"},
		{"mid-clause mention is plain text", "ask @codex about it", duoThread(), "→ claude (last spoke)"},
		{"adds a missing kind", "@codex take a look", soloThread(), "+ adds codex"},
		{"adds both kinds in an empty thread", "@claude plan, @codex review", session.ThreadInfo{ID: "t3"}, "+ adds claude, codex"},
		{"member and addition", "@claude go on, @codex check", soloThread(), "→ claude · + adds codex"},
		{"an exited member cannot be addressed", "@codex again", exited, "codex has exited"},
		{"unknown name", "@nobody hi", duoThread(), "no agent @nobody"},
		{"one member says nothing", "just go", soloThread(), ""},
		{"empty thread says nothing", "hello", session.ThreadInfo{ID: "t3"}, ""},
		{"nobody spoke yet: the first member", "go", func() session.ThreadInfo {
			th := duoThread()
			th.LastSpeaker = ""
			return th
		}(), "→ claude"},
		{"last speaker has exited", "go", func() session.ThreadInfo {
			th := duoThread()
			th.LastSpeaker = "codex"
			th.Members[1].Status = session.StatusExited
			return th
		}(), "→ claude"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := addressHint(c.text, c.th, kinds); got != c.want {
				t.Fatalf("hint(%q) = %q, want %q", c.text, got, c.want)
			}
		})
	}
}
