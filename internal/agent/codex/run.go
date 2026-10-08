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

	"github.com/dev-dami/deck/internal/agent"
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

	mu       sync.Mutex
	threadID string
	turnID   string
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

	var resp threadResponse
	var err error
	if r.spec.ResumeID != "" {
		err = r.client.Call(ictx, methodThreadResume, threadResumeParams{
			ThreadID:       r.spec.ResumeID,
			Cwd:            r.spec.Dir,
			Model:          r.spec.Model,
			ApprovalPolicy: opts.ApprovalPolicy,
			Sandbox:        opts.Sandbox,
		}, &resp)
		if err != nil {
			return fmt.Errorf("codex: thread/resume: %w", err)
		}
	} else {
		err = r.client.Call(ictx, methodThreadStart, threadStartParams{
			Cwd:            r.spec.Dir,
			Model:          r.spec.Model,
			ApprovalPolicy: opts.ApprovalPolicy,
			Sandbox:        opts.Sandbox,
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
	r.events <- r.tr.sessionStarted(resp.Thread.ID, raw)
	go r.loop()
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
func (r *run) Send(prompt string) error {
	tid := r.SessionID()
	if tid == "" {
		return errNoThread
	}
	ctx, cancel := context.WithTimeout(context.Background(), rpcTimeout)
	defer cancel()
	var resp turnStartResponse
	err := r.client.Call(ctx, methodTurnStart, turnStartParams{
		ThreadID: tid,
		Input:    []userInput{{Type: "text", Text: prompt, TextElements: []any{}}},
	}, &resp)
	if err != nil {
		return err
	}
	if resp.Turn.ID != "" {
		r.mu.Lock()
		r.turnID = resp.Turn.ID
		r.mu.Unlock()
	}
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
