package wire

import (
	"encoding/json"
	"time"
	"unicode/utf8"
)

// Protocol is the wire protocol version; hello.v must equal it.
const Protocol = 2

// Frame types (Frame.T).
const (
	FrameHello   = "hello"
	FrameWelcome = "welcome"
	FrameReq     = "req"
	FrameRes     = "res"
	FrameEv      = "ev"
	FrameResync  = "resync"
)

// Error codes (Error.Code).
const (
	CodeUnauthorized = "unauthorized"
	CodeTokenInvalid = "token_invalid"
	CodeRateLimited  = "rate_limited"
	CodeForbidden    = "forbidden"
	CodeNotFound     = "not_found"
	CodeInvalid      = "invalid"
	CodeYoloDisabled = "yolo_disabled"
	CodeBusy         = "busy"
	CodeTooLarge     = "too_large"
	CodeInternal     = "internal"
)

// WebSocket close codes. The reason of a CloseRateLimited close is the
// number of seconds to wait before reconnecting.
const (
	CloseBadHello     = 4400
	CloseTokenInvalid = 4401
	CloseRevoked      = 4403
	CloseSlowConsumer = 4408
	CloseRateLimited  = 4429
)

// Tunnel kinds (ServerInfo.Tunnel).
const (
	TunnelNone       = ""
	TunnelCloudflare = "cloudflare"
	TunnelTailscale  = "tailscale"
)

// Caps on strings the server sends, in bytes (R12). A field cut to its cap
// has a true *_truncated sibling.
const (
	MaxTextBytes      = 64 << 10
	MaxReasoningBytes = 16 << 10
	MaxOutputBytes    = 8 << 10
	MaxCommandBytes   = 4 << 10
	MaxDiffBytes      = 256 << 10
	MaxErrorMsgRunes  = 200
)

// Connection and pairing limits (§3.6, R3, R4, R11).
const (
	MaxHelloBytes      = 4 << 10
	MaxFrameBytes      = 12 << 20
	HelloTimeout       = 5 * time.Second
	MaxUnauthPerSource = 4
	MaxUnauthTotal     = 32
	MaxInFlight        = 8
	CallTimeout        = 2 * time.Minute
	IdleTimeout        = 75 * time.Second
	PingInterval       = 30 * time.Second
	OutboundQueue      = 256
	MaxPageLimit       = 100
	MaxPairBodyBytes   = 4 << 10
	PairCodeTTL        = 5 * time.Minute
	DeviceKeyExpiry    = 90 * 24 * time.Hour
	IdempotencyKeys    = 256
	IdempotencyTTL     = 10 * time.Minute
)

// Frame is the envelope of every WebSocket text frame. Only the fields of
// its type are set; the per-type groups are embedded pointers so a type's
// required fields are written even when zero and absent from other types.
type Frame struct {
	T string `json:"t"`

	*Hello
	*Welcome

	// req and res.
	ID  string          `json:"id,omitempty"`
	M   string          `json:"m,omitempty"`
	OK  *bool           `json:"ok,omitempty"`
	R   json.RawMessage `json:"r,omitempty"`
	Err *Error          `json:"err,omitempty"`

	*Ev

	// P is the params of a req and the payload of an ev.
	P json.RawMessage `json:"p,omitempty"`
}

// Hello is the first frame a phone sends.
type Hello struct {
	V      int     `json:"v"`
	Token  string  `json:"token"`
	Resume *Resume `json:"resume,omitempty"`
}

// Resume asks for a replay after Seq if Epoch still matches, and restores
// the session the phone had focused.
type Resume struct {
	Epoch string `json:"epoch"`
	Seq   uint64 `json:"seq"`
	Focus string `json:"focus,omitempty"`
}

// Welcome answers a valid hello.
type Welcome struct {
	Epoch  string     `json:"epoch"`
	Replay bool       `json:"replay"`
	Server ServerInfo `json:"server"`
	Device DeviceRef  `json:"device"`
}

// ServerInfo describes the Deck on the other end. Project is a name, never
// a path.
type ServerInfo struct {
	Version   string   `json:"version"`
	Project   string   `json:"project"`
	Kinds     []string `json:"kinds"`
	AllowYolo bool     `json:"allow_yolo"`
	Features  []string `json:"features"`
	ServerID  string   `json:"server_id"`
	Protocol  int      `json:"protocol"`
	Headless  bool     `json:"headless"`
	Tunnel    string   `json:"tunnel"`
	// PushKey is the VAPID public key (base64url); empty when push is off.
	PushKey string `json:"push_key,omitempty"`
	Theme   Theme  `json:"theme"`
}

