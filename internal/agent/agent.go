// Package agent defines the contract between Deck and the coding-agent CLIs it drives.
//
// Deck never talks to a model API itself. Every adapter launches the user's own
// installed CLI (claude, codex) under the user's own login, over that CLI's
// documented structured protocol, and translates its output into Events.
package agent

import (
	"context"
	"encoding/json"
	"time"
)

// Kind is the kind of a normalized event.
type Kind string

const (
	KindSessionStarted Kind = "session_started" // SessionID is known
	KindAssistantText  Kind = "assistant_text"  // Text; Delta=true while streaming
	KindReasoning      Kind = "reasoning"       // Text; Delta=true while streaming
	KindUserText       Kind = "user_text"       // echo of a prompt Deck sent
	KindToolCall       Kind = "tool_call"       // Tool populated, Status=running
	KindToolResult     Kind = "tool_result"     // Tool populated, Status=ok|error
	KindFileChange     Kind = "file_change"     // Diff populated
	KindUsage          Kind = "usage"           // Usage populated (cumulative for the turn)
	KindTurnDone       Kind = "turn_done"       // a turn finished; agent is idle
	KindError          Kind = "error"           // Text holds the message
	KindExited         Kind = "exited"          // the CLI process ended
)

// ToolStatus is the lifecycle state of a tool call.
type ToolStatus string

const (
	ToolRunning ToolStatus = "running"
	ToolOK      ToolStatus = "ok"
	ToolError   ToolStatus = "error"
)

// Tool describes one tool call (Bash, Edit, Read, command_execution, ...).
type Tool struct {
	ID     string
	Name   string // display name, e.g. "Bash", "Edit", "Read"
	Title  string // one-line summary, e.g. "go test ./..." or "internal/store/store.go"
	Input  json.RawMessage
	Output string
	Status ToolStatus
}

// LineOp is the kind of a diff line.
type LineOp byte

const (
	OpContext LineOp = ' '
	OpAdd     LineOp = '+'
	OpDel     LineOp = '-'
)

// DiffLine is one line of a hunk.
type DiffLine struct {
	Op   LineOp
	Text string
}

// Hunk is one unified-diff hunk.
type Hunk struct {
	OldStart, OldLines int
	NewStart, NewLines int
	Lines              []DiffLine
}

// FileDiff is a change to a single file.
type FileDiff struct {
	Path    string
	OldPath string // set on rename
	Added   int
	Removed int
	Hunks   []Hunk
	New     bool
	Deleted bool
}

// Usage is token and cost accounting reported by the CLI.
type Usage struct {
	InputTokens  int
	OutputTokens int
	CachedTokens int
	CostUSD      float64 // 0 when the CLI does not report cost
	// ContextTokens is how much of the model's context window the
	// conversation currently fills; ContextWindow is that window's size.
	// Either may be 0 when the CLI does not report it.
	ContextTokens int
	ContextWindow int
}

// Event is the normalized event every adapter emits.
type Event struct {
	AgentID string
	Time    time.Time
	Kind    Kind
	Text    string
	Delta   bool
	Tool    *Tool
	Diff    *FileDiff
	Usage   *Usage
	// SessionID is the CLI's own session/thread id (KindSessionStarted).
	SessionID string
	// Raw is the original protocol line, kept so nothing is lost.
	Raw json.RawMessage
}

// ApprovalKind classifies what an agent is asking permission for.
type ApprovalKind string

const (
	ApproveCommand ApprovalKind = "command"
	ApproveEdit    ApprovalKind = "edit"
	ApproveOther   ApprovalKind = "other"
)

// Decision is the user's answer to an approval request.
type Decision struct {
	Allow  bool
	Always bool   // allow this tool for the rest of the session
	Reason string // optional, sent back to the agent on deny
}

// ApprovalRequest is a permission prompt raised by an agent.
type ApprovalRequest struct {
	ID       string
	AgentID  string
	Kind     ApprovalKind
	ToolName string
	Summary  string // the command line, or the file path
	Cwd      string
	Diff     *FileDiff
	// Respond must be called exactly once.
	Respond func(Decision)
}

