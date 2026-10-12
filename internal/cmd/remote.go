package cmd

import (
	"bufio"
	"context"
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

	"github.com/dev-dami/deck/internal/config"
	"github.com/dev-dami/deck/internal/version"
	"github.com/dev-dami/deck/internal/workspace"
	"github.com/dev-dami/deck/pkg/remote"
	"github.com/dev-dami/deck/pkg/remote/adapter"
	"github.com/dev-dami/deck/pkg/remote/auth"
	"github.com/dev-dami/deck/pkg/remote/server"
	"github.com/dev-dami/deck/pkg/remote/wire"
)

// addRemoteFlags registers the flags `deck remote` and `deck --remote` share.
func addRemoteFlags(fs *pflag.FlagSet) {
	fs.Int("port", server.DefaultPort, "Port for remote control (0 picks a free one)")
	fs.Bool("lan", false, "Let phones on the same Wi-Fi connect (self-signed https)")
}

var remoteCmd = &cobra.Command{
	Use:   "remote",
	Short: "Serve Deck to your phone (no terminal UI)",
	Long: `Serve Deck's sessions to a phone's browser: read the chat, send prompts,
answer approvals, switch models. Nothing listens until you run this.

Only this machine can connect unless you add --lan, which serves https with
a certificate made for this project; the phone compares its fingerprint
with the one shown here before it pairs.

Deck keeps one process per project folder, so while this runs the terminal
UI cannot open the same project. To use both, run 'deck --remote' instead.
Pair a phone with 'deck remote pair' from another terminal.`,
	Example: `
# Same Wi-Fi
deck remote --lan

# Pair a phone (from another terminal)
deck remote pair

# Paired phones
deck remote devices
`,
	Args: cobra.NoArgs,
	RunE: runRemote,
}

var remotePairCmd = &cobra.Command{
	Use:   "pair",
	Short: "Show a one-time code for a phone to pair with the running server",
	Args:  cobra.NoArgs,
	RunE:  runRemotePair,
}

var remoteDevicesCmd = &cobra.Command{
	Use:   "devices",
	Short: "List paired phones, or let one switch Deck into yolo mode",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		store, err := remoteStore(cmd)
		if err != nil {
			return err
		}
		if id, _ := cmd.Flags().GetString("allow-yolo"); id != "" {
			if err := store.SetAllowYolo(id, true); err != nil {
				return deviceErr(err, id)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Phone %s may now switch Deck into yolo mode.\n", id)
		}
		if id, _ := cmd.Flags().GetString("deny-yolo"); id != "" {
			if err := store.SetAllowYolo(id, false); err != nil {
				return deviceErr(err, id)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Phone %s may no longer switch Deck into yolo mode.\n", id)
		}
		return printDevices(cmd.OutOrStdout(), store)
	},
}

var remoteRevokeCmd = &cobra.Command{
	Use:   "revoke <id>",
	Short: "Unpair a phone (a running server drops it within seconds)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		store, err := remoteStore(cmd)
		if err != nil {
			return err
		}
		if err := store.Revoke(args[0]); err != nil {
			return deviceErr(err, args[0])
		}
		fmt.Fprintln(cmd.OutOrStdout(), "Unpaired.")
		return nil
	},
}

func init() {
	addRemoteFlags(remoteCmd.Flags())
	remoteDevicesCmd.Flags().String("allow-yolo", "", "Let this phone switch Deck into yolo mode (approve everything)")
	remoteDevicesCmd.Flags().String("deny-yolo", "", "Take yolo away from this phone")
	remoteCmd.AddCommand(remotePairCmd, remoteDevicesCmd, remoteRevokeCmd)
	rootCmd.AddCommand(remoteCmd)

	rootCmd.Flags().Bool("remote", false, "Also serve this session to your phone (see 'deck remote --help')")
	addRemoteFlags(rootCmd.Flags())
}

