package agent

import (
	"encoding/json"
	"testing"
)

func TestParseExecInput(t *testing.T) {
	in := ParseExecInput(json.RawMessage(`{"op":" git.log ","args":{"n":3}}`))
	if in.Op != "git.log" || string(in.Args) != `{"n":3}` {
		t.Errorf("nested = %+v", in)
	}
	in = ParseExecInput(json.RawMessage(`{"op":"thread.send","to":"codex","message":"hi"}`))
	if in.Op != "thread.send" || string(in.Args) != `{"message":"hi","to":"codex"}` {
		t.Errorf("flattened = %+v %s", in, in.Args)
	}
	in = ParseExecInput(json.RawMessage(`{"op":"thread.read","args":null}`))
	if in.Op != "thread.read" || in.Args != nil {
		t.Errorf("null args = %+v", in)
	}
}

func TestDeckToolDisplay(t *testing.T) {
	for _, c := range []struct {
		tool, input, name, title string
		ok                       bool
	}{
		{"execute", `{"op":"thread.send","args":{"to":"codex","message":"look\nhere"}}`, "message", "to codex: look here", true},
		{"execute", `{"op":"thread.send","to":"all","message":"x"}`, "message", "to all: x", true},
		{"execute", `{"op":"thread.read"}`, "read", "shared thread", true},
		{"execute", `{"op":"skills.read","args":{"name":"pdf"}}`, "deck", `skills.read {"name":"pdf"}`, true},
		{"search", `{"query":"git log"}`, "deck", "search git log", true},
		{"Bash", `{}`, "", "", false},
	} {
		n, title, ok := DeckToolDisplay(c.tool, json.RawMessage(c.input))
		if n != c.name || title != c.title || ok != c.ok {
			t.Errorf("DeckToolDisplay(%s, %s) = %q, %q, %v", c.tool, c.input, n, title, ok)
		}
	}
}
