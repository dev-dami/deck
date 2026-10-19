package cmd

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/spf13/cobra"

	"github.com/dev-dami/deck/internal/ui/util"
	"github.com/dev-dami/deck/internal/version"
	"github.com/dev-dami/deck/internal/workspace"
	"github.com/dev-dami/deck/pkg/remote"
	"github.com/dev-dami/deck/pkg/remote/adapter"
	"github.com/dev-dami/deck/pkg/remote/autostart"
	"github.com/dev-dami/deck/pkg/remote/devstore"
	"github.com/dev-dami/deck/pkg/remote/identity"
	"github.com/dev-dami/deck/pkg/remote/ipc"
	"github.com/dev-dami/deck/pkg/remote/mobile"
	"github.com/dev-dami/deck/pkg/remote/projects"
	"github.com/dev-dami/deck/pkg/remote/wire"
)

// hostIdleExit is how long a headless host with nothing to do stays up, so it
// does not keep a project's data directory from the terminal UI.
const hostIdleExit = 10 * time.Minute

var mobileCmd = &cobra.Command{
	Use:   "mobile",
	Short: "Control Deck from your phone, from anywhere",
	Long: `Pair your phone with this computer once, then see every session, answer
approvals and leave work for when the computer wakes, on the same Wi-Fi or
across the world. No flags to remember: a small background process
('deck mobile daemon') keeps this computer reachable.

  deck mobile setup --relay https://your-relay.example   (once)
  deck mobile projects add                               (share this folder)
  deck mobile pair                                       (scan with your phone)

'deck remote' still works for a quick one-off page on this machine.`,
}

var mobileSetupCmd = &cobra.Command{
	Use:   "setup",
	Short: "Set the relay that lets your phone reach this computer from anywhere",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		dir, err := mobileDir()
		if err != nil {
			return err
		}
		cfg, err := mobile.LoadConfig(dir)
		if err != nil {
			return err
		}
		if v, _ := cmd.Flags().GetString("relay"); cmd.Flags().Changed("relay") {
			cfg.RelayURL = v
		}
		if cmd.Flags().Changed("allow-lan-http") {
			cfg.AllowLANHTTP, _ = cmd.Flags().GetBool("allow-lan-http")
		}
		if v, _ := cmd.Flags().GetString("name"); cmd.Flags().Changed("name") {
			cfg.Name = v
		}
		if v, _ := cmd.Flags().GetStringSlice("android-origin"); cmd.Flags().Changed("android-origin") {
			cfg.AndroidOrigins = v
		}
		if err := mobile.SaveConfig(dir, cfg); err != nil {
			return err
		}
		out := cmd.OutOrStdout()
		fmt.Fprintf(out, "Relay: %s\nName:  %s\n", orNone(cfg.RelayURL), orNone(cfg.Name))
		fmt.Fprintln(out, "Restart the background process for this to take effect ('deck mobile daemon').")
		return nil
	},
}

var mobileDaemonCmd = &cobra.Command{
	Use:   "daemon",
	Short: "Run the background process that keeps this computer reachable",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		dir, err := mobileDir()
		if err != nil {
			return err
		}
		exe, err := os.Executable()
		if err != nil {
			return err
		}
		if detached, _ := cmd.Flags().GetBool("detach"); detached {
			// Re-run without a console and let this one exit, so a logon task
			// leaves nothing on screen.
			child := exec.Command(exe, "mobile", "daemon", "--log")
			detach(child)
			if err := child.Start(); err != nil {
				return fmt.Errorf("could not start the background process: %w", err)
			}
			return child.Process.Release()
		}
		if logging, _ := cmd.Flags().GetBool("log"); logging {
			closeLog, err := logToFile(filepath.Join(dir, "daemon.log"))
			if err != nil {
				return err
			}
			defer closeLog()
		}
		cfg, err := mobile.LoadConfig(dir)
		if err != nil {
			return err
		}
		ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		err = mobile.Run(ctx, mobile.Options{
			Dir: dir, Config: cfg, Version: version.Version,
			Spawn: func(path string) error { return spawnHost(exe, path) },
		})
		if errors.Is(err, mobile.ErrAlreadyRunning) {
			fmt.Fprintln(cmd.ErrOrStderr(), "Deck mobile is already running.")
			return nil
		}
		return err
	},
}

