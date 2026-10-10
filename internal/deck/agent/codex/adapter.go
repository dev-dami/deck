// Package codex is the Deck adapter for the OpenAI Codex CLI. It launches the
// user's own installed `codex app-server` (under the user's own login) and
// speaks its JSON-RPC 2.0 protocol over newline-delimited JSON on stdio.
// Deck never reads Codex credentials and never calls any model API itself.
package codex

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/dev-dami/deck/internal/deck/agent"
)

// Options tune how threads are started.
type Options struct {
	// ApprovalPolicy is the codex AskForApproval value: "untrusted",
	// "on-request" or "never". Default "on-request".
	ApprovalPolicy string
	// Sandbox is the codex SandboxMode: "read-only", "workspace-write" or
	// "danger-full-access". Default "workspace-write".
	Sandbox string
	// ClientVersion is reported in the initialize handshake.
	ClientVersion string
	// Tap, if set, sees every protocol line (both directions). For tests and
	// fixture recording.
	Tap Tap
}

type adapter struct {
	binary string
	opts   Options

	modelsMu sync.Mutex
	models   []agent.ModelInfo // cached for the adapter's lifetime
}

var _ agent.ModelLister = (*adapter)(nil)

// New returns the codex adapter. binary is the codex executable ("" = "codex"
// on PATH); Spec.Binary overrides it per run.
func New(binary string) agent.Adapter { return NewWithOptions(binary, Options{}) }

// NewWithOptions is New with explicit options.
func NewWithOptions(binary string, opts Options) agent.Adapter {
	if opts.ApprovalPolicy == "" {
		opts.ApprovalPolicy = DefaultApprovalPolicy
	}
	if opts.Sandbox == "" {
		opts.Sandbox = DefaultSandbox
	}
	if opts.ClientVersion == "" {
		opts.ClientVersion = "0.1.0"
	}
	return &adapter{binary: binary, opts: opts}
}

func (a *adapter) Name() string { return "codex" }

func (a *adapter) Start(ctx context.Context, spec agent.Spec) (agent.Run, error) {
	bin := spec.Binary
	if bin == "" {
		bin = a.binary
	}
	if bin == "" {
		bin = "codex"
	}
	path, err := exec.LookPath(bin) // resolves .exe/.cmd via PATHEXT on Windows
	if err != nil {
		return nil, fmt.Errorf("codex: cannot find %q: %w", bin, err)
	}

	cmd := exec.Command(path, "app-server")
	cmd.Dir = spec.Dir
	cmd.Env = os.Environ()
	for k, v := range spec.Env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	// Deck's MCP server env (the hub token) rides in the process environment,
	// never argv or the protocol; the thread config only names the variables.
	cmd.Env = append(cmd.Env, mcpProcessEnv(spec.MCP)...)
	configureProc(cmd)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr := &tailBuffer{max: 2048}
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("codex: start: %w", err)
	}

	r := &run{
		spec:      spec,
		cmd:       cmd,
		stdin:     stdin,
		stderr:    stderr,
		opts:      a.opts,
		client:    NewClient(stdout, stdin, a.opts.Tap),
		events:    make(chan agent.Event, 256),
		approvals: make(chan agent.ApprovalRequest, 16),
		closing:   make(chan struct{}),
		loopDone:  make(chan struct{}),

		pendingEffort: spec.Effort,
	}
	r.tr = newTranslator(spec.ID, spec.Dir)
	r.tr.onTurn = r.setTurn
	r.tr.onTurnDone = r.markTurnDone

	if err := r.handshake(ctx, a.opts); err != nil {
		r.kill()
		go func() {
			for range r.client.Incoming() {
			}
		}()
		_ = r.cmd.Wait()
		if tail := r.stderr.String(); tail != "" {
			err = fmt.Errorf("%w (codex stderr: %s)", err, tail)
		}
		return nil, err
	}
	go r.watchContext(ctx)
	if spec.FirstPrompt != "" {
		if err := r.Send(spec.FirstPrompt); err != nil {
			r.Close()
			return nil, fmt.Errorf("codex: first prompt: %w", err)
		}
	}
	return r, nil
}

// tailBuffer keeps the last max bytes written to it.
type tailBuffer struct {
	mu  sync.Mutex
	max int
	buf []byte
}

func (b *tailBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.buf = append(b.buf, p...)
	if len(b.buf) > b.max {
		b.buf = b.buf[len(b.buf)-b.max:]
	}
	return len(p), nil
}

func (b *tailBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(bytes.TrimSpace(b.buf))
}

var errNoThread = errors.New("codex: no thread")

const rpcTimeout = 60 * time.Second
