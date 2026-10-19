// Package engine starts Deck's agent engine: the installed claude, codex,
// opencode and agy CLIs, driven over their own protocols under the user's own
// login, and the thread manager that puts them in shared threads.
//
// Deck never reads the CLIs' credentials and never calls a model API itself.
package engine

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"

	"github.com/dev-dami/deck/internal/deck/agent"
	"github.com/dev-dami/deck/internal/deck/agent/antigravity"
	"github.com/dev-dami/deck/internal/deck/agent/claude"
	"github.com/dev-dami/deck/internal/deck/agent/codex"
	"github.com/dev-dami/deck/internal/deck/agent/opencode"
	"github.com/dev-dami/deck/internal/deck/approve"
	"github.com/dev-dami/deck/internal/deck/config"
	"github.com/dev-dami/deck/internal/deck/peer"
	"github.com/dev-dami/deck/internal/deck/session"
	"github.com/dev-dami/deck/internal/deck/store"
	"github.com/dev-dami/deck/internal/deck/sub"
)

// Engine is a running thread manager and what it needs.
type Engine struct {
	Manager *session.Manager
	Config  config.Config
	broker  *approve.Broker
	// peers is the peer link, made idle; its Manager starts the first time
	// peers are on, at startup or from Settings. Nil only when the peers
	// directory cannot be found.
	peers *peer.Service
	// unlock drops the lock on the thread store; nil when none was taken.
	unlock func()
}

// Peers is the peer link, or nil.
func (e *Engine) Peers() *peer.Service { return e.peers }

// ExclusiveStore makes Start take the thread store alone (deck serve);
// otherwise it shares it with other terminal Decks (store.Lock).
var ExclusiveStore bool

// Start finds the CLIs and starts the thread manager. It takes the lock on
// the thread store first (store.Lock).
func Start() (_ *Engine, err error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	unlock, err := store.Lock("", ExclusiveStore)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			unlock()
		}
	}()
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
	if found(cfg.AntigravityBinary, "agy") {
		adapters["antigravity"] = antigravity.New(cfg.AntigravityBinary)
	}
	if len(adapters) == 0 {
		broker.Close()
		return nil, fmt.Errorf("none of claude, codex, opencode or antigravity was found on PATH — run `deck doctor`")
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
	work := &localWork{mgr: mgr}
	deals := newContracts(mgr)
	peers := startPeers(cfg, peer.ServiceOptions{SubStore: mgr.SubStore, Local: peer.Local{Presence: work.Presence, Repos: work.Repos},
		Contracts: deals.hooks(), Shared: sharing{mgr}})
	if peers != nil {
		deals.svc.Store(peers)
		mgr.SetContractor(deals)
		mgr.SetTaskEnd(deals.taskEnd)
		mgr.SetTeam(peerTeam{peers})
		mgr.SetSubFetch(func(ctx context.Context, name string, id sub.ID, s *sub.Store) (int, error) {
			return peers.FetchCapsule(ctx, name, id, s)
		})
	}
	go mgr.CollectSub()
	return &Engine{Manager: mgr, Config: cfg, broker: broker, peers: peers, unlock: unlock}, nil
}

// startPeers makes the peer link and starts it when peers.enabled is set;
// otherwise it runs nothing at all (no key, no listener, no goroutines)
// until Settings turns peers on. A failure is logged and leaves Deck running
// without peers.
func startPeers(cfg config.Config, o peer.ServiceOptions) *peer.Service {
	// The config path lets the link pick up role, allow_inbound and remote
	// edits while Deck runs, and lets Settings change the rest.
	o.ConfigPath, _ = config.Path()
	svc, err := peer.NewService(o)
	if err != nil {
		slog.Warn("Peers not available", "error", err)
		return nil
	}
	if err := svc.Start(cfg.Peers); err != nil {
		slog.Warn("Peers not started", "error", err)
	}
	return svc
}

// Drain makes teammates unable to start new work here: new contracts and
// posts to shared threads are refused. Work under way goes on.
func (e *Engine) Drain() {
	if e.peers != nil {
		e.peers.Drain()
	}
}

// Busy is how many agents are in a turn, or wait for an approval in one.
func (e *Engine) Busy() int {
	n := 0
	for _, a := range e.Manager.Agents() {
		switch a.Status {
		case session.StatusWorking, session.StatusTool, session.StatusApproval:
			n++
		}
	}
	return n
}

// Waiting is how many peer calls wait in the outbox for a teammate.
func (e *Engine) Waiting() int {
	if e.peers == nil {
		return 0
	}
	return e.peers.Waiting()
}

// Close stops every agent, the peer link and the approval broker, and then
// drops the lock on the thread store.
func (e *Engine) Close() {
	if e.peers != nil {
		e.peers.Close()
	}
	e.Manager.Close()
	e.broker.Close()
	if e.unlock != nil {
		e.unlock()
	}
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

// MCPApprove is Deck's stdio MCP server, which every agent CLI launches: the
// search and execute tools, plus approve (Claude Code's
// --permission-prompt-tool) with --approve-tool. The broker token arrives via the environment, or
// in a file named by --token-file for CLIs that would otherwise keep it in
// their own config; never on the command line.
func MCPApprove(args []string) error {
	fs := flag.NewFlagSet("mcp-approve", flag.ContinueOnError)
	addr := fs.String("addr", "", "broker address")
	id := fs.String("agent", "", "agent id")
	tokenFile := fs.String("token-file", "", "file holding the broker token")
	approveTool := fs.Bool(strings.TrimPrefix(approve.ApproveToolFlag, "--"), false, "also expose the approve tool (Claude's permission prompts)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *addr == "" || *id == "" {
		return fmt.Errorf("mcp-approve: --addr and --agent are required")
	}
	token := approve.TokenFromEnv()
	if *tokenFile != "" {
		b, err := os.ReadFile(*tokenFile)
		if err != nil {
			return fmt.Errorf("mcp-approve: %w", err)
		}
		token = strings.TrimSpace(string(b))
	}
	return approve.MCPServe(os.Stdin, os.Stdout, *addr, token, *id, *approveTool)
}
