// Package fake is a scripted agent.Adapter. It plays back a fixed Script of
// events so the TUI can be driven and tested without launching a real CLI or
// spending tokens.
package fake

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dev-dami/deck/internal/deck/agent"
)

// ErrBusy is returned by Send while a turn is still playing back.
var ErrBusy = errors.New("fake: agent is busy")

// ErrClosed is returned by Send after the run has been closed.
var ErrClosed = errors.New("fake: run is closed")

// Step is one scripted moment of playback.
//
// Playback waits Delay (scaled by the adapter speed), then emits Event when its
// Kind is set. If Approval is set, Event (if any) is emitted first, then the
// approval is raised on Approvals() and playback blocks until Respond is called.
// Denying an approval emits a tool_result with Status error and Output "denied",
// then a turn_done; the rest of the script is abandoned.
//
// Message, when set, makes the agent call Deck's execute tool with op
// thread.send after the Event is emitted: a tool_call event (named "message",
// as the real adapters render it), then the message is handed to the Hub (only
// To and Text are used; the sender is the agent itself), playback waits for
// the Hub's answer, and a tool_result event carries it. ReadThread does the
// same for op thread.read (a "read" tool), and Exec for any other op (a "deck"
// tool, answered by the Hub's executor). All need a Hub (see NewTurns /
// NewHub); without one the tool_result is an error.
type Step struct {
	Delay      time.Duration
	Event      agent.Event
	Approval   *agent.ApprovalRequest
	Message    *agent.Message
	ReadThread bool
	Exec       *Exec
}

// Exec is one execute call a fake agent makes.
type Exec struct {
	Op   string
	Args json.RawMessage
}

// Script is an ordered list of steps. A turn ends when a step emits
// agent.KindTurnDone.
type Script []Step

// New returns a fake adapter that plays script on Start. Send replays the same
// script with any session_started steps removed, so a follow-up turn does not
// announce a new session. speed scales every Delay: 1 is real time, 2 is twice
// as fast, and 0 (or any value <= 0) plays instantly.
func New(script Script, speed float64) agent.Adapter {
	return NewWithReply(script, withoutSessionStart(script), speed)
}

// NewWithReply is like New but uses reply for every Send instead of the
// derived script.
func NewWithReply(start, reply Script, speed float64) agent.Adapter {
	return &adapter{start: start, reply: reply, speed: speed}
}

// NewTurns returns a fake adapter that plays one script per turn, in order: the
// first turn is the first script, the second turn the second, and so on. After
// the scripts run out every turn is a short acknowledgement. Unlike New, a run
// started without a FirstPrompt stays idle until Send, which is how Deck starts
// members of a shared thread. hub (may be nil) serves Step.Message and
// Step.ReadThread. speed is as in New.
func NewTurns(speed float64, hub *Hub, turns ...Script) agent.Adapter {
	return &adapter{turns: turns, perTurn: true, hub: hub, speed: speed}
}

type adapter struct {
	start   Script
	reply   Script
	speed   float64
	turns   []Script
	perTurn bool
	hub     *Hub
	models  []agent.ModelInfo // nil = ClaudeModels
}

// Name returns "fake".
func (a *adapter) Name() string { return "fake" }

// Start begins a run and plays the start script on a goroutine. If
// spec.FirstPrompt is set it is echoed as a user_text event first. The run is
// closed when ctx is done.
func (a *adapter) Start(ctx context.Context, spec agent.Spec) (agent.Run, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	id := spec.ID
	if id == "" {
		id = "fake"
	}
	r := &run{
		agentID:   id,
		dir:       spec.Dir,
		speed:     a.speed,
		reply:     a.reply,
		events:    make(chan agent.Event, 1024),
		approvals: make(chan agent.ApprovalRequest, 16),
		closed:    make(chan struct{}),
		hub:       a.hub,
		turns:     a.turns,
		perTurn:   a.perTurn,
		sessionID: spec.ResumeID,
		mode:      spec.Mode,
		model:     spec.Model,
		effort:    spec.Effort,
	}
	if spec.ResumeID != "" {
		r.next = len(a.turns) // a resumed conversation is past its scripted opening
	}

	if a.perTurn && spec.FirstPrompt == "" {
		go func() {
			select {
			case <-ctx.Done():
				_ = r.Close()
			case <-r.closed:
			}
		}()
		return r, nil
	}

	r.mu.Lock()
	t := r.newTurnLocked()
	script := a.start
	if a.perTurn {
		script = r.nextTurnLocked()
	}
	r.mu.Unlock()

	if spec.FirstPrompt != "" {
		r.emit(nil, agent.Event{Kind: agent.KindUserText, Text: spec.FirstPrompt})
	}
	go r.play(t, script)

	go func() {
		select {
		case <-ctx.Done():
			_ = r.Close()
		case <-r.closed:
		}
	}()
	return r, nil
}