// Spec describes how to start an agent.
type Spec struct {
	ID          string            // Deck's id for this agent, e.g. "claude2"
	Dir         string            // working directory
	Model       string            // empty = CLI default
	Binary      string            // empty = look up on PATH
	Env         map[string]string // extra env (e.g. CLAUDE_CONFIG_DIR); never logged
	ResumeID    string            // CLI session/thread id to resume
	FirstPrompt string
	UserName    string // optional: told to the agent only if TellName is true
	TellName    bool
	// Instructions are extra system/developer instructions, e.g. the
	// shared-thread briefing. Claude: --append-system-prompt; Codex: the
	// thread's developer instructions.
	Instructions string
	// MCP is Deck's own stdio MCP server for this agent (approve tool for
	// Claude, plus the send_message / read_thread messaging tools). Adapters
	// register it with their CLI under MCP.Name. Nil = none.
	MCP *MCPServer
	// Mode is the starting permission mode; empty means ModeAsk.
	Mode Mode
}

// Mode is the permission mode of a thread.
type Mode string

const (
	// ModePlan: the agent may read and explore but must not change anything.
	ModePlan Mode = "plan"
	// ModeAsk: every action that needs permission asks the user (default).
	ModeAsk Mode = "ask"
	// ModeYolo: everything is approved without asking.
	ModeYolo Mode = "yolo"
)

// Modes in shift+tab order.
var Modes = []Mode{ModePlan, ModeAsk, ModeYolo}

// ModelInfo is one model a CLI offers.
type ModelInfo struct {
	ID          string // value passed to the CLI (--model / model field)
	Name        string // display name
	Description string
	Default     bool
}

// ModeSetter is implemented by runs that can change permission mode.
// The change applies from the next turn at the latest.
type ModeSetter interface {
	SetMode(Mode) error
}

// ModelSetter is implemented by runs that can switch model mid-session.
// The change applies from the next turn at the latest.
type ModelSetter interface {
	SetModel(model string) error
}

// Image is an image attached to a prompt. Path is a local file the user
// chose (dropped, pasted or saved from the clipboard by Deck).
type Image struct {
	Path      string
	MediaType string // "image/png", "image/jpeg", "image/gif", "image/webp"
}

// ImageSender is implemented by runs that accept images with a prompt.
type ImageSender interface {
	SendWithImages(prompt string, images []Image) error
}

// Steerer is implemented by runs that can inject a message into the turn in
// progress (redirecting the agent) instead of waiting for it to finish.
// Runs without it are steered by Interrupt followed by Send.
type Steerer interface {
	Steer(prompt string, images []Image) error
}

// ModelLister is implemented by adapters that can list their CLI's models.
type ModelLister interface {
	Models(ctx context.Context) ([]ModelInfo, error)
}

// MCPServer describes a stdio MCP server an adapter registers with its CLI.
// Env carries secrets (the hub token); it must never appear in Args or logs.
type MCPServer struct {
	Name    string // "deck"
	Command string
	Args    []string
	Env     map[string]string
}

// Message is an agent-to-agent (or agent-to-thread) message raised through
// Deck's send_message MCP tool.
type Message struct {
	ID   string
	From string // sending agent id, e.g. "claude"
	To   string // agent id/name, or "all"
	Text string
	// Respond must be called exactly once; result is returned to the sending
	// agent as the tool result (e.g. "delivered to codex").
	Respond func(result string, err error)
}

// Hub is Deck's side of the messaging tools: it receives send_message calls
// from every agent and answers read_thread calls.
type Hub interface {
	Messages() <-chan Message
	// SetThreadReader installs the read_thread provider: given the calling
	// agent id, return the shared thread as plain text.
	SetThreadReader(func(agentID string) string)
}

// Run is a live agent process.
type Run interface {
	Events() <-chan Event
	Approvals() <-chan ApprovalRequest
	Send(prompt string) error
	Interrupt() error
	SessionID() string
	Close() error
}

// Adapter starts runs of one CLI.
type Adapter interface {
	Name() string // "claude" | "codex" | "fake"
	Start(ctx context.Context, spec Spec) (Run, error)
}
