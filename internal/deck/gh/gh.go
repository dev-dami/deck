// Package gh drives git and the GitHub CLI for the UI's git pane: branch
// status, commits and pushes, and pull requests with their checks.
//
// Deck never reads GitHub credentials. Everything that talks to GitHub goes
// through the user's own `gh` CLI, signed in with `gh auth login`; when gh is
// missing or signed out, the git half keeps working and the GitHub half
// reports [ErrNoGH].
package gh

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// ErrNoGH is returned by every GitHub call when gh is missing or signed out.
var ErrNoGH = errors.New("install gh and run `gh auth login`")

// Timeouts for one command. Network commands get longer.
const (
	localTimeout   = 30 * time.Second
	networkTimeout = 2 * time.Minute
)

// availTTL is how long a failed `gh auth status` is believed; a success is
// believed for the rest of the run.
const availTTL = 2 * time.Minute

// Runner runs one command in dir with stdin and returns its stdout. A
// command that fails returns an error carrying its stderr; an [*ExitError]
// when it ran and exited non-zero, with whatever stdout it wrote.
type Runner func(ctx context.Context, dir, stdin, name string, args ...string) (string, error)

// ExitError is a command that ran and exited with a non-zero code.
type ExitError struct {
	Cmd    string // "git push", "gh pr view"
	Code   int
	Stderr string
}

func (e *ExitError) Error() string {
	msg := firstLine(e.Stderr)
	if msg == "" {
		msg = fmt.Sprintf("exit status %d", e.Code)
	}
	return e.Cmd + ": " + msg
}

// exitCode is err's exit code, or -1 when it is not an [*ExitError].
func exitCode(err error) int {
	var ee *ExitError
	if errors.As(err, &ee) {
		return ee.Code
	}
	return -1
}

// stderrOf is the full stderr of a failed command, or err's text.
func stderrOf(err error) string {
	var ee *ExitError
	if errors.As(err, &ee) {
		return ee.Stderr
	}
	return err.Error()
}

// Client runs git and gh in one work tree. It is safe for concurrent use.
type Client struct {
	dir string
	run Runner

	mu        sync.Mutex
	avail     bool
	availAt   time.Time
	availSeen bool
}

// New is a client for the work tree at dir that runs the real git and gh.
func New(dir string) *Client {
	return NewWithRunner(dir, Exec)
}

// NewWithRunner is a client that runs commands through run (for tests).
func NewWithRunner(dir string, run Runner) *Client {
	return &Client{dir: dir, run: run}
}

// Dir is the work tree the client runs in.
func (c *Client) Dir() string { return c.dir }

func (c *Client) git(ctx context.Context, timeout time.Duration, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return c.run(ctx, c.dir, "", "git", args...)
}

func (c *Client) gh(ctx context.Context, timeout time.Duration, stdin string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return c.run(ctx, c.dir, stdin, "gh", args...)
}

// Exec is the real [Runner]: a hidden process with a bounded lifetime (the
// caller's context), an environment that cannot point git at another
// repository and never prompts, and the first line of stderr in its error.
// Output past maxExec bytes is an error, not a truncated result, so a runaway
// command cannot use unbounded memory and JSON is never parsed half-read.
func Exec(ctx context.Context, dir, stdin, name string, args ...string) (string, error) {
	o, e := &limitBuffer{max: maxExec}, &limitBuffer{max: maxExec}
	out, err := execTo(ctx, dir, stdin, name, args, o, e)
	if err == nil && o.total > int64(maxExec) {
		return "", fmt.Errorf("%s: output over %d bytes", name, maxExec)
	}
	return out, err
}

// maxExec caps what [Exec] keeps of a command's output.
const maxExec = 16 << 20

// ExecLimit is [Exec] keeping at most limit bytes of the command's stdout (and
// of its stderr); the rest is read and dropped, so a huge output costs no
// memory. total is how many bytes stdout had in all.
func ExecLimit(ctx context.Context, dir, stdin, name string, limit int, args ...string) (out string, total int64, err error) {
	o, e := &limitBuffer{max: limit}, &limitBuffer{max: limit}
	out, err = execTo(ctx, dir, stdin, name, args, o, e)
	return out, o.total, err
}

// output is where a command's stdout or stderr goes.
type output interface {
	io.Writer
	String() string
}

// limitBuffer keeps the first max bytes written to it and counts the rest.
type limitBuffer struct {
	buf   bytes.Buffer
	max   int
	total int64
}

func (w *limitBuffer) Write(p []byte) (int, error) {
	w.total += int64(len(p))
	if room := w.max - w.buf.Len(); room > 0 {
		w.buf.Write(p[:min(len(p), room)])
	}
	return len(p), nil
}

func (w *limitBuffer) String() string { return w.buf.String() }

// found remembers where git and gh were found, per PATH: on Windows a
// LookPath tries every PATH folder with every PATHEXT extension, and the git
// pane runs a command every few seconds. A miss is not remembered, so a gh
// installed mid-run is found.
var found sync.Map // name + "\x00" + PATH -> path

func lookPath(name string) (string, error) {
	key := name + "\x00" + os.Getenv("PATH")
	if p, ok := found.Load(key); ok {
		return p.(string), nil
	}
	p, err := exec.LookPath(name)
	if err == nil {
		found.Store(key, p)
	}
	return p, err
}

func execTo(ctx context.Context, dir, stdin, name string, args []string, out, errb output) (string, error) {
	bin, err := lookPath(name)
	if err != nil {
		return "", fmt.Errorf("%s is not installed", name)
	}
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = dir
	cmd.Env = cleanEnv()
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	cmd.Stdout, cmd.Stderr = out, errb
	hide(cmd)
	err = cmd.Run()
	if err == nil {
		return out.String(), nil
	}
	label := name
	if len(args) > 0 {
		label += " " + args[0]
		if name == "gh" && len(args) > 1 && !strings.HasPrefix(args[1], "-") {
			label += " " + args[1]
		}
	}
	if ctx.Err() != nil {
		return out.String(), fmt.Errorf("%s: timed out", label)
	}
	var xe *exec.ExitError
	if errors.As(err, &xe) {
		return out.String(), &ExitError{Cmd: label, Code: xe.ExitCode(), Stderr: strings.TrimSpace(errb.String())}
	}
	return out.String(), fmt.Errorf("%s: %w", label, err)
}

// cleanEnv is the environment without anything that would point git at
// another repository or index, plus settings that keep git and gh from
// prompting, colouring or taking optional locks.
func cleanEnv() []string {
	var out []string
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		switch strings.ToUpper(k) {
		case "GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE", "GIT_OBJECT_DIRECTORY", "GIT_COMMON_DIR", "GIT_PREFIX":
			continue
		}
		out = append(out, kv)
	}
	return append(out,
		"GIT_OPTIONAL_LOCKS=0",
		"GIT_TERMINAL_PROMPT=0",
		"GH_PROMPT_DISABLED=1",
		"GH_NO_UPDATE_NOTIFIER=1",
		"GH_SPINNER_DISABLED=1",
		"NO_COLOR=1",
		"CLICOLOR=0",
	)
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	l, _, _ := strings.Cut(s, "\n")
	return strings.TrimSpace(l)
}

func lines(s string) []string {
	s = strings.TrimRight(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}
