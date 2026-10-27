// Package approve implements Deck's loopback approval broker and the tiny stdio
// MCP server that Claude Code's --permission-prompt-tool talks to.
package approve

import (
	"bufio"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/dev-dami/deck/internal/deck/agent"
)

// Wire request kinds. An empty kind means approve (backward compatible).
const (
	kindApprove = "approve"
	kindExec    = "exec" // the search tool (op "search") and every execute op
)

// opSearch is the op the search tool travels under.
const opSearch = "search"

// maxExecOutput caps the text one search or execute call returns. The hub
// already caps each result to its agent's own budget (hub.Env.MaxOutput,
// relative to its context window); this is the backstop at the hub's
// largest, hub.MaxOutputCap.
const maxExecOutput = 64 << 10

// messageTimeout is how long thread.send waits for Deck to answer.
var messageTimeout = 30 * time.Second

// wireRequest is one newline-JSON request from the MCP helper process.
type wireRequest struct {
	Token string          `json:"token"`
	Kind  string          `json:"kind,omitempty"` // approve (default) | exec
	Op    string          `json:"op,omitempty"`   // exec: the op ("search" for the search tool)
	Args  json.RawMessage `json:"args,omitempty"` // exec: the op's arguments
	To    string          `json:"to,omitempty"`   // thread.send: target agent name or "all"
	Text  string          `json:"text,omitempty"` // thread.send: the text
	// thread.send under a contract: the task and its terms (agent.Message).
	Task         string          `json:"-"`
	NewTask      string          `json:"-"`
	Expect       string          `json:"-"`
	DoneWhen     string          `json:"-"`
	ExplicitDone bool            `json:"-"`
	Capsule      string          `json:"-"`
	AgentID      string          `json:"agent_id"`
	RequestID    string          `json:"request_id"`
	ToolName     string          `json:"tool_name"`
	Input        json.RawMessage `json:"input"`
	Cwd          string          `json:"cwd,omitempty"`
}

type agentState struct {
	ch     chan agent.ApprovalRequest
	always map[string]bool
}

// Broker accepts approval requests from MCP helper processes on a loopback port.
type Broker struct {
	ln    net.Listener
	token string

	mu     sync.Mutex
	agents map[string]*agentState
	done   chan struct{}
	once   sync.Once

	messages chan agent.Message
	reader   func(agentID string) string
	exec     agent.Executor
	gate     func(agentID string, paths []string) string // gate.go
}

var _ agent.Hub = (*Broker)(nil)

// NewBroker starts listening on 127.0.0.1 on a random port.
func NewBroker() (*Broker, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return nil, err
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	b := &Broker{
		ln:     ln,
		token:  hex.EncodeToString(raw[:]),
		agents: map[string]*agentState{},
		done:   make(chan struct{}),

		messages: make(chan agent.Message, 32),
	}
	go b.accept()
	return b, nil
}

// Addr is the host:port the broker listens on.
func (b *Broker) Addr() string { return b.ln.Addr().String() }

// Token is the shared secret clients must present.
func (b *Broker) Token() string { return b.token }

// Messages delivers thread.send calls from every agent. Each message's Respond
// must be called exactly once; the sending agent is blocked until then.
func (b *Broker) Messages() <-chan agent.Message { return b.messages }

// SetThreadReader installs the thread.read provider.
func (b *Broker) SetThreadReader(f func(agentID string) string) {
	b.mu.Lock()
	b.reader = f
	b.mu.Unlock()
}

// SetExecutor installs the provider of search and of every execute op other
// than thread.send and thread.read, which the broker answers itself.
func (b *Broker) SetExecutor(f agent.Executor) {
	b.mu.Lock()
	b.exec = f
	b.mu.Unlock()
}

// ServerFor builds the MCP server description for one agent: the Deck binary
// run as `mcp-approve`, with the broker token in its environment only.
func (b *Broker) ServerFor(selfExe, agentID string) *agent.MCPServer {
	return &agent.MCPServer{
		Name:    "deck",
		Command: selfExe,
		// Claude's adapter adds ApproveToolFlag; other agents get only
		// search and execute.
		Args: []string{"mcp-approve", "--addr", b.Addr(), "--agent", agentID},
		Env:  map[string]string{TokenEnv: b.token},
	}
}

