package cmd

import (
	"context"
	_ "embed"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	xstrings "github.com/charmbracelet/x/exp/strings"
	"github.com/charmbracelet/x/term"
	"github.com/spf13/cobra"

	"github.com/dev-dami/deck/internal/app"
	"github.com/dev-dami/deck/internal/config"
	"github.com/dev-dami/deck/internal/db"
	"github.com/dev-dami/deck/internal/deck/doctor"
	"github.com/dev-dami/deck/internal/deck/engine"
	decklog "github.com/dev-dami/deck/internal/log"
	"github.com/dev-dami/deck/internal/projects"
	"github.com/dev-dami/deck/internal/session"
	"github.com/dev-dami/deck/internal/skills"
	"github.com/dev-dami/deck/internal/ui/common"
	"github.com/dev-dami/deck/internal/ui/decklogo"
	"github.com/dev-dami/deck/internal/ui/exitbanner"
	"github.com/dev-dami/deck/internal/ui/logo"
	ui "github.com/dev-dami/deck/internal/ui/model"
	"github.com/dev-dami/deck/internal/ui/styles"
	"github.com/dev-dami/deck/internal/version"
	"github.com/dev-dami/deck/internal/workspace"
)

func init() {
	rootCmd.PersistentFlags().StringP("cwd", "c", "", "Current working directory")
	rootCmd.PersistentFlags().StringP("data-dir", "D", "", "Custom deck data directory")
	rootCmd.PersistentFlags().BoolP("debug", "d", false, "Debug")
	rootCmd.Flags().BoolP("help", "h", false, "Help")
	rootCmd.Flags().BoolP("yolo", "y", false, "Approve everything the agents ask without asking you")
	rootCmd.Flags().StringP("session", "s", "", "Continue a previous session by ID")
	rootCmd.Flags().BoolP("continue", "C", false, "Continue the most recent session")
	rootCmd.MarkFlagsMutuallyExclusive("session", "continue")

	rootCmd.AddCommand(
		doctorCmd,
		dirsCmd,
		projectsCmd,
		logsCmd,
		sessionCmd,
		peerCmd,
	)
}

var rootCmd = &cobra.Command{
	Use:     "deck",
	Short:   logo.Tagline,
	Long:    "Deck runs your own claude, codex, opencode and agy CLIs side by side in one shared chat.",
	Version: version.Version,
	Example: `
# Open the deck
deck

# Open it in another folder
deck --cwd /path/to/project

# Approve everything the agents ask (use with care)
deck --yolo

# Continue a previous session, or the most recent one
deck --session {session-id}
deck --continue

# Check which agent CLIs Deck can drive
deck doctor
  `,
	RunE: func(cmd *cobra.Command, args []string) error {
		sessionID, _ := cmd.Flags().GetString("session")
		continueLast, _ := cmd.Flags().GetBool("continue")

		ws, cleanup, err := setupWorkspaceWithProgressBar(cmd)
		if err != nil {
			return err
		}
		defer cleanup()

		stopRemote, err := startRemoteIfAsked(cmd, ws)
		if err != nil {
			return err
		}
		defer stopRemote()
		defer startMobileProvider(cmd, ws)()

		if sessionID != "" {
			sess, err := resolveWorkspaceSessionID(cmd.Context(), ws, sessionID)
			if err != nil {
				return err
			}
			sessionID = sess.ID
		}

		// With no theme chosen Deck follows the terminal, as the desktop
		// app follows the system: its light look on a light background.
		// The terminal is asked once, before Bubble Tea takes it over.
		if common.ThemeNameFromConfig(ws.Config()) == "" {
			styles.SetTerminalLight(!lipgloss.HasDarkBackground(os.Stdin, os.Stdout))
		}
		com := common.DefaultCommon(ws)
		model := ui.New(com, sessionID, continueLast)

		inputFilter := ui.NewFilter()
		var env uv.Environ = os.Environ()
		program := tea.NewProgram(
			model,
			tea.WithEnvironment(env),
			tea.WithContext(cmd.Context()),
			tea.WithFilter(inputFilter.Filter),
		)
		go ws.Subscribe(program)
		setMobileNotifier(program)

		if _, err := program.Run(); err != nil {
			slog.Error("TUI run error", "error", err)
			return fmt.Errorf("deck stopped: %w", err)
		}
		var banner config.ExitBanner
		if cfg := com.Config(); cfg != nil {
			banner = cfg.Options.TUI.ExitBanner
		}
		printSessionResume(model, banner, com.Styles)
		return nil
	},
}

