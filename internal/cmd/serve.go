package cmd

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/dev-dami/deck/internal/remote"
	"github.com/dev-dami/deck/internal/remote/auth"
	"github.com/dev-dami/deck/internal/remote/tunnel"
	"github.com/dev-dami/deck/internal/version"
	"github.com/dev-dami/deck/internal/workspace"
)

// addRemoteFlags registers the flags both `deck serve` and `deck --remote` take.
func addRemoteFlags(fs *pflag.FlagSet) {
	fs.Int("port", remote.DefaultPort, "Port for remote control (0 picks a free one)")
	fs.Bool("lan", false, "Let phones on the same Wi-Fi connect (plain http, no install-to-home-screen)")
	fs.String("tunnel", "", "Reach Deck from anywhere through a tunnel: cloudflare (default) or tailscale")
	fs.Lookup("tunnel").NoOptDefVal = tunnel.Cloudflare
	fs.Bool("allow-yolo", false, "Let a phone switch Deck into yolo mode (approve everything)")
}

func remoteRunOptions(cmd *cobra.Command, headless bool) (remote.RunOptions, error) {
	port, _ := cmd.Flags().GetInt("port")
	lan, _ := cmd.Flags().GetBool("lan")
	tun, _ := cmd.Flags().GetString("tunnel")
	yolo, _ := cmd.Flags().GetBool("allow-yolo")
	if port < 0 || port > 65535 {
		return remote.RunOptions{}, fmt.Errorf("--port %d is not a valid port", port)
	}
	if tun != "" && tun != tunnel.Cloudflare && tun != tunnel.Tailscale {
		return remote.RunOptions{}, fmt.Errorf("--tunnel %q: use %s or %s", tun, tunnel.Cloudflare, tunnel.Tailscale)
	}
	return remote.RunOptions{Port: port, LAN: lan, Tunnel: tun, AllowYolo: yolo, Headless: headless, Version: version.Version}, nil
}

// startRemote starts the remote server over ws.
func startRemote(cmd *cobra.Command, ro remote.RunOptions, ws workspace.Workspace) (*remote.Running, error) {
	cwd, err := ResolveCwd(cmd)
	if err != nil {
		return nil, err
	}
	be, err := remote.NewWorkspaceBackend(ws, remote.Info{Version: version.Version, Project: filepath.Base(cwd), Cwd: cwd})
	if err != nil {
		return nil, err
	}
	r, err := remote.Start(cmd.Context(), be, ro)
	if err != nil {
		var missing *tunnel.ErrNotInstalled
		if errors.As(err, &missing) {
			return nil, fmt.Errorf("%w; or drop --tunnel and use --lan on the same Wi-Fi", err)
		}
		return nil, err
	}
	remote.SetActive(r)
	return r, nil
}

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Serve Deck to your phone (no terminal UI)",
	Long: `Serve Deck's sessions to a phone's browser: read the chat, send prompts, answer
approvals, switch models. Nothing listens until you run this.

Only this machine can connect unless you choose how a phone gets here:
  --lan      the same Wi-Fi (plain http)
  --tunnel   anywhere (needs cloudflared, or tailscale with --tunnel=tailscale)

Deck keeps one process per project folder, so while this runs the terminal UI
cannot open the same project. To use both, run 'deck --remote' instead.`,
	Example: `
# Same Wi-Fi
deck serve --lan

# From anywhere
deck serve --tunnel

# Stable address on your tailnet
deck serve --tunnel=tailscale
`,
	Args: cobra.NoArgs,
	RunE: runServe,
}

var serveDevicesCmd = &cobra.Command{
	Use:   "devices",
	Short: "List paired phones",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		store, err := auth.New(remote.DefaultDir())
		if err != nil {
			return err
		}
		return printDevices(cmd.OutOrStdout(), store)
	},
}

var serveRevokeCmd = &cobra.Command{
	Use:   "revoke <id>|--all",
	Short: "Unpair a phone (a running server drops it within seconds)",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		store, err := auth.New(remote.DefaultDir())
		if err != nil {
			return err
		}
		all, _ := cmd.Flags().GetBool("all")
		return revokeDevices(cmd.OutOrStdout(), store, args, all)
	},
}

func init() {
	addRemoteFlags(serveCmd.Flags())
	serveRevokeCmd.Flags().Bool("all", false, "Unpair every phone")
	serveCmd.AddCommand(serveDevicesCmd, serveRevokeCmd)
	rootCmd.AddCommand(serveCmd)

	rootCmd.Flags().Bool("remote", false, "Also serve this session to your phone (see 'deck serve --help')")
	addRemoteFlags(rootCmd.Flags())
}