type turn struct {
	cancel chan struct{}
	done   chan struct{}
	once   sync.Once
	// silent is set when a Steer replaces the turn: it ends without turn_done,
	// because to the caller the steered turn is still the same turn.
	silent atomic.Bool
}

func (t *turn) stop() { t.once.Do(func() { close(t.cancel) }) }

type run struct {
	agentID string
	dir     string
	speed   float64
	reply   Script

	events    chan agent.Event
	approvals chan agent.ApprovalRequest
	closed    chan struct{}

	hub     *Hub
	turns   []Script
	perTurn bool
	next    int // index of the next scripted turn (perTurn only)

	mu        sync.Mutex
	shut      bool // Close has begun
	sessionID string
	cur       *turn      // the turn currently playing, nil when idle
	mode      agent.Mode // last permission mode set (starts as Spec.Mode)
	effort    string     // last effort set (starts as Spec.Effort)
	model     string     // last model set (starts as Spec.Model)
	modes     []agent.Mode
	models    []string
	images    []PromptCall
	steers    []PromptCall
	ctx       int // context tokens reported so far (grows with every turn)
	wg        sync.WaitGroup
}

var (
	idCounter      int64
	sessionCounter int64
)

// Events returns the stream of normalized events. It is closed after
// KindExited when the run is closed.
func (r *run) Events() <-chan agent.Event { return r.events }

// Approvals returns approval requests. Each must be answered with Respond.
// The channel is closed when the run is closed.
func (r *run) Approvals() <-chan agent.ApprovalRequest { return r.approvals }

// SessionID returns the session id from the most recent session_started event.
func (r *run) SessionID() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.sessionID
}

// Send emits a user_text event and then replays the reply script. It returns
// ErrBusy if a turn is still playing and ErrClosed after Close.
func (r *run) Send(prompt string) error {
	r.mu.Lock()
	if r.shut {
		r.mu.Unlock()
		return ErrClosed
	}
	if r.cur != nil {
		r.mu.Unlock()
		return ErrBusy
	}
	t := r.newTurnLocked()
	script := r.reply
	if r.perTurn {
		script = r.nextTurnLocked()
	}
	r.mu.Unlock()

	r.emit(nil, agent.Event{Kind: agent.KindUserText, Text: prompt})
	go r.play(t, script)
	return nil
}

// nextTurnLocked returns the script for the next turn of a NewTurns run.
func (r *run) nextTurnLocked() Script {
	i := r.next
	r.next++
	if i < len(r.turns) {
		return r.turns[i]
	}
	return Script{
		{Event: agent.Event{Kind: agent.KindAssistantText, Text: "Noted."}},
		{Event: agent.Event{Kind: agent.KindUsage, Usage: &agent.Usage{
			InputTokens: 100, OutputTokens: 5, CostUSD: 0.01,
			ContextTokens: r.growContextLocked(), ContextWindow: contextWindowFor(r.agentID),
		}}},
		{Event: agent.Event{Kind: agent.KindTurnDone}},
	}
}

// Interrupt stops the current turn, if any. The playback goroutine emits
// KindTurnDone before Interrupt returns. It is a no-op when the agent is idle.
func (r *run) Interrupt() error {
	r.mu.Lock()
	t := r.cur
	r.mu.Unlock()
	if t == nil {
		return nil
	}
	t.stop()
	<-t.done
	return nil
}

