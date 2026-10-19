package vsh

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"regexp"
	"slices"
	"strings"
	"syscall"
	"time"

	"mvdan.cc/sh/v3/interp"
)

// shimEnv is the whole environment of a shim process.
var shimEnv = []string{"PATH=/usr/bin:/bin", "LANG=C"}

// session is the state of one Run that the interpreter handlers share.
type session struct {
	dir     string
	fs      *scratch
	shims   map[string]*shim
	targets []string
}

// printfWide finds a printf width or precision that is * or has five or
// more digits, which could make one huge string.
var printfWide = regexp.MustCompile(`%[^a-zA-Z%]*(\*|[0-9]{5})`)

// call runs before every command, builtins included. The dialect check
// already refused other names, so this is a second check after expansion.
func (s *session) call(_ context.Context, args []string) ([]string, error) {
	if !allowed(args[0], s.shims) {
		return nil, refusef("%q: not permitted by contract", args[0])
	}
	if args[0] == "printf" {
		if slices.Contains(args[1:], "-v") {
			return nil, refusef("printf -v is not allowed")
		}
		format := args[1:]
		if len(format) > 0 && format[0] == "--" {
			format = format[1:]
		}
		if len(format) > 0 && printfWide.MatchString(format[0]) {
			return nil, refusef("printf: width and precision must be at most 4 digits")
		}
	}
	return args, nil
}

// exec runs every command that is not an interpreter builtin. It never
// falls through to the default handler, which would search PATH.
func (s *session) exec(ctx context.Context, args []string) error {
	hc := interp.HandlerCtx(ctx)
	if b := goBuiltins[args[0]]; b != nil {
		stdin := hc.Stdin
		if stdin == nil {
			stdin = strings.NewReader("")
		}
		e := &env{ctx: ctx, fs: s.fs, stdin: stdin, stdout: hc.Stdout, stderr: hc.Stderr}
		return e.run(args[0], b, args[1:])
	}
	sh := s.shims[args[0]]
	if sh == nil {
		fmt.Fprintf(hc.Stderr, "vsh: %q: not permitted by contract\n", args[0])
		return interp.ExitStatus(exitRefused)
	}
	if !sh.match(args[1:], s.targets) {
		fmt.Fprintf(hc.Stderr, "vsh: %s: arguments do not match an allowed pattern: not permitted by contract\n", args[0])
		return interp.ExitStatus(exitRefused)
	}
	cmd := exec.CommandContext(ctx, sh.Binary, args[1:]...)
	cmd.Env = shimEnv
	cmd.Dir = s.dir
	cmd.Stdout = hc.Stdout
	cmd.Stderr = hc.Stderr
	cmd.WaitDelay = time.Second
	killGroup(cmd)
	err := cmd.Run()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	var exitErr *exec.ExitError
	switch {
	case err == nil, errors.Is(err, exec.ErrWaitDelay):
		return nil
	case errors.As(err, &exitErr):
		return interp.ExitStatus(uint8(max(exitErr.ExitCode(), 1)))
	default:
		fmt.Fprintf(hc.Stderr, "vsh: %s: %v\n", args[0], err)
		return interp.ExitStatus(exitRefused)
	}
}

// env is what a Go builtin runs with.
type env struct {
	ctx            context.Context
	fs             *scratch
	stdin          io.Reader
	stdout, stderr io.Writer
}

// run runs a Go builtin and turns its error into an exit status with one
// line on stderr.
func (e *env) run(name string, b builtin, args []string) error {
	err := b(e, args)
	var status interp.ExitStatus
	switch {
	case err == nil:
		return nil
	case e.ctx.Err() != nil:
		return e.ctx.Err()
	case errors.As(err, &status):
		return status
	case errors.Is(err, syscall.EPIPE), errors.Is(err, io.ErrClosedPipe):
		// The reader went away, as with head. Shells stay silent here.
		return interp.ExitStatus(141)
	default:
		fmt.Fprintf(e.stderr, "%s: %v\n", name, err)
		if name == "grep" {
			return interp.ExitStatus(2)
		}
		return interp.ExitStatus(1)
	}
}
