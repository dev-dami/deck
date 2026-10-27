package approve

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// The hook hands Claude's edit to Deck's gate and turns a refusal into a
// PreToolUse deny; anything else lets the edit go on.
func TestEditGateHook(t *testing.T) {
	b, err := NewBroker()
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	var asked []string
	b.SetEditGate(func(agentID string, paths []string) string {
		asked = append(asked, agentID+":"+strings.Join(paths, ","))
		if paths[0] == "/repo/a.go" {
			return "ada was changing it; token " + b.Token()
		}
		return ""
	})
	run := func(input, token string) string {
		var out bytes.Buffer
		if err := EditGateHook(strings.NewReader(input), &out, b.Addr(), token, "claude"); err != nil {
			t.Fatal(err)
		}
		return out.String()
	}

	out := run(`{"tool_name":"Edit","tool_input":{"file_path":"/repo/a.go","old_string":"x","new_string":"y"}}`, b.Token())
	var o hookOutput
	if err := json.Unmarshal([]byte(out), &o); err != nil {
		t.Fatalf("output %q: %v", out, err)
	}
	h := o.HookSpecificOutput
	if h.HookEventName != "PreToolUse" || h.PermissionDecision != "deny" || !strings.HasPrefix(h.PermissionDecisionReason, "ada was changing it") {
		t.Fatalf("decision = %+v", h)
	}
	if strings.Contains(out, b.Token()) {
		t.Fatal("the token leaked into the reason")
	}
	if got := run(`{"tool_name":"NotebookEdit","tool_input":{"notebook_path":"/repo/n.ipynb"}}`, b.Token()); got != "" {
		t.Fatalf("an allowed edit printed %q", got)
	}
	// No path, bad input, a wrong token or no Deck: the edit goes on.
	for _, in := range []string{`{"tool_name":"Bash","tool_input":{"command":"ls"}}`, `not json`} {
		if got := run(in, b.Token()); got != "" {
			t.Fatalf("%s printed %q", in, got)
		}
	}
	if got := run(`{"tool_name":"Edit","tool_input":{"file_path":"/repo/a.go"}}`, "wrong"); got != "" {
		t.Fatalf("a wrong token printed %q", got)
	}
	if want := []string{"claude:/repo/a.go", "claude:/repo/n.ipynb"}; strings.Join(asked, " ") != strings.Join(want, " ") {
		t.Fatalf("asked %q", asked)
	}
	var out2 bytes.Buffer
	if err := EditGateHook(strings.NewReader(`{"tool_name":"Edit","tool_input":{"file_path":"/repo/a.go"}}`), &out2, "127.0.0.1:1", b.Token(), "claude"); err != nil || out2.Len() != 0 {
		t.Fatalf("no Deck: %q, %v", out2.String(), err)
	}
}
