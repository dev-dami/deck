// Package doctor prints a short report on whether Deck can drive the agent
// CLIs: whether each is installed, its version, and whether it is signed in.
//
// Sign-in is checked by running only the CLI's own status command, after
// confirming with --help that the command exists. Doctor never opens
// credential files and never prints the output of a status command, since that
// output can contain account identifiers. It prints only signed in or not
// signed in, plus a login hint from the CLI when one is offered.
package doctor

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"golang.org/x/term"

	"github.com/dev-dami/deck/internal/brand"
	"github.com/dev-dami/deck/internal/config"
	"github.com/dev-dami/deck/internal/store"
	"github.com/dev-dami/deck/internal/termimg"
)

// Runner runs external commands. Tests inject a fake; Run uses the real one.
type Runner interface {
	// LookPath resolves name on PATH, like exec.LookPath.
	LookPath(name string) (string, error)
	// Run runs path with args and returns combined output and the exit code.
	// err is set only when the process could not be run at all.
	Run(ctx context.Context, path string, args ...string) (output string, exitCode int, err error)
}

// Options controls what Doctor checks and where it looks. The zero value is
// not usable; start from DefaultOptions.
type Options struct {
	Runner       Runner
	ClaudeBinary string // command name or path; "claude" by default
	CodexBinary  string // command name or path; "codex" by default
	ConfigPath   string // shown in the report
	ThreadsDir   string // shown in the report
	Timeout      time.Duration
	// Terminal reports the output terminal's size and whether it is a TTY.
	Terminal func() (cols, rows int, tty bool)
	// Logo returns the escape sequence that draws a CLI's logo (logoCols x
	// logoRows cells) at the cursor, or "" when the terminal cannot.
	Logo func(kind string) string

	configErr error
}

// Run prints the report to w using the real CLIs and the user's config.
func Run(ctx context.Context, w io.Writer) error {
	opts := DefaultOptions()
	return RunWith(ctx, w, opts)
}

// DefaultOptions returns options for a real check: the configured binaries,
// the default config and threads paths, and the real command runner.
func DefaultOptions() Options {
	opts := Options{
		Runner:       execRunner{},
		ClaudeBinary: "claude",
		CodexBinary:  "codex",
		Timeout:      10 * time.Second,
		Terminal:     stdoutTerminal,
		Logo:         terminalLogo,
	}
	if p, err := config.Path(); err == nil {
		opts.ConfigPath = p
	}
	if d, err := store.DefaultDir(); err == nil {
		opts.ThreadsDir = d
	}
	cfg, err := config.Load()
	if err != nil {
		opts.configErr = err
		return opts
	}
	if cfg.ClaudeBinary != "" {
		opts.ClaudeBinary = cfg.ClaudeBinary
	}
	if cfg.CodexBinary != "" {
		opts.CodexBinary = cfg.CodexBinary
	}
	return opts
}

// RunWith prints the report to w using opts. A nil Runner means the real one.
func RunWith(ctx context.Context, w io.Writer, opts Options) error {
	if opts.Runner == nil {
		opts.Runner = execRunner{}
	}
	if opts.Timeout <= 0 {
		opts.Timeout = 10 * time.Second
	}
	if opts.Terminal == nil {
		opts.Terminal = stdoutTerminal
	}

	var b strings.Builder
	b.WriteString("deck doctor\n\n")
	b.WriteString(withLogo(opts, "claude", checkAgent(ctx, opts, agentSpec{
		name:       "claude",
		binary:     opts.ClaudeBinary,
		helpWord:   authWord,
		statusArgs: []string{"auth", "status"},
		install:    "npm install -g @anthropic-ai/claude-code",
	})))
	b.WriteString(withLogo(opts, "codex", checkAgent(ctx, opts, agentSpec{
		name:       "codex",
		binary:     opts.CodexBinary,
		helpWord:   loginWord,
		statusArgs: []string{"login", "status"},
		install:    "npm install -g @openai/codex",
	})))
	b.WriteString("\n")

	b.WriteString(configLine(opts))
	b.WriteString(dirLine("threads", opts.ThreadsDir))
	b.WriteString(terminalLine(opts))

	_, err := io.WriteString(w, b.String())
	return err
}

type agentSpec struct {
	name       string
	binary     string
	helpWord   *regexp.Regexp // the status subcommand's word, which must appear in --help
	statusArgs []string       // the CLI's own status command
	install    string
}

var (
	versionRE = regexp.MustCompile(`\d+\.\d+\.\d+[0-9A-Za-z.+-]*`)
	authWord  = regexp.MustCompile(`\bauth\b`)
	loginWord = regexp.MustCompile(`\blogin\b`)
)

const maxHintRunes = 100

// checkAgent returns one report line, ending in a newline.
func checkAgent(ctx context.Context, opts Options, a agentSpec) string {
	path, err := opts.Runner.LookPath(a.binary)
	if err != nil {
		return fmt.Sprintf("✗ %-6s  not found — install: %s\n", a.name, a.install)
	}

	version := ""
	if out, code, err := runWithTimeout(ctx, opts, path, "--version"); err == nil && code == 0 {
		version = versionRE.FindString(out)
	}

	icon, state := signInState(ctx, opts, path, a)

	parts := []string{}
	if version != "" {
		parts = append(parts, version)
	} else {
		parts = append(parts, "version unknown")
	}
	parts = append(parts, state)
	return fmt.Sprintf("%s %-6s  %s\n", icon, a.name, strings.Join(parts, "  "))
}