// DeviceRef names the paired device behind a connection.
type DeviceRef struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	ExpiresAt time.Time `json:"expires_at"`
}

// Ev heads an event frame; Seq is always written.
type Ev struct {
	Seq uint64 `json:"seq"`
	E   string `json:"e"`
}

// Error is a failed response: a code and at most MaxErrorMsgRunes of text
// for the person, never a Go error string.
type Error struct {
	Code string `json:"code"`
	Msg  string `json:"msg"`
}

func (e *Error) Error() string { return e.Code + ": " + e.Msg }

// Theme is the TUI's resolved palette and the tint and mark of each agent
// kind; the phone takes every colour from it.
type Theme struct {
	// Palette maps internal/ui/styles palette tokens to "#rrggbb".
	Palette map[string]string `json:"palette"`
	// Kinds maps an agent kind to its tone.
	Kinds map[string]KindTone `json:"kinds"`
}

// KindTone is one agent kind's colour and mark.
type KindTone struct {
	Tint string `json:"tint"`
	Mark string `json:"mark"`
}

// Modes of a session.
const (
	ModePlan = "plan"
	ModeAsk  = "ask"
	ModeYolo = "yolo"
)

// Session is one chat session.
type Session struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	UpdatedAt time.Time `json:"updated_at"`
	Busy      bool      `json:"busy"`
	Pending   int       `json:"pending"`
	Mode      string    `json:"mode"`
	LastText  string    `json:"last_text,omitempty"`
}

// Message roles.
const (
	RoleUser      = "user"
	RoleAssistant = "assistant"
	RoleSystem    = "system"
)

// Message is one chat row.
type Message struct {
	ID        string    `json:"id"`
	SessionID string    `json:"session_id"`
	Role      string    `json:"role"`
	Agent     string    `json:"agent,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	Finished  bool      `json:"finished"`
	// Editable is true for a message of the user's that message.edit can change.
	Editable bool `json:"editable,omitempty"`
	// Edited is true once the user has rewritten the message.
	Edited bool   `json:"edited,omitempty"`
	Parts  []Part `json:"parts"`
}

// Part types (Part.Type).
const (
	PartText       = "text"
	PartReasoning  = "reasoning"
	PartToolCall   = "tool_call"
	PartToolResult = "tool_result"
	PartImage      = "image"
)

// Part is one piece of a message; the fields used depend on Type.
type Part struct {
	Type string `json:"type"`
	// Text is the body of a text or reasoning part.
	Text          string `json:"text,omitempty"`
	TextTruncated bool   `json:"text_truncated,omitempty"`
	// ID, Name and Summary describe a tool call.
	ID      string `json:"id,omitempty"`
	Name    string `json:"name,omitempty"`
	Summary string `json:"summary,omitempty"`
	// ToolCallID, IsError, Output and Diff describe a tool result.
	ToolCallID      string `json:"tool_call_id,omitempty"`
	IsError         bool   `json:"is_error,omitempty"`
	Output          string `json:"output,omitempty"`
	OutputTruncated bool   `json:"output_truncated,omitempty"`
	Diff            string `json:"diff,omitempty"`
	DiffTruncated   bool   `json:"diff_truncated,omitempty"`
	// Image describes an image part.
	Image *Image `json:"image,omitempty"`
}

// Image is an attached image: a display name, never a path, and its MIME
// type. Data carries the bytes when the phone uploads one.
type Image struct {
	Name string `json:"name"`
	Mime string `json:"mime"`
	Data string `json:"data_b64,omitempty"`
}

// Approval kinds (Approval.Kind).
const (
	ApprovalCommand = "command"
	ApprovalEdit    = "edit"
	ApprovalOther   = "other"
	// ApprovalQuestion is an agent asking the person something (Questions).
	// It is answered with DecisionAnswer and Answers, or declined with
	// DecisionDeny; allow does not answer it.
	ApprovalQuestion = "question"
)

// Decisions of approvals.answer.
const (
	DecisionAllow        = "allow"
	DecisionAllowSession = "allow_session"
	DecisionDeny         = "deny"
	// DecisionAnswer answers a question approval with Answers.
	DecisionAnswer = "answer"
)

// Approval is a pending permission request. Agent is the agent's id.
type Approval struct {
	ID               string `json:"id"`
	SessionID        string `json:"session_id"`
	ToolCallID       string `json:"tool_call_id,omitempty"`
	Agent            string `json:"agent"`
	Kind             string `json:"kind"`
	Description      string `json:"description"`
	Command          string `json:"command,omitempty"`
	CommandTruncated bool   `json:"command_truncated,omitempty"`
	Path             string `json:"path,omitempty"`
	Diff             string `json:"diff,omitempty"`
	DiffTruncated    bool   `json:"diff_truncated,omitempty"`
	Raw              string `json:"raw,omitempty"`
	RawTruncated     bool   `json:"raw_truncated,omitempty"`
	// Questions are what a question approval asks, in order.
	Questions []Question `json:"questions,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
}