var mobilePairCmd = &cobra.Command{
	Use:   "pair",
	Short: "Show a QR code to pair a phone with this computer",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		dir, err := mobileDir()
		if err != nil {
			return err
		}
		ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		cc, err := controlClient(ctx, dir)
		if err != nil {
			return err
		}
		defer cc.Close()

		devs, err := devstore.Open(dir)
		if err != nil {
			return err
		}
		known := map[string]bool{}
		for _, d := range devs.List() {
			known[d.ID] = true
		}
		var res mobile.PairResult
		if err := retryUntilRelay(ctx, func() error { return cc.Call(ctx, "pair", nil, &res) }); err != nil {
			return err
		}
		qr, err := remote.RenderQR(res.URL)
		if err != nil {
			return err
		}
		out := cmd.OutOrStdout()
		fmt.Fprintf(out, "\nScan with your phone's camera (valid %d minutes, one use):\n\n%s\n%s\n", int(wire.PairCodeTTL/time.Minute), qr, res.URL)
		defer func() { _ = cc.Call(context.Background(), "unpair", nil, nil) }()

		tick := time.NewTicker(500 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(time.Until(res.ExpiresAt)):
				fmt.Fprintln(out, "\nThe code expired; run 'deck mobile pair' again.")
				return nil
			case <-tick.C:
				if _, err := devs.Reload(); err != nil {
					continue
				}
				for _, d := range devs.List() {
					if !known[d.ID] {
						fmt.Fprintf(out, "\nPaired %s (%s).\n", d.Name, d.ID)
						return nil
					}
				}
			}
		}
	},
}

var mobileStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show whether the background process is up and what it shares",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		dir, err := mobileDir()
		if err != nil {
			return err
		}
		out := cmd.OutOrStdout()
		cc, err := dialControl(cmd.Context(), dir)
		if err != nil {
			fmt.Fprintln(out, "Not running. Start it with 'deck mobile daemon'.")
			return nil
		}
		defer cc.Close()
		var st mobile.StatusResult
		if err := cc.Call(cmd.Context(), "status", nil, &st); err != nil {
			return err
		}
		relay := "not set up ('deck mobile setup --relay URL')"
		if st.RelayURL != "" {
			relay = st.RelayURL + " (offline)"
			if st.RelayOnline {
				relay = st.RelayURL + " (connected)"
			}
		}
		fmt.Fprintf(out, "Computer: %s (%s)\nRelay:    %s\nPhones:   %d paired\n", st.Name, st.CID, relay, st.Devices)
		if len(st.Projects) == 0 {
			fmt.Fprintln(out, "Projects: none shared ('deck mobile projects add')")
		}
		for _, p := range st.Projects {
			state := "closed"
			if p.Online {
				state = "open"
			}
			fmt.Fprintf(out, "Project:  %-20s %s  %s\n", p.Name, state, p.Path)
		}
		return nil
	},
}

var mobileDevicesCmd = &cobra.Command{
	Use:   "devices",
	Short: "List paired phones, or let one switch Deck into yolo mode",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		store, err := mobileDevices()
		if err != nil {
			return err
		}
		out := cmd.OutOrStdout()
		if id, _ := cmd.Flags().GetString("allow-yolo"); id != "" {
			if err := store.SetYolo(id, true); err != nil {
				return mobileDeviceErr(err, id)
			}
			fmt.Fprintf(out, "Phone %s may now switch Deck into yolo mode (it still needs its fingerprint or face).\n", id)
		}
		if id, _ := cmd.Flags().GetString("deny-yolo"); id != "" {
			if err := store.SetYolo(id, false); err != nil {
				return mobileDeviceErr(err, id)
			}
			fmt.Fprintf(out, "Phone %s may no longer switch Deck into yolo mode.\n", id)
		}
		devs := store.List()
		if len(devs) == 0 {
			fmt.Fprintln(out, "No paired phones.")
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
			fmt.Fprintf(out, "%s  %-24s paired %s  last seen %s  passkeys %d%s\n", d.ID, d.Name,
				d.CreatedAt.Local().Format("2006-01-02"), seen, len(d.Passkeys), yolo)
		}
		return nil
	},
}

var mobileRevokeCmd = &cobra.Command{
	Use:   "revoke <id>",
	Short: "Unpair a phone (the background process drops it within seconds)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		store, err := mobileDevices()
		if err != nil {
			return err
		}
		if err := store.Revoke(args[0]); err != nil {
			return mobileDeviceErr(err, args[0])
		}
		fmt.Fprintln(cmd.OutOrStdout(), "Unpaired.")
		return nil
	},
}

