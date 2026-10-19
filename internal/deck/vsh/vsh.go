package vsh

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/interp"
)

// Shim is a host program a contract may run, with the argv shapes it may run it with.
type Shim struct {
	Name   string    // what scripts call it by, e.g. "podman"
	Binary string    // absolute path, resolved once (exec.LookPath at config time)
	Allow  []Pattern // argv (after Name) must match one exactly
}

// Pattern is one allowed argv shape. The package doc gives the syntax.
type Pattern []string

// Sandbox is the configuration of one run.
type Sandbox struct {
	Dir        string        // scratch directory; the only place files may be read or written
	QuotaBytes int64         // scratch size cap
	OutputCap  int           // bytes of stdout+stderr kept (rest dropped, Result.Truncated)
	Timeout    time.Duration // whole script
	Shims      []Shim
	Targets    []string // names a {target} placeholder may take
}

// Result is what a run produced.
type Result struct {
	Stdout, Stderr string
	Exit           int
	Truncated      bool
}

// Exit codes for a refusal and a timeout.
const (
	exitRefused = 126
	exitTimeout = 124
)

// refusal is a script refusal. Its text says why in one short line.
type refusal string

func (r refusal) Error() string { return string(r) }

func refusef(format string, a ...any) error { return refusal(fmt.Sprintf(format, a...)) }

// Run validates script against the dialect, then runs it. A command that is
// neither an allowed builtin nor a matching shim exits 126 "not permitted by contract".
func Run(ctx context.Context, sb Sandbox, script string) (Result, error) {
	shims, err := sb.compile()
	if err != nil {
		return Result{}, err
	}
	out := &output{left: max(sb.OutputCap, 0)}
	file, err := parse(script, shims)
	if err != nil {
		out.refuse(err)
		return out.result(exitRefused), nil
	}
	scratch, err := openScratch(sb.Dir, sb.QuotaBytes)
	if err != nil {
		return Result{}, err
	}
	defer scratch.root.Close()

	runCtx, cancel := context.WithTimeout(ctx, sb.Timeout)
	defer cancel()
	s := &session{dir: scratch.dir, fs: scratch, shims: shims, targets: sb.Targets}
	r, err := interp.New(
		interp.StdIO(nil, out.writer(&out.stdout), out.writer(&out.stderr)),
		interp.Env(expand.ListEnviron("LANG=C")),
		interp.Dir(scratch.dir),
		interp.CallHandler(s.call),
		interp.ExecHandlers(func(interp.ExecHandlerFunc) interp.ExecHandlerFunc { return s.exec }),
		interp.OpenHandler(scratch.open),
		interp.ReadDirHandler2(scratch.readDir),
		interp.StatHandler(scratch.stat),
		interp.AccessHandler(scratch.access),
	)
	if err != nil {
		return Result{}, fmt.Errorf("vsh: %w", err)
	}
	err = r.Run(runCtx, file)
	// echo and printf ignore write errors, so say it once here.
	if scratch.full.Load() {
		out.refuse(errQuota)
	}
	var ref refusal
	switch code, isExit := interp.IsExitStatus(err); {
	case err == nil:
		return out.result(0), nil
	case isExit:
		return out.result(int(code)), nil
	case errors.As(err, &ref):
		out.refuse(ref)
		return out.result(exitRefused), nil
	case ctx.Err() != nil:
		return out.result(exitTimeout), ctx.Err()
	case runCtx.Err() != nil:
		out.refuse(fmt.Errorf("timed out after %s", sb.Timeout))
		return out.result(exitTimeout), nil
	default:
		return out.result(1), fmt.Errorf("vsh: %w", err)
	}
}

// compile checks the sandbox and its shims and returns the shims by name.
func (sb Sandbox) compile() (map[string]*shim, error) {
	if !filepath.IsAbs(sb.Dir) {
		return nil, fmt.Errorf("vsh: scratch dir %q is not an absolute path", sb.Dir)
	}
	if fi, err := os.Stat(sb.Dir); err != nil || !fi.IsDir() {
		return nil, fmt.Errorf("vsh: scratch dir %q is not a directory", sb.Dir)
	}
	if sb.Timeout <= 0 {
		return nil, errors.New("vsh: timeout must be positive")
	}
	shims := make(map[string]*shim, len(sb.Shims))
	for _, s := range sb.Shims {
		c, err := compileShim(s)
		if err != nil {
			return nil, fmt.Errorf("vsh: shim %q: %w", s.Name, err)
		}
		if shims[s.Name] != nil {
			return nil, fmt.Errorf("vsh: shim %q: defined twice", s.Name)
		}
		shims[s.Name] = c
	}
	return shims, nil
}

// output keeps at most left bytes of stdout and stderr together.
type output struct {
	mu             sync.Mutex
	stdout, stderr bytes.Buffer
	left           int
	truncated      bool
}

type capWriter struct {
	o *output
	b *bytes.Buffer
}

func (o *output) writer(b *bytes.Buffer) io.Writer { return capWriter{o, b} }

// Write keeps what fits and drops the rest. It never fails, so a producer
// does not see an error because of the cap.
func (w capWriter) Write(p []byte) (int, error) {
	w.o.mu.Lock()
	defer w.o.mu.Unlock()
	n := min(len(p), w.o.left)
	w.b.Write(p[:n])
	w.o.left -= n
	if n < len(p) {
		w.o.truncated = true
	}
	return len(p), nil
}

// refuse adds one line that says why. The line skips the cap, so the agent
// always sees it, but it is cut to 200 bytes.
func (o *output) refuse(err error) {
	msg := "vsh: " + err.Error()
	if len(msg) > 200 {
		msg = msg[:200]
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	o.stderr.WriteString(msg + "\n")
}

func (o *output) result(exit int) Result {
	o.mu.Lock()
	defer o.mu.Unlock()
	return Result{Stdout: o.stdout.String(), Stderr: o.stderr.String(), Exit: exit, Truncated: o.truncated}
}