// printSessionResume prints the exit banner after the TUI exits, including the
// session title and the hint to resume it with `deck -s <id>`. The banner
// style decides how much of that is shown; see config.ExitBanner.
func printSessionResume(model *ui.UI, banner config.ExitBanner, t *styles.Styles) {
	tw, _, _ := term.GetSize(os.Stdout.Fd())
	body := exitbanner.RenderTheme(banner, model.CurrentSession(), tw, t)
	if body == "" {
		return
	}
	fmt.Fprintln(colorprofile.NewWriter(os.Stderr, os.Environ()), body)
}

var doctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "Check the agent CLIs Deck can drive",
	RunE: func(cmd *cobra.Command, _ []string) error {
		return doctor.Run(cmd.Context(), os.Stdout)
	},
}

// Execute runs the deck command. The caller exits non-zero on an error
// (cobra has already printed it), after stopping any profiles.
func Execute() error {
	// config.Load logs while it resolves; the log file only exists once the
	// data directory is known, so early logs are dropped.
	slog.SetDefault(slog.New(slog.DiscardHandler))
	if term.IsTerminal(os.Stdout.Fd()) {
		w := colorprofile.NewWriter(os.Stdout, os.Environ())
		var b strings.Builder
		w.Forward = &b
		_, _ = w.WriteString(decklogo.Static(40, 4, true))
		rootCmd.SetVersionTemplate(b.String() + `
{{printf "deck %s" .Version}}
`)
	}
	return rootCmd.ExecuteContext(context.Background())
}

// supportsProgressBar tries to determine whether the current terminal supports
// progress bars by looking into environment variables.
func supportsProgressBar() bool {
	if !term.IsTerminal(os.Stderr.Fd()) {
		return false
	}
	termProg := os.Getenv("TERM_PROGRAM")
	_, isWindowsTerminal := os.LookupEnv("WT_SESSION")

	return isWindowsTerminal || xstrings.ContainsAnyOf(strings.ToLower(termProg), "ghostty", "iterm2", "rio")
}

// setupWorkspaceWithProgressBar wraps setupWorkspace with an optional
// terminal progress bar shown during initialization.
func setupWorkspaceWithProgressBar(cmd *cobra.Command) (workspace.Workspace, func(), error) {
	showProgress := supportsProgressBar()
	if showProgress {
		_, _ = fmt.Fprintf(os.Stderr, ansi.SetIndeterminateProgressBar)
	}

	ws, cleanup, err := setupWorkspace(cmd)

	if showProgress {
		_, _ = fmt.Fprintf(os.Stderr, ansi.ResetProgressBar)
	}

	return ws, cleanup, err
}

// setupWorkspace returns the workspace and its cleanup.
func setupWorkspace(cmd *cobra.Command) (workspace.Workspace, func(), error) {
	return setupLocalWorkspace(cmd)
}

// setupLocalWorkspace creates the in-process app (sessions, messages, files)
// and Deck's engine, and joins them in a DeckWorkspace.
func setupLocalWorkspace(cmd *cobra.Command) (workspace.Workspace, func(), error) {
	debug, _ := cmd.Flags().GetBool("debug")
	yolo, _ := cmd.Flags().GetBool("yolo")
	dataDir, _ := cmd.Flags().GetString("data-dir")
	ctx := cmd.Context()

	cwd, err := ResolveCwd(cmd)
	if err != nil {
		return nil, nil, err
	}

	store, err := config.Init(cwd, dataDir, debug)
	if err != nil {
		return nil, nil, err
	}

	cfg := store.Config()
	store.Overrides().SkipPermissionRequests = yolo

	if err := os.MkdirAll(cfg.Options.DataDirectory, 0o700); err != nil {
		return nil, nil, fmt.Errorf("failed to create data directory: %q %w", cfg.Options.DataDirectory, err)
	}

	gitIgnorePath := filepath.Join(cfg.Options.DataDirectory, ".gitignore")
	if _, err := os.Stat(gitIgnorePath); os.IsNotExist(err) {
		if err := os.WriteFile(gitIgnorePath, []byte("*\n"), 0o644); err != nil {
			return nil, nil, fmt.Errorf("failed to create .gitignore file: %q %w", gitIgnorePath, err)
		}
	}

	if err := projects.Register(cwd, cfg.Options.DataDirectory); err != nil {
		slog.Warn("Failed to register project", "error", err)
	}

	// Set the log file up first: the work below runs side by side, and all
	// of it should log to the file rather than the discard handler Execute
	// starts with.
	logFile := filepath.Join(cfg.Options.DataDirectory, "logs", "deck.log")
	decklog.Setup(logFile, debug)

	// Deck's engine drives the user's own agent CLIs in place of Deck's
	// agent. No telemetry is sent. It shares nothing with the app below
	// (its own config, thread store, broker and PATH lookups), so it starts
	// while the database is opened and skills are discovered.
	type engineResult struct {
		eng *engine.Engine
		err error
	}
	engineCh := make(chan engineResult, 1)
	go func() {
		eng, err := engine.Start()
		engineCh <- engineResult{eng, err}
	}()
	closeEngine := func() {
		if r := <-engineCh; r.eng != nil {
			r.eng.Close()
		}
	}

	// Discover skills once before app.New, while the database opens (and
	// migrates). Local mode hosts a single workspace per process, so
	// WithGlobalMirror keeps the package globals (which the TUI reads via
	// skills.GetLatestStates) in sync with the manager.
	discoveryCfg := localSkillsDiscoveryConfig(store)
	type skillsResult struct {
		all, active []*skills.Skill
		states      []*skills.SkillState
	}
	skillsCh := make(chan skillsResult, 1)
	go func() {
		all, active, states := skills.DiscoverFromConfig(discoveryCfg)
		skillsCh <- skillsResult{all, active, states}
	}()

	conn, err := db.Connect(ctx, cfg.Options.DataDirectory)
	if err != nil {
		<-skillsCh
		closeEngine()
		return nil, nil, err
	}

	found := <-skillsCh
	skillsMgr := skills.NewManager(
		found.all, found.active, found.states,
		skills.WithGlobalMirror(),
		skills.WithResolvedPaths(discoveryCfg.ResolvePaths()),
		skills.WithWorkingDir(discoveryCfg.WorkingDir),
	)

	appInstance, err := app.New(ctx, conn, store, skillsMgr)
	if err != nil {
		_ = conn.Close()
		slog.Error("Failed to create app instance", "error", err)
		closeEngine()
		return nil, nil, err
	}

	r := <-engineCh
	if r.err != nil {
		appInstance.Shutdown()
		return nil, nil, r.err
	}
	eng := r.eng
	ws := workspace.NewDeckWorkspace(appInstance, store, eng)
	cleanup := func() {
		eng.Close()
		ws.Shutdown() // stores queued tool results, then shuts the app down
	}
	return ws, cleanup, nil
}