var mobileProjectsCmd = &cobra.Command{
	Use:   "projects",
	Short: "Choose which folders your phone can see",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		reg, err := mobileRegistry()
		if err != nil {
			return err
		}
		list := reg.List()
		if len(list) == 0 {
			fmt.Fprintln(cmd.OutOrStdout(), "No folders shared. Add one with 'deck mobile projects add'.")
			return nil
		}
		for _, p := range list {
			state := "on"
			if !p.Enabled {
				state = "off"
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s  %-20s %-3s %s\n", p.ID, p.Name, state, p.Path)
		}
		return nil
	},
}

var mobileProjectsAddCmd = &cobra.Command{
	Use:   "add [folder]",
	Short: "Share a folder (default: the current one) with your phone",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		reg, err := mobileRegistry()
		if err != nil {
			return err
		}
		path := "."
		if len(args) == 1 {
			path = args[0]
		} else if cwd, err := ResolveCwd(cmd); err == nil {
			path = cwd
		}
		name, _ := cmd.Flags().GetString("name")
		p, err := reg.Add(path, name)
		if err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Sharing %s as %q (%s).\n", p.Path, p.Name, p.ID)
		return nil
	},
}

var mobileProjectsRmCmd = &cobra.Command{
	Use:   "rm <id>",
	Short: "Stop sharing a folder",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		reg, err := mobileRegistry()
		if err != nil {
			return err
		}
		if err := reg.Remove(args[0]); err != nil {
			if errors.Is(err, projects.ErrNotFound) {
				return fmt.Errorf("no shared folder %q (see 'deck mobile projects')", args[0])
			}
			return err
		}
		fmt.Fprintln(cmd.OutOrStdout(), "No longer shared.")
		return nil
	},
}

var mobileQueueCmd = &cobra.Command{
	Use:   "queue",
	Short: "See what your phone left for this computer to do",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		dir, err := mobileDir()
		if err != nil {
			return err
		}
		out := cmd.OutOrStdout()
		cc, err := dialControl(cmd.Context(), dir)
		if err != nil {
			fmt.Fprintln(out, "Not running, so nothing is waiting.")
			return nil
		}
		defer cc.Close()
		var items []mobile.QueueItem
		if err := cc.Call(cmd.Context(), "queue.list", nil, &items); err != nil {
			return err
		}
		if len(items) == 0 {
			fmt.Fprintln(out, "Nothing is waiting.")
			return nil
		}
		for _, it := range items {
			text := ""
			if it.Text != "" {
				text = "  " + strconv.Quote(truncateRunes(it.Text, 50))
			}
			fmt.Fprintf(out, "%s  %-12s from %-14s in %-14s expires %s%s\n", it.QID, it.Kind, it.Device, it.Project,
				it.ExpiresAt.Local().Format("Jan 2 15:04"), text)
		}
		return nil
	},
}

var mobileQueueCancelCmd = &cobra.Command{
	Use:   "cancel [id]",
	Short: "Cancel one waiting action, or all of them with --all",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		all, _ := cmd.Flags().GetBool("all")
		if all == (len(args) == 1) {
			return errors.New("give one action id, or --all")
		}
		dir, err := mobileDir()
		if err != nil {
			return err
		}
		cc, err := dialControl(cmd.Context(), dir)
		if err != nil {
			return errors.New("Deck mobile is not running, so nothing is waiting")
		}
		defer cc.Close()
		ca := mobile.QueueCancelArgs{All: all}
		if len(args) == 1 {
			ca.QID = args[0]
		}
		var n int
		if err := cc.Call(cmd.Context(), "queue.cancel", ca, &n); err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Cancelled %d. The phone is told.\n", n)
		return nil
	},
}

func truncateRunes(s string, n int) string {
	r := []rune(strings.Join(strings.Fields(s), " "))
	if len(r) <= n {
		return string(r)
	}
	return string(r[:n-1]) + "…"
}

var mobileHostCmd = &cobra.Command{
	Use:    "host",
	Short:  "Run a project's Deck with no terminal UI, for the phone",
	Hidden: true,
	Args:   cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		dir, err := mobileDir()
		if err != nil {
			return err
		}
		cwd, err := ResolveCwd(cmd)
		if err != nil {
			return err
		}
		abs, err := filepath.Abs(cwd)
		if err != nil {
			return err
		}
		reg, err := mobileRegistry()
		if err != nil {
			return err
		}
		pj := projects.IDOf(abs)
		if _, err := reg.Get(pj); err != nil {
			return errors.New("this folder is not shared with the phone ('deck mobile projects add')")
		}
		ws, cleanup, err := setupWorkspace(cmd)
		if err != nil {
			return err
		}
		defer cleanup()
		be, err := adapter.New(ws, version.Version, filepath.Base(abs))
		if err != nil {
			return err
		}
		ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		ctx, cancel := context.WithCancel(ctx)
		defer cancel()
		go mobile.Provide(ctx, dir, pj, be)
		waitIdle(ctx, be, hostIdleExit)
		return nil
	},
}

