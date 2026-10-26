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
	KindSessionStarted Kind = "session_started" // SessionID is known; Text = model slug the CLI runs, if it says
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
	// KindLimits carries the signed-in account's plan windows (Limits), or why
	// the CLI reports none. It is about the account, not the thread: it can
	// arrive at any time, including between turns.
	KindLimits Kind = "limits"
	// KindRestarting says the CLI process is being replaced to apply a
	// setting it only takes at start-up (Text names it: "model", "effort",
	// "mode", or several joined by ", "). Prompts sent meanwhile are held
	// and delivered once it is back. KindRestarted follows when the new
	// process is up; a restart that fails ends with KindError and KindExited.
	KindRestarting Kind = "restarting"
	KindRestarted  Kind = "restarted"
	// KindCompacted says the CLI compacted the conversation: earlier turns
	// were replaced by a summary, so the context use drops. Text is how it
	// was started when the CLI says ("auto", "manual"). It is not a turn.
	KindCompacted Kind = "compacted"
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
	// Binary marks a file whose change has no text hunks to show.
	Binary bool `json:",omitempty"`
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
	// CacheWriteTokens are input tokens written to the prompt cache, and
	// ReasoningTokens output tokens spent thinking (counted in OutputTokens
	// too when the CLI does). 0 when the CLI does not report them.
	CacheWriteTokens int
	ReasoningTokens  int
	// TTFTms is the time to the turn's first token and APIms the time spent
	// in model requests, and DurationMs the turn's whole time, as the CLI
	// measured them; 0 when it does not say.
	TTFTms     int
	APIms      int
	DurationMs int
}

// Why a CLI's Limits carry no windows.
const (
	// LimitsUnsupported: the CLI has no way to report plan limits that Deck
	// may use (reading its credentials is off-limits).
	LimitsUnsupported = "unsupported"
	// LimitsProbeFailed: asking the CLI failed; windows may still arrive later
	// with the CLI's own events.
	LimitsProbeFailed = "probe_failed"
)

// Kinds of plan window.
const (
	WindowSession = "session" // a rolling few-hour window
	WindowWeekly  = "weekly"
	WindowMonthly = "monthly"
	WindowOther   = "other"
)

// Limits are the signed-in account's plan windows as a CLI reports them. Every
// KindLimits event carries the whole current picture, not a change. Deck
// never reads the CLI's credentials to get them, and no account email is
// ever part of them.
type Limits struct {
	Plan string // the plan's name as the CLI says it ("max", "pro", ...); "" when not known
	// Unavailable is "" when the CLI reports limits (Windows may still be
	// empty until it does), else LimitsUnsupported or LimitsProbeFailed.
	Unavailable string
	Windows     []LimitWindow
}

// LimitWindow is one usage window of a plan.
type LimitWindow struct {
	ID           string    // stable per CLI, e.g. "five_hour", "seven_day", "primary"
	Kind         string    // WindowSession | WindowWeekly | WindowMonthly | WindowOther
	Label        string    // short display name, e.g. "5h", "Weekly"
	UsedPct      float64   // 0-100
	ResetsAt     time.Time // zero when not known
	DurationMins int       // 0 when not known
}

// UnsupportedLimits is the KindLimits event of a CLI that reports no plan
// limits.
func UnsupportedLimits(agentID string) Event {
	return Event{AgentID: agentID, Time: time.Now(), Kind: KindLimits, Limits: &Limits{Unavailable: LimitsUnsupported}}
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
	Limits  *Limits
	// SessionID is the CLI's own session/thread id (KindSessionStarted).
	SessionID string
	// Raw is the original protocol line, kept so nothing is lost. Deck keeps
	// it past the adapter only where KeepsRaw says so.
	Raw json.RawMessage
}

// KeepsRaw reports whether an event of kind k keeps its Raw line once Deck
// has it: only errors and kinds Deck does not know, whose protocol line may
// be the only full account of what happened. For every other kind Raw just
// repeats Text, Tool and the rest, so Deck drops it before the event is
// logged, stored or passed on.
func KeepsRaw(k Kind) bool {
	switch k {
	case KindSessionStarted, KindAssistantText, KindReasoning, KindUserText,
		KindToolCall, KindToolResult, KindFileChange, KindUsage, KindTurnDone,
		KindExited, KindLimits, KindRestarting, KindRestarted, KindCompacted:
		return false
	}
	return true
}

// ApprovalKind classifies what an agent is asking permission for.
type ApprovalKind string

