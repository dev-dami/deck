package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	deckconfig "github.com/dev-dami/deck/internal/deck/config"
	"github.com/dev-dami/deck/internal/deck/engine"
	"github.com/dev-dami/deck/internal/usage"
	"github.com/dev-dami/deck/internal/version"
	"github.com/dev-dami/deck/internal/workspace"
	"github.com/dev-dami/deck/pkg/remote/adapter"
	"github.com/dev-dami/deck/pkg/remote/server"
	"github.com/dev-dami/deck/pkg/rpc"
)

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Run Deck headless for teammates (no terminal UI)",
	Long: `Run Deck on a server: the agents and the peer link (the listener and the
links that peers in the config ask for), with no terminal UI. Add --remote to
also serve Deck to your phone (see 'deck remote --help').

Peers must be on (peers.enabled in Deck's config.json), unless you add --remote.
deck serve has the thread store (~/.deck/threads) to itself: it does not
start while a terminal Deck runs, and a terminal Deck does not start while it
runs. Either one stops at once and names the lock file.

On SIGTERM or ctrl+c, Deck refuses new work from teammates, gives running
agents the --drain time to end their turns and the outbox time to deliver,
and then stops. A second signal stops it at once. On SIGHUP it applies the
peers section of the config again (after 'deck peer relay', say). Logs go to
stderr.

With --rpc, Deck instead serves this project to the Deck desktop app over
gRPC (proto/deck/v1) on a Unix socket only this user can open. It prints one
JSON line on stdout with the socket path and a token made for this run, and
stops when stdin closes, so it lives as long as the app that started it.`,
	Example: `
# On a server (see docs/examples/deck-serve.service)
deck serve

# Give agents 5 minutes to end their turns on shutdown
deck serve --drain 5m

# Also serve to your phone on the same network
deck serve --remote --lan

# Serve this project to the desktop app (the app runs this itself)
deck serve --rpc
`,
	Args: cobra.NoArgs,
	RunE: runServe,
}

func init() {
	serveCmd.Flags().Duration("drain", 2*time.Minute, "How long running agents get to end their turns when Deck stops")
	serveCmd.Flags().Bool("remote", false, "Also serve Deck to your phone (see 'deck remote --help')")
	serveCmd.Flags().Bool("rpc", false, "Serve this project to the desktop app: gRPC on a local socket, until stdin closes")
	addRemoteFlags(serveCmd.Flags())
	rootCmd.AddCommand(serveCmd)
}

func runServe(cmd *cobra.Command, _ []string) error {
	if on, _ := cmd.Flags().GetBool("rpc"); on {
		return runServeRPC(cmd)
	}
	limit, _ := cmd.Flags().GetDuration("drain")
	remoteOn, _ := cmd.Flags().GetBool("remote")
	logToStderr(cmd)
	// serve has the thread store alone: no terminal Deck writes it meanwhile.
	engine.ExclusiveStore = true
	cfg, err := deckconfig.Load()
	if err != nil {
		return err
	}
	if err := servePeersOn(cfg, remoteOn); err != nil {
		return err
	}

	var eng *engine.Engine
	stopRemote := func() {}
	var remoteDone <-chan struct{}
	if remoteOn {
		ro, err := remoteRunOptions(cmd, true)
		if err != nil {
			return err
		}
		ws, cleanup, err := setupWorkspace(cmd)
		if err != nil {
			return err
		}
		defer cleanup()
		logToStderr(cmd) // setupWorkspace sends the logs to the project's log file
		dw, ok := ws.(*workspace.DeckWorkspace)
		if !ok {
			return errors.New("deck serve needs Deck's own workspace")
		}
		eng = dw.Engine()
		r, err := startRemote(cmd, ro, ws, cmd.ErrOrStderr())
		if err != nil {
			return err
		}
		stopRemote = sync.OnceFunc(func() {
			server.SetActive(nil)
			_ = r.Store.Disarm()
			r.Stop()
		})
		defer stopRemote()
		remoteDone = r.Done()
		printBanner(cmd.ErrOrStderr(), r)
	} else {
		eng, err = engine.Start()
		if err != nil {
			return err
		}
		defer eng.Close()
		// No view reads the agents' updates here. Drop them, so the engine
		// never blocks on a full stream.
		go func() {
			next := eng.Manager.Listen()
			for next() != nil {
			}
		}()
	}

	name, fp, listen, relay := "", "", "off", false
	if p := eng.Peers(); p != nil {
		snap := p.Snapshot()
		name, fp, relay = snap.Name, snap.Fingerprint, snap.Relay
		if snap.Listening != "" {
			listen = snap.Listening
		}
		if snap.ListenErr != "" {
			slog.Warn("deck serve: the peer listener is not up", "error", snap.ListenErr)
		}
	}
	slog.Info("deck serve: started", "name", name, "fingerprint", fp, "listen", listen, "relay", relay, "remote", remoteOn, "pid", os.Getpid())

	sigs := make(chan os.Signal, 2)
	signal.Notify(sigs, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigs)
	hup := make(chan os.Signal, 1)
	signal.Notify(hup, syscall.SIGHUP)
	defer signal.Stop(hup)
wait:
	for {
		select {
		case sig := <-sigs:
			slog.Info("deck serve: draining", "signal", sig.String(), "drain", limit.String())
			break wait
		case <-hup:
			reloadPeers(eng)
		case <-remoteDone:
			return errors.New("the remote server stopped unexpectedly")
		}
	}
	stopRemote()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		select {
		case <-sigs:
			slog.Warn("deck serve: second signal, stopping now")
			cancel()
		case <-ctx.Done():
		}
	}()
	drain(ctx, eng, limit)
	slog.Info("deck serve: stopped")
	return nil
}

