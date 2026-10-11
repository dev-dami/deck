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

	"github.com/dev-dami/deck/internal/deck/agent"
	"github.com/dev-dami/deck/internal/deck/approve"
	"github.com/dev-dami/deck/internal/deck/agent/modelcache"
	"github.com/dev-dami/deck/internal/deck/brand"
)

// MCPToolFlagValue is the tool name passed to --permission-prompt-tool: server
// "deck", tool "approve".
const MCPToolFlagValue = "mcp__deck__" + approve.MCPToolName

// deckToolPrefix is how Claude names the tools of Deck's MCP server.
const deckToolPrefix = "mcp__deck__"

type adapter struct {
	binary string
	self   string
	broker *approve.Broker

	models modelcache.Cache // Models() result

	// test hooks
	extraArgs []string
	tap       func(line []byte)                     // called with every raw stdout line
	helpFn    func(context.Context) (string, error) // replaces running `claude --help`
	initFn    func(context.Context) ([]byte, error) // replaces asking claude for its models
	// beforeRespawn runs in restart between stopping the old process and
	// starting the new one.
	beforeRespawn func()
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
		// Only Claude gets the approve tool (its --permission-prompt-tool);
		// every other agent sees just search and execute.
		s := *server
		s.Args = withFlag(server.Args, approve.ApproveToolFlag)
		server = &s
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
	settings, err := writeSettings(spec.Attribution)
	if err != nil {
		r.removeConfig()
		return nil, err
	}
	r.settingsPath = settings
	r.sessionID = spec.ResumeID
	r.model = spec.Model
	r.effort = spec.Effort
	r.mode = spec.Mode
	if r.mode == "" {
		r.mode = agent.ModeAsk
	}

	if err := r.spawn(spec.ResumeID); err != nil {
		r.removeConfig()
		return nil, err
	}
	r.probeUsage()
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

// withFlag is args plus flag, unless it is already there.
func withFlag(args []string, flag string) []string {
	for _, a := range args {
		if a == flag {
			return args
		}
	}
	return append(append([]string{}, args...), flag)
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
	// settingsPath is the --settings file: Deck's commit/PR attribution and
	// plans folder in place of Claude's own.
	settingsPath string
	mcpName      string // server name in the MCP config ("deck")

	tr *translator // used by the single active reader goroutine at a time

	mu        sync.Mutex
	cur       *proc
	sessionID string
	busy      bool
	closed    bool
	model     string     // current --model ("" = CLI default)
	effort    string     // current --effort ("" = the model's default)
	mode      agent.Mode // current permission mode (never empty)
	// dirty: model or mode changed while busy (or while a restart was running);
	// the process is restarted with the new flags once the turn is done.
	dirty bool
	// spawned: the model, effort and mode the newest process was started
	// with, so a restart can tell whether a change that arrived while it was
	// under way already made it in.
	spawned struct {
		model, effort string
		mode          agent.Mode
	}
	// restarting: a deliberate restart is in flight; Sends are queued in
	// queue and written, in order, to the new process when it is up.
	restarting bool
	queue      [][]byte
	readers    sync.WaitGroup
	sendMu     sync.Mutex
	evMu       sync.RWMutex
	evClosed   bool
}

// flagsChangedLocked reports whether the model, effort or mode differ from
// what the newest process was started with. Caller holds r.mu.
func (r *run) flagsChangedLocked() bool {
	return r.model != r.spawned.model || r.effort != r.spawned.effort || r.mode != r.spawned.mode
}

func (r *run) args(resume string) []string {
	r.mu.Lock()
	model, mode, effort := r.model, r.mode, r.effort
	r.spawned.model, r.spawned.effort, r.spawned.mode = model, effort, mode
	r.mu.Unlock()
	args := []string{
		"-p",
		"--input-format", "stream-json",
		"--output-format", "stream-json",
		"--verbose",
		"--include-partial-messages",
	}
	if r.settingsPath != "" {
		args = append(args, "--settings", r.settingsPath)
	}
	if r.cfgPath != "" {
		// --strict-mcp-config: only Deck's server, not every MCP server in the
		// user's Claude config, whose tool lists would fill the context window.
		args = append(args, "--mcp-config", r.cfgPath, "--strict-mcp-config",
			"--permission-prompt-tool", "mcp__"+r.mcpName+"__"+approve.MCPToolName,
			"--allowedTools", "mcp__"+r.mcpName+"__"+approve.MCPToolSearch+",mcp__"+r.mcpName+"__"+approve.MCPToolExecute)
	}
	if model != "" {
		args = append(args, "--model", model)
	}
	if effort != "" {
		args = append(args, "--effort", effort)
	}
	switch mode {
	case agent.ModePlan:
		args = append(args, "--permission-mode", "plan")
	case agent.ModeYolo:
		args = append(args, "--permission-mode", "bypassPermissions")
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
	// Nor the claude.ai connectors (Notion, Granola, ...) a signed-in claude
	// loads on top: dozens of tools in every prompt.
	env = append(env, "ENABLE_CLAUDEAI_MCP_SERVERS=false")
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

// readerPool recycles the 1 MiB stdout readers: an interrupt or a mode or
// model change spawns a new process, and with it a new reader.
var readerPool = sync.Pool{New: func() any { return bufio.NewReaderSize(nil, 1<<20) }}

// readLine returns the next line of br, newline included. The line is only
// valid until the next call: it is br's own buffer, or scratch (returned
// for reuse) for a line longer than that buffer. A scratch grown past 4 MiB
// is not handed back, so one huge line is not kept alive for the whole run.
func readLine(br *bufio.Reader, scratch []byte) (line, buf []byte, err error) {
	line, err = br.ReadSlice('\n')
	if err != bufio.ErrBufferFull {
		return line, scratch, err
	}
	buf = append(scratch[:0], line...)
	for err == bufio.ErrBufferFull {
		line, err = br.ReadSlice('\n')
		buf = append(buf, line...)
	}
	if cap(buf) > 4<<20 {
		return buf, nil, err
	}
	return buf, buf, err
}

func (r *run) read(p *proc, stdout interface{ Read([]byte) (int, error) }) {
	defer r.readers.Done()
	sc := readerPool.Get().(*bufio.Reader)
	sc.Reset(stdout)
	defer func() {
		sc.Reset(nil)
		readerPool.Put(sc)
	}()
	var scratch []byte
	for {
		var line []byte
		var err error
		line, scratch, err = readLine(sc, scratch)
		if len(bytes.TrimSpace(line)) > 0 {
			if r.a.tap != nil {
				r.a.tap(append([]byte(nil), line...))
			}
			for _, ev := range r.tr.Line(line) {
				restart := false
				switch ev.Kind {
				case agent.KindSessionStarted:
					r.mu.Lock()
					r.sessionID = ev.SessionID
					r.mu.Unlock()
				case agent.KindTurnDone:
					r.mu.Lock()
					r.busy = false
					// A mode/model change arrived mid-turn: from here on Sends
					// queue until the restart (below) has finished.
					if r.dirty && !r.closed && !p.intentional && !r.restarting {
						r.restarting = true
						restart = true
					}
					r.mu.Unlock()
				}
				r.mu.Lock()
				stale := p.intentional
				r.mu.Unlock()
				if stale {
					continue
				}
				r.emit(ev)
				if restart {
					go r.restart()
				}
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

func jsonMarshal(v any) ([]byte, error) { return json.Marshal(v) }

// probeUsage asks the CLI, once per run, for the plan's usage windows (the
// get_usage control request; see limits.go). The answer comes back on stdout
// as a control_response. It is best effort: a write error is ignored and the
// rate_limit_event lines still arrive with each turn.
func (r *run) probeUsage() {
	r.mu.Lock()
	p := r.cur
	r.mu.Unlock()
	if p == nil {
		return
	}
	r.sendMu.Lock()
	_, _ = p.stdin.Write(append(usageProbe(), '\n'))
	r.sendMu.Unlock()
}

// Send sends a text-only prompt.
func (r *run) Send(prompt string) error { return r.send(prompt, nil) }

// SendWithImages implements agent.ImageSender: the images go into the same
// user message, before the text. The echo (KindUserText) carries the prompt
// only; images are the UI's own knowledge.
func (r *run) SendWithImages(prompt string, images []agent.Image) error {
	return r.send(prompt, images)
}

// Steer implements agent.Steerer. Idle: same as SendWithImages. Busy: the
// current turn is interrupted (Interrupt: kill, respawn with --resume, one
// KindTurnDone "interrupted") and the steer prompt is then delivered to the
// replacement process as the next message. Claude Code's stream-json input has
// no documented way to inject into a running turn, so this is the only
// supported route. A mode/model restart already in flight just queues it.
func (r *run) Steer(prompt string, images []agent.Image) error {
	// Validate (and read) the images before touching the running turn, so a bad
	// attachment cannot cost the user their turn. The message built here is
	// the one sent: images are read and encoded once.
	data, echo, err := buildUserMessage(prompt, images)
	if err != nil {
		return err
	}
	r.mu.Lock()
	busy := r.busy && !r.restarting && !r.closed
	r.mu.Unlock()
	if busy {
		if err := r.Interrupt(); err != nil {
			return err
		}
	}
	return r.sendMessage(prompt, data, echo)
}

func (r *run) send(prompt string, images []agent.Image) error {
	data, echo, err := buildUserMessage(prompt, images)
	if err != nil {
		return err
	}
	return r.sendMessage(prompt, data, echo)
}

// sendMessage writes a user message built by buildUserMessage.
func (r *run) sendMessage(prompt string, data, echo []byte) error {
	r.mu.Lock()
	if r.closed || r.cur == nil {
		r.mu.Unlock()
		return errors.New("claude run is closed")
	}
	if r.restarting {
		// A mode/model restart is in flight: hold the prompt, restart() writes it.
		r.queue = append(r.queue, append(append([]byte(nil), data...), '\n'))
		r.mu.Unlock()
		r.emit(agent.Event{AgentID: r.spec.ID, Time: time.Now(), Kind: agent.KindUserText, Text: prompt, Raw: echo})
		return nil
	}
	p := r.cur
	r.busy = true
	r.mu.Unlock()

	r.sendMu.Lock()
	_, err := p.stdin.Write(append(data, '\n'))
	r.sendMu.Unlock()
	if err != nil {
		return fmt.Errorf("writing to claude: %w", err)
	}
	r.emit(agent.Event{AgentID: r.spec.ID, Time: time.Now(), Kind: agent.KindUserText, Text: prompt, Raw: echo})
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
	if r.restarting {
		// Nothing is running; drop what was queued behind the restart.
		r.queue = nil
		r.mu.Unlock()
		return nil
	}
	if !r.busy {
		r.mu.Unlock()
		return nil
	}
	old := r.cur
	old.intentional = true
	sid := r.sessionID
	r.busy = false
	r.dirty = false // the replacement below starts with the current model/mode
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

// SetMode implements agent.ModeSetter. Claude Code takes its permission mode
// as a start-up flag, so a change restarts the process with --resume (see
// apply). Idle: now. Busy: right after the current turn is done.
func (r *run) SetMode(m agent.Mode) error {
	if m == "" {
		m = agent.ModeAsk
	}
	switch m {
	case agent.ModePlan, agent.ModeAsk, agent.ModeYolo:
	default:
		return fmt.Errorf("unknown mode %q", m)
	}
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return errors.New("claude run is closed")
	}
	if r.mode == m {
		r.mu.Unlock()
		return nil
	}
	r.mode = m
	r.applyLocked()
	return nil
}

// SetModel implements agent.ModelSetter; "" selects the CLI default.
func (r *run) SetModel(model string) error {
	model = strings.TrimSpace(model)
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return errors.New("claude run is closed")
	}
	if r.model == model {
		r.mu.Unlock()
		return nil
	}
	r.model = model
	r.applyLocked()
	return nil
}

// SetEffort switches the reasoning effort ("" = the model's default). Like
// SetModel it restarts the process with the new --effort, resuming the
// session, once the turn in progress is done.
func (r *run) SetEffort(effort string) error {
	effort = strings.TrimSpace(effort)
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return errors.New("claude run is closed")
	}
	if r.effort == effort {
		r.mu.Unlock()
		return nil
	}
	r.effort = effort
	r.applyLocked()
	return nil
}

// applyLocked is called with r.mu held after r.mode/r.model changed, and
// releases it. If a turn (or another restart) is in progress the restart is
// deferred; otherwise it starts now.
func (r *run) applyLocked() {
	if r.busy || r.restarting {
		r.dirty = true
		r.mu.Unlock()
		return
	}
	r.restarting = true
	r.mu.Unlock()
	go r.restart()
}

// restart replaces the idle process with one started from the current
// model/mode flags (and --resume of the session, if one exists). It is a
// deliberate restart: it emits no TurnDone / Exited. The caller has set
// r.restarting, so Sends are queued meanwhile and flushed here, in order.
func (r *run) restart() {
	for {
		r.mu.Lock()
		if r.closed {
			r.restarting = false
			r.queue = nil
			r.mu.Unlock()
			return
		}
		old := r.cur
		sid := r.sessionID
		r.dirty = false // spawn reads the flags after this point; later changes set it again
		if old != nil {
			old.intentional = true
		}
		r.mu.Unlock()

		if old != nil {
			killTree(old.cmd)
			old.closeStdin()
			waitProcExit(old)
		}
		if r.a.beforeRespawn != nil {
			r.a.beforeRespawn()
		}
		err := r.spawn(sid)

		r.mu.Lock()
		if err != nil {
			closed := r.closed
			r.restarting = false
			r.queue = nil
			r.mu.Unlock()
			if !closed {
				r.emit(agent.Event{AgentID: r.spec.ID, Time: time.Now(), Kind: agent.KindError, Text: err.Error()})
				r.emit(agent.Event{AgentID: r.spec.ID, Time: time.Now(), Kind: agent.KindExited})
				go r.Close()
			}
			return
		}
		if r.dirty && !r.closed {
			if r.flagsChangedLocked() {
				r.mu.Unlock()
				continue // changed again while restarting
			}
			// Changed again, but before the new process read its flags (a
			// model and an effort picked together): it already runs with
			// them, so a second restart would only cost another cold start.
			r.dirty = false
		}
		r.restarting = false
		q := r.queue
		r.queue = nil
		p := r.cur
		if len(q) > 0 {
			r.busy = true
		}
		r.mu.Unlock()

		for _, data := range q {
			r.sendMu.Lock()
			_, werr := p.stdin.Write(data)
			r.sendMu.Unlock()
			if werr != nil {
				r.emit(agent.Event{AgentID: r.spec.ID, Time: time.Now(), Kind: agent.KindError, Text: fmt.Sprintf("writing to claude: %v", werr)})
				break
			}
		}
		return
	}
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
	if r.settingsPath != "" {
		os.Remove(r.settingsPath)
	}
}

// writeSettings writes the --settings file. Commits and PRs Claude makes in
// Deck credit Deck, and plan files go to ~/.deck/plans, not ~/.claude/plans.
func writeSettings(attribution bool) (string, error) {
	_ = os.MkdirAll(brand.PlansDir(), 0o755)
	settings := map[string]any{"plansDirectory": brand.PlansDir()}
	if attribution {
		settings["attribution"] = map[string]string{"commit": brand.CommitTrailer, "pr": brand.PRFooter}
		settings["includeCoAuthoredBy"] = false
		// The guard's hooks are skipped by --no-verify, so Claude may not use it.
		settings["permissions"] = map[string]any{"deny": []string{
			"Bash(git commit --no-verify:*)", "Bash(git commit -n:*)",
		}}
	}
	data, _ := json.Marshal(settings)
	f, err := os.CreateTemp("", "deck-claude-settings-*.json")
	if err != nil {
		return "", err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(f.Name())
		return "", err
	}
	return f.Name(), f.Close()
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
