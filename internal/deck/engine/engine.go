// Package engine starts Deck's agent engine: the installed claude, codex and
// opencode CLIs, driven over their own protocols under the user's own login,
// and the thread manager that puts them in shared threads.
//
// Deck never reads the CLIs' credentials and never calls a model API itself.
package engine

import (
	"flag"
	"fmt"
	"os"
	"os/exec"

	"github.com/dev-dami/deck/internal/deck/agent"
	"github.com/dev-dami/deck/internal/deck/agent/claude"
	"github.com/dev-dami/deck/internal/deck/agent/codex"
	"github.com/dev-dami/deck/internal/deck/agent/opencode"
	"github.com/dev-dami/deck/internal/deck/approve"
	"github.com/dev-dami/deck/internal/deck/config"
	"github.com/dev-dami/deck/internal/deck/session"
	"github.com/dev-dami/deck/internal/deck/store"
)

// Engine is a running thread manager and what it needs.
type Engine struct {
	Manager *session.Manager
	Config  config.Config
	broker  *approve.Broker
}

// Start finds the CLIs and starts the thread manager.
func Start() (*Engine, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	st, err := store.New("")
	if err != nil {
		return nil, fmt.Errorf("threads: %w", err)
	}
	self, err := os.Executable()
	if err != nil {
		return nil, err
	}
	broker, err := approve.NewBroker()
	if err != nil {
		return nil, fmt.Errorf("approval broker: %w", err)
	}
	adapters := map[string]agent.Adapter{}
	if found(cfg.ClaudeBinary, "claude") {
		adapters["claude"] = claude.New(cfg.ClaudeBinary, self, broker)
	}
	if found(cfg.CodexBinary, "codex") {
		adapters["codex"] = codex.New(cfg.CodexBinary)
	}
	if found(cfg.OpencodeBinary, "opencode") {
		adapters["opencode"] = opencode.New(cfg.OpencodeBinary)
	}
	if len(adapters) == 0 {
		broker.Close()
		return nil, fmt.Errorf("none of claude, codex or opencode was found on PATH — run `deck doctor`")
	}
	if adapters[cfg.DefaultAgent] == nil {
		for _, k := range config.Kinds {
			if adapters[k] != nil {
				cfg.DefaultAgent = k
				break
			}
		}
	}
	mgr := session.NewManager(adapters, st, cfg, session.Options{
		Hub:    broker,
		MCPFor: func(id string) *agent.MCPServer { return broker.ServerFor(self, id) },
	})
	return &Engine{Manager: mgr, Config: cfg, broker: broker}, nil
}

// Close stops every agent and the approval broker.
func (e *Engine) Close() {
	e.Manager.Close()
	e.broker.Close()
}

// found reports whether the configured binary (or the default name) is runnable.
func found(configured, def string) bool {
	name := configured
	if name == "" {
		name = def
	}
	_, err := exec.LookPath(name)
	return err == nil
}

// MCPApprove is the stdio MCP server Claude Code launches for
// --permission-prompt-tool. The broker token arrives via the environment,
// never on the command line.
func MCPApprove(args []string) error {
	fs := flag.NewFlagSet("mcp-approve", flag.ContinueOnError)
	addr := fs.String("addr", "", "broker address")
	id := fs.String("agent", "", "agent id")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *addr == "" || *id == "" {
		return fmt.Errorf("mcp-approve: --addr and --agent are required")
	}
	return approve.MCPServe(os.Stdin, os.Stdout, *addr, approve.TokenFromEnv(), *id)
}