// Question is one thing an agent asks. Its answer is keyed by ID. Multi
// lets the person pick several options; Other lets them type their own
// answer.
type Question struct {
	ID          string           `json:"id"`
	Header      string           `json:"header,omitempty"`
	Text        string           `json:"text"`
	Options     []QuestionOption `json:"options,omitempty"`
	MultiSelect bool             `json:"multi_select,omitempty"`
	Other       bool             `json:"other,omitempty"`
}

// QuestionOption is one choice of a Question.
type QuestionOption struct {
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
}

// QuestionAnswer is the person's answer to the Question with ID: the
// labels picked and the text typed in "Other".
type QuestionAnswer struct {
	ID       string   `json:"id"`
	Selected []string `json:"selected,omitempty"`
	Other    string   `json:"other,omitempty"`
}

// QueuedItem is a user message waiting for a busy agent.
type QueuedItem struct {
	ID            string    `json:"id"`
	Agent         string    `json:"agent"`
	Text          string    `json:"text"`
	TextTruncated bool      `json:"text_truncated,omitempty"`
	At            time.Time `json:"at"`
}

// AgentLine is one member of a session's roster; ID is what Approval.Agent
// and agent.interrupt name.
type AgentLine struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Kind   string `json:"kind"`
	Model  string `json:"model,omitempty"`
	Effort string `json:"effort,omitempty"`
	Busy   bool   `json:"busy"`
	// Profile is the named agent the member was started from (absent for
	// none) and Role that profile's one-line role. Name is then the
	// member's own name ("charles", "charles-2"); Kind still says which
	// CLI runs it. Color is the profile's CSS colour (absent for the
	// kind's hue).
	Profile string `json:"profile,omitempty"`
	Role    string `json:"role,omitempty"`
	Color   string `json:"color,omitempty"`
}

// AgentKind is an installed agent CLI that can join a session. Model is
// what a new member of the kind runs; Joined is true when the session has
// a live member of the kind already.
type AgentKind struct {
	Kind   string `json:"kind"`
	Model  string `json:"model,omitempty"`
	Joined bool   `json:"joined"`
}

// ModelChoice is one model an agent kind offers.
type ModelChoice struct {
	ID       string   `json:"id"`
	Label    string   `json:"label"`
	Efforts  []string `json:"efforts,omitempty"`
	Selected bool     `json:"selected,omitempty"`
}

// ChangedFile is one file an agent touched in a session.
type ChangedFile struct {
	Path          string `json:"path"`
	Added         int    `json:"added"`
	Removed       int    `json:"removed"`
	Diff          string `json:"diff,omitempty"`
	DiffTruncated bool   `json:"diff_truncated,omitempty"`
}

// Device is a paired phone. Self marks the caller's own device; AllowYolo
// is the per-device grant of R15.
type Device struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
	LastSeen  time.Time `json:"last_seen,omitzero"`
	ExpiresAt time.Time `json:"expires_at"`
	AllowYolo bool      `json:"allow_yolo"`
	Self      bool      `json:"self,omitempty"`
}

