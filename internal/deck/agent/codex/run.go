package codex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"sync"
	"time"

	"github.com/dev-dami/deck/internal/deck/agent"
)

// run is one live `codex app-server` process bound to one thread.
type run struct {
	spec   agent.Spec
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stderr *tailBuffer
	client *Client
	tr     *translator

	events    chan agent.Event
	approvals chan agent.ApprovalRequest
	pushers   sync.WaitGroup // goroutines that may still send on approvals

	closing   chan struct{}
	closeOnce sync.Once
	loopDone  chan struct{}

	opts Options

	mu       sync.Mutex
	threadID string
	turnID   string

	// mode is the current permission mode. pendingMode / pendingModel /
	// pendingEffort hold a SetMode / SetModel / SetEffort change (or the
	// spec's effort) that the next turn/start carries as overrides.
	mode          agent.Mode
	pendingMode   agent.Mode
	pendingModel  string
	pendingEffort string

	// doneTurns holds ids of turns that already completed.
	doneTurns map[string]bool
}

func (r *run) Events() <-chan agent.Event              { return r.events }
func (r *run) Approvals() <-chan agent.ApprovalRequest { return r.approvals }

func (r *run) SessionID() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.threadID
}

func (r *run) setTurn(id string) {
	r.mu.Lock()
	r.turnID = id
	r.mu.Unlock()
}

// handshake performs initialize -> initialized -> thread/start|resume and
// starts the event loop.
func (r *run) handshake(ctx context.Context, opts Options) error {
	ictx, cancel := context.WithTimeout(ctx, rpcTimeout)
	defer cancel()

	init := initializeParams{ClientInfo: clientInfo{Name: "deck", Title: "Deck", Version: opts.ClientVersion}}
	if err := r.client.Call(ictx, methodInitialize, init, nil); err != nil {
		return fmt.Errorf("codex: initialize: %w", err)
	}
	if err := r.client.Notify(notifyInitialized, nil); err != nil {
		return fmt.Errorf("codex: initialized: %w", err)
	}

	ms, err := opts.settingsFor(r.spec.Mode)
	if err != nil {
		return err
	}
	r.mode = r.spec.Mode
	if r.mode == "" {
		r.mode = agent.ModeAsk
	}

	var resp threadResponse
	if r.spec.ResumeID != "" {
		err = r.client.Call(ictx, methodThreadResume, threadResumeParams{
			ThreadID:       r.spec.ResumeID,
			Cwd:            r.spec.Dir,
			Model:          r.spec.Model,
			ApprovalPolicy: ms.approval,
			Sandbox:        ms.sandbox,

			Config:                mcpThreadConfig(r.spec.MCP),
			DeveloperInstructions: r.spec.Instructions,
		}, &resp)
		if err != nil {
			return fmt.Errorf("codex: thread/resume: %w", err)
		}
	} else {
		err = r.client.Call(ictx, methodThreadStart, threadStartParams{
			Cwd:            r.spec.Dir,
			Model:          r.spec.Model,
			ApprovalPolicy: ms.approval,
			Sandbox:        ms.sandbox,

			Config:                mcpThreadConfig(r.spec.MCP),
			DeveloperInstructions: r.spec.Instructions,
		}, &resp)
		if err != nil {
			return fmt.Errorf("codex: thread/start: %w", err)
		}
	}
	if resp.Thread.ID == "" {
		return errors.New("codex: thread response carried no thread id")
	}
	r.mu.Lock()
	r.threadID = resp.Thread.ID
	r.mu.Unlock()

	raw, _ := json.Marshal(map[string]any{"threadId": resp.Thread.ID, "resumed": r.spec.ResumeID != ""})
	r.events <- r.tr.sessionStarted(resp.Thread.ID, resp.Model, raw)
	r.pushers.Add(1) // before the loop, which waits for pushers when it ends
	go r.loop()
	go r.probeLimits()
	return nil
}

func (r *run) emit(e agent.Event) {
	select {
	case r.events <- e:
	case <-r.closing:
	}
}

// loop consumes everything the server sends until the process goes away.
func (r *run) loop() {
	defer close(r.loopDone)
	for m := range r.client.Incoming() {
		switch {
		case m.IsRequest():
			r.handleServerRequest(m)
		case m.IsNotification():
			for _, e := range r.tr.notification(m.Method, m.Params, m.Raw) {
				r.emit(e)
			}
		}
	}

	// The stream ended: the process is gone (or was killed).
	r.kill()
	exit := "exited"
	if err := r.cmd.Wait(); err != nil {
		exit = err.Error()
	}
	if tail := r.stderr.String(); tail != "" {
		exit += ": " + tail
	}
	e := agent.Event{AgentID: r.spec.ID, Time: time.Now(), Kind: agent.KindExited, Text: exit}
	// Deliver the final event even if nobody is draining, unless Close was called.
	select {
	case r.events <- e:
	case <-r.closing:
		select {
		case r.events <- e:
		default:
		}
	}
	r.pushers.Wait()
	close(r.approvals)
	close(r.events)
}