func deviceErr(err error, id string) error {
	if errors.Is(err, auth.ErrNotFound) {
		return fmt.Errorf("no paired phone %q (see 'deck remote devices')", id)
	}
	return err
}

// remoteDataDir resolves the project's data dir without opening the app.
func remoteDataDir(cmd *cobra.Command) (string, error) {
	cwd, err := ResolveCwd(cmd)
	if err != nil {
		return "", err
	}
	dataDir, _ := cmd.Flags().GetString("data-dir")
	cfg, err := config.Init(cwd, dataDir, false)
	if err != nil {
		return "", err
	}
	return cfg.Config().Options.DataDirectory, nil
}

func remoteStore(cmd *cobra.Command) (*auth.Store, error) {
	dir, err := remoteDataDir(cmd)
	if err != nil {
		return nil, err
	}
	return auth.Open(auth.Dir(dir))
}

func remoteRunOptions(cmd *cobra.Command, headless bool) (server.RunOptions, error) {
	port, _ := cmd.Flags().GetInt("port")
	lan, _ := cmd.Flags().GetBool("lan")
	if port < 0 || port > 65535 {
		return server.RunOptions{}, fmt.Errorf("--port %d is not a valid port", port)
	}
	dir, err := remoteDataDir(cmd)
	if err != nil {
		return server.RunOptions{}, err
	}
	return server.RunOptions{Port: port, LAN: lan, Headless: headless, Version: version.Version, DataDir: dir}, nil
}

// startRemote starts the remote server over ws and records it as this
// process's.
func startRemote(cmd *cobra.Command, ro server.RunOptions, ws workspace.Workspace, out io.Writer) (*server.Running, error) {
	cwd, err := ResolveCwd(cmd)
	if err != nil {
		return nil, err
	}
	be, err := adapter.New(ws, version.Version, filepath.Base(cwd))
	if err != nil {
		return nil, err
	}
	ro.Paired = func(d auth.Device) {
		fmt.Fprintf(out, "Paired %s (%s).\n", d.Name, d.ID)
	}
	r, err := server.Start(cmd.Context(), be, ro)
	if err != nil {
		return nil, err
	}
	server.SetActive(r)
	return r, nil
}

// startRemoteIfAsked starts the server beside the TUI when --remote is
// set; the returned stop is a no-op otherwise.
func startRemoteIfAsked(cmd *cobra.Command, ws workspace.Workspace) (stop func(), err error) {
	if on, _ := cmd.Flags().GetBool("remote"); !on {
		return func() {}, nil
	}
	ro, err := remoteRunOptions(cmd, false)
	if err != nil {
		return nil, err
	}
	r, err := startRemote(cmd, ro, ws, io.Discard)
	if err != nil {
		return nil, err
	}
	return func() {
		server.SetActive(nil)
		r.Stop()
	}, nil
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
		seen := "never"
		if !d.LastSeen.IsZero() {
			seen = d.LastSeen.Local().Format("2006-01-02 15:04")
		}
		yolo := ""
		if d.AllowYolo {
			yolo = "  yolo"
		}
		fmt.Fprintf(w, "%s  %-24s paired %s  last seen %s  expires %s%s\n", d.ID, d.Name,
			d.CreatedAt.Local().Format("2006-01-02"), seen, d.ExpiresAt().Local().Format("2006-01-02"), yolo)
	}
	return nil
}

// printPairing shows the code's QR, its URL and the invite line.
func printPairing(w io.Writer, code auth.Code, p server.Pairing, cert auth.Certificate) error {
	qr, err := remote.RenderQR(p.URL)
	if err != nil {
		return err
	}
	fmt.Fprintf(w, "\nScan with your phone's camera (valid %d minutes, one use):\n\n%s\n%s\n", int(wire.PairCodeTTL/time.Minute), qr, p.URL)
	if p.Invite != "" {
		fmt.Fprintf(w, "\nOr type this on the page instead of scanning:\n%s\n", p.Invite)
	}
	if cert.Hex != "" {
		fmt.Fprintf(w, "\nThe phone's browser will warn about the certificate. Accept it only if its SHA-256 fingerprint is\n%s\n", cert.Hex)
	}
	return nil
}

