// Package approve implements Deck's loopback approval broker and the tiny stdio
// MCP server that Claude Code's --permission-prompt-tool talks to.
package approve

import (
	"bufio"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/dev-dami/deck/internal/agent"
)

// wireRequest is one newline-JSON request from the MCP helper process.
type wireRequest struct {
	Token     string          `json:"token"`
	AgentID   string          `json:"agent_id"`
	RequestID string          `json:"request_id"`
	ToolName  string          `json:"tool_name"`
	Input     json.RawMessage `json:"input"`
	Cwd       string          `json:"cwd,omitempty"`
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
}

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
	}
	go b.accept()
	return b, nil
}

// Addr is the host:port the broker listens on.
func (b *Broker) Addr() string { return b.ln.Addr().String() }

// Token is the shared secret clients must present.
func (b *Broker) Token() string { return b.token }

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
	r := bufio.NewReaderSize(c, 1<<20)
	for {
		line, err := r.ReadBytes('\n')
		if err != nil {
			return
		}
		var req wireRequest
		if json.Unmarshal(line, &req) != nil {
			return
		}
		if subtle.ConstantTimeCompare([]byte(req.Token), []byte(b.token)) != 1 {
			return
		}
		resp := b.handle(req)
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

func (b *Broker) handle(req wireRequest) wireResponse {
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

	select {
	case st.ch <- ar:
	case <-b.done:
		return wireResponse{Behavior: "deny", Message: "Deck is shutting down"}
	}
	select {
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
