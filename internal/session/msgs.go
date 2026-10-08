package session

import (
	"strconv"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/dev-dami/deck/internal/agent"
	"github.com/dev-dami/deck/internal/store"
)

// EventMsg carries one normalized event to the UI.
//
// Agent events have Event.AgentID set to the member that produced them. A
// message the user typed arrives as Kind == agent.KindUserText with AgentID ==
// "" and To holding the member ids it was addressed to. The adapters' own
// user_text echoes (which would contain Deck's briefing) are never forwarded.
type EventMsg struct {
	agent.Event
	ThreadID string
	To       []string // user messages only
}

// FromUser reports whether the event is a message the user typed.
func (e EventMsg) FromUser() bool { return e.Kind == agent.KindUserText && e.AgentID == "" }

// ApprovalMsg carries a permission request to the UI. Its Respond is wrapped by
// the Manager: calling it (once) clears the request from the agent's pending
// count. Approvals always need the user, whichever agent raised them.
type ApprovalMsg struct {
	agent.ApprovalRequest
	ThreadID string
}

// ApprovalGoneMsg tells the UI that a request it holds is no longer answerable
// (the turn ended, the agent was interrupted or exited).
type ApprovalGoneMsg struct {
	ThreadID string
	AgentID  string
	ID       string
}

// RelayMsg reports a message one agent sent to others, so the thread can show
// it ("codex -> claude: ..."). Via is "tool" (the send_message MCP tool) or
// "mention" (a line of the sender's final reply starting with @name). Held is
// true when the thread was paused and the relay was not delivered; it is
// delivered when the user calls Continue.
type RelayMsg struct {
	ThreadID string
	From     string   // sending member id
	To       []string // receiving member ids
	Text     string
	Via      string
	Held     bool
}

// PausedMsg says the thread hit its agent-to-agent hop limit: further relays
// are held until Continue (or a new user message). Hops is the count reached.
type PausedMsg struct {
	ThreadID string
	Hops     int
}

// ContinuedMsg says a pause ended (Continue, or the user posted).
type ContinuedMsg struct{ ThreadID string }

// MemberAddedMsg says an agent joined a thread.
type MemberAddedMsg struct {
	ThreadID string
	AgentID  string
	Kind     string
}

// NoticeMsg is a note from Deck itself, shown inline (for example a dropped
// duplicate relay).
type NoticeMsg struct {
	ThreadID string
	Text     string
}

// msgFor converts a log entry to the message the live stream carries for it.
// It returns nil for entries that are not shown (streaming deltas).
func msgFor(threadID string, e store.Entry) tea.Msg {
	switch e.Type {
	case store.EntryEvent:
		if e.Delta {
			return nil
		}
		return EventMsg{Event: e.Event, ThreadID: threadID}
	case store.EntryUser:
		ev := e.Event
		ev.Kind, ev.AgentID = agent.KindUserText, ""
		return EventMsg{Event: ev, ThreadID: threadID, To: e.To}
	case store.EntryRelay:
		return RelayMsg{ThreadID: threadID, From: e.Speaker, To: e.To, Text: e.Text, Via: e.Via, Held: e.Held}
	case store.EntryMember:
		return MemberAddedMsg{ThreadID: threadID, AgentID: e.Speaker, Kind: e.Text}
	case store.EntryPause:
		n, _ := strconv.Atoi(e.Text)
		return PausedMsg{ThreadID: threadID, Hops: n}
	case store.EntryContinue:
		return ContinuedMsg{ThreadID: threadID}
	case store.EntryNotice:
		return NoticeMsg{ThreadID: threadID, Text: e.Text}
	}
	return nil
}

// ModeChangedMsg says a thread's permission mode changed (SetMode). Prev is the
// mode it had before.
type ModeChangedMsg struct {
	ThreadID string
	Mode     agent.Mode
	Prev     agent.Mode
}

// ModelChangedMsg says a member now runs a different model (SetModel). An empty
// Model means the CLI's default.
type ModelChangedMsg struct {
	ThreadID string
	AgentID  string
	Model    string
}

// AutoApprovedMsg reports an approval request that Deck answered Allow itself
// because the thread is in yolo mode. It never appears as an ApprovalMsg; the
// transcript can show it as a faint "✓ auto-approved: <Summary>" line.
type AutoApprovedMsg struct {
	ThreadID string
	AgentID  string
	Summary  string // the command line or file path
}

// AutoDeniedMsg reports an edit or command request that Deck denied itself
// because the thread is in plan mode ("auto-denied: <Summary>"). It never
// appears as an ApprovalMsg.
type AutoDeniedMsg struct {
	ThreadID string
	AgentID  string
	Summary  string
}