// signInState returns the icon and text for the sign-in state.
func signInState(ctx context.Context, opts Options, path string, a agentSpec) (icon, state string) {
	help, code, err := runWithTimeout(ctx, opts, path, "--help")
	if err != nil || code != 0 || !a.helpWord.MatchString(help) {
		return "!", "sign-in status not available in this version"
	}

	out, code, err := runWithTimeout(ctx, opts, path, a.statusArgs...)
	if err != nil {
		return "!", "sign-in check failed"
	}
	if code == 0 {
		return "✓", "signed in"
	}
	if hint := loginHint(out); hint != "" {
		return "✗", "not signed in — " + hint
	}
	return "✗", "not signed in"
}

// loginHint returns the first line of a status command's output that tells the
// user how to sign in. It skips any line with an "@" so account identifiers are
// never shown.
func loginHint(out string) string {
	for _, line := range strings.Split(out, "\n") {
		l := strings.TrimSpace(line)
		if l == "" || strings.Contains(l, "@") {
			continue
		}
		lower := strings.ToLower(l)
		if strings.Contains(lower, "login") || strings.Contains(lower, "log in") || strings.Contains(lower, "sign in") {
			r := []rune(l)
			if len(r) > maxHintRunes {
				l = string(r[:maxHintRunes-1]) + "…"
			}
			return l
		}
	}
	return ""
}

func runWithTimeout(ctx context.Context, opts Options, path string, args ...string) (string, int, error) {
	cctx, cancel := context.WithTimeout(ctx, opts.Timeout)
	defer cancel()
	return opts.Runner.Run(cctx, path, args...)
}

func configLine(opts Options) string {
	if opts.configErr != nil {
		return fmt.Sprintf("  %-9s invalid: %v\n", "config", opts.configErr)
	}
	if opts.ConfigPath == "" {
		return fmt.Sprintf("  %-9s unknown (no home directory)\n", "config")
	}
	if _, err := os.Stat(opts.ConfigPath); errors.Is(err, os.ErrNotExist) {
		return fmt.Sprintf("  %-9s %s (not created; defaults in use)\n", "config", opts.ConfigPath)
	}
	return fmt.Sprintf("  %-9s %s\n", "config", opts.ConfigPath)
}

func dirLine(label, dir string) string {
	if dir == "" {
		return fmt.Sprintf("  %-9s unknown (no home directory)\n", label)
	}
	if _, err := os.Stat(dir); errors.Is(err, os.ErrNotExist) {
		return fmt.Sprintf("  %-9s %s (created on first thread)\n", label, dir)
	}
	return fmt.Sprintf("  %-9s %s\n", label, dir)
}

func terminalLine(opts Options) string {
	cols, rows, tty := opts.Terminal()
	if !tty {
		return fmt.Sprintf("  %-9s not a terminal (output is piped)\n", "terminal")
	}
	if cols == 0 || rows == 0 {
		return fmt.Sprintf("  %-9s interactive, size unknown\n", "terminal")
	}
	return fmt.Sprintf("  %-9s %d×%d, interactive\n", "terminal", cols, rows)
}

func stdoutTerminal() (int, int, bool) {
	fd := int(os.Stdout.Fd())
	if !term.IsTerminal(fd) {
		return 0, 0, false
	}
	cols, rows, err := term.GetSize(fd)
	if err != nil {
		return 0, 0, true
	}
	return cols, rows, true
}

// execRunner runs real processes.
type execRunner struct{}

func (execRunner) LookPath(name string) (string, error) {
	return exec.LookPath(name)
}

func (execRunner) Run(ctx context.Context, path string, args ...string) (string, int, error) {
	cmd := exec.CommandContext(ctx, path, args...)
	out, err := cmd.CombinedOutput()
	if err == nil {
		return string(out), 0, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return string(out), exitErr.ExitCode(), nil
	}
	return string(out), -1, err
}

// The logo beside each CLI's line, in cells.
const logoCols, logoRows = 4, 2

// withLogo puts a CLI's logo to the left of its report line: two rows are
// reserved, the logo is drawn at their start, and the line is written beside
// it. Without a logo the line is unchanged.
func withLogo(opts Options, kind, line string) string {
	if opts.Logo == nil {
		return line
	}
	logo := opts.Logo(kind)
	if logo == "" {
		return line
	}
	return strings.Repeat("\n", logoRows) + fmt.Sprintf("\x1b[%dA\x1b7", logoRows) + logo +
		fmt.Sprintf("\x1b8\x1b[%dC", logoCols+2) + strings.TrimSuffix(line, "\n") + strings.Repeat("\n", logoRows)
}

// terminalLogo draws the real logo when stdout is a terminal that can show
// images.
func terminalLogo(kind string) string {
	if _, _, tty := stdoutTerminal(); !tty {
		return ""
	}
	p := termimg.Detect(os.Getenv)
	img, ok := brand.Logo(kind)
	if p == termimg.None || !ok {
		return ""
	}
	return termimg.Draw(p, img, logoCols, logoRows)
}
