// Package claude is the Deck adapter for Claude Code. It launches the user's own
// installed `claude` binary in headless stream-json mode under the user's own
// login and translates its output into agent.Events. Tool permission prompts are
// routed to Deck through --permission-prompt-tool and a tiny stdio MCP server
// (the Deck binary itself, run as `deck mcp-approve`).
package claude

import (
	"bufio"
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

	"github.com/dev-dami/deck/internal/agent"
	"github.com/dev-dami/deck/internal/approve"
)

// MCPToolFlagValue is the tool name passed to --permission-prompt-tool: server
// "deck", tool "approve".
const MCPToolFlagValue = "mcp__deck__" + approve.MCPToolName

// Messaging tool names as Claude sees them (server "deck").
const (
	toolSendMessage = "mcp__deck__" + approve.MCPToolSendMessage
	toolReadThread  = "mcp__deck__" + approve.MCPToolReadThread
)

type adapter struct {
	binary string
	self   string
	broker *approve.Broker

	// test hooks
	extraArgs []string
	tap       func(line []byte) // called with every raw stdout line
}

// New returns the Claude Code adapter. binary may be empty (looks up "claude" on
// PATH); selfExe is the path of the Deck executable, which must answer
// `mcp-approve --addr A --agent ID` by calling approve.MCPServe with
// approve.TokenFromEnv() (the token is passed in the MCP server env, not argv).
func New(binary string, selfExe string, broker *approve.Broker) agent.Adapter {
	return &adapter{binary: binary, self: selfExe, broker: broker}
}

func (a *adapter) Name() string { return "claude" }

func (a *adapter) Start(ctx context.Context, spec agent.Spec) (agent.Run, error) {
	bin := spec.Binary
	if bin == "" {
		bin = a.binary
	}
	if bin == "" {
		bin = "claude"
	}
	path, err := exec.LookPath(bin)
	if err != nil {
		return nil, fmt.Errorf("claude binary %q not found: %w", bin, err)
	}

	r := &run{
		a:      a,
		spec:   spec,
		path:   path,
		events: make(chan agent.Event, 256),
		done:   make(chan struct{}),
		tr:     newTranslator(spec.ID),
	}
	if a.broker != nil {
		r.approvals = a.broker.Register(spec.ID)
	} else {
		r.approvals = make(chan agent.ApprovalRequest)
	}
	// Prefer the server the caller put in the spec; fall back to one built from
	// the adapter's own broker so older callers keep working.
	server := spec.MCP
	if server == nil && a.broker != nil {
		server = a.broker.ServerFor(a.self, spec.ID)
	}
	if server != nil {
		cfg, err := writeMCPConfig(server)
		if err != nil {
			return nil, err
		}
		r.cfgPath = cfg
		r.mcpName = server.Name
		if r.mcpName == "" {
			r.mcpName = "deck"
		}
	}
	r.sessionID = spec.ResumeID

	if err := r.spawn(spec.ResumeID); err != nil {
		r.removeConfig()
		return nil, err
	}
	go func() {
		select {
		case <-ctx.Done():
			r.Close()
		case <-r.done:
		}
	}()
	if spec.FirstPrompt != "" {
		if err := r.Send(spec.FirstPrompt); err != nil {
			r.Close()
			return nil, err
		}
	}
	return r, nil
}

// writeMCPConfig writes the --mcp-config file (0600; it carries the hub token in
// the server env) for one stdio MCP server.
func writeMCPConfig(s *agent.MCPServer) (string, error) {
	name := s.Name
	if name == "" {
		name = "deck"
	}
	entry := map[string]any{"command": s.Command, "args": s.Args}
	if len(s.Env) > 0 {
		entry["env"] = s.Env
	}
	if s.Args == nil {
		entry["args"] = []string{}
	}
	cfg := map[string]any{"mcpServers": map[string]any{name: entry}}
	data, _ := json.Marshal(cfg)
	f, err := os.CreateTemp("", "deck-mcp-*.json")
	if err != nil {
		return "", err
	}
	// CreateTemp already creates the file 0600.
	_ = f.Chmod(0o600)
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(f.Name())
		return "", err
	}
	if err := f.Close(); err != nil {
		os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
}

