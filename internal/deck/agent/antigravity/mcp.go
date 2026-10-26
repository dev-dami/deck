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
	// A run of the same agent closed just before may still be removing
	// this name in the background: let it finish, or it would remove the
	// entry added here.
	a.waitRemoved(ctx, name)
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

// mcpRemove deletes the token file of the server added by mcpAdd and
// unregisters the server in the background: `agy mcp remove` can take
// seconds, and Close should not wait for it. Errors are ignored: a stale
// entry is harmless (it points at a dead broker) and Close must not fail.
// A later mcpAdd of the same name waits for the removal (waitRemoved), and
// Deck waits for it on the way out (Drain).
func (r *run) mcpRemove() {
	name, file := r.mcpName, r.mcpFile
	r.mcpName, r.mcpFile = "", ""
	if file != "" {
		_ = os.Remove(file)
	}
	if name == "" {
		return
	}
	a := r.a
	done := make(chan struct{})
	a.rmMu.Lock()
	prev := a.removing[name]
	if a.removing == nil {
		a.removing = map[string]chan struct{}{}
	}
	a.removing[name] = done
	a.rmMu.Unlock()
	a.removes.Add(1)
	go func() {
		defer a.removes.Done()
		if prev != nil {
			<-prev
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		_ = a.mcpRun(ctx, r.path, "mcp", "remove", name)
		cancel()
		a.rmMu.Lock()
		if a.removing[name] == done {
			delete(a.removing, name)
		}
		a.rmMu.Unlock()
		close(done)
	}()
}

// waitRemoved waits for a background removal of name, if one is running.
func (a *adapter) waitRemoved(ctx context.Context, name string) {
	a.rmMu.Lock()
	ch := a.removing[name]
	a.rmMu.Unlock()
	if ch == nil {
		return
	}
	select {
	case <-ch:
	case <-ctx.Done():
	}
}

// Drain implements agent.Drainer: it waits, at most d, for the background
// `agy mcp remove` calls of closed runs, so quitting Deck does not leave
// their entries in agy's config.
func (a *adapter) Drain(d time.Duration) {
	done := make(chan struct{})
	go func() {
		a.removes.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(d):
	}
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
