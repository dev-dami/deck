package antigravity

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/dev-dami/deck/internal/deck/agent"
	"github.com/dev-dami/deck/internal/deck/approve"
)

// Deck's MCP server reaches agy through `agy mcp add/remove`, which writes
// the user's global agy config. agy offers no per-run --mcp-config flag, so
// the adapter registers a per-agent server name on Start and removes it on
// Close. A crash between the two leaves a harmless entry behind (it points
// at a dead broker); remove it with `agy mcp remove <name>`.
//
// The broker token never goes into agy's config: it is written to a 0600
// temp file, the entry names only that file (`mcp-approve --token-file`),
// and the file is deleted on Close, like the Claude adapter's --mcp-config
// file. The token is per run, so a file a crash leaves behind is useless.
func mcpServerName(agentID string) string {
	var sb strings.Builder
	sb.WriteString("deck-")
	for _, c := range agentID {
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' {
			sb.WriteRune(c)
		} else {
			sb.WriteByte('-')
		}
	}
	if sb.Len() == len("deck-") {
		return "deck"
	}
	return sb.String()
}

// mcpAdd registers Deck's MCP server and returns its name and the token
// file it made ("" if none). Flags must precede the name. Other env values
// go in --env; the broker token goes in the file.
func (a *adapter) mcpAdd(ctx context.Context, agyPath string, srv *agent.MCPServer, agentID string) (name, tokenFile string, err error) {
	name = mcpServerName(agentID)
	args := []string{"mcp", "add"}
	keys := make([]string, 0, len(srv.Env))
	for k := range srv.Env {
		if k != approve.TokenEnv {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	for _, k := range keys {
		args = append(args, "--env", k+"="+srv.Env[k])
	}
	args = append(args, name, srv.Command)
	args = append(args, srv.Args...)
	if token, ok := srv.Env[approve.TokenEnv]; ok {
		tokenFile, err = writeTokenFile(token)
		if err != nil {
			return "", "", fmt.Errorf("agy mcp add %s: %w", name, err)
		}
		args = append(args, "--token-file", tokenFile)
	}
	if err := a.mcpRun(ctx, agyPath, args...); err != nil {
		if tokenFile != "" {
			_ = os.Remove(tokenFile)
		}
		return "", "", fmt.Errorf("agy mcp add %s: %w", name, err)
	}
	return name, tokenFile, nil
}

// writeTokenFile writes token to a new 0600 temp file and returns its path.
func writeTokenFile(token string) (string, error) {
	f, err := os.CreateTemp("", "deck-agy-token-*")
	if err != nil {
		return "", err
	}
	// CreateTemp already creates the file 0600.
	_ = f.Chmod(0o600)
	if _, err := f.WriteString(token); err != nil {
		f.Close()
		os.Remove(f.Name())
		return "", err
	}
	if err := f.Close(); err != nil {
		os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
}

// mcpRemove unregisters the server added by mcpAdd and deletes its token
// file. Errors are ignored: a stale entry is harmless (it points at a dead
// broker) and Close must not fail.
func (r *run) mcpRemove() {
	name, file := r.mcpName, r.mcpFile
	r.mcpName, r.mcpFile = "", ""
	if file != "" {
		_ = os.Remove(file)
	}
	if name == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_ = r.a.mcpRun(ctx, r.path, "mcp", "remove", name)
}

func (a *adapter) mcpRun(ctx context.Context, agyPath string, args ...string) error {
	if a.mcpRunFn != nil {
		return a.mcpRunFn(ctx, agyPath, args...)
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, agyPath, args...)
	setProcAttr(cmd)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}
