package antigravity

import (
	"context"
	"fmt"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/dev-dami/deck/internal/deck/agent"
)

// Deck's MCP server reaches agy through `agy mcp add/remove`, which writes
// the user's global agy config. agy offers no per-run --mcp-config flag, so
// the adapter registers a per-agent server name on Start and removes it on
// Close. A crash between the two leaves a harmless entry behind (it points
// at a dead broker); remove it with `agy mcp remove <name>`. The broker
// token travels in the server's env (never as a bare argument), but note it
// is persisted in agy's config file until removal, unlike the Claude
// adapter's temp-file registration.
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

// mcpAdd registers Deck's MCP server and returns its name. Flags must precede
// the name; the token stays in --env values, never in the command line.
func (a *adapter) mcpAdd(ctx context.Context, agyPath string, srv *agent.MCPServer, agentID string) (string, error) {
	name := mcpServerName(agentID)
	args := []string{"mcp", "add"}
	keys := make([]string, 0, len(srv.Env))
	for k := range srv.Env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		args = append(args, "--env", k+"="+srv.Env[k])
	}
	args = append(args, name, srv.Command)
	args = append(args, srv.Args...)
	if err := a.mcpRun(ctx, agyPath, args...); err != nil {
		return "", fmt.Errorf("agy mcp add %s: %w", name, err)
	}
	return name, nil
}

// mcpRemove unregisters the server added by mcpAdd. Errors are ignored: a
// stale entry is harmless (it points at a dead broker) and Close must not fail.
func (r *run) mcpRemove() {
	name := r.mcpName
	r.mcpName = ""
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