// printBanner tells the user how to reach the server.
func printBanner(w io.Writer, r *server.Running) {
	fmt.Fprintln(w, "Deck remote control is on.")
	for _, h := range r.Hosts {
		scheme := "http"
		if r.TLS {
			scheme = "https"
		}
		fmt.Fprintf(w, "  %s://%s:%d\n", scheme, h, r.Port)
	}
	if !r.TLS {
		fmt.Fprintln(w, "\nOnly this machine can reach it. Add --lan for a phone on the same Wi-Fi.")
	}
}

func runRemote(cmd *cobra.Command, _ []string) error {
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

	out := cmd.OutOrStdout()
	r, err := startRemote(cmd, ro, ws, out)
	if err != nil {
		return err
	}
	defer func() {
		server.SetActive(nil)
		r.Stop()
	}()
	printBanner(out, r)
	cwd, _ := ResolveCwd(cmd)
	show := func() error {
		code, p, err := r.Pairing(filepath.Base(cwd))
		if err != nil {
			return err
		}
		return printPairing(out, code, p, r.Cert)
	}
	if err := show(); err != nil {
		return err
	}
	fmt.Fprintln(out, "\nPress enter for a new pairing code, ctrl+c to stop.")

	lines := make(chan struct{})
	go func() {
		sc := bufio.NewScanner(cmd.InOrStdin())
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
			if err := show(); err != nil {
				return err
			}
		}
	}
}

// runRemotePair arms a code for the server running in this project (in
// any process), shows it and waits for the phone.
func runRemotePair(cmd *cobra.Command, _ []string) error {
	store, err := remoteStore(cmd)
	if err != nil {
		return err
	}
	ad, err := store.ReadAdvert()
	if err != nil {
		if errors.Is(err, auth.ErrNoServer) {
			return errors.New("deck remote is not running for this project; start 'deck remote' or 'deck --remote' first")
		}
		return err
	}
	cwd, _ := ResolveCwd(cmd)
	code, err := store.Arm()
	if err != nil {
		return err
	}
	var cert auth.Certificate
	if ad.TLS {
		cert, _ = store.EnsureCertificate(ad.Hosts)
	}
	out := cmd.OutOrStdout()
	if err := printPairing(out, code, server.NewPairing(ad, code, filepath.Base(cwd)), cert); err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	paired, err := waitPaired(ctx, store, code.ExpiresAt)
	if err != nil {
		return err
	}
	if paired.ID == "" {
		fmt.Fprintln(out, "The code expired; run 'deck remote pair' again.")
		return nil
	}
	fmt.Fprintf(out, "\nPaired %s (%s).\n", paired.Name, paired.ID)
	return nil
}

// waitPaired watches the device store until a new device appears, the
// code expires or ctx ends.
func waitPaired(ctx context.Context, store *auth.Store, until time.Time) (auth.Device, error) {
	before, err := store.List()
	if err != nil {
		return auth.Device{}, err
	}
	known := map[string]bool{}
	for _, d := range before {
		known[d.ID] = true
	}
	changes := make(chan struct{}, 1)
	wctx, cancel := context.WithCancel(ctx)
	defer cancel()
	store.Watch(wctx, func() {
		select {
		case changes <- struct{}{}:
		default:
		}
	})
	expiry := time.NewTimer(time.Until(until))
	defer expiry.Stop()
	for {
		select {
		case <-ctx.Done():
			return auth.Device{}, ctx.Err()
		case <-expiry.C:
			return auth.Device{}, nil
		case <-changes:
			devs, err := store.List()
			if err != nil {
				continue
			}
			for _, d := range devs {
				if !known[d.ID] {
					return d, nil
				}
			}
		}
	}
}