var mobileInstallCmd = &cobra.Command{
	Use:   "install",
	Short: "Start the background process whenever you log in",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		dir, err := mobileDir()
		if err != nil {
			return err
		}
		exe, err := os.Executable()
		if err != nil {
			return err
		}
		if resolved, err := filepath.EvalSymlinks(exe); err == nil {
			exe = resolved
		}
		args := []string{"mobile", "daemon", "--log"}
		if runtime.GOOS == "windows" {
			args = []string{"mobile", "daemon", "--detach"}
		}
		method, err := (autostart.Installer{}).Install(cmd.Context(), autostart.Spec{
			Exe: exe, Args: args, LogPath: filepath.Join(dir, "daemon.log"),
		})
		if err != nil {
			return err
		}
		out := cmd.OutOrStdout()
		fmt.Fprintf(out, "Deck mobile will start when you log in (%s).\n", method)
		if cc, err := dialControl(cmd.Context(), dir); err == nil {
			cc.Close()
			return nil
		}
		// Start it now too, so there is nothing to wait for.
		child := exec.Command(exe, "mobile", "daemon", "--log")
		detach(child)
		if err := child.Start(); err != nil {
			return fmt.Errorf("installed, but could not start it now: %w", err)
		}
		_ = child.Process.Release()
		fmt.Fprintln(out, "Started it now.")
		return nil
	},
}

var mobileUninstallCmd = &cobra.Command{
	Use:   "uninstall",
	Short: "Stop starting the background process at login",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		if err := (autostart.Installer{}).Uninstall(cmd.Context()); err != nil {
			return err
		}
		fmt.Fprintln(cmd.OutOrStdout(), "Removed. A running background process keeps going until you log out or stop it.")
		return nil
	},
}

// logToFile sends slog output to path (appending; truncated past 5 MiB) and
// returns the close func.
func logToFile(path string) (func(), error) {
	flags := os.O_CREATE | os.O_WRONLY | os.O_APPEND
	if st, err := os.Stat(path); err == nil && st.Size() > 5<<20 {
		flags = os.O_CREATE | os.O_WRONLY | os.O_TRUNC
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, flags, 0o600)
	if err != nil {
		return nil, err
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(f, nil)))
	return func() { f.Close() }, nil
}

func init() {
	mobileDaemonCmd.Flags().Bool("detach", false, "Start it in the background and return")
	mobileDaemonCmd.Flags().Bool("log", false, "Write the log to a file in Deck's mobile folder")
	mobileSetupCmd.Flags().Bool("allow-lan-http", false, "Allow private IPv4 HTTP relays for LAN testing")
	mobileSetupCmd.Flags().String("relay", "", "The relay's https address")
	mobileSetupCmd.Flags().String("name", "", "How this computer appears on your phone")
	mobileSetupCmd.Flags().StringSlice("android-origin", nil, "Trusted native app origin android:apk-key-hash:BASE64URL_SHA256 (repeat for signing certificates)")
	mobileDevicesCmd.Flags().String("allow-yolo", "", "Let this phone switch Deck into yolo mode (with its fingerprint or face)")
	mobileDevicesCmd.Flags().String("deny-yolo", "", "Take yolo away from this phone")
	mobileProjectsAddCmd.Flags().String("name", "", "Name shown on the phone (default: the folder's name)")
	mobileQueueCancelCmd.Flags().Bool("all", false, "Cancel everything that is waiting")
	mobileQueueCmd.AddCommand(mobileQueueCancelCmd)
	mobileProjectsCmd.AddCommand(mobileProjectsAddCmd, mobileProjectsRmCmd)
	mobileCmd.AddCommand(mobileSetupCmd, mobileDaemonCmd, mobilePairCmd, mobileStatusCmd,
		mobileDevicesCmd, mobileRevokeCmd, mobileProjectsCmd, mobileHostCmd, mobileInstallCmd, mobileUninstallCmd, mobileQueueCmd)
	rootCmd.AddCommand(mobileCmd)
}

func orNone(s string) string {
	if s == "" {
		return "(not set)"
	}
	return s
}

// mobileDir is the computer-wide state directory; DECK_MOBILE_DIR overrides
// it for tests and unusual setups.
func mobileDir() (string, error) {
	if d := os.Getenv("DECK_MOBILE_DIR"); d != "" {
		return d, nil
	}
	return identity.DefaultDir()
}