// Close stops the listener. Pending requests are denied.
func (b *Broker) Close() {
	b.once.Do(func() {
		close(b.done)
		b.ln.Close()
	})
}

func (b *Broker) state(id string) *agentState {
	b.mu.Lock()
	defer b.mu.Unlock()
	s := b.agents[id]
	if s == nil {
		s = &agentState{ch: make(chan agent.ApprovalRequest, 32), always: map[string]bool{}}
		b.agents[id] = s
	}
	return s
}

// Register returns the channel on which approval requests for agentID arrive.
// Calling it again for the same agent returns the same channel.
func (b *Broker) Register(agentID string) <-chan agent.ApprovalRequest {
	return b.state(agentID).ch
}

func (b *Broker) accept() {
	for {
		c, err := b.ln.Accept()
		if err != nil {
			return
		}
		go b.serve(c)
	}
}

func (b *Broker) serve(c net.Conn) {
	defer c.Close()
	stop := make(chan struct{})
	defer close(stop)
	go func() { // unblock on broker close
		select {
		case <-b.done:
			c.Close()
		case <-stop:
		}
	}()
	// Lines are read on their own goroutine, so a request waiting for its
	// answer notices when the helper hangs up (gone closes) and stops
	// waiting, instead of holding its goroutine and connection until Deck
	// exits. ReadBytes grows past the reader's buffer for a long line.
	lines := make(chan []byte)
	gone := make(chan struct{})
	go func() {
		defer close(gone)
		r := bufio.NewReader(c)
		for {
			line, err := r.ReadBytes('\n')
			if err != nil {
				return
			}
			select {
			case lines <- line:
			case <-stop:
				return
			}
		}
	}()
	for {
		var line []byte
		select {
		case line = <-lines:
		case <-gone:
			return
		}
		var req wireRequest
		if json.Unmarshal(line, &req) != nil {
			return
		}
		if subtle.ConstantTimeCompare([]byte(req.Token), []byte(b.token)) != 1 {
			return
		}
		var resp any
		switch req.Kind {
		case "", kindApprove:
			resp = b.handle(req, gone)
		case kindExec:
			resp = b.handleExec(req, gone)
		case kindGate:
			resp = b.handleGate(req, gone)
		default:
			resp = wireResponse{Behavior: "deny", Message: "unknown request kind"}
		}
		out, _ := json.Marshal(resp)
		c.SetWriteDeadline(time.Now().Add(10 * time.Second))
		if _, err := c.Write(append(out, '\n')); err != nil {
			return
		}
	}
}

type wireResponse struct {
	Behavior     string          `json:"behavior"`
	UpdatedInput json.RawMessage `json:"updatedInput,omitempty"`
	Message      string          `json:"message,omitempty"`
}

// wireReply answers exec requests.
type wireReply struct {
	Text    string `json:"text"`
	IsError bool   `json:"is_error,omitempty"`
}

// handleExec answers the search tool and one execute op. thread.send and
// thread.read are answered here; everything else goes to the executor,
// bounded by execTimeout. Results are capped and never carry the broker
// token.
func (b *Broker) handleExec(req wireRequest, gone <-chan struct{}) wireReply {
	var r wireReply
	switch strings.TrimSpace(req.Op) {
	case agent.OpThreadSend:
		var a struct {
			To           string `json:"to"`
			Message      string `json:"message"`
			Task         string `json:"task"`
			NewTask      string `json:"new_task"`
			Expect       string `json:"expect"`
			DoneWhen     string `json:"done_when"`
			ExplicitDone bool   `json:"explicit_done"`
			Capsule      string `json:"capsule"`
		}
		if json.Unmarshal(req.Args, &a) != nil || strings.TrimSpace(a.To) == "" || strings.TrimSpace(a.Message) == "" {
			return wireReply{Text: "thread.send needs args {\"to\": an agent name or \"all\", \"message\": the text}", IsError: true}
		}
		req.To, req.Text = strings.TrimSpace(a.To), a.Message
		req.Task, req.NewTask = strings.TrimSpace(a.Task), strings.TrimSpace(a.NewTask)
		req.Expect, req.DoneWhen, req.ExplicitDone = strings.TrimSpace(a.Expect), strings.TrimSpace(a.DoneWhen), a.ExplicitDone
		req.Capsule = strings.TrimSpace(a.Capsule)
		r = b.handleMessage(req, gone)
	case agent.OpThreadRead:
		r = b.handleRead(req)
	default:
		r = b.runExec(req, gone)
	}
	r.Text = b.scrub(r.Text)
	return r
}

