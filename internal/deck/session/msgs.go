package session

import (
	"strconv"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/dev-dami/deck/internal/deck/agent"
	"github.com/dev-dami/deck/internal/deck/store"
)

// EventMsg carries one normalized event to the UI.
//
// Agent events have Event.AgentID set to the member that produced them. A
// message the user typed arrives as Kind == agent.KindUserText with AgentID ==
// "" and To holding the member ids it was addressed to. The adapters' own
// user_text echoes (which would contain Deck's briefing) are never forwarded.
//
// For user messages the extra fields are set: EntryID identifies the message (for
// Edit), Images holds the paths of attached images, and Steer is true when the
// message is a steer (see Manager.Steer): a queued message the user pushed into
// the agent's running turn. A steer arrives as its own EventMsg (To = the
// steered agent, SteerOf = EntryID of the queued message it delivers, which the UI
// already showed when it was posted); show it as a steer line, or fold it into
// that message.
type EventMsg struct {
	agent.Event
	ThreadID string
	To       []string // user messages only
	EntryID  string   // user messages only: id for Manager.Edit
	Images   []string // user messages only: attached image paths
	Steer    bool     // user messages only: a queued message delivered as a steer
	SteerOf  string   // steers only: EntryID of the queued message being delivered
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
// "mention" (a line of the sender's final reply starting with $name). Held is
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
		msg := EventMsg{Event: ev, ThreadID: threadID, To: e.To, EntryID: e.ID, Images: e.Images, Steer: e.Steer}
		if e.Steer {
			msg.SteerOf = e.Ref
		}
		return msg
	case store.EntryEdit:
		return EditedMsg{ThreadID: threadID, EntryID: e.Ref, OldText: e.Old, NewText: e.Text}
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

// ModelChangedMsg says a member now runs a different model or effort
// (SetModel, SetEffort). An empty Model means the CLI's default, an empty
// Effort the model's.
type ModelChangedMsg struct {
	ThreadID string
	AgentID  string
	Model    string
	Effort   string
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

// QueuedItem is a user-authored message (or edit) waiting for a busy member. It
// is delivered, merged with anything else queued for that member, when the
// member's turn ends - unless the user steers it (Manager.Steer) or cancels it
// (Manager.CancelQueued).
type QueuedItem struct {
	// ID identifies the item for Steer and CancelQueued. A message addressed to
	// several busy members yields one item per member.
	ID       string
	ThreadID string
	AgentID  string // the busy member it waits for
	// Text is the message as it will be delivered (the latest edited text; for an
	// Edit item, the new text).
	Text   string
	Images []agent.Image
	At     time.Time
	// Steer is true only while a steer of this item is in flight (the agent had to
	// be interrupted first); the item disappears once it is delivered.
	Steer bool
	// Edit is true for an item that delivers an edit of an earlier message.
	Edit bool
	// EntryID is the user message the item belongs to (the id EventMsg.EntryID
	// carries): for an Edit item, the message that was edited.
	EntryID string
}

// PostResult is what PostWith returns.
type PostResult struct {
	// EntryID identifies the user message just posted (EventMsg.EntryID; the id
	// Manager.Edit takes). Empty when the post only added members.
	EntryID string
	// Queued lists the items that were queued because their addressee was busy.
	Queued []QueuedItem
}

// QueueChangedMsg reports that a thread's user queue (Manager.Queue) changed:
// an item was added, delivered, steered, cancelled, edited or dropped by an
// interrupt. Items is the whole queue after the change, oldest first.
type QueueChangedMsg struct {
	ThreadID string
	Items    []QueuedItem
}

// EditedMsg says the user edited an earlier message (Manager.Edit). The UI shows
// NewText on the message EntryID with an "edited" tag. History and Resume replay
// it after the message itself, so a rebuilt view shows the latest text.
type EditedMsg struct {
	ThreadID string
	EntryID  string
	OldText  string // the text before this edit
	NewText  string
}