func mobileDevices() (*devstore.Store, error) {
	dir, err := mobileDir()
	if err != nil {
		return nil, err
	}
	return devstore.Open(dir)
}

func mobileRegistry() (*projects.Registry, error) {
	dir, err := mobileDir()
	if err != nil {
		return nil, err
	}
	return projects.Open(dir)
}

func mobileDeviceErr(err error, id string) error {
	if errors.Is(err, devstore.ErrNotFound) {
		return fmt.Errorf("no paired phone %q (see 'deck mobile devices')", id)
	}
	return err
}

func dialControl(ctx context.Context, dir string) (*ipc.ControlClient, error) {
	ep, err := ipc.ReadEndpoint(dir)
	if err != nil {
		return nil, err
	}
	dctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	return ipc.DialControl(dctx, ep)
}

// controlClient reaches the daemon, starting it in the background first if it
// is not running, so a person never has to remember to.
func controlClient(ctx context.Context, dir string) (*ipc.ControlClient, error) {
	if cc, err := dialControl(ctx, dir); err == nil {
		return cc, nil
	}
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	child := exec.Command(exe, "mobile", "daemon")
	detach(child)
	if err := child.Start(); err != nil {
		return nil, fmt.Errorf("could not start the background process: %w", err)
	}
	go func() { _ = child.Wait() }()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
		if cc, err := dialControl(ctx, dir); err == nil {
			return cc, nil
		}
	}
	return nil, errors.New("the background process did not start; try 'deck mobile daemon' to see why")
}

// retryUntilRelay retries while the daemon is still connecting to the relay.
func retryUntilRelay(ctx context.Context, f func() error) error {
	deadline := time.Now().Add(15 * time.Second)
	for {
		err := f()
		if err == nil || time.Now().After(deadline) || !strings.Contains(err.Error(), "relay is not reachable yet") {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
}

// spawnHost starts a headless Deck for the project at path, detached from the
// daemon.
func spawnHost(exe, path string) error {
	child := exec.Command(exe, "mobile", "host", "--cwd", path)
	detach(child)
	if err := child.Start(); err != nil {
		return err
	}
	go func() { _ = child.Wait() }()
	return nil
}

// waitIdle blocks until ctx ends or the project has had nothing to do (no busy
// agent, no pending approval) for idle.
func waitIdle(ctx context.Context, be remote.Backend, idle time.Duration) {
	last := time.Now()
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if busy(ctx, be) {
				last = time.Now()
			} else if time.Since(last) >= idle {
				return
			}
		}
	}
}

func busy(ctx context.Context, be remote.Backend) bool {
	sessions, err := be.Sessions(ctx)
	if err != nil {
		return true
	}
	for _, s := range sessions {
		if s.Busy || s.Pending > 0 {
			return true
		}
	}
	return false
}

// mobileNotify shows a message in the running terminal UI; it is set once the
// program exists and is nil before that.
var mobileNotify atomic.Pointer[func(string)]

// setMobileNotifier lets the daemon put short messages in the UI's toasts.
func setMobileNotifier(p *tea.Program) {
	f := func(text string) { p.Send(util.NewWarnMsg(text)) }
	mobileNotify.Store(&f)
}

// uiBackend is the terminal UI's backend: it also has a screen, which tells the
// daemon it may show notices here and wait for a person to cancel queued work.
type uiBackend struct{ remote.Backend }

// Notice implements ipc.Noticer.
func (uiBackend) Notice(text string) {
	if f := mobileNotify.Load(); f != nil {
		(*f)(text)
	}
}

// startMobileProvider offers this terminal UI's project to the background
// process, if the folder is shared with the phone. The terminal keeps working
// exactly as before when it is not.
func startMobileProvider(cmd *cobra.Command, ws workspace.Workspace) (stop func()) {
	noop := func() {}
	dir, err := mobileDir()
	if err != nil {
		return noop
	}
	cwd, err := ResolveCwd(cmd)
	if err != nil {
		return noop
	}
	abs, err := filepath.Abs(cwd)
	if err != nil {
		return noop
	}
	reg, err := projects.Open(dir)
	if err != nil {
		return noop
	}
	pj := projects.IDOf(abs)
	be, err := adapter.New(ws, version.Version, filepath.Base(abs))
	if err != nil {
		return noop
	}
	ctx, cancel := context.WithCancel(cmd.Context())
	go provideSharedProject(ctx, reg, pj, time.Second, func(active context.Context) {
		mobile.Provide(active, dir, pj, uiBackend{Backend: be})
	})
	return cancel
}