func (b *Broker) runExec(req wireRequest, gone <-chan struct{}) wireReply {
	b.mu.Lock()
	f := b.exec
	b.mu.Unlock()
	if f == nil {
		return wireReply{Text: "Deck's search and execute are not available in this session.", IsError: true}
	}
	done := make(chan wireReply, 1)
	go func() {
		text, isErr := f(req.AgentID, req.Op, req.Args)
		done <- wireReply{Text: text, IsError: isErr}
	}()
	timer := time.NewTimer(execTimeout)
	defer timer.Stop()
	select {
	case r := <-done:
		return r
	case <-timer.C:
		return wireReply{Text: req.Op + " took too long; Deck gave up after " + execTimeout.String(), IsError: true}
	case <-b.done:
		return wireReply{Text: "Deck is shutting down", IsError: true}
	case <-gone:
		return wireReply{Text: "the caller went away", IsError: true}
	}
}

// scrub caps a result at maxExecOutput and removes the broker token.
func (b *Broker) scrub(s string) string {
	if b.token != "" {
		s = strings.ReplaceAll(s, b.token, "[redacted]")
	}
	if len(s) <= maxExecOutput {
		return s
	}
	cut := maxExecOutput
	for cut > 0 && (s[cut]&0xC0) == 0x80 {
		cut--
	}
	return s[:cut] + fmt.Sprintf("\n[... output truncated at %d KB; narrow the request ...]", maxExecOutput>>10)
}

func (b *Broker) handleMessage(req wireRequest, gone <-chan struct{}) wireReply {
	type result struct {
		text string
		err  error
	}
	reply := make(chan result, 1)
	var once sync.Once
	m := agent.Message{
		ID:   req.RequestID,
		From: req.AgentID,
		To:   req.To,
		Text: req.Text,
		Task: req.Task, NewTask: req.NewTask, Expect: req.Expect, DoneWhen: req.DoneWhen, ExplicitDone: req.ExplicitDone,
		Capsule: req.Capsule,
		Respond: func(text string, err error) {
			once.Do(func() { reply <- result{text, err} })
		},
	}
	timer := time.NewTimer(messageTimeout)
	defer timer.Stop()
	select {
	case b.messages <- m:
	case <-b.done:
		return wireReply{Text: "Deck is shutting down", IsError: true}
	case <-timer.C:
		return wireReply{Text: "Deck did not answer", IsError: true}
	case <-gone:
		return wireReply{Text: "the caller went away", IsError: true}
	}
	select {
	case r := <-reply:
		if r.err != nil {
			text := r.err.Error()
			if r.text != "" {
				text = r.text
			}
			return wireReply{Text: text, IsError: true}
		}
		return wireReply{Text: r.text}
	case <-b.done:
		return wireReply{Text: "Deck is shutting down", IsError: true}
	case <-timer.C:
		return wireReply{Text: "Deck did not answer", IsError: true}
	case <-gone:
		return wireReply{Text: "the caller went away", IsError: true}
	}
}

func (b *Broker) handleRead(req wireRequest) wireReply {
	b.mu.Lock()
	f := b.reader
	b.mu.Unlock()
	if f == nil {
		return wireReply{Text: "No shared thread."}
	}
	return wireReply{Text: f(req.AgentID)}
}

// approvalTimeout bounds how long an approval waits for the user. It only
// frees a request nobody will ever answer (its turn ended, say, while the
// helper kept its connection); a person answers long before.
var approvalTimeout = 12 * time.Hour

