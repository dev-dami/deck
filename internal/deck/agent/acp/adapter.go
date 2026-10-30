// Package acp is Deck's client for the Agent Client Protocol (ACP): JSON-RPC
// 2.0 over newline-delimited JSON on the stdio of an agent process, as Zed
// defined it (initialize, session/new|load|resume, session/prompt with
// session/update notifications, session/request_permission,
// session/set_config_option and session/cancel). It is the shared core of
// every Deck adapter whose CLI speaks ACP: OpenCode (`opencode acp`) and Pi
// (`pi-acp`) are each a Config on top of it.
//
// The agent brings its own providers and login: every model it offers comes
// from the session's "model" config option, and a model's reasoning efforts
// from its "thought_level" option. Deck never reads the agent's credentials
// and never calls a model API itself.
package acp

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/dev-dami/deck/internal/deck/agent"
	"github.com/dev-dami/deck/internal/deck/agent/codex"
	"github.com/dev-dami/deck/internal/deck/agent/modelcache"
)

// Config is what one ACP agent needs beyond the protocol.
type Config struct {
	// Kind is Deck's name for the agent ("opencode", "pi"): the adapter's
	// Name and the prefix of its errors.
	Kind string
	// Command is the executable when neither New's binary nor Spec.Binary
	// names one; Args are passed to it ("acp" for OpenCode).
	Command string
	Args    []string
	// Env builds the child's environment from the spec's extra variables;
	// nil means the user's environment plus extra.
	Env func(extra map[string]string) []string
	// Install says how to install the command, added to the error when it
	// cannot be found.
	Install string
	// ClientVersion is reported in the initialize handshake.
	ClientVersion string
	// Tap, if set, sees every protocol line (both directions). For tests.
	Tap codex.Tap
	// Caps are the adapter's ContextCaps.
	Caps agent.ContextCaps
	// ContractSafe: in ask mode the agent's edits and commands ask through
	// session/request_permission (see agent.ContractSafe).
	ContractSafe bool
	// EffortAliases maps an effort value of the agent's to Deck's level
	// ("off" → "none"); the reverse is used when setting one.
	EffortAliases map[string]string
	// Video reports whether a model of the agent takes video (see
	// agent.VideoInput); nil means none does. A video goes to the agent as
	// a resource_link to the local file with its MIME type.
	Video func(model string) bool
}

// Adapter starts runs of one ACP agent.
type Adapter struct {
	binary string
	cfg    Config

	models modelcache.Cache // see Models
}

var (
	_ agent.ModelLister    = (*Adapter)(nil)
	_ agent.ModelForgetter = (*Adapter)(nil)
	_ agent.ContextCapser  = (*Adapter)(nil)
	_ agent.ContractSafe   = (*Adapter)(nil)
	_ agent.VideoInput     = (*Adapter)(nil)
)

// New returns the adapter for cfg. binary is the executable ("" =
// cfg.Command on PATH); Spec.Binary overrides it per run.
func New(binary string, cfg Config) *Adapter {
	if cfg.ClientVersion == "" {
		cfg.ClientVersion = "0.1.0"
	}
	a := &Adapter{binary: binary, cfg: cfg}
	a.models.RetryAfter = modelsRetryAfter
	return a
}

func (a *Adapter) Name() string { return a.cfg.Kind }

// ContextCaps implements agent.ContextCapser.
func (a *Adapter) ContextCaps() agent.ContextCaps { return a.cfg.Caps }

// ContractSafe implements agent.ContractSafe.
func (a *Adapter) ContractSafe() bool { return a.cfg.ContractSafe }

// TakesVideo implements agent.VideoInput.
func (a *Adapter) TakesVideo(model string) bool { return a.cfg.Video != nil && a.cfg.Video(model) }

const rpcTimeout = 60 * time.Second

func (a *Adapter) errorf(format string, args ...any) error {
	return fmt.Errorf(a.cfg.Kind+": "+format, args...)
}

func (a *Adapter) lookPath(bin string) (string, error) {
	if bin == "" {
		bin = a.binary
	}
	if bin == "" {
		bin = a.cfg.Command
	}
	path, err := exec.LookPath(bin) // resolves .exe/.cmd via PATHEXT on Windows
	if err != nil {
		if a.cfg.Install != "" {
			return "", a.errorf("cannot find %q (install: %s): %w", bin, a.cfg.Install, err)
		}
		return "", a.errorf("cannot find %q: %w", bin, err)
	}
	return path, nil
}

func (a *Adapter) env(extra map[string]string) []string {
	if a.cfg.Env != nil {
		return a.cfg.Env(extra)
	}
	env := os.Environ()
	for k, v := range extra {
		env = append(env, k+"="+v)
	}
	return env
}

// proc is one running agent process and its client.
type proc struct {
	kind   string
	cmd    *exec.Cmd
	stdin  interface{ Close() error }
	stderr *tailBuffer
	client *codex.Client
	caps   initializeResult
}

// startProc starts the agent in dir and performs the initialize handshake.
func (a *Adapter) startProc(ctx context.Context, bin, dir string, env map[string]string) (*proc, error) {
	path, err := a.lookPath(bin)
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(path, a.cfg.Args...)
	cmd.Dir = dir
	cmd.Env = a.env(env)
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
		return nil, a.errorf("start: %w", err)
	}
	p := &proc{kind: a.cfg.Kind, cmd: cmd, stdin: stdin, stderr: stderr, client: codex.NewClient2Decode(stdout, stdin, a.cfg.Tap, decodeUpdate)}
	ictx, cancel := context.WithTimeout(ctx, rpcTimeout)
	defer cancel()
	init := initializeParams{
		ProtocolVersion: 1,
		ClientInfo:      clientInfo{Name: "deck", Title: "Deck", Version: a.cfg.ClientVersion},
	}
	if err := p.client.Call(ictx, methodInitialize, init, &p.caps); err != nil {
		p.stop()
		return nil, p.withTail(a.errorf("initialize: %w", err))
	}
	return p, nil
}

// stop kills the process and drains what is left of its output.
func (p *proc) stop() {
	_ = p.stdin.Close()
	killTree(p.cmd)
	go func() {
		for range p.client.Incoming() {
		}
	}()
	_ = p.cmd.Wait()
}

func (p *proc) withTail(err error) error {
	if tail := p.stderr.String(); tail != "" {
		err = fmt.Errorf("%w (%s stderr: %s)", err, p.kind, tail)
	}
	return err
}

func (a *Adapter) Start(ctx context.Context, spec agent.Spec) (agent.Run, error) {
	env := map[string]string{}
	for k, v := range spec.Env {
		env[k] = v
	}
	p, err := a.startProc(ctx, spec.Binary, spec.Dir, env)
	if err != nil {
		return nil, err
	}
	r := &run{
		a:         a,
		spec:      spec,
		p:         p,
		events:    make(chan agent.Event, 256),
		approvals: make(chan agent.ApprovalRequest, 16),
		closing:   make(chan struct{}),
		loopDone:  make(chan struct{}),
		tr:        newTranslator(spec.ID, spec.Dir),
		briefed:   strings.TrimSpace(spec.Instructions) == "" || spec.Briefed && spec.ResumeID != "",
	}
	if err := r.handshake(ctx); err != nil {
		p.stop()
		return nil, p.withTail(err)
	}
	go r.watchContext(ctx)
	if spec.FirstPrompt != "" {
		if err := r.Send(spec.FirstPrompt); err != nil {
			r.Close()
			return nil, a.errorf("first prompt: %w", err)
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