const (
	ApproveCommand ApprovalKind = "command"
	ApproveEdit    ApprovalKind = "edit"
	ApproveOther   ApprovalKind = "other"
	// ApproveShare is sending work out of this Deck (a code contract to a
	// teammate). The person answers it in every mode: yolo does not.
	ApproveShare ApprovalKind = "share"
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
	Effort      string            // reasoning effort ("low" … "max"); empty = the model's default
	Binary      string            // empty = look up on PATH
	Env         map[string]string // extra env (e.g. CLAUDE_CONFIG_DIR); never logged
	ResumeID    string            // CLI session/thread id to resume
	FirstPrompt string
	UserName    string // optional: told to the agent only if TellName is true
	TellName    bool
	// Attribution: commits and PRs credit Deck instead of the CLI (Claude:
	// its attribution settings; every agent: the git hooks in Env).
	Attribution bool
	// Instructions are extra system/developer instructions, e.g. the
	// shared-thread briefing. Claude: --append-system-prompt; Codex: the
	// thread's developer instructions.
	Instructions string
	// Briefed says the session ResumeID names already had Instructions in
	// one of its prompts. Adapters that deliver them inside the
	// conversation (OpenCode, Antigravity) then do not send them again;
	// those that pass them as system or developer instructions ignore it.
	Briefed bool
	// MCP is Deck's own stdio MCP server for this agent: the search and
	// execute tools (see DeckToolSearch), plus the approve tool for Claude.
	// Adapters register it with their CLI under MCP.Name. Nil = none.
	MCP *MCPServer
	// Mode is the starting permission mode; empty means ModeAsk.
	Mode Mode
	// NoTools starts the agent with none of its CLI's own tools (shell,
	// edits, web): only the tools of MCP. Deck sets it only for adapters
	// that implement OpsSafe.
	NoTools bool
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
	// Efforts are the reasoning efforts the model takes, in the CLI's order;
	// none when it has no effort setting. DefaultEffort is the one it uses
	// when none is chosen ("" when the CLI doesn't say).
	Efforts       []EffortInfo
	DefaultEffort string
}

// EffortInfo is one reasoning effort a model offers.
type EffortInfo struct {
	Level       string // value passed to the CLI: "low", "medium", "high", …
	Description string
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

// EffortSetter is implemented by runs that can change reasoning effort
// mid-session ("" = the model's default). The change applies from the next
// turn at the latest.
type EffortSetter interface {
	SetEffort(effort string) error
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

// Drainer is implemented by adapters that finish a run's cleanup in the
// background after its Close returns. Drain waits for that cleanup, at
// most d; Deck calls it once on the way out.
type Drainer interface {
	Drain(d time.Duration)
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
// Deck's execute MCP tool with op thread.send.
type Message struct {
	ID   string
	From string // sending agent id, e.g. "claude"
	To   string // agent id/name, or "all"
	Text string
	// Task hands the message over as an existing task of the thread (its id);
	// NewTask creates one from this title. Both need a single recipient.
	// Expect and DoneWhen are the terms the recipient is held to, and
	// ExplicitDone says its last reply alone does not finish the task: it
	// must report it with task.done.
	Task, NewTask, Expect, DoneWhen string
	ExplicitDone                    bool
	// Capsule is a SUB capsule id handed over with the message: the code
	// change itself, by reference, instead of a description of it.
	Capsule string
	// Respond must be called exactly once; result is returned to the sending
	// agent as the tool result (e.g. "delivered to codex").
	Respond func(result string, err error)
}

// Hub is Deck's side of the search and execute tools: it receives every
// agent's thread.send calls, answers thread.read and hands the other ops to
// the Executor.
type Hub interface {
	Messages() <-chan Message
	// SetThreadReader installs the thread.read provider: given the calling
	// agent id, return the shared thread as plain text.
	SetThreadReader(func(agentID string) string)
	// SetExecutor installs the provider of search and of every execute op
	// other than thread.send and thread.read.
	SetExecutor(Executor)
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

// ContractSafe is implemented by an adapter whose agent Deck can hold to a
// thread's mode, at most ask, when it runs a teammate's code contract:
// every edit and command it makes asks the person through Deck, or is
// denied. Deck never runs a contract with an adapter that lacks it or
// answers false.
type ContractSafe interface {
	ContractSafe() bool
}

// OpsSafe is implemented by an adapter that honors Spec.NoTools: its agent
// then has Deck's MCP tools and nothing else. Deck runs a teammate's ops
// contract only with an adapter that has it and answers true.
type OpsSafe interface {
	OpsSafe() bool
}
