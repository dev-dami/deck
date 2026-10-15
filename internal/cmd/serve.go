package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/dev-dami/deck/internal/version"
	"github.com/dev-dami/deck/pkg/remote/adapter"
	"github.com/dev-dami/deck/pkg/rpc"
)

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Run Deck headless for the desktop app (gRPC over a local socket)",
	Long: `Run Deck's engine for this project with no terminal UI and serve it to the
Deck desktop app over gRPC (proto/deck/v1) on a Unix socket only this user
can open.

Once listening it prints one JSON line on stdout with the socket path and a
token made for this run, and it stops when stdin closes, so it lives exactly
as long as the app that started it.`,
	Args: cobra.NoArgs,
	RunE: runServe,
}

func init() {
	serveCmd.Flags().Bool("rpc", false, "Serve gRPC on a local socket (the desktop app's transport)")
	rootCmd.AddCommand(serveCmd)
}

func runServe(cmd *cobra.Command, _ []string) error {
	if on, _ := cmd.Flags().GetBool("rpc"); !on {
		return errors.New("pass --rpc (the only transport so far; for a phone see 'deck remote')")
	}
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

	l, sock, closeSock, err := rpc.Listen(dataDir)
	if err != nil {
		return err
	}
	defer closeSock()

	token := rpc.NewToken()
	srv := rpc.New(be, token)
	go srv.Run(ctx)
	g := srv.GRPC()
	go func() { _ = g.Serve(l) }()

	hs, _ := json.Marshal(rpc.Handshake{V: rpc.Protocol, Sock: sock, Token: token, PID: os.Getpid(), Version: version.Version})
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
