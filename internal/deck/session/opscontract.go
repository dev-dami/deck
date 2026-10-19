package session

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/dev-dami/deck/internal/deck/agent"
	"github.com/dev-dami/deck/internal/deck/store"
)

// Ops contracts on the contractor (docs/peers-design.md §3-4). A teammate
// asks for a report inside the scope this Deck allows it. It runs in a
// thread of its own, in ask mode, with an agent that has none of its CLI's
// own tools (agent.OpsSafe) and only two Deck ops: sh.run, which runs a
// command in the contract's sandbox, and contract.result, which delivers
// the result. The result must fit the contract's schema; the agent's chat
// and the command output stay on this Deck.

// Sandbox runs the commands of one ops contract. The engine puts the
// vsh sandbox behind it.
type Sandbox interface {
	Run(ctx context.Context, script string) (stdout, stderr string, exit int, truncated bool, err error)
}

// NewSandbox makes the sandbox of one ops contract: the commands of the
// presets in scope, limited to targets (empty for all), with dir as its
// scratch folder.
type NewSandbox func(scope, targets []string, dir string) (Sandbox, error)

// SetSandbox installs how ops contracts get their sandbox (the engine
// does). Without it, no ops contract runs.
func (m *Manager) SetSandbox(fn NewSandbox) {
	m.mu.Lock()
	m.sandbox = fn
	m.mu.Unlock()
}

// OpsRun is a teammate's ops contract that this Deck runs.
type OpsRun struct {
	Peer, Title string
	// Brief is the agent's task.
	Brief          string
	Scope, Targets []string
	// TTL is how long it may run; 0 is the config's peers.sandbox.ttl.
	TTL time.Duration
	// Check checks a result against the contract's schema and returns it
	// clean, or why it does not fit.
	Check func(json.RawMessage) (json.RawMessage, error)
	// Audit records an event of the contract; nil records nothing.
	Audit func(event, detail string)
}

// maxOpsTries is how many results that do not fit an agent may deliver
// before its contract fails.
const maxOpsTries = 3

// errOpsOnly refuses every other tool to an ops contract's agent.
var errOpsOnly = errors.New("an ops contract's agent has only sh.run and contract.result")

// opsInstructions replace the thread briefing for an ops contract's agent.
const opsInstructions = "You run an ops contract for a teammate's Deck. Your only tools are Deck's search and execute, and execute has two ops: " +
	"sh.run {cmd} runs one command in a sandbox that allows only the contract's read-only commands, and contract.result {result} delivers the result. " +
	"You cannot read or change files, use the web or use other tools. Only the result goes back to the teammate."

// opsState is the ops contract a thread runs. task, tries and result
// change under the thread's lock.
type opsState struct {
	run    OpsRun
	box    Sandbox
	task   string
	tries  int
	result json.RawMessage
}

// opsOf is the ops contract thread t runs, or nil.
func (t *thread) opsOf() *opsState {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.ops
}

// opsSafe reports whether agents of kind may run an ops contract
// (agent.OpsSafe).
func (m *Manager) opsSafe(kind string) bool {
	s, ok := m.adapters[kind].(agent.OpsSafe)
	return ok && s.OpsSafe()
}

// RunOps readies a teammate's ops contract: a scratch folder, its sandbox,
// a thread there in ask mode, and an agent with no tools of its own that
// gets r.Brief as its task: the default agent when it is ops safe, else
// the first that is. It returns the thread and the task; StartContract
// starts the work. The contract fails when r.TTL passes.
func (m *Manager) RunOps(r OpsRun) (threadID, taskID string, err error) {
	m.mu.Lock()
	newBox := m.sandbox
	m.mu.Unlock()
	kind := m.config().DefaultAgent
	if !m.opsSafe(kind) {
		kind = ""
		for _, k := range m.Kinds() {
			if m.opsSafe(k) {
				kind = k
				break
			}
		}
	}
	switch {
	case m.st == nil:
		return "", "", errors.New("no Deck store to run a contract in")
	case newBox == nil:
		return "", "", errors.New("this Deck has no sandbox for ops contracts")
	case kind == "":
		return "", "", errors.New("no agent on this Deck can run an ops contract: Deck must turn off the agent's own tools, and only claude lets it")
	case r.Check == nil:
		return "", "", errors.New("an ops contract needs a check for its result")
	}
	if r.TTL <= 0 {
		r.TTL, _, _ = m.config().Peers.SandboxLimits()
	}
	if r.Audit == nil {
		r.Audit = func(string, string) {}
	}
	parent := filepath.Join(filepath.Dir(m.st.Dir()), "contracts")
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return "", "", err
	}
	dir, err := os.MkdirTemp(parent, r.Peer+"-ops-")
	if err != nil {
		return "", "", err
	}
	box, err := newBox(r.Scope, r.Targets, dir)
	if err != nil {
		_ = os.RemoveAll(dir)
		return "", "", fmt.Errorf("no sandbox for the contract: %w", err)
	}
	tid, err := m.NewThread(dir)
	if err != nil {
		return "", "", err
	}
	t := m.thread(tid)
	o := &opsState{run: r, box: box}
	t.mu.Lock()
	t.contract, t.ops, t.mode = r.Peer, o, agent.ModeAsk
	t.mu.Unlock()
	_ = m.RenameThread(tid, r.Peer+": "+r.Title)
	aid, err := m.AddMember(tid, kind, "")
	if err != nil {
		return tid, "", err
	}
	brief := r.Brief + "\n\n[deck] This is an ops contract from teammate " + r.Peer + ", which this Deck allows. " +
		"Use sh.run to find the facts, then contract.result to deliver them. Only the result goes back to " + r.Peer + "."
	tk, err := m.addTask(t, TaskSpec{Title: r.Title, Brief: brief, Owner: aid, MaxAttempts: 1})
	if err != nil {
		return tid, "", err
	}
	t.mu.Lock()
	o.task = tk.ID
	t.mu.Unlock()
	time.AfterFunc(r.TTL, func() { m.failOps(t, tk.ID, fmt.Sprintf("it ran out of its time (%s)", r.TTL)) })
	return tid, tk.ID, nil
}

