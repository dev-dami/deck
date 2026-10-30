//go:build ignore

// A real Deck workspace/RPC server with explicit temporary stores and a
// deterministic CLI adapter. It never reads or writes the user's settings,
// thread store, login, HOME or CODEX_HOME. Build explicitly before running
// artifact-integration.cjs (see the commands at the top of that file).
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/dev-dami/deck/internal/app"
	appconfig "github.com/dev-dami/deck/internal/config"
	"github.com/dev-dami/deck/internal/db"
	"github.com/dev-dami/deck/internal/deck/agent"
	"github.com/dev-dami/deck/internal/deck/agent/claude"
	"github.com/dev-dami/deck/internal/deck/approve"
	"github.com/dev-dami/deck/internal/deck/config"
	"github.com/dev-dami/deck/internal/deck/engine"
	"github.com/dev-dami/deck/internal/deck/session"
	"github.com/dev-dami/deck/internal/deck/store"
	"github.com/dev-dami/deck/internal/skills"
	"github.com/dev-dami/deck/internal/workspace"
	"github.com/dev-dami/deck/pkg/remote/adapter"
	"github.com/dev-dami/deck/pkg/rpc"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "mcp-approve" {
		if err := engine.MCPApprove(os.Args[2:]); err != nil {
			panic(err)
		}
		return
	}
	if err := serve(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func serve() error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	dir := os.Getenv("DECK_ARTIFACT_TEST_DATA")
	if !filepath.IsAbs(dir) {
		return fmt.Errorf("DECK_ARTIFACT_TEST_DATA must be absolute")
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	cfg := config.Defaults()
	cfg.ClaudeBinary = os.Getenv("DECK_ARTIFACT_TEST_AGENT")
	cfg.DefaultAgent, cfg.AutoTitle, cfg.TrackChanges, cfg.Orchestration = "claude", false, false, "off"
	st, err := store.New(filepath.Join(dir, "threads"))
	if err != nil {
		return err
	}
	broker, err := approve.NewBroker()
	if err != nil {
		return err
	}
	defer broker.Close()
	self, err := os.Executable()
	if err != nil {
		return err
	}
	cli := claude.New(cfg.ClaudeBinary, self, broker)
	mgr := session.NewManager(map[string]agent.Adapter{"claude": cli}, st, cfg, session.Options{
		Hub: broker, MCPFor: func(id string) *agent.MCPServer { return broker.ServerFor(self, id) },
	})
	defer mgr.Close()
	conf := appconfig.NewTestStore(&appconfig.Config{Options: &appconfig.Options{DataDirectory: dir}})
	conn, err := db.Connect(ctx, dir)
	if err != nil {
		return err
	}
	a, err := app.New(ctx, conn, conf, skills.NewManager(nil, nil, nil))
	if err != nil {
		return err
	}
	ws := workspace.NewDeckWorkspace(a, conf, &engine.Engine{Manager: mgr, Config: cfg})
	defer ws.Shutdown()
	be, err := adapter.New(ws, "artifact-integration", "Artifacts fixture")
	if err != nil {
		return err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	l, sock, closeSock, err := rpc.Listen(dir)
	if err != nil {
		return err
	}
	defer closeSock()
	token := rpc.NewToken()
	srv := rpc.New(be, token).WithProject(cwd, nil).WithWorkspace(ws)
	defer srv.Close()
	go srv.Run(ctx)
	g := srv.GRPC()
	defer g.Stop()
	go func() { _ = g.Serve(l) }()
	if err := json.NewEncoder(os.Stdout).Encode(rpc.Handshake{V: rpc.Protocol, Sock: sock, Token: token, PID: os.Getpid(), Version: "artifact-integration", BrowserCapability: srv.BrowserCapability()}); err != nil {
		return err
	}
	go func() { _, _ = io.Copy(io.Discard, os.Stdin); cancel() }()
	<-ctx.Done()
	return nil
}