// proc is one OS process of `claude`. An interrupt replaces it with a new one.
type proc struct {
	cmd         *exec.Cmd
	stdin       interface{ Write([]byte) (int, error) }
	closeStdin  func() error
	stderr      *tailBuffer
	exited      chan struct{} // closed after cmd.Wait returns
	intentional bool          // killed by Interrupt/Close; do not report as an exit
}

type run struct {
	a    *adapter
	spec agent.Spec
	path string

	events    chan agent.Event
	approvals <-chan agent.ApprovalRequest
	done      chan struct{}
	cfgPath   string
	mcpName   string // server name in the MCP config ("deck")

	tr *translator // used by the single active reader goroutine at a time

	mu        sync.Mutex
	cur       *proc
	sessionID string
	busy      bool
	closed    bool
	readers   sync.WaitGroup
	sendMu    sync.Mutex
	evMu      sync.RWMutex
	evClosed  bool
}

func (r *run) args(resume string) []string {
	args := []string{
		"-p",
		"--input-format", "stream-json",
		"--output-format", "stream-json",
		"--verbose",
		"--include-partial-messages",
	}
	if r.cfgPath != "" {
		args = append(args, "--mcp-config", r.cfgPath,
			"--permission-prompt-tool", "mcp__"+r.mcpName+"__"+approve.MCPToolName,
			"--allowedTools", "mcp__"+r.mcpName+"__"+approve.MCPToolSendMessage+",mcp__"+r.mcpName+"__"+approve.MCPToolReadThread)
	}
	if r.spec.Model != "" {
		args = append(args, "--model", r.spec.Model)
	}
	if resume != "" {
		args = append(args, "--resume", resume)
	}
	var sys []string
	if r.spec.TellName && r.spec.UserName != "" {
		sys = append(sys, "The user's name is "+r.spec.UserName+".")
	}
	if in := strings.TrimSpace(r.spec.Instructions); in != "" {
		sys = append(sys, in)
	}
	if len(sys) > 0 {
		args = append(args, "--append-system-prompt", strings.Join(sys, "\n\n"))
	}
	args = append(args, r.a.extraArgs...)
	return args
}

// spawn starts a claude process and its reader goroutine. Caller must not hold r.mu.
func (r *run) spawn(resume string) error {
	cmd := exec.Command(r.path, r.args(resume)...)
	cmd.Dir = r.spec.Dir
	var env []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(strings.ToUpper(kv), approve.TokenEnv+"=") {
			env = append(env, kv) // the token only goes to the MCP helper, via the MCP config
		}
	}
	for k, v := range r.spec.Env {
		if strings.EqualFold(k, approve.TokenEnv) {
			continue
		}
		env = append(env, k+"="+v)
	}
	cmd.Env = env
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	p := &proc{cmd: cmd, stdin: stdin, closeStdin: stdin.Close, stderr: &tailBuffer{max: 4096}, exited: make(chan struct{})}
	cmd.Stderr = p.stderr
	setProcAttr(cmd)

	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return errors.New("claude run is closed")
	}
	if err := cmd.Start(); err != nil {
		r.mu.Unlock()
		return fmt.Errorf("starting claude: %w", err)
	}
	r.cur = p
	r.readers.Add(1)
	r.mu.Unlock()

	go r.read(p, stdout)
	return nil
}

func (r *run) emit(ev agent.Event) {
	r.evMu.RLock()
	defer r.evMu.RUnlock()
	if r.evClosed {
		return
	}
	select {
	case r.events <- ev:
	case <-r.done:
	}
}