// OpsResult is the result the agent of the ops contract on thread threadID
// delivered, checked; false when there is none.
func (m *Manager) OpsResult(threadID string) (json.RawMessage, bool) {
	t := m.thread(threadID)
	if t == nil {
		return nil, false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.ops == nil || t.ops.result == nil {
		return nil, false
	}
	return t.ops.result, true
}

// OpsRestarted is why an ops contract that ran when Deck stopped failed.
const OpsRestarted = "Deck restarted while it ran"

// failOps fails the ops contract task taskID with why and stops its
// agent. A task that no longer runs stays as it is.
func (m *Manager) failOps(t *thread, taskID, why string) {
	if m.isClosed() {
		return
	}
	if tk, ok := m.Task(t.id, taskID); !ok || tk.Status != store.TaskRunning {
		return
	}
	tk, err := m.moveTask(t, taskID, store.TaskFailed, "", why, nil)
	if err != nil {
		return
	}
	m.emit(NoticeMsg{ThreadID: t.id, Text: why})
	if tk.Owner != "" {
		// Not here: failOps can run inside the agent's own tool call, which
		// an interrupt waits for.
		go func() { _ = m.Interrupt(tk.Owner) }()
	}
}

// opsBoard is an ops contract as its agent's sh.run and contract.result
// see it (hub.OpsRunner).
type opsBoard struct {
	m *Manager
	t *thread
	o *opsState
}

// task is the contract's task, when it still runs.
func (b opsBoard) task() (string, error) {
	b.t.mu.Lock()
	id := b.o.task
	b.t.mu.Unlock()
	if tk, ok := b.m.Task(b.t.id, id); !ok || tk.Status != store.TaskRunning {
		return "", errors.New("this contract is over")
	}
	return id, nil
}

// ShRun implements hub.OpsRunner: it runs cmd in the contract's sandbox.
func (b opsBoard) ShRun(ctx context.Context, cmd string) (string, error) {
	if _, err := b.task(); err != nil {
		return "", err
	}
	stdout, stderr, exit, truncated, err := b.o.box.Run(ctx, cmd)
	if err != nil {
		b.o.run.Audit("sh.run", "error "+err.Error()+": "+cmd)
		return "", err
	}
	b.o.run.Audit("sh.run", fmt.Sprintf("exit %d: %s", exit, cmd))
	var out strings.Builder
	out.WriteString(stdout)
	if stderr != "" {
		out.WriteString("\n[stderr]\n" + stderr)
	}
	fmt.Fprintf(&out, "\n[exit %d]", exit)
	if truncated {
		out.WriteString("\n[the output was cut at the contract's limit]")
	}
	return out.String(), nil
}

// Deliver implements hub.OpsRunner: a result that fits the schema ends the
// task; one that does not goes back with why, until maxOpsTries.
func (b opsBoard) Deliver(raw json.RawMessage) (string, error) {
	id, err := b.task()
	if err != nil {
		return "", err
	}
	res, err := b.o.run.Check(raw)
	if err != nil {
		b.t.mu.Lock()
		b.o.tries++
		n := b.o.tries
		b.t.mu.Unlock()
		b.o.run.Audit("result refused", err.Error())
		if n >= maxOpsTries {
			b.m.failOps(b.t, id, fmt.Sprintf("its result did not fit the contract %d times: %v", n, err))
			return "", fmt.Errorf("the result does not fit the contract: %v. That was try %d of %d, so the contract failed", err, n, maxOpsTries)
		}
		return "", fmt.Errorf("the result does not fit the contract: %v. Fix it and call contract.result again (try %d of %d)", err, n, maxOpsTries)
	}
	b.t.mu.Lock()
	b.o.result = res
	b.t.mu.Unlock()
	if _, err := b.m.moveTask(b.t, id, store.TaskDone, "", fmt.Sprintf("delivered its result (%d bytes)", len(res)), nil); err != nil {
		return "", err
	}
	return "delivered: Deck sends it to " + b.o.run.Peer + ". End your turn now.", nil
}
