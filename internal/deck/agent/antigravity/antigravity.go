// Package antigravity is the Deck adapter for Google Antigravity's `agy`
// CLI. It launches the user's own installed binary in headless stream-json
// mode (`--input-format stream-json --output-format stream-json`) under the
// user's own login and translates its NDJSON events into agent.Events.
//
// Differences from the Claude Code adapter it is modelled on:
//   - agy has no --mcp-config flag and no permission-prompt hook, so Deck's
//     MCP server is registered with `agy mcp add` on Start and removed with
//     `agy mcp remove` on Close (per-run, best-effort cleanup). Approvals
//     cannot be routed to Deck: in ask mode tools that need approval are
//     denied by the CLI and surface as visible errors.
//   - Model effort is encoded in the model slug (`gemini-3.8-flash-high`),
//     so Model+Effort resolve to one --model slug (see models.go); the
//     --effort flag is never passed.
//   - agy's stream-json input accepts text only: images are rejected.
package antigravity

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
)

type adapter struct {
	binary string

	modelsMu sync.Mutex
	models   []agent.ModelInfo // cached Models() result

	// test hooks
	extraArgs []string
	tap       func(line []byte) // called with every raw stdout line
	modelsFn  func(ctx context.Context) (string, error)
	mcpRunFn  func(ctx context.Context, path string, args ...string) error
}

// New returns the Antigravity adapter. binary may be empty (looks up "agy"
// on PATH).
func New(binary string) agent.Adapter {
	return &adapter{binary: binary}
}

func (a *adapter) Name() string { return "antigravity" }

func (a *adapter) Start(ctx context.Context, spec agent.Spec) (agent.Run, error) {
	bin := spec.Binary
	if bin == "" {
		bin = a.binary
	}
	if bin == "" {
		bin = defaultBinary
	}
	path, err := exec.LookPath(bin)
	if err != nil {
		return nil, fmt.Errorf("agy binary %q not found: %w", bin, err)
	}

	r := &run{
		a:      a,
		spec:   spec,
		path:   path,
		events: make(chan agent.Event, 256),
		done:   make(chan struct{}),
		tr:     newTranslator(spec.ID),
	}
	r.approvals = make(chan agent.ApprovalRequest)
	r.sessionID = spec.ResumeID
	r.model = spec.Model
	r.effort = spec.Effort
	r.slug = a.resolveSlug(ctx, spec.Model, spec.Effort)
	r.mode = spec.Mode
	if r.mode == "" {
		r.mode = agent.ModeAsk
	}

	if spec.MCP != nil {
		if name, file, err := a.mcpAdd(ctx, path, spec.MCP, spec.ID); err != nil {
			r.emit(agent.Event{
				AgentID: spec.ID, Time: time.Now(), Kind: agent.KindError,
				Text: "deck messaging unavailable: " + err.Error(),
			})
		} else {
			r.mcpName, r.mcpFile = name, file
		}
	}

	if err := r.spawn(spec.ResumeID); err != nil {
		r.mcpRemove()
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

// proc is one OS process of `agy`. An interrupt replaces it with a new one.
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
	mcpName   string // `agy mcp` server name holding Deck's MCP server, "" = none
	mcpFile   string // the broker token file that server reads, "" = none

	tr *translator // used by the single active reader goroutine at a time

	mu        sync.Mutex
	cur       *proc
	sessionID string
	busy      bool
	closed    bool
	model     string     // base model id ("" = CLI default)
	effort    string     // low|medium|high ("" = the model's default)
	slug      string     // resolved --model value ("" = omit the flag)
	mode      agent.Mode // current permission mode (never empty)
	firstSend bool       // instructions are prepended to the first prompt
	// dirty: model/mode/effort changed while busy (or while a restart was
	// running); the process is restarted with the new flags once the turn
	// is done.
	dirty bool
	// restarting: a deliberate restart is in flight; Sends are queued in
	// queue and written, in order, to the new process when it is up.
	restarting bool
	queue      [][]byte
	readers    sync.WaitGroup
	sendMu     sync.Mutex
	evMu       sync.RWMutex
	evClosed   bool
}

func (r *run) args(resume string) []string {
	r.mu.Lock()
	slug, mode := r.slug, r.mode
	r.mu.Unlock()
	args := []string{
		"--input-format", "stream-json",
		"--output-format", "stream-json",
	}
	if slug != "" {
		args = append(args, "--model", slug)
	}
	switch mode {
	case agent.ModePlan:
		args = append(args, "--mode", "plan")
	case agent.ModeYolo:
		args = append(args, "--mode", "accept-edits", "--dangerously-skip-permissions")
	}
	if resume != "" {
		args = append(args, "--conversation", resume)
	}
	args = append(args, r.a.extraArgs...)
	return args
}

// spawn starts an agy process and its reader goroutine. Caller must not hold r.mu.
func (r *run) spawn(resume string) error {
	cmd := exec.Command(r.path, r.args(resume)...)
	cmd.Dir = r.spec.Dir
	cmd.Env = os.Environ()
	for k, v := range r.spec.Env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
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
		return errors.New("antigravity run is closed")
	}
	if err := cmd.Start(); err != nil {
		r.mu.Unlock()
		return fmt.Errorf("starting agy: %w", err)
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
}

func (r *run) Events() <-chan agent.Event              { return r.events }
func (r *run) Approvals() <-chan agent.ApprovalRequest { return r.approvals }

func (r *run) SessionID() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.sessionID
}