// reloadPeers puts the peers section of the config file in effect again.
func reloadPeers(eng *engine.Engine) {
	p := eng.Peers()
	if p == nil {
		slog.Warn("deck serve: reload: the peer link is not running; restart to turn it on")
		return
	}
	if err := p.Reload(); err != nil {
		slog.Warn("deck serve: reload: not all of the peers config is in effect", "error", err)
		return
	}
	snap := p.Snapshot()
	slog.Info("deck serve: reloaded the peers config", "listen", snap.Listening, "relay", snap.Relay, "peers", len(snap.Peers))
}

// servePeersOn refuses a serve with nothing to serve: peers off and no
// phone remote.
func servePeersOn(cfg deckconfig.Config, remote bool) error {
	if remote || (cfg.Peers != nil && cfg.Peers.Enabled) {
		return nil
	}
	path, _ := deckconfig.Path()
	return fmt.Errorf("peers are off, so deck serve has nothing to serve: set peers.enabled in %s (or turn on Team in Settings), or add --remote", path)
}

// logToStderr sends the logs to stderr as plain text lines, for systemd.
func logToStderr(cmd *cobra.Command) {
	level := slog.LevelInfo
	if debug, _ := cmd.Flags().GetBool("debug"); debug {
		level = slog.LevelDebug
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(cmd.ErrOrStderr(), &slog.HandlerOptions{Level: level})))
}

// drainer is the part of the engine that drain uses.
type drainer interface {
	Drain()
	Busy() int
	Waiting() int
}

const (
	// drainPoll is how often drain looks at the agents and the outbox.
	drainPoll = 200 * time.Millisecond
	// outboxGrace caps the wait for the outbox after the agents stop. A
	// teammate that is away gets the calls after the next start.
	outboxGrace = 15 * time.Second
)

// drain refuses new work, then waits until no agent is in a turn and then
// until the outbox is empty. The whole wait takes at most limit, and the
// outbox wait at most outboxGrace. When ctx ends, drain returns at once.
func drain(ctx context.Context, d drainer, limit time.Duration) {
	d.Drain()
	ctx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	if !waitUntil(ctx, func() bool { return d.Busy() == 0 }) {
		slog.Warn("deck serve: agents are still in a turn; stopping them", "agents", d.Busy())
	}
	octx, ocancel := context.WithTimeout(ctx, outboxGrace)
	defer ocancel()
	if !waitUntil(octx, func() bool { return d.Waiting() == 0 }) {
		slog.Info("deck serve: calls wait in the outbox for the next start", "calls", d.Waiting())
	}
}

// waitUntil polls cond until it holds (true) or ctx ends (false).
func waitUntil(ctx context.Context, cond func() bool) bool {
	t := time.NewTicker(drainPoll)
	defer t.Stop()
	for !cond() {
		select {
		case <-ctx.Done():
			return false
		case <-t.C:
		}
	}
	return true
}

// runServeRPC serves the desktop app: Deck's engine for this project over
// gRPC (proto/deck/v1) on a Unix socket only this user can open. It prints
// one JSON line on stdout with the socket path and a token made for this
// run, and stops when stdin closes, so it lives as long as the app.
func runServeRPC(cmd *cobra.Command) error {
	dataDir, err := remoteDataDir(cmd)
	if err != nil {
		return err
	}
	ws, cleanup, err := setupWorkspace(cmd)
	if err != nil {
		return err
	}
	defer cleanup()

	cwd, err := ResolveCwd(cmd)
	if err != nil {
		return err
	}
	be, err := adapter.New(ws, version.Version, filepath.Base(cwd))
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// The desktop app's project is the phone's too once it is shared: offer
	// it to the background process as the terminal UI does, or the phone
	// lists the project with no conversations.
	defer startMobileProvider(cmd, ws)()

	l, sock, closeSock, err := rpc.Listen(dataDir)
	if err != nil {
		return err
	}
	defer closeSock()

	token := rpc.NewToken()
	// The project folder is where git runs and terminals start; the usage
	// record is Deck's own, when the workspace keeps one.
	var record usage.Service
	if u, ok := ws.(interface{ UsageService() usage.Service }); ok {
		record = u.UsageService()
	}
	srv := rpc.New(be, token).WithProject(cwd, record).WithWorkspace(ws)
	defer srv.Close()
	go srv.Run(ctx)
	g := srv.GRPC()
	go func() { _ = g.Serve(l) }()

	hs, _ := json.Marshal(rpc.Handshake{V: rpc.Protocol, Sock: sock, Token: token, PID: os.Getpid(), Version: version.Version, BrowserCapability: srv.BrowserCapability()})
	if _, err := cmd.OutOrStdout().Write(append(hs, '\n')); err != nil {
		g.Stop()
		return err
	}

	// The app holds our stdin; when it goes away, so do we.
	go func() {
		_, _ = io.Copy(io.Discard, cmd.InOrStdin())
		cancel()
	}()
	<-ctx.Done()
	g.Stop()
	return nil
}
