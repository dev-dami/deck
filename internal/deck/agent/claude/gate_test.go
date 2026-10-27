package claude

import (
	"encoding/json"
	"os"
	"runtime"
	"strings"
	"testing"

	"github.com/dev-dami/deck/internal/deck/agent"
	"github.com/dev-dami/deck/internal/deck/approve"
)

// The edit gate's hook runs Deck's own binary before Claude's edits, with
// the broker token in a file of its own, never on the command line.
func TestGateHookInSettings(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no hook on Windows")
	}
	t.Setenv("HOME", t.TempDir())
	server := &agent.MCPServer{Name: "deck", Command: "/opt/my deck/deck",
		Args: []string{"mcp-approve", "--addr", "127.0.0.1:9", "--agent", "claude", approve.ApproveToolFlag},
		Env:  map[string]string{approve.TokenEnv: "s3cret"}}
	cmd, tok, err := gateHook(server)
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tok)
	if b, _ := os.ReadFile(tok); string(b) != "s3cret" {
		t.Fatalf("token file holds %q", b)
	}
	want := "'/opt/my deck/deck' mcp-approve --addr 127.0.0.1:9 --agent claude --token-file " + tok + " --edit-gate"
	if cmd != want || strings.Contains(cmd, "s3cret") {
		t.Fatalf("cmd = %q", cmd)
	}
	path, err := writeSettings(t.TempDir(), false, cmd)
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(path)
	data, _ := os.ReadFile(path)
	var got struct {
		Hooks map[string][]struct {
			Matcher string
			Hooks   []struct {
				Type, Command string
				Timeout       int
			}
		}
	}
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	pre := got.Hooks["PreToolUse"]
	if len(pre) != 1 || pre[0].Matcher != approve.EditGateTools || len(pre[0].Hooks) != 1 || pre[0].Hooks[0].Command != cmd || pre[0].Hooks[0].Timeout == 0 {
		t.Fatalf("hooks = %+v", got.Hooks)
	}
	// Not Deck's server: no hook.
	if cmd, tok, _ := gateHook(&agent.MCPServer{Command: "x", Args: []string{"serve"}}); cmd != "" || tok != "" {
		t.Fatalf("hook for another server: %q", cmd)
	}
}