// Close stops any playing turn, emits KindExited, and closes the event and
// approval channels. It is safe to call more than once.
func (r *run) Close() error {
	r.mu.Lock()
	if r.shut {
		r.mu.Unlock()
		return nil
	}
	r.shut = true
	close(r.closed)
	t := r.cur
	r.mu.Unlock()

	if t != nil {
		t.stop()
	}
	r.wg.Wait()

	select {
	case r.events <- agent.Event{AgentID: r.agentID, Time: time.Now(), Kind: agent.KindExited}:
	case <-time.After(time.Second):
	}
	close(r.events)
	close(r.approvals)
	return nil
}

// newTurnLocked registers a new turn and reserves a goroutine slot for it.
// The caller must hold r.mu and must start the goroutine with play.
func (r *run) newTurnLocked() *turn {
	t := &turn{cancel: make(chan struct{}), done: make(chan struct{})}
	r.cur = t
	r.wg.Add(1)
	return t
}

func (r *run) play(t *turn, script Script) {
	defer r.wg.Done()
	defer close(t.done)
	defer r.clearTurn(t)

	for _, st := range script {
		if !wait(t.cancel, r.scale(st.Delay)) {
			r.endTurn(t)
			return
		}
		if st.Event.Kind == agent.KindTurnDone {
			r.endTurn(t)
			return
		}
		if st.Event.Kind != "" && !r.emit(t.cancel, st.Event) {
			r.endTurn(t)
			return
		}
		if st.Message != nil && !r.callHub(t, "message", st.Message.To, st.Message.Text, nil, st.Message) {
			r.endTurn(t)
			return
		}
		if st.ReadThread && !r.callHub(t, "read", "", "", nil, nil) {
			r.endTurn(t)
			return
		}
		if st.Exec != nil && !r.callHub(t, "deck", "", "", st.Exec, nil) {
			r.endTurn(t)
			return
		}
		if st.Approval == nil {
			continue
		}

		req := *st.Approval
		req.AgentID = r.agentID
		if req.ID == "" {
			req.ID = fmt.Sprintf("fake-approval-%d", atomic.AddInt64(&idCounter, 1))
		}
		if req.Cwd == "" {
			req.Cwd = r.dir
		}
		decided := make(chan agent.Decision, 1)
		var once sync.Once
		req.Respond = func(d agent.Decision) {
			once.Do(func() { decided <- d })
		}

		select {
		case r.approvals <- req:
		case <-t.cancel:
			r.endTurn(t)
			return
		case <-r.closed:
			return
		}

		var d agent.Decision
		select {
		case d = <-decided:
		case <-t.cancel:
			r.endTurn(t)
			return
		case <-r.closed:
			return
		}
		if !d.Allow {
			toolID := req.ID
			if st.Event.Tool != nil {
				toolID = st.Event.Tool.ID
			}
			r.emit(t.cancel, agent.Event{
				Kind: agent.KindToolResult,
				Text: "denied",
				Tool: &agent.Tool{
					ID:     toolID,
					Name:   req.ToolName,
					Title:  req.Summary,
					Output: "denied",
					Status: agent.ToolError,
				},
			})
			r.endTurn(t)
			return
		}
	}
	// A script that runs out without turn_done simply goes idle; the deferred
	// clearTurn lets the next Send start a new turn.
}

