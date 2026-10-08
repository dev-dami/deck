package tui

import (
	"testing"

	"github.com/dev-dami/deck/internal/session"
)

func agentsFixture() []session.AgentInfo {
	return []session.AgentInfo{
		{ID: "claude", Kind: "claude", Name: "claude", Status: session.StatusIdle},
		{ID: "claude2", Kind: "claude", Name: "auth", Status: session.StatusWorking},
		{ID: "codex", Kind: "codex", Name: "codex", Status: session.StatusExited},
	}
}

func TestParseInput(t *testing.T) {
	kinds := []string{"claude", "codex"}
	ag := agentsFixture()
	cases := []struct {
		name     string
		text     string
		agents   []session.AgentInfo
		sel      string
		newMode  bool
		want     Action
		wantID   string
		wantKind string
		wantText string
	}{
		{"empty deck enter", "", nil, "", false, ActStart, "", "claude", ""},
		{"empty deck text", "fix the build", nil, "", false, ActStart, "", "claude", "fix the build"},
		{"empty text with agents", "   ", ag, "claude", false, ActNone, "", "", ""},
		{"empty text in new mode", "", ag, "claude", true, ActStart, "", "claude", ""},
		{"plain goes to selected", "hello", ag, "claude", false, ActSend, "claude", "", "hello"},
		{"plain in new mode starts", "hello", ag, "claude", true, ActStart, "", "claude", "hello"},
		{"exited selection refuses", "hello", ag, "codex", false, ActError, "", "", ""},
		{"@id from anywhere", "@claude2 status?", ag, "claude", false, ActSend, "claude2", "", "status?"},
		{"@name by display name", "@auth status?", ag, "claude", false, ActSend, "claude2", "", "status?"},
		{"@running kind name wins", "@claude hi", ag, "claude2", false, ActSend, "claude", "", "hi"},
		{"@kind not running starts new", "@codex build it", ag, "claude", false, ActStart, "", "codex", "build it"},
		{"@kind alone arms new mode", "@codex", ag, "claude", false, ActNewKind, "", "codex", ""},
		{"@running alone selects", "@claude2", ag, "claude", false, ActSelect, "claude2", "", ""},
		{"@kind in new mode always starts new", "@claude another", ag, "claude", true, ActStart, "", "claude", "another"},
		{"@unknown", "@nobody hi", ag, "claude", false, ActError, "", "", ""},
		{"bare @", "@ hi", ag, "claude", false, ActError, "", "", ""},
		{"multiline body kept", "@claude2 one\ntwo", ag, "claude", false, ActSend, "claude2", "", "one\ntwo"},
		{"command", "/model opus", ag, "claude", false, ActCommand, "", "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ParseInput(c.text, c.agents, c.sel, c.newMode, "claude", kinds)
			if got.Action != c.want {
				t.Fatalf("action = %v, want %v (%+v)", got.Action, c.want, got)
			}
			if got.AgentID != c.wantID || got.Kind != c.wantKind || got.Text != c.wantText {
				t.Fatalf("got %+v, want id=%q kind=%q text=%q", got, c.wantID, c.wantKind, c.wantText)
			}
		})
	}
}

func TestParseCommand(t *testing.T) {
	p := ParseInput("/cd   ~/some dir ", agentsFixture(), "claude", false, "claude", []string{"claude"})
	if p.Action != ActCommand || p.Cmd != "cd" || p.Arg != "~/some dir" {
		t.Fatalf("%+v", p)
	}
	p = ParseInput("/new", nil, "", false, "claude", []string{"claude"})
	if p.Cmd != "new" || p.Arg != "" {
		t.Fatalf("%+v", p)
	}
	if len(Commands) != 5 {
		t.Fatalf("exactly five commands, have %d", len(Commands))
	}
	for _, n := range []string{"new", "resume", "model", "cd", "name"} {
		if !isCommand(n) {
			t.Errorf("missing /%s", n)
		}
	}
	if isCommand("quit") {
		t.Error("/quit is not a command")
	}
}