// Send starts a new turn with the prompt.
func (r *run) Send(prompt string) error { return r.SendWithImages(prompt, nil) }

// SendWithImages starts a new turn whose input is the images (as localImage
// items) followed by the prompt text.
func (r *run) SendWithImages(prompt string, images []agent.Image) error {
	tid := r.SessionID()
	if tid == "" {
		return errNoThread
	}
	imgs, err := imageInputs(images)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), rpcTimeout)
	defer cancel()
	r.mu.Lock()
	mode, pMode, pModel, pEffort := r.mode, r.pendingMode, r.pendingModel, r.pendingEffort
	r.mu.Unlock()

	params := turnStartParams{ThreadID: tid}
	if mode == agent.ModePlan {
		params.Input = append(params.Input, userInput{Type: "text", Text: planNote, TextElements: []any{}})
	}
	params.Input = append(params.Input, imgs...)
	if prompt != "" || len(imgs) == 0 {
		params.Input = append(params.Input, userInput{Type: "text", Text: prompt, TextElements: []any{}})
	}
	if pMode != "" {
		ms, err := r.opts.settingsFor(pMode)
		if err != nil {
			return err
		}
		params.ApprovalPolicy = ms.approval
		params.SandboxPolicy = sandboxPolicyFor(ms.sandbox)
	}
	params.Model = pModel
	params.Effort = pEffort

	var resp turnStartResponse
	if err := r.client.Call(ctx, methodTurnStart, params, &resp); err != nil {
		return err
	}
	// The overrides now stick for this and later turns; drop them unless a
	// newer SetMode / SetModel replaced them while the call was in flight.
	r.mu.Lock()
	if r.pendingMode == pMode {
		r.pendingMode = ""
	}
	if r.pendingModel == pModel {
		r.pendingModel = ""
	}
	if r.pendingEffort == pEffort {
		r.pendingEffort = ""
	}
	r.mu.Unlock()
	r.mu.Lock()
	// A very short turn can complete before this response is handled.
	if resp.Turn.ID != "" && !r.doneTurns[resp.Turn.ID] {
		r.turnID = resp.Turn.ID
	}
	r.mu.Unlock()
	return nil
}

// Interrupt asks the server to stop the running turn. It is a no-op when idle.
func (r *run) Interrupt() error {
	r.mu.Lock()
	tid, turn := r.threadID, r.turnID
	r.mu.Unlock()
	if tid == "" || turn == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), rpcTimeout)
	defer cancel()
	return r.client.Call(ctx, methodTurnInterrupt, turnInterruptParams{ThreadID: tid, TurnID: turn}, nil)
}

// Close kills the process tree. Safe to call more than once.
func (r *run) Close() error {
	r.closeOnce.Do(func() { close(r.closing) })
	r.kill()
	return nil
}

func (r *run) kill() {
	_ = r.stdin.Close()
	killTree(r.cmd)
}

// watchContext ties the process lifetime to the context passed to Start.
func (r *run) watchContext(ctx context.Context) {
	select {
	case <-ctx.Done():
		r.Close()
	case <-r.loopDone:
	}
}

// SetMode switches the permission mode. It is sent as approvalPolicy and
// sandboxPolicy overrides on the next turn/start; a running turn is unaffected.
func (r *run) SetMode(m agent.Mode) error {
	if _, err := r.opts.settingsFor(m); err != nil {
		return err
	}
	if m == "" {
		m = agent.ModeAsk
	}
	r.mu.Lock()
	r.mode = m
	r.pendingMode = m
	r.mu.Unlock()
	return nil
}

// SetModel switches the model, sent as the `model` override on the next
// turn/start; a running turn is unaffected.
func (r *run) SetModel(model string) error {
	if model == "" {
		return errors.New("codex: empty model")
	}
	r.mu.Lock()
	r.pendingModel = model
	r.mu.Unlock()
	return nil
}

// SetEffort switches the reasoning effort, sent as the `effort` override on
// the next turn/start. Codex has no "back to the default" override, so a level
// is required (the picker offers the model's default level by name).
func (r *run) SetEffort(effort string) error {
	if effort == "" {
		return errors.New("codex: pick an effort level")
	}
	r.mu.Lock()
	r.pendingEffort = effort
	r.mu.Unlock()
	return nil
}

var (
	_ agent.ModeSetter   = (*run)(nil)
	_ agent.ModelSetter  = (*run)(nil)
	_ agent.EffortSetter = (*run)(nil)
)