// callHub plays one call of Deck's execute tool, under the name the real
// adapters show ("message", "read" or "deck"): tool_call, the hub round trip,
// tool_result. It reports false when the turn was cancelled meanwhile.
func (r *run) callHub(t *turn, name, to, text string, ex *Exec, msg *agent.Message) bool {
	id := fmt.Sprintf("fake-%s-%d", name, atomic.AddInt64(&idCounter, 1))
	var input json.RawMessage
	var title string
	switch {
	case name == "message":
		title = "to " + to + ": " + oneLine(text)
		args := map[string]any{"to": to, "message": text}
		if msg != nil {
			for k, v := range map[string]string{"task": msg.Task, "new_task": msg.NewTask, "expect": msg.Expect, "done_when": msg.DoneWhen} {
				if v != "" {
					args[k] = v
				}
			}
			if msg.ExplicitDone {
				args["explicit_done"] = true
			}
		}
		input, _ = json.Marshal(map[string]any{"op": agent.OpThreadSend, "args": args})
	case name == "read":
		title = "shared thread"
		input = json.RawMessage(`{"op":"` + agent.OpThreadRead + `"}`)
	case ex != nil:
		input, _ = json.Marshal(agent.ExecInput{Op: ex.Op, Args: ex.Args})
		_, title, _ = agent.DeckToolDisplay(agent.DeckToolExecute, input)
	}
	if !r.emit(t.cancel, agent.Event{Kind: agent.KindToolCall, Tool: &agent.Tool{
		ID: id, Name: name, Title: title, Input: input, Status: agent.ToolRunning,
	}}) {
		return false
	}
	var out string
	var err error
	switch {
	case r.hub == nil:
		err = errors.New("no hub")
	case name == "read":
		out = r.hub.ReadThread(r.agentID)
	case ex != nil:
		var isErr bool
		out, isErr = r.hub.Exec(r.agentID, ex.Op, ex.Args)
		if isErr {
			err = errors.New(out)
		}
	default:
		if msg != nil {
			m := *msg
			m.From = r.agentID
			out, err = r.hub.SendMessage(t.cancel, m)
		} else {
			out, err = r.hub.Send(t.cancel, r.agentID, to, text)
		}
	}
	select {
	case <-t.cancel:
		return false
	default:
	}
	status := agent.ToolOK
	if err != nil {
		status, out = agent.ToolError, err.Error()
	}
	return r.emit(t.cancel, agent.Event{Kind: agent.KindToolResult, Text: out, Tool: &agent.Tool{
		ID: id, Name: name, Title: title, Input: input, Output: out, Status: status,
	}})
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

// endTurn clears the active turn before emitting turn_done, so a consumer that
// reacts to turn_done by calling Send does not race with the playback
// goroutine.
func (r *run) endTurn(t *turn) {
	r.clearTurn(t)
	if t.silent.Load() {
		return
	}
	r.emit(nil, agent.Event{Kind: agent.KindTurnDone})
}

func (r *run) clearTurn(t *turn) {
	r.mu.Lock()
	if r.cur == t {
		r.cur = nil
	}
	r.mu.Unlock()
}

// emit sends ev unless stop or the run is closed first. It stamps AgentID and
// Time, and records the session id from session_started events.
func (r *run) emit(stop <-chan struct{}, ev agent.Event) bool {
	ev.AgentID = r.agentID
	if ev.Time.IsZero() {
		ev.Time = time.Now()
	}
	if ev.Kind == agent.KindSessionStarted {
		if ev.SessionID == "" {
			ev.SessionID = fmt.Sprintf("fake-session-%d", atomic.AddInt64(&sessionCounter, 1))
		}
		r.mu.Lock()
		r.sessionID = ev.SessionID
		r.mu.Unlock()
	}

	select {
	case <-stop:
		return false
	case <-r.closed:
		return false
	default:
	}
	select {
	case r.events <- ev:
		return true
	case <-stop:
		return false
	case <-r.closed:
		return false
	}
}

func (r *run) scale(d time.Duration) time.Duration {
	if r.speed <= 0 {
		return 0
	}
	return time.Duration(float64(d) / r.speed)
}

// wait sleeps for d and reports whether it completed (false if stop closed first).
func wait(stop <-chan struct{}, d time.Duration) bool {
	if d <= 0 {
		select {
		case <-stop:
			return false
		default:
			return true
		}
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-stop:
		return false
	}
}

func withoutSessionStart(script Script) Script {
	out := make(Script, 0, len(script))
	for _, st := range script {
		if st.Event.Kind == agent.KindSessionStarted {
			continue
		}
		out = append(out, st)
	}
	return out
}

// DemoScript returns a named demo. "codex" returns DemoCodexScript; any other
// name returns the Claude demo.
func DemoScript(name string) Script {
	if name == "codex" {
		return DemoCodexScript()
	}
	return claudeDemo()
}

// streamed splits a paragraph into the chunks a model would stream.
var streamedReply = []string{
	"I'll check the store layer before changing anything. ",
	"`Append` currently writes every event, so streaming ",
	"deltas bloat the thread files. Here is the plan:\n\n",
	"- read `store.go` to see the current layout\n",
	"- drop streaming deltas before they reach disk\n",
	"- run the package tests\n",
}

func streamSteps(chunks []string, delay time.Duration) []Step {
	steps := make([]Step, 0, len(chunks))
	for _, c := range chunks {
		steps = append(steps, Step{
			Delay: delay,
			Event: agent.Event{Kind: agent.KindAssistantText, Text: c, Delta: true},
		})
	}
	return steps
}

func claudeDemo() Script {
	diff := storeDiff()
	editInput := []byte(`{"file_path":"internal/store/store.go","edits":2}`)
	readInput := []byte(`{"file_path":"internal/store/store.go"}`)
	bashInput := []byte(`{"command":"go test ./..."}`)

	var s Script
	s = append(s, Step{Event: agent.Event{Kind: agent.KindSessionStarted, SessionID: "fake-demo-claude"}})
	s = append(s, contextStep(claudeWindow, 14200))
	s = append(s, streamSteps(streamedReply, 80*time.Millisecond)...)
	s = append(s, Step{Event: agent.Event{
		Kind: agent.KindAssistantText,
		Text: strings.Join(streamedReply, ""),
	}})

	s = append(s,
		Step{Delay: 300 * time.Millisecond, Event: agent.Event{Kind: agent.KindToolCall, Tool: &agent.Tool{
			ID: "toolu_read", Name: "Read", Title: "internal/store/store.go", Input: readInput, Status: agent.ToolRunning,
		}}},
		Step{Delay: 400 * time.Millisecond, Event: agent.Event{Kind: agent.KindToolResult, Tool: &agent.Tool{
			ID: "toolu_read", Name: "Read", Title: "internal/store/store.go", Input: readInput,
			Output: "148 lines read", Status: agent.ToolOK,
		}}},
		contextStep(claudeWindow, 31800),
	)

	s = append(s,
		Step{
			Delay: 500 * time.Millisecond,
			Event: agent.Event{Kind: agent.KindToolCall, Tool: &agent.Tool{
				ID: "toolu_edit", Name: "Edit", Title: "internal/store/store.go", Input: editInput, Status: agent.ToolRunning,
			}},
			Approval: &agent.ApprovalRequest{
				ID:       "fake-approval-edit",
				Kind:     agent.ApproveEdit,
				ToolName: "Edit",
				Summary:  "internal/store/store.go",
				Diff:     diff,
			},
		},
		Step{Delay: 100 * time.Millisecond, Event: agent.Event{Kind: agent.KindFileChange, Diff: diff}},
		Step{Delay: 150 * time.Millisecond, Event: agent.Event{Kind: agent.KindToolResult, Tool: &agent.Tool{
			ID: "toolu_edit", Name: "Edit", Title: "internal/store/store.go", Input: editInput,
			Output: "Applied 2 hunks to internal/store/store.go", Status: agent.ToolOK,
		}}},
		contextStep(claudeWindow, 52400),
	)

	s = append(s,
		Step{Delay: 300 * time.Millisecond, Event: agent.Event{Kind: agent.KindToolCall, Tool: &agent.Tool{
			ID: "toolu_bash", Name: "Bash", Title: "go test ./...", Input: bashInput, Status: agent.ToolRunning,
		}}},
		Step{Delay: 900 * time.Millisecond, Event: agent.Event{Kind: agent.KindToolResult, Tool: &agent.Tool{
			ID: "toolu_bash", Name: "Bash", Title: "go test ./...", Input: bashInput,
			Output: "ok  \tgithub.com/dev-dami/deck/internal/deck/store\t0.41s\n" +
				"?   \tgithub.com/dev-dami/deck/internal/deck/agent\t[no test files]\n" +
				"ok  \tgithub.com/dev-dami/deck/internal/deck/config\t0.12s\n" +
				"PASS",
			Status: agent.ToolOK,
		}}},
	)

	s = append(s,
		Step{Delay: 200 * time.Millisecond, Event: agent.Event{Kind: agent.KindAssistantText,
			Text: "The store now drops streaming deltas, and `go test ./...` passes."}},
		Step{Event: agent.Event{Kind: agent.KindUsage, Usage: &agent.Usage{
			InputTokens: 18432, OutputTokens: 1260, CachedTokens: 12010, CostUSD: 0.12,
			ContextTokens: 61800, ContextWindow: claudeWindow,
		}}},
		Step{Event: agent.Event{Kind: agent.KindTurnDone}},
	)
	return s
}

// DemoCodexScript returns a Codex-style turn that asks to run a shell command.
func DemoCodexScript() Script {
	cmd := "rm -rf dist && npm run build"
	input := []byte(`{"command":"rm -rf dist && npm run build"}`)

	var s Script
	s = append(s, Step{Event: agent.Event{Kind: agent.KindSessionStarted, SessionID: "fake-demo-codex"}})
	s = append(s, contextStep(codexWindow, 9600))
	pre := []string{
		"I'll rebuild `dist` from a clean slate. ",
		"The old output is stale, so I'm removing it first.",
	}
	s = append(s, streamSteps(pre, 100*time.Millisecond)...)
	s = append(s, Step{Event: agent.Event{Kind: agent.KindAssistantText, Text: strings.Join(pre, "")}})
	s = append(s, Step{
		Delay: 200 * time.Millisecond,
		Event: agent.Event{Kind: agent.KindToolCall, Tool: &agent.Tool{
			ID: "item_cmd", Name: "command_execution", Title: cmd, Input: input, Status: agent.ToolRunning,
		}},
		Approval: &agent.ApprovalRequest{
			ID:       "fake-approval-command",
			Kind:     agent.ApproveCommand,
			ToolName: "command_execution",
			Summary:  cmd,
		},
	})
	s = append(s,
		Step{Delay: 800 * time.Millisecond, Event: agent.Event{Kind: agent.KindToolResult, Tool: &agent.Tool{
			ID: "item_cmd", Name: "command_execution", Title: cmd, Input: input,
			Output: "> build\n> vite build\n\nbuilt in 3.2s", Status: agent.ToolOK,
		}}},
		contextStep(codexWindow, 27400),
		Step{Delay: 150 * time.Millisecond, Event: agent.Event{Kind: agent.KindAssistantText,
			Text: "Rebuilt `dist` from a clean slate."}},
		Step{Event: agent.Event{Kind: agent.KindUsage, Usage: &agent.Usage{
			InputTokens: 6120, OutputTokens: 310, CostUSD: 0.04,
			ContextTokens: 34900, ContextWindow: codexWindow,
		}}},
		Step{Event: agent.Event{Kind: agent.KindTurnDone}},
	)
	return s
}

// storeDiff is a +24 -9 change to internal/store/store.go in two hunks.
func storeDiff() *agent.FileDiff {
	return fileDiff("internal/store/store.go",
		hunk(9, 9, `
 type Store struct {
 	dir string
-	mu  sync.Mutex
+	mu    sync.Mutex
+	locks map[string]*sync.Mutex
 }

 // lock returns the mutex that serialises writes to one thread's log.
-func (s *Store) lockFor(id string) *sync.Mutex {
-	return &s.mu
+func (s *Store) lock(threadID string) *sync.Mutex {
+	// one lock per thread, so writers to other threads do not wait
+	s.mu.Lock()
+	defer s.mu.Unlock()
+	return s.locks[threadID]
 }
`),
		hunk(31, 35, `
 func (s *Store) Append(threadID string, ev agent.Event) error {
-	f, err := os.OpenFile(s.path(threadID), os.O_APPEND|os.O_WRONLY, 0o600)
-	if err != nil {
-		return err
-	}
-	defer f.Close()
-	return json.NewEncoder(f).Encode(ev)
+	if ev.Kind == agent.KindAssistantText && ev.Delta {
+		return nil // streaming deltas are dropped; the final text is kept
+	}
+	mu := s.lock(threadID)
+	mu.Lock()
+	defer mu.Unlock()
+	f, err := os.OpenFile(s.path(threadID), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
+	if err != nil {
+		return fmt.Errorf("open thread log: %w", err)
+	}
+	defer f.Close()
+	line, err := json.Marshal(ev)
+	if err != nil {
+		return fmt.Errorf("encode event: %w", err)
+	}
+	_, err = f.Write(append(line, '\n'))
+	return err
 }
`),
	)
}

// hunk builds a hunk from diff-style body lines: ' ' context, '+' add, '-' delete.
// A fully empty line is treated as an empty context line.
func hunk(oldStart, newStart int, body string) agent.Hunk {
	h := agent.Hunk{OldStart: oldStart, NewStart: newStart}
	body = strings.TrimPrefix(body, "\n")
	body = strings.TrimSuffix(body, "\n")
	for _, line := range strings.Split(body, "\n") {
		op := agent.OpContext
		text := ""
		if line != "" {
			op = agent.LineOp(line[0])
			text = line[1:]
		}
		h.Lines = append(h.Lines, agent.DiffLine{Op: op, Text: text})
		if op != agent.OpAdd {
			h.OldLines++
		}
		if op != agent.OpDel {
			h.NewLines++
		}
	}
	return h
}

func fileDiff(path string, hunks ...agent.Hunk) *agent.FileDiff {
	fd := &agent.FileDiff{Path: path, Hunks: hunks}
	for _, h := range hunks {
		for _, l := range h.Lines {
			switch l.Op {
			case agent.OpAdd:
				fd.Added++
			case agent.OpDel:
				fd.Removed++
			}
		}
	}
	return fd
}

// ClaudeModels is the model list a fake adapter offers by default, shaped like
// the list Claude Code's initialize answer gives: efforts, no default effort.
func ClaudeModels() []agent.ModelInfo {
	es := claudeEfforts()
	return []agent.ModelInfo{
		{ID: "default", Name: "Default", Description: "The CLI's recommended model", Default: true, Efforts: es},
		{ID: "opus", Name: "Opus", Description: "Most capable, for complex work", Efforts: es},
		{ID: "sonnet", Name: "Sonnet", Description: "Best for everyday tasks", Efforts: es},
		{ID: "haiku", Name: "Haiku", Description: "Fastest, for quick answers"},
	}
}

func claudeEfforts() []agent.EffortInfo {
	return []agent.EffortInfo{
		{Level: "low", Description: "Quick answers, light reasoning"},
		{Level: "medium", Description: "Balanced speed and reasoning depth"},
		{Level: "high", Description: "Deeper reasoning for complex work"},
		{Level: "max", Description: "The most reasoning, slowest"},
	}
}

// CodexModels is a model list shaped like the Codex CLI's: each model has
// efforts and names its default one.
func CodexModels() []agent.ModelInfo {
	es := []agent.EffortInfo{
		{Level: "low", Description: "Fast responses with lighter reasoning"},
		{Level: "medium", Description: "Balances speed and reasoning depth"},
		{Level: "high", Description: "Greater reasoning depth for complex problems"},
	}
	return []agent.ModelInfo{
		{ID: "gpt-5-codex", Name: "gpt-5-codex", Description: "Tuned for agentic coding", Default: true, Efforts: es, DefaultEffort: "medium"},
		{ID: "gpt-5-codex-mini", Name: "gpt-5-codex-mini", Description: "Cheaper and faster", Efforts: es, DefaultEffort: "low"},
		{ID: "gpt-5", Name: "gpt-5", Description: "General purpose", Efforts: es, DefaultEffort: "medium"},
	}
}

// WithModels returns a copy of a fake adapter that lists models instead of
// ClaudeModels. Any other adapter is returned unchanged.
func WithModels(a agent.Adapter, models []agent.ModelInfo) agent.Adapter {
	fa, ok := a.(*adapter)
	if !ok {
		return a
	}
	cp := *fa
	cp.models = append([]agent.ModelInfo(nil), models...)
	return &cp
}

// Models implements agent.ModelLister.
func (a *adapter) Models(ctx context.Context) ([]agent.ModelInfo, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	ms := a.models
	if ms == nil {
		ms = ClaudeModels()
	}
	return append([]agent.ModelInfo(nil), ms...), nil
}

// SetMode implements agent.ModeSetter: it records the mode (the fake does not
// change its script because of it).
func (r *run) SetMode(m agent.Mode) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.shut {
		return ErrClosed
	}
	r.mode = m
	r.modes = append(r.modes, m)
	return nil
}