func (r *run) read(p *proc, stdout interface{ Read([]byte) (int, error) }) {
	defer r.readers.Done()
	sc := bufio.NewReaderSize(stdout, 1<<20)
	for {
		line, err := sc.ReadBytes('\n')
		if len(bytes.TrimSpace(line)) > 0 {
			if r.a.tap != nil {
				r.a.tap(append([]byte(nil), line...))
			}
			for _, ev := range r.tr.Line(line) {
				switch ev.Kind {
				case agent.KindSessionStarted:
					r.mu.Lock()
					r.sessionID = ev.SessionID
					r.mu.Unlock()
				case agent.KindTurnDone:
					r.mu.Lock()
					r.busy = false
					r.mu.Unlock()
				}
				r.mu.Lock()
				stale := p.intentional
				r.mu.Unlock()
				if stale {
					continue
				}
				r.emit(ev)
			}
		}
		if err != nil {
			break
		}
	}
	waitErr := p.cmd.Wait()
	close(p.exited)

	r.mu.Lock()
	intentional := p.intentional
	r.mu.Unlock()
	if intentional {
		return
	}
	if waitErr != nil {
		if tail := strings.TrimSpace(p.stderr.String()); tail != "" {
			r.emit(agent.Event{AgentID: r.spec.ID, Time: time.Now(), Kind: agent.KindError, Text: tail})
		}
	}
	r.emit(agent.Event{AgentID: r.spec.ID, Time: time.Now(), Kind: agent.KindExited})
	go r.Close()
}

func (r *run) Events() <-chan agent.Event              { return r.events }
func (r *run) Approvals() <-chan agent.ApprovalRequest { return r.approvals }

func (r *run) SessionID() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.sessionID
}

type userMessage struct {
	Type    string `json:"type"`
	Message struct {
		Role    string `json:"role"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	} `json:"message"`
}

func (r *run) Send(prompt string) error {
	var um userMessage
	um.Type = "user"
	um.Message.Role = "user"
	um.Message.Content = []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}{{Type: "text", Text: prompt}}
	data, err := json.Marshal(um)
	if err != nil {
		return err
	}

	r.mu.Lock()
	if r.closed || r.cur == nil {
		r.mu.Unlock()
		return errors.New("claude run is closed")
	}
	p := r.cur
	r.busy = true
	r.mu.Unlock()

	r.sendMu.Lock()
	_, err = p.stdin.Write(append(data, '\n'))
	r.sendMu.Unlock()
	if err != nil {
		return fmt.Errorf("writing to claude: %w", err)
	}
	r.emit(agent.Event{AgentID: r.spec.ID, Time: time.Now(), Kind: agent.KindUserText, Text: prompt, Raw: data})
	return nil
}

// Interrupt stops the current turn. Claude Code in stream-json mode has no
// documented portable interrupt, so the process tree is killed and a fresh
// process is started with --resume so the agent stays usable.
func (r *run) Interrupt() error {
	r.mu.Lock()
	if r.closed || r.cur == nil {
		r.mu.Unlock()
		return errors.New("claude run is closed")
	}
	if !r.busy {
		r.mu.Unlock()
		return nil
	}
	old := r.cur
	old.intentional = true
	sid := r.sessionID
	r.busy = false
	r.mu.Unlock()

	killTree(old.cmd)
	old.closeStdin()
	// Wait for the old reader to finish so events never interleave.
	waitProcExit(old)

	// start a replacement
	if err := r.spawn(sid); err != nil {
		r.emit(agent.Event{AgentID: r.spec.ID, Time: time.Now(), Kind: agent.KindError, Text: err.Error()})
		r.emit(agent.Event{AgentID: r.spec.ID, Time: time.Now(), Kind: agent.KindExited})
		go r.Close()
		return err
	}
	r.emit(agent.Event{AgentID: r.spec.ID, Time: time.Now(), Kind: agent.KindTurnDone, Text: "interrupted"})
	return nil
}

func waitProcExit(p *proc) {
	select {
	case <-p.exited:
	case <-time.After(5 * time.Second):
	}
}

func (r *run) Close() error {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return nil
	}
	r.closed = true
	p := r.cur
	if p != nil {
		p.intentional = true
	}
	r.mu.Unlock()

	close(r.done)
	if p != nil {
		p.closeStdin()
		killTree(p.cmd)
	}
	r.readers.Wait()
	r.removeConfig()
	r.evMu.Lock()
	r.evClosed = true
	close(r.events)
	r.evMu.Unlock()
	return nil
}

func (r *run) removeConfig() {
	if r.cfgPath != "" {
		os.Remove(r.cfgPath)
	}
}

// tailBuffer keeps the last max bytes written to it.
type tailBuffer struct {
	mu  sync.Mutex
	max int
	b   []byte
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.b = append(t.b, p...)
	if len(t.b) > t.max {
		t.b = t.b[len(t.b)-t.max:]
	}
	return len(p), nil
}

func (t *tailBuffer) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return string(t.b)
}