// Method names (Frame.M).
const (
	MSessionsList    = "sessions.list"
	MSessionsCreate  = "sessions.create"
	MSessionsRename  = "sessions.rename"
	MSessionsDelete  = "sessions.delete"
	MSessionsFocus   = "sessions.focus"
	MMessagesList    = "messages.list"
	MChatSend        = "chat.send"
	MMessageEdit     = "message.edit"
	MChatCancel      = "chat.cancel"
	MChatContinue    = "chat.continue"
	MAgentInterrupt  = "agent.interrupt"
	MQueueList       = "queue.list"
	MQueueSteer      = "queue.steer"
	MQueueCancel     = "queue.cancel"
	MRosterGet       = "roster.get"
	MRosterKinds     = "roster.kinds"
	MRosterAdd       = "roster.add"
	MModelsList      = "models.list"
	MModelsSet       = "models.set"
	MRaceStart       = "race.start"
	MModeGet         = "mode.get"
	MModeSet         = "mode.set"
	MApprovalsList   = "approvals.list"
	MApprovalsAnswer = "approvals.answer"
	MChangesGet      = "changes.get"
	MDevicesList     = "devices.list"
	MDevicesRevoke   = "devices.revoke"
	MPushStatus      = "push.status"
	MPushSubscribe   = "push.subscribe"
	MPushUnsubscribe = "push.unsubscribe"
	MPushTest        = "push.test"
)

// Method params and results. Methods without params take Empty; methods
// that return nothing return Empty.
type (
	// Empty is the params or result of a method that carries none.
	Empty struct{}

	// IDParams names one session or device: sessions.delete, sessions.focus
	// ("" unfocuses) and devices.revoke.
	IDParams struct {
		ID string `json:"id"`
	}

	// SessionParams names one session: chat.cancel, chat.continue,
	// queue.list, roster.get, mode.get and changes.get.
	SessionParams struct {
		SessionID string `json:"session_id"`
	}

	// SessionsCreateParams is the params of sessions.create.
	SessionsCreateParams struct {
		Title string `json:"title,omitempty"`
	}

	// SessionsRenameParams is the params of sessions.rename.
	SessionsRenameParams struct {
		ID    string `json:"id"`
		Title string `json:"title"`
	}

	// MessagesListParams is the params of messages.list. Before is a
	// cursor from an earlier result; Limit is at most MaxPageLimit.
	MessagesListParams struct {
		SessionID string `json:"session_id"`
		Before    string `json:"before,omitempty"`
		Limit     int    `json:"limit,omitempty"`
	}

	// MessagesListResult is a page of rows, oldest first. More says older
	// rows exist and Cursor fetches them.
	MessagesListResult struct {
		Messages []Message `json:"messages"`
		More     bool      `json:"more"`
		Cursor   string    `json:"cursor,omitempty"`
	}

	// ChatSendParams is the params of chat.send.
	ChatSendParams struct {
		SessionID      string  `json:"session_id"`
		Text           string  `json:"text"`
		Images         []Image `json:"images,omitempty"`
		IdempotencyKey string  `json:"idempotency_key,omitempty"`
	}

	// ChatSendResult names the message chat.send created.
	ChatSendResult struct {
		MessageID string `json:"message_id"`
	}

	// MessageEditParams is the params of message.edit.
	MessageEditParams struct {
		SessionID string `json:"session_id"`
		MessageID string `json:"message_id"`
		Text      string `json:"text"`
	}

	// AgentInterruptParams is the params of agent.interrupt.
	AgentInterruptParams struct {
		SessionID string `json:"session_id"`
		Agent     string `json:"agent"`
	}

	// QueueItemParams is the params of queue.steer and queue.cancel.
	QueueItemParams struct {
		SessionID string `json:"session_id"`
		ItemID    string `json:"item_id"`
	}

	// ModelsListParams is the params of models.list.
	ModelsListParams struct {
		SessionID string `json:"session_id"`
		Kind      string `json:"kind"`
	}

	// RosterAddParams is the params of roster.add. An empty Model means the
	// model configured for the kind.
	RosterAddParams struct {
		SessionID string `json:"session_id"`
		Kind      string `json:"kind"`
		Model     string `json:"model,omitempty"`
	}

	// RaceStartParams is the params of race.start: its entrants do one
	// brief, each on a worktree of its own. Entrants may mix kinds and
	// repeat one. Judge has one more agent, of the first entrant's kind,
	// rank the entries.
	RaceStartParams struct {
		SessionID string        `json:"session_id"`
		Brief     string        `json:"brief"`
		Entrants  []RaceEntrant `json:"entrants"`
		Judge     bool          `json:"judge,omitempty"`
	}

	// RaceEntrant is one agent of a race: a kind or a named agent's name,
	// and the model and effort it runs ("" for its own).
	RaceEntrant struct {
		Agent  string `json:"agent"`
		Model  string `json:"model,omitempty"`
		Effort string `json:"effort,omitempty"`
	}

	// RaceStartResult is the result of race.start: the race's task.
	RaceStartResult struct {
		TaskID string `json:"task_id"`
	}

	// ModelsSetParams is the params of models.set.
	ModelsSetParams struct {
		SessionID string `json:"session_id"`
		Kind      string `json:"kind"`
		Model     string `json:"model"`
		Effort    string `json:"effort,omitempty"`
	}

	// ModeSetParams is the params of mode.set.
	ModeSetParams struct {
		SessionID string `json:"session_id"`
		Mode      string `json:"mode"`
	}

	// ModeResult is the result of mode.get and mode.set.
	ModeResult struct {
		Mode string `json:"mode"`
	}

	// ApprovalsListParams is the params of approvals.list; no session
	// lists every session's approvals.
	ApprovalsListParams struct {
		SessionID string `json:"session_id,omitempty"`
	}

	// ApprovalsAnswerParams is the params of approvals.answer.
	ApprovalsAnswerParams struct {
		SessionID string `json:"session_id"`
		ID        string `json:"id"`
		Decision  string `json:"decision"`
		// Answers go with DecisionAnswer, one per question answered.
		Answers []QuestionAnswer `json:"answers,omitempty"`
	}

	// ApprovalsAnswerResult says whether the approval was still pending.
	ApprovalsAnswerResult struct {
		Resolved bool `json:"resolved"`
	}

	// PushStatus is the result of push.status.
	PushStatus struct {
		Available bool `json:"available"`
		Enabled   bool `json:"enabled"`
		Approvals bool `json:"approvals"`
		Done      bool `json:"done"`
	}

	// PushSubscription is the browser's PushSubscription.toJSON().
	PushSubscription struct {
		Endpoint string               `json:"endpoint"`
		Keys     PushSubscriptionKeys `json:"keys"`
	}

	// PushSubscriptionKeys are the client keys of a push subscription.
	PushSubscriptionKeys struct {
		P256dh string `json:"p256dh"`
		Auth   string `json:"auth"`
	}

	// PushSubscribeParams is the params of push.subscribe.
	PushSubscribeParams struct {
		Subscription PushSubscription `json:"subscription"`
		Approvals    bool             `json:"approvals"`
		Done         bool             `json:"done"`
	}
)