// SetModel implements agent.ModelSetter: it records the model.
func (r *run) SetModel(model string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.shut {
		return ErrClosed
	}
	r.model = model
	r.models = append(r.models, model)
	return nil
}

// SetEffort implements agent.EffortSetter: it records the effort.
func (r *run) SetEffort(effort string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.shut {
		return ErrClosed
	}
	r.effort = effort
	return nil
}

// Effort is the effort the run currently has: Spec.Effort until SetEffort.
func (r *run) Effort() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.effort
}

// Mode is the permission mode the run currently has: Spec.Mode until SetMode is called.
func (r *run) Mode() agent.Mode {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.mode
}

// Model is the model the run currently has: Spec.Model until SetModel is called.
func (r *run) Model() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.model
}

// ModeCalls lists every mode passed to SetMode, in order.
func (r *run) ModeCalls() []agent.Mode {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]agent.Mode(nil), r.modes...)
}

// ModelCalls lists every model passed to SetModel, in order.
func (r *run) ModelCalls() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.models...)
}

// Inspector is what tests can assert on a fake run (type-assert an agent.Run to it).
type Inspector interface {
	Mode() agent.Mode
	Model() string
	Effort() string
	ModeCalls() []agent.Mode
	ModelCalls() []string
	ImageCalls() []PromptCall
	SteerCalls() []PromptCall
}