func (b *Broker) handle(req wireRequest, gone <-chan struct{}) wireResponse {
	allow := wireResponse{Behavior: "allow", UpdatedInput: req.Input}
	if len(allow.UpdatedInput) == 0 {
		allow.UpdatedInput = json.RawMessage(`{}`)
	}
	st := b.state(req.AgentID)

	b.mu.Lock()
	auto := st.always[req.ToolName]
	b.mu.Unlock()
	if auto {
		return allow
	}

	reply := make(chan agent.Decision, 1)
	var once sync.Once
	ar := agent.ApprovalRequest{
		ID:       req.RequestID,
		AgentID:  req.AgentID,
		ToolName: req.ToolName,
		Cwd:      req.Cwd,
		Respond: func(d agent.Decision) {
			once.Do(func() { reply <- d })
		},
	}
	fillRequest(&ar, req.Input)

	timer := time.NewTimer(approvalTimeout)
	defer timer.Stop()
	select {
	case st.ch <- ar:
	case <-b.done:
		return wireResponse{Behavior: "deny", Message: "Deck is shutting down"}
	case <-gone:
		return wireResponse{Behavior: "deny", Message: "the request was abandoned"}
	case <-timer.C:
		return wireResponse{Behavior: "deny", Message: "nobody answered in Deck"}
	}
	select {
	case <-gone:
		return wireResponse{Behavior: "deny", Message: "the request was abandoned"}
	case <-timer.C:
		return wireResponse{Behavior: "deny", Message: "nobody answered in Deck"}
	case d := <-reply:
		if !d.Allow {
			msg := d.Reason
			if msg == "" {
				msg = "Denied by user in Deck"
			}
			return wireResponse{Behavior: "deny", Message: msg}
		}
		if d.Always {
			b.mu.Lock()
			st.always[req.ToolName] = true
			b.mu.Unlock()
		}
		return allow
	case <-b.done:
		return wireResponse{Behavior: "deny", Message: "Deck is shutting down"}
	}
}

func fillRequest(ar *agent.ApprovalRequest, input json.RawMessage) {
	var in struct {
		Command      string `json:"command"`
		FilePath     string `json:"file_path"`
		NotebookPath string `json:"notebook_path"`
		OldString    string `json:"old_string"`
		NewString    string `json:"new_string"`
		Content      string `json:"content"`
		NewSource    string `json:"new_source"`
		Edits        []struct {
			OldString string `json:"old_string"`
			NewString string `json:"new_string"`
		} `json:"edits"`
	}
	_ = json.Unmarshal(input, &in)
	path := in.FilePath
	if path == "" {
		path = in.NotebookPath
	}
	switch ar.ToolName {
	case "Bash":
		ar.Kind = agent.ApproveCommand
		ar.Summary = in.Command
	case "Edit":
		ar.Kind = agent.ApproveEdit
		ar.Summary = path
		ar.Diff = BuildEditDiff(path, in.OldString, in.NewString)
	case "Write":
		ar.Kind = agent.ApproveEdit
		ar.Summary = path
		ar.Diff = BuildEditDiff(path, "", in.Content)
	case "MultiEdit":
		ar.Kind = agent.ApproveEdit
		ar.Summary = path
		var olds, news []string
		for _, e := range in.Edits {
			olds = append(olds, strings.TrimSuffix(e.OldString, "\n"))
			news = append(news, strings.TrimSuffix(e.NewString, "\n"))
		}
		ar.Diff = BuildEditDiff(path, strings.Join(olds, "\n...\n"), strings.Join(news, "\n...\n"))
	case "NotebookEdit":
		ar.Kind = agent.ApproveEdit
		ar.Summary = path
		ar.Diff = BuildEditDiff(path, "", in.NewSource)
		ar.Diff.New = false
	default:
		ar.Kind = agent.ApproveOther
		ar.Summary = path
		if ar.Summary == "" {
			ar.Summary = shortInput(input)
		}
	}
}

func shortInput(in json.RawMessage) string {
	s := string(in)
	if len(s) > 120 {
		s = s[:117] + "..."
	}
	return s
}