// userMessage is one stream-json stdin line.
func userMessage(content string) []byte {
	data, _ := json.Marshal(map[string]any{
		"event":   "user",
		"message": map[string]any{"content": content},
	})
	return data
}

// Send sends a text-only prompt. Extra instructions from the spec (user name,
// thread briefing) are prepended to the first prompt: agy has no
// system-prompt flag.
func (r *run) Send(prompt string) error {
	content := prompt
	r.mu.Lock()
	if !r.firstSend {
		r.firstSend = true
		var prefix []string
		if r.spec.TellName && r.spec.UserName != "" {
			prefix = append(prefix, "The user's name is "+r.spec.UserName+".")
		}
		if in := strings.TrimSpace(r.spec.Instructions); in != "" {
			prefix = append(prefix, in)
		}
		if len(prefix) > 0 {
			content = strings.Join(prefix, "\n\n") + "\n\n" + prompt
		}
	}
	if r.closed || r.cur == nil {
		r.mu.Unlock()
		return errors.New("antigravity run is closed")
	}
	if r.restarting {
		// A mode/model restart is in flight: hold the prompt, restart() writes it.
		r.queue = append(r.queue, append(userMessage(content), '\n'))
		r.mu.Unlock()
		r.emit(agent.Event{AgentID: r.spec.ID, Time: time.Now(), Kind: agent.KindUserText, Text: prompt, Raw: userMessage(prompt)})
		return nil
	}
	p := r.cur
	r.busy = true
	r.mu.Unlock()

	data := append(userMessage(content), '\n')
	r.sendMu.Lock()
	_, err := p.stdin.Write(data)
	r.sendMu.Unlock()
	if err != nil {
		return fmt.Errorf("writing to agy: %w", err)
	}
	r.emit(agent.Event{AgentID: r.spec.ID, Time: time.Now(), Kind: agent.KindUserText, Text: prompt, Raw: userMessage(prompt)})
	return nil
}

// Steer implements agent.Steerer. Idle: same as Send. Busy: the current turn
// is interrupted (Interrupt: kill, respawn with --conversation, one
// KindTurnDone "interrupted") and the steer prompt is then delivered to the
// replacement process as the next message. agy's stream-json input has no
// documented way to inject into a running turn, so this is the only
// supported route. agy accepts text blocks only, so images are rejected.
func (r *run) Steer(prompt string, images []agent.Image) error {
	if len(images) > 0 {
		return errors.New("antigravity does not accept images")
	}
	r.mu.Lock()
	busy := r.busy && !r.restarting && !r.closed
	r.mu.Unlock()
	if busy {
		if err := r.Interrupt(); err != nil {
			return err
		}
	}
	return r.Send(prompt)
}

// Interrupt stops the current turn. agy in stream-json mode has no
// documented portable interrupt, so the process tree is killed and a fresh
// process is started with --conversation so the agent stays usable.
func (r *run) Interrupt() error {
	r.mu.Lock()
	if r.closed || r.cur == nil {
		r.mu.Unlock()
		return errors.New("antigravity run is closed")
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

// SetMode implements agent.ModeSetter. agy takes its execution mode as
// start-up flags, so a change restarts the process with --conversation (see
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
		return errors.New("antigravity run is closed")
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
	effort, closed, same := r.effort, r.closed, r.model == model
	r.mu.Unlock()
	if closed {
		return errors.New("antigravity run is closed")
	}
	if same {
		return nil
	}
	slug := r.a.resolveSlug(context.Background(), model, effort)
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return errors.New("antigravity run is closed")
	}
	r.model = model
	r.slug = slug
	if r.effort != effort {
		r.slug = r.a.resolveSlug(context.Background(), r.model, r.effort)
	}
	r.applyLocked()
	return nil
}

// SetEffort switches the reasoning effort ("" = the model's default). The
// effort is encoded in the --model slug (see models.go), so like SetModel it
// restarts the process with the new slug, resuming the session, once the
// turn in progress is done.
func (r *run) SetEffort(effort string) error {
	effort = strings.TrimSpace(effort)
	switch effort {
	case "", "low", "medium", "high":
	default:
		return fmt.Errorf("unknown effort %q", effort)
	}
	r.mu.Lock()
	model, closed, same := r.model, r.closed, r.effort == effort
	r.mu.Unlock()
	if closed {
		return errors.New("antigravity run is closed")
	}
	if same {
		return nil
	}
	slug := r.a.resolveSlug(context.Background(), model, effort)
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return errors.New("antigravity run is closed")
	}
	r.effort = effort
	r.slug = slug
	if r.model != model {
		r.slug = r.a.resolveSlug(context.Background(), r.model, r.effort)
	}
	r.applyLocked()
	return nil
}

// applyLocked is called with r.mu held after r.mode/r.model/r.effort changed,
// and releases it. If a turn (or another restart) is in progress the restart
// is deferred; otherwise it starts now.
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
// model/mode flags (and --conversation of the session, if one exists). It is
// a deliberate restart: it emits no TurnDone / Exited. The caller has set
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
			r.mu.Unlock()
			continue // changed again while restarting
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
				r.emit(agent.Event{AgentID: r.spec.ID, Time: time.Now(), Kind: agent.KindError, Text: fmt.Sprintf("writing to agy: %v", werr)})
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
	r.mcpRemove()
	r.evMu.Lock()
	r.evClosed = true
	close(r.events)
	r.evMu.Unlock()
	return nil
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