var (
	_ agent.ModeSetter   = (*run)(nil)
	_ agent.ModelSetter  = (*run)(nil)
	_ agent.EffortSetter = (*run)(nil)
	_ agent.ModelLister  = (*adapter)(nil)
	_ agent.ImageSender  = (*run)(nil)
	_ agent.Steerer      = (*run)(nil)
	_ Inspector          = (*run)(nil)
)

// Context windows the fake models report: Claude's and Codex's.
const (
	claudeWindow = 200000
	codexWindow  = 272000
)

// contextStep is a mid-turn usage event that carries only the context fields,
// like the real CLIs' per-message usage: cost and token totals stay as they were.
func contextStep(window, used int) Step {
	return Step{Event: agent.Event{Kind: agent.KindUsage, Usage: &agent.Usage{
		ContextTokens: used, ContextWindow: window,
	}}}
}

// contextWindowFor is the window a fake agent reports: 272000 for ids that start
// with "codex", else 200000.
func contextWindowFor(agentID string) int {
	if strings.HasPrefix(agentID, "codex") {
		return codexWindow
	}
	return claudeWindow
}

// growContextLocked is the context size for the next default turn: it starts at
// 8000 tokens and grows by 6000 with every turn that reports it. r.mu is held.
func (r *run) growContextLocked() int {
	if r.ctx == 0 {
		r.ctx = 8000
	} else {
		r.ctx += 6000
	}
	return r.ctx
}