// Event names (Ev.E).
const (
	EvSessionCreated    = "session.created"
	EvSessionUpdated    = "session.updated"
	EvSessionDeleted    = "session.deleted"
	EvMessageCreated    = "message.created"
	EvMessageUpdated    = "message.updated"
	EvMessageDeleted    = "message.deleted"
	EvApprovalRequested = "approval.requested"
	EvApprovalResolved  = "approval.resolved"
	EvBusy              = "busy"
	EvQueueChanged      = "queue.changed"
	EvRosterChanged     = "roster.changed"
	EvModeChanged       = "mode.changed"
	EvNotice            = "notice"
	EvThemeChanged      = "theme.changed"
	EvDevicePaired      = "device.paired"
	EvDeviceRevoked     = "device.revoked"
)

// Event payloads that are not an entity.
type (
	// MessageDeleted is the payload of message.deleted.
	MessageDeleted struct {
		ID        string `json:"id"`
		SessionID string `json:"session_id"`
	}

	// ApprovalResolved is the payload of approval.resolved.
	ApprovalResolved struct {
		ID        string `json:"id"`
		SessionID string `json:"session_id"`
		Granted   bool   `json:"granted"`
	}

	// BusyChanged is the payload of busy.
	BusyChanged struct {
		SessionID string `json:"session_id"`
		Busy      bool   `json:"busy"`
	}

	// QueueChanged is the payload of queue.changed.
	QueueChanged struct {
		SessionID string       `json:"session_id"`
		Items     []QueuedItem `json:"items"`
	}

	// RosterChanged is the payload of roster.changed.
	RosterChanged struct {
		SessionID string `json:"session_id"`
	}

	// ModeChanged is the payload of mode.changed.
	ModeChanged struct {
		SessionID string `json:"session_id"`
		Mode      string `json:"mode"`
	}

	// Notice is the payload of notice.
	Notice struct {
		SessionID string `json:"session_id"`
		Text      string `json:"text"`
		// Via is for the desktop's gRPC (deck.v1 Notice.via); the phone
		// protocol leaves it out.
		Via string `json:"-"`
	}

	// DeviceRevoked is the payload of device.revoked.
	DeviceRevoked struct {
		ID string `json:"id"`
	}
)