func printDevices(w io.Writer, store *auth.Store) error {
	devs, err := store.List()
	if err != nil {
		return err
	}
	if len(devs) == 0 {
		fmt.Fprintln(w, "No paired phones.")
		return nil
	}
	for _, d := range devs {
		fmt.Fprintf(w, "%s  %-24s paired %s  last seen %s\n", d.ID, d.Name, d.CreatedAt.Local().Format("2006-01-02"), d.LastSeen.Local().Format("2006-01-02 15:04"))
	}
	return nil
}

func revokeDevices(w io.Writer, store *auth.Store, args []string, all bool) error {
	switch {
	case all && len(args) > 0:
		return errors.New("give a device id or --all, not both")
	case all:
		n, err := store.RevokeAll()
		if err != nil {
			return err
		}
		fmt.Fprintf(w, "Unpaired %d phone(s).\n", n)
		return nil
	case len(args) == 0:
		return errors.New("give a device id (see 'deck serve devices') or --all")
	}
	if err := store.Revoke(args[0]); err != nil {
		if errors.Is(err, auth.ErrNotFound) {
			return fmt.Errorf("no paired phone %q (see 'deck serve devices')", args[0])
		}
		return err
	}
	fmt.Fprintln(w, "Unpaired.")
	return nil
}

// printBanner tells the user how to reach the server and shows the pairing QR.
func printBanner(w io.Writer, r *remote.Running, ro remote.RunOptions) error {
	fmt.Fprintln(w, "Deck remote control is on.")
	if r.PublicURL != "" {
		fmt.Fprintf(w, "  anywhere     %s  (%s tunnel)\n", r.PublicURL, r.Tunnel)
	}
	for _, u := range r.LANURLs {
		fmt.Fprintf(w, "  same Wi-Fi   %s\n", u)
	}
	fmt.Fprintf(w, "  this machine %s\n", r.LocalURL)

	switch {
	case r.PublicURL == "" && len(r.LANURLs) == 0:
		fmt.Fprintln(w, "\nOnly this machine can reach it. Add --lan (same Wi-Fi) or --tunnel (anywhere) for a phone.")
	case r.PublicURL == "":
		fmt.Fprintln(w, "\nPlain http on your Wi-Fi: anyone on the network can see the traffic, and the phone cannot install the app to its home screen. Use --tunnel for https.")
	case r.Tunnel == tunnel.Cloudflare:
		fmt.Fprintln(w, "\nCloudflare quick tunnels get a new address every run: scan again after a restart. Cloudflare relays the traffic; use --tunnel=tailscale to keep it private and stable.")
	}
	if ro.AllowYolo {
		fmt.Fprintln(w, "\nWARNING: --allow-yolo lets a paired phone make every agent run anything without asking.")
	}

	link, qr, err := r.Pairing()
	if err != nil {
		return err
	}
	fmt.Fprintf(w, "\nScan with your phone's camera (valid %d minutes, one use):\n\n%s\n%s\n", int(auth.CodeTTL/time.Minute), qr, link)
	return nil
}

func runServe(cmd *cobra.Command, _ []string) error {
	ro, err := remoteRunOptions(cmd, true)
	if err != nil {
		return err
	}
	ws, cleanup, err := setupWorkspaceWithProgressBar(cmd)
	if err != nil {
		return err
	}
	defer cleanup()

	ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	cmd.SetContext(ctx)

	r, err := startRemote(cmd, ro, ws)
	if err != nil {
		return err
	}
	defer func() {
		remote.SetActive(nil)
		r.Stop()
	}()

	out := cmd.OutOrStdout()
	if err := printBanner(out, r, ro); err != nil {
		return err
	}
	fmt.Fprintln(out, "\nPress enter for a new pairing code, ctrl+c to stop.")

	lines := make(chan struct{})
	go func() {
		sc := bufio.NewScanner(os.Stdin)
		for sc.Scan() {
			select {
			case lines <- struct{}{}:
			case <-ctx.Done():
				return
			}
		}
	}()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-r.Done():
			return errors.New("the remote server stopped unexpectedly")
		case <-lines:
			if r.Server.PairingDisabled() {
				fmt.Fprintln(out, "Pairing is disabled after repeated failed attempts; restart deck serve.")
				continue
			}
			if err := printBanner(out, r, ro); err != nil {
				return err
			}
		}
	}
}