// PromptCall is a prompt a fake run was given through SendWithImages or Steer.
type PromptCall struct {
	Prompt string
	Images []agent.Image
}

// SendWithImages implements agent.ImageSender: it records the call and then
// behaves like Send.
func (r *run) SendWithImages(prompt string, images []agent.Image) error {
	r.mu.Lock()
	if r.shut {
		r.mu.Unlock()
		return ErrClosed
	}
	r.images = append(r.images, PromptCall{prompt, append([]agent.Image(nil), images...)})
	r.mu.Unlock()
	return r.Send(prompt)
}

// Steer implements agent.Steerer: it records the call and replaces the turn in
// progress with a new one that answers the prompt. The interrupted turn ends
// without a turn_done, so the caller sees a single turn that changed course. On
// an idle run it simply starts a turn.
func (r *run) Steer(prompt string, images []agent.Image) error {
	r.mu.Lock()
	if r.shut {
		r.mu.Unlock()
		return ErrClosed
	}
	r.steers = append(r.steers, PromptCall{prompt, append([]agent.Image(nil), images...)})
	t := r.cur
	if t != nil {
		t.silent.Store(true)
	}
	r.mu.Unlock()
	if t != nil {
		t.stop()
		<-t.done
	}
	return r.Send(prompt)
}

// ImageCalls lists every SendWithImages call, in order.
func (r *run) ImageCalls() []PromptCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]PromptCall(nil), r.images...)
}

// SteerCalls lists every Steer call, in order.
func (r *run) SteerCalls() []PromptCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]PromptCall(nil), r.steers...)
}