// Pairing HTTP bodies (§3.5).
type (
	// Health is the body of GET /api/v1/health.
	Health struct {
		OK       bool `json:"ok"`
		Protocol int  `json:"protocol"`
	}

	// PairRequest is the body of POST /api/v1/pair.
	PairRequest struct {
		Code       string `json:"code"`
		DeviceName string `json:"device_name"`
	}

	// PairResponse is returned once; the token is never retrievable again.
	PairResponse struct {
		Token     string    `json:"token"`
		DeviceID  string    `json:"device_id"`
		ServerID  string    `json:"server_id"`
		ExpiresAt time.Time `json:"expires_at"`
	}

	// PairError is the body of a failed POST /api/v1/pair.
	PairError struct {
		Error string `json:"error"`
	}
)

// Validate checks what the contract constrains: V must be Protocol and
// Token present. A failure closes the connection with CloseBadHello.
func (h *Hello) Validate() error {
	if h == nil {
		return invalid("missing hello")
	}
	if h.V != Protocol {
		return invalid("unsupported protocol version")
	}
	if h.Token == "" {
		return invalid("missing token")
	}
	return nil
}

// Validate checks that Mode is one of the modes.
func (p ModeSetParams) Validate() error {
	switch p.Mode {
	case ModePlan, ModeAsk, ModeYolo:
		return nil
	}
	return invalid("unknown mode")
}

// Validate checks that Decision is one of the decisions.
func (p ApprovalsAnswerParams) Validate() error {
	switch p.Decision {
	case DecisionAllow, DecisionAllowSession, DecisionDeny:
		return nil
	case DecisionAnswer:
		if len(p.Answers) == 0 {
			return invalid("answer needs answers")
		}
		return nil
	}
	return invalid("unknown decision")
}

// Validate checks that Limit is not negative and at most MaxPageLimit.
func (p MessagesListParams) Validate() error {
	if p.Limit < 0 || p.Limit > MaxPageLimit {
		return invalid("limit out of range")
	}
	return nil
}

func invalid(msg string) error { return &Error{Code: CodeInvalid, Msg: msg} }

// Truncate cuts s to at most limit bytes on a rune boundary and reports
// whether it cut anything.
func Truncate(s string, limit int) (string, bool) {
	if limit < 0 {
		limit = 0
	}
	if len(s) <= limit {
		return s, false
	}
	for limit > 0 && !utf8.RuneStart(s[limit]) {
		limit--
	}
	return s[:limit], true
}

// Fixture names one file in testdata (Name + ".json") and the type it
// decodes into.
type Fixture struct {
	Name string
	Zero any
}