// localSkillsDiscoveryConfig adapts a *config.ConfigStore to the inputs
// skills.DiscoverFromConfig expects.
func localSkillsDiscoveryConfig(store *config.ConfigStore) skills.DiscoveryConfig {
	opts := store.Config().Options
	var paths, disabled []string
	if opts != nil {
		paths = opts.SkillsPaths
		disabled = opts.DisabledSkills
	}
	var resolver func(string) (string, error)
	if r := store.Resolver(); r != nil {
		resolver = r.ResolveValue
	}
	return skills.DiscoveryConfig{
		SkillsPaths:    paths,
		DisabledSkills: disabled,
		WorkingDir:     store.WorkingDir(),
		Resolver:       resolver,
	}
}

// resolveWorkspaceSessionID resolves a session ID that may be a full
// UUID, full hash, or hash prefix. Works against the Workspace
// interface so both local and client/server paths get hash prefix
// support.
func resolveWorkspaceSessionID(ctx context.Context, ws workspace.Workspace, id string) (session.Session, error) {
	if sess, err := ws.GetSession(ctx, id); err == nil {
		return sess, nil
	}

	sessions, err := ws.ListSessions(ctx)
	if err != nil {
		return session.Session{}, err
	}

	var matches []session.Session
	for _, s := range sessions {
		hash := session.HashID(s.ID)
		if hash == id || strings.HasPrefix(hash, id) {
			matches = append(matches, s)
		}
	}

	switch len(matches) {
	case 0:
		return session.Session{}, fmt.Errorf("session not found: %s", id)
	case 1:
		return matches[0], nil
	default:
		return session.Session{}, fmt.Errorf("session ID %q is ambiguous (%d matches)", id, len(matches))
	}
}

func ResolveCwd(cmd *cobra.Command) (string, error) {
	cwd, _ := cmd.Flags().GetString("cwd")
	if cwd != "" {
		err := os.Chdir(cwd)
		if err != nil {
			return "", fmt.Errorf("failed to change directory: %v", err)
		}
		return cwd, nil
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("failed to get current working directory: %v", err)
	}
	return cwd, nil
}

func createDotDeckDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("failed to create data directory: %q %w", dir, err)
	}

	gitIgnorePath := filepath.Join(dir, ".gitignore")
	content, err := os.ReadFile(gitIgnorePath)

	// create or update if old version
	if os.IsNotExist(err) || string(content) == oldGitIgnore {
		if err := os.WriteFile(gitIgnorePath, []byte(defaultGitIgnore), 0o644); err != nil {
			return fmt.Errorf("failed to create .gitignore file: %q %w", gitIgnorePath, err)
		}
	}

	return nil
}

//go:embed gitignore/old
var oldGitIgnore string

//go:embed gitignore/default
var defaultGitIgnore string
