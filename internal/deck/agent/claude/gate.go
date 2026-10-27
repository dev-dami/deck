package claude

import (
	"os"
	"runtime"
	"strings"

	"github.com/dev-dami/deck/internal/deck/agent"
	"github.com/dev-dami/deck/internal/deck/approve"
)

// The edit gate (session/coord.go) reaches Claude's edits in every mode
// through a PreToolUse hook in the --settings file: Deck's own binary as
// `mcp-approve ... --edit-gate`, which asks the broker before each Edit,
// MultiEdit, Write or NotebookEdit and refuses it with Deck's reason. The
// broker token reaches the hook in a 0600 file, as it reaches the MCP
// server in its config, never on a command line.

// gateHookTimeout is how long Claude lets the hook run, in seconds: past
// Deck's own wait for a file's owner (config edit_gate, 2 minutes by
// default). A hook that runs out lets the edit through.
const gateHookTimeout = 600

// gateHook builds the hook's command for server, Deck's MCP server, and
// writes the token file it reads. Both are "" when there is no hook: no
// server, a server that is not Deck's, or Windows, whose hooks run without
// a POSIX shell.
func gateHook(server *agent.MCPServer) (cmd, tokenFile string, err error) {
	if server == nil || runtime.GOOS == "windows" || len(server.Args) == 0 || server.Args[0] != "mcp-approve" {
		return "", "", nil
	}
	token := server.Env[approve.TokenEnv]
	if token == "" {
		return "", "", nil
	}
	f, err := os.CreateTemp("", "deck-gate-*.tok")
	if err != nil {
		return "", "", err
	}
	_ = f.Chmod(0o600)
	if _, err := f.WriteString(token); err != nil {
		f.Close()
		os.Remove(f.Name())
		return "", "", err
	}
	if err := f.Close(); err != nil {
		os.Remove(f.Name())
		return "", "", err
	}
	parts := []string{shellQuote(server.Command)}
	for _, a := range server.Args {
		if a != approve.ApproveToolFlag {
			parts = append(parts, shellQuote(a))
		}
	}
	parts = append(parts, "--token-file", shellQuote(f.Name()), approve.EditGateFlag)
	return strings.Join(parts, " "), f.Name(), nil
}

// gateHooks is the settings' hooks entry for the command.
func gateHooks(cmd string) map[string]any {
	return map[string]any{"PreToolUse": []any{map[string]any{
		"matcher": approve.EditGateTools,
		"hooks":   []any{map[string]any{"type": "command", "command": cmd, "timeout": gateHookTimeout}},
	}}}
}

// shellQuote quotes s for a POSIX shell.
func shellQuote(s string) string {
	if s != "" && strings.Trim(s, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_./:=@") == "" {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