// Fixtures lists every fixture the protocol declares: one per frame type,
// entity, event payload and method params and result, and the pairing HTTP
// bodies. The tests fail when a listed file is missing or an unlisted one
// exists.
func Fixtures() []Fixture {
	return []Fixture{
		// Frames.
		{"frame_hello", Frame{}},
		{"frame_welcome", Frame{}},
		{"frame_req", Frame{}},
		{"frame_res", Frame{}},
		{"frame_res_err", Frame{}},
		{"frame_ev", Frame{}},
		{"frame_resync", Frame{}},

		// Entities.
		{"entity_session", Session{}},
		{"entity_message", Message{}},
		{"entity_approval", Approval{}},
		{"entity_approval_edit", Approval{}},
		{"entity_approval_question", Approval{}},
		{"entity_queued_item", QueuedItem{}},
		{"entity_agent_line", AgentLine{}},
		{"entity_model_choice", ModelChoice{}},
		{"entity_changed_file", ChangedFile{}},
		{"entity_device", Device{}},
		{"entity_theme", Theme{}},

		// Event payloads.
		{"event_session.created", Session{}},
		{"event_session.updated", Session{}},
		{"event_session.deleted", Session{}},
		{"event_message.created", Message{}},
		{"event_message.updated", Message{}},
		{"event_message.deleted", MessageDeleted{}},
		{"event_approval.requested", Approval{}},
		{"event_approval.resolved", ApprovalResolved{}},
		{"event_busy", BusyChanged{}},
		{"event_queue.changed", QueueChanged{}},
		{"event_roster.changed", RosterChanged{}},
		{"event_mode.changed", ModeChanged{}},
		{"event_notice", Notice{}},
		{"event_theme.changed", Theme{}},
		{"event_device.paired", Device{}},
		{"event_device.revoked", DeviceRevoked{}},

		// Method params and results.
		{"req_sessions.list", Empty{}},
		{"res_sessions.list", []Session{}},
		{"req_sessions.create", SessionsCreateParams{}},
		{"res_sessions.create", Session{}},
		{"req_sessions.rename", SessionsRenameParams{}},
		{"res_sessions.rename", Empty{}},
		{"req_sessions.delete", IDParams{}},
		{"res_sessions.delete", Empty{}},
		{"req_sessions.focus", IDParams{}},
		{"res_sessions.focus", Empty{}},
		{"req_messages.list", MessagesListParams{}},
		{"res_messages.list", MessagesListResult{}},
		{"req_chat.send", ChatSendParams{}},
		{"res_chat.send", ChatSendResult{}},
		{"req_message.edit", MessageEditParams{}},
		{"res_message.edit", Empty{}},
		{"req_chat.cancel", SessionParams{}},
		{"res_chat.cancel", Empty{}},
		{"req_chat.continue", SessionParams{}},
		{"res_chat.continue", Empty{}},
		{"req_agent.interrupt", AgentInterruptParams{}},
		{"res_agent.interrupt", Empty{}},
		{"req_queue.list", SessionParams{}},
		{"res_queue.list", []QueuedItem{}},
		{"req_queue.steer", QueueItemParams{}},
		{"res_queue.steer", Empty{}},
		{"req_queue.cancel", QueueItemParams{}},
		{"res_queue.cancel", Empty{}},
		{"req_roster.get", SessionParams{}},
		{"res_roster.get", []AgentLine{}},
		{"req_roster.kinds", SessionParams{}},
		{"res_roster.kinds", []AgentKind{}},
		{"req_roster.add", RosterAddParams{}},
		{"res_roster.add", AgentLine{}},
		{"req_models.list", ModelsListParams{}},
		{"res_models.list", []ModelChoice{}},
		{"req_models.set", ModelsSetParams{}},
		{"res_models.set", Empty{}},
		{"req_race.start", RaceStartParams{}},
		{"res_race.start", RaceStartResult{}},
		{"req_mode.get", SessionParams{}},
		{"res_mode.get", ModeResult{}},
		{"req_mode.set", ModeSetParams{}},
		{"res_mode.set", ModeResult{}},
		{"req_approvals.list", ApprovalsListParams{}},
		{"res_approvals.list", []Approval{}},
		{"req_approvals.answer", ApprovalsAnswerParams{}},
		{"req_approvals.answer_question", ApprovalsAnswerParams{}},
		{"res_approvals.answer", ApprovalsAnswerResult{}},
		{"req_changes.get", SessionParams{}},
		{"res_changes.get", []ChangedFile{}},
		{"req_devices.list", Empty{}},
		{"res_devices.list", []Device{}},
		{"req_devices.revoke", IDParams{}},
		{"res_devices.revoke", Empty{}},
		{"req_push.status", Empty{}},
		{"res_push.status", PushStatus{}},
		{"req_push.subscribe", PushSubscribeParams{}},
		{"res_push.subscribe", Empty{}},
		{"req_push.unsubscribe", Empty{}},
		{"res_push.unsubscribe", Empty{}},
		{"req_push.test", Empty{}},
		{"res_push.test", Empty{}},

		// Pairing HTTP bodies.
		{"http_health", Health{}},
		{"http_pair_request", PairRequest{}},
		{"http_pair_response", PairResponse{}},
		{"http_pair_error", PairError{}},
	}
}
