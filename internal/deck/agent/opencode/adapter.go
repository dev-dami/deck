// Package opencode is the Deck adapter for OpenCode. It launches the user's
// own installed `opencode acp` (the Agent Client Protocol server OpenCode
// ships) and speaks ACP's JSON-RPC 2.0 over newline-delimited JSON on stdio.
//
// OpenCode brings its own providers: every model it is configured for,
// including external providers, is offered to Deck through the session's
// "model" option, and models that take a reasoning effort add an "effort"
// option. Deck never reads OpenCode's credentials and never calls a model API
// itself; it never writes OpenCode's config either. The one setting it passes
// is OPENCODE_CONFIG_CONTENT in the child's environment, so that edits and
// commands ask first and Deck's approvals (and its plan / ask / yolo modes)
// decide them.
package opencode

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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

// Options tune the adapter.
type Options struct {
	// ClientVersion is reported in the initialize handshake.
	ClientVersion string
	// Tap, if set, sees every protocol line (both directions). For tests.
	Tap codex.Tap
}

type adapter struct {
	binary string
	opts   Options

	models modelcache.Cache // see Models
}

var _ agent.ModelLister = (*adapter)(nil)

// New returns the OpenCode adapter. binary is the opencode executable
// ("" = "opencode" on PATH); Spec.Binary overrides it per run.
func New(binary string) agent.Adapter { return NewWithOptions(binary, Options{}) }

// NewWithOptions is New with explicit options.
func NewWithOptions(binary string, opts Options) agent.Adapter {
	if opts.ClientVersion == "" {
		opts.ClientVersion = "0.1.0"
	}
	a := &adapter{binary: binary, opts: opts}
	a.models.RetryAfter = modelsRetryAfter
	return a
}

func (a *adapter) Name() string { return "opencode" }

// ContractSafe implements agent.ContractSafe: in ask mode its edits,
// commands and fetches ask through Deck.
func (a *adapter) ContractSafe() bool { return true }

const rpcTimeout = 60 * time.Second

// askFirst makes OpenCode ask before it edits a file, runs a command or
// fetches a page, so every such action reaches Deck as an approval.
var askFirst = map[string]any{"edit": "ask", "bash": "ask", "webfetch": "ask"}

// childEnv is the environment for `opencode acp`: the user's, the spec's, and
// OPENCODE_CONFIG_CONTENT with the ask-first permissions laid over whatever
// inline config the user already had there.
func childEnv(extra map[string]string) []string {
	var env []string
	inline := map[string]any{}
	for _, kv := range os.Environ() {
		if k, v, ok := strings.Cut(kv, "="); ok && strings.EqualFold(k, "OPENCODE_CONFIG_CONTENT") {
			_ = json.Unmarshal([]byte(v), &inline)
			continue
		}
		env = append(env, kv)
	}
	for k, v := range extra {
		if strings.EqualFold(k, "OPENCODE_CONFIG_CONTENT") {
			_ = json.Unmarshal([]byte(v), &inline)
			continue
		}
		env = append(env, k+"="+v)
	}
	perm, _ := inline["permission"].(map[string]any)
	if perm == nil {
		perm = map[string]any{}
	}
	for k, v := range askFirst {
		perm[k] = v
	}
	inline["permission"] = perm
	b, _ := json.Marshal(inline)
	return append(env, "OPENCODE_CONFIG_CONTENT="+string(b))
}

func (a *adapter) lookPath(bin string) (string, error) {
	if bin == "" {
		bin = a.binary
	}
	if bin == "" {
		bin = "opencode"
	}
	path, err := exec.LookPath(bin) // resolves .exe/.cmd via PATHEXT on Windows
	if err != nil {
		return "", fmt.Errorf("opencode: cannot find %q: %w", bin, err)
	}
	return path, nil
}

// proc is one running `opencode acp` and its client.
type proc struct {
	cmd    *exec.Cmd
	stdin  interface{ Close() error }
	stderr *tailBuffer
	client *codex.Client
	caps   initializeResult
}

// startProc starts `opencode acp` in dir and performs the initialize handshake.
func (a *adapter) startProc(ctx context.Context, bin, dir string, env map[string]string) (*proc, error) {
	path, err := a.lookPath(bin)
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(path, "acp")
	cmd.Dir = dir
	cmd.Env = childEnv(env)
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
		return nil, fmt.Errorf("opencode: start: %w", err)
	}
	p := &proc{cmd: cmd, stdin: stdin, stderr: stderr, client: codex.NewClient2(stdout, stdin, a.opts.Tap)}
	ictx, cancel := context.WithTimeout(ctx, rpcTimeout)
	defer cancel()
	init := initializeParams{
		ProtocolVersion: 1,
		ClientInfo:      clientInfo{Name: "deck", Title: "Deck", Version: a.opts.ClientVersion},
	}
	if err := p.client.Call(ictx, methodInitialize, init, &p.caps); err != nil {
		p.stop()
		return nil, p.withTail(fmt.Errorf("opencode: initialize: %w", err))
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
		err = fmt.Errorf("%w (opencode stderr: %s)", err, tail)
	}
	return err
}

func (a *adapter) Start(ctx context.Context, spec agent.Spec) (agent.Run, error) {
	env := map[string]string{}
	for k, v := range spec.Env {
		env[k] = v
	}
	p, err := a.startProc(ctx, spec.Binary, spec.Dir, env)
	if err != nil {
		return nil, err
	}
	r := &run{
		spec:      spec,
		p:         p,
		events:    make(chan agent.Event, 256),
		approvals: make(chan agent.ApprovalRequest, 16),
		closing:   make(chan struct{}),
		loopDone:  make(chan struct{}),
		tr:        newTranslator(spec.ID, spec.Dir),
		briefed:   strings.TrimSpace(spec.Instructions) == "",
	}
	if err := r.handshake(ctx); err != nil {
		p.stop()
		return nil, p.withTail(err)
	}
	go r.watchContext(ctx)
	if spec.FirstPrompt != "" {
		if err := r.Send(spec.FirstPrompt); err != nil {
			r.Close()
			return nil, fmt.Errorf("opencode: first prompt: %w", err)
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

var errNoSession = errors.New("opencode: no session")
