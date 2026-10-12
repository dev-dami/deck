// Package wire defines the JSON protocol between `deck serve` and its phone
// clients. Nothing here may import Deck internals: the types are the contract,
// and wire/testdata holds golden fixtures that the web client's tests parse too.
package wire

import "encoding/json"

// ProtocolVersion is bumped on any change an old client cannot ignore.
const ProtocolVersion = 1

// Frame types.
const (
	FrameHello   = "hello"
	FrameWelcome = "welcome"
	FrameReq     = "req"
	FrameRes     = "res"
	FrameEv      = "ev"
	FrameResync  = "resync"
)

// Error codes carried in Error.Code.
const (
	CodeUnauthorized = "unauthorized"
	CodeForbidden    = "forbidden"
	CodeNotFound     = "not_found"
	CodeInvalid      = "invalid"
	CodeYoloDisabled = "yolo_disabled"
	CodeBusy         = "busy"
	CodeTooLarge     = "too_large"
	CodeInternal     = "internal"
)

// WebSocket close codes.
const (
	CloseUnauthorized = 4401
	CloseRevoked      = 4403
	CloseSlowConsumer = 4408
)

// Frame is the envelope of every WebSocket message. Only the fields of its
// type are set.
type Frame struct {
	T string `json:"t"`

	// hello
	V      int     `json:"v,omitempty"`
	Token  string  `json:"token,omitempty"`
	Resume *Resume `json:"resume,omitempty"`

	// welcome
	Welcome

	// req / res
	ID  string          `json:"id,omitempty"`
	M   string          `json:"m,omitempty"`
	P   json.RawMessage `json:"p,omitempty"`
	OK  *bool           `json:"ok,omitempty"`
	R   json.RawMessage `json:"r,omitempty"`
	Err *Error          `json:"err,omitempty"`

	// ev
	Seq uint64 `json:"seq,omitempty"`
	E   string `json:"e,omitempty"`
}

// Resume asks the server to replay events after Seq if Epoch still matches.
type Resume struct {
	Epoch string `json:"epoch"`
	Seq   uint64 `json:"seq"`
}

// Welcome answers a valid hello.
type Welcome struct {
	Epoch  string      `json:"epoch,omitempty"`
	Replay bool        `json:"replay,omitempty"`
	Server *ServerInfo `json:"server,omitempty"`
	Device *DeviceRef  `json:"device,omitempty"`
}

// ServerInfo describes the Deck on the other end.
type ServerInfo struct {
	Version    string   `json:"version"`
	Project    string   `json:"project"`
	Cwd        string   `json:"cwd"`
	Kinds      []string `json:"kinds"`
	AllowYolo  bool     `json:"allow_yolo"`
	Features   []string `json:"features,omitempty"`
	ServerID   string   `json:"server_id,omitempty"`
	ProtocolV  int      `json:"protocol"`
	Headless   bool     `json:"headless"`
	TunnelKind string   `json:"tunnel,omitempty"`
	// PushKey is the VAPID public key (base64url) a phone passes to
	// pushManager.subscribe; empty when push is unavailable.
	PushKey string `json:"push_key,omitempty"`
}

// DeviceRef names the paired device behind a connection.
type DeviceRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Error is a failed response.
type Error struct {
	Code string `json:"code"`
	Msg  string `json:"msg"`
}

func (e *Error) Error() string { return e.Code + ": " + e.Msg }

// PairRequest is the body of POST /api/v1/pair.
type PairRequest struct {
	Code       string `json:"code"`
	DeviceName string `json:"device_name"`
}

// PairResponse is returned once; the token is never retrievable again.
type PairResponse struct {
	Token    string `json:"token"`
	DeviceID string `json:"device_id"`
	ServerID string `json:"server_id,omitempty"`
}

// Health is the unauthenticated GET /api/v1/health body.
type Health struct {
	OK bool `json:"ok"`
	V  int  `json:"v"`
}

// Session is one chat session in the list.
type Session struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	UpdatedAt int64  `json:"updated_at"`
	Busy      bool   `json:"busy"`
	Pending   int    `json:"pending"`
	LastText  string `json:"last_text,omitempty"`
}

// Part types inside Message.Parts.
const (
	PartText       = "text"
	PartReasoning  = "reasoning"
	PartToolCall   = "tool_call"
	PartToolResult = "tool_result"
	PartImage      = "image"
)

// Part is one piece of a message; the fields used depend on Type.
type Part struct {
	Type       string `json:"type"`
	Text       string `json:"text,omitempty"`
	ID         string `json:"id,omitempty"`
	Name       string `json:"name,omitempty"`
	Summary    string `json:"summary,omitempty"`
	ToolCallID string `json:"tool_call_id,omitempty"`
	IsError    bool   `json:"is_error,omitempty"`
	Output     string `json:"output,omitempty"`
	Truncated  bool   `json:"truncated,omitempty"`
	Diff       string `json:"diff,omitempty"`
	Mime       string `json:"mime,omitempty"`
}

// Message roles.
const (
	RoleUser      = "user"
	RoleAssistant = "assistant"
	RoleSystem    = "system"
)

// Message is one chat row.
type Message struct {
	ID        string `json:"id"`
	SessionID string `json:"session_id"`
	Role      string `json:"role"`
	Agent     string `json:"agent,omitempty"`
	CreatedAt int64  `json:"created_at"`
	UpdatedAt int64  `json:"updated_at"`
	Finished  bool   `json:"finished"`
	// Editable is true for a message of the user's that message.edit can change.
	Editable bool `json:"editable,omitempty"`
	// Edited is true once the user has rewritten the message.
	Edited bool   `json:"edited,omitempty"`
	Parts  []Part `json:"parts"`
}

// Approval kinds.
const (
	ApprovalCommand = "command"
	ApprovalEdit    = "edit"
	ApprovalOther   = "other"
)

// Approval decisions for approvals.answer.
const (
	DecisionAllow        = "allow"
	DecisionAllowSession = "allow_session"
	DecisionDeny         = "deny"
)

// Approval is a pending permission request.
type Approval struct {
	ID          string `json:"id"`
	SessionID   string `json:"session_id"`
	ToolCallID  string `json:"tool_call_id,omitempty"`
	Agent       string `json:"agent,omitempty"`
	Kind        string `json:"kind"`
	Description string `json:"description"`
	Command     string `json:"command,omitempty"`
	Cwd         string `json:"cwd,omitempty"`
	Path        string `json:"path,omitempty"`
	Diff        string `json:"diff,omitempty"`
	Raw         string `json:"raw,omitempty"`
	CreatedAt   int64  `json:"created_at"`
}

// Modes accepted by mode.set.
const (
	ModePlan = "plan"
	ModeAsk  = "ask"
	ModeYolo = "yolo"
)

// QueuedItem is a user message waiting for a busy agent.
type QueuedItem struct {
	ID    string `json:"id"`
	Agent string `json:"agent"`
	Text  string `json:"text"`
	At    int64  `json:"at"`
}

// AgentLine is one member of a session's roster.
type AgentLine struct {
	Name   string `json:"name"`
	Kind   string `json:"kind"`
	Model  string `json:"model,omitempty"`
	Effort string `json:"effort,omitempty"`
	Busy   bool   `json:"busy"`
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
	Path      string `json:"path"`
	Added     int    `json:"added"`
	Removed   int    `json:"removed"`
	Diff      string `json:"diff,omitempty"`
	Truncated bool   `json:"truncated,omitempty"`
}

// Device is a paired phone as shown in the device list.
type Device struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	CreatedAt int64  `json:"created_at"`
	LastSeen  int64  `json:"last_seen"`
	Current   bool   `json:"current,omitempty"`
}

// Event names.
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
	EvRosterChanged     = "roster.changed"
	EvQueueChanged      = "queue.changed"
	EvModeChanged       = "mode.changed"
	EvNotice            = "notice"
	EvDeviceRevoked     = "device.revoked"
)

// Method names.
const (
	MSessionsList    = "sessions.list"
	MSessionsCreate  = "sessions.create"
	MSessionsRename  = "sessions.rename"
	MSessionsDelete  = "sessions.delete"
	MSessionsFocus   = "sessions.focus"
	MMessagesList    = "messages.list"
	MChatSend        = "chat.send"
	MMessageEdit     = "message.edit"
	MPushStatus      = "push.status"
	MPushSubscribe   = "push.subscribe"
	MPushUnsubscribe = "push.unsubscribe"
	MPushTest        = "push.test"
	MChatCancel      = "chat.cancel"
	MAgentInterrupt  = "agent.interrupt"
	MChatContinue    = "chat.continue"
	MQueueList       = "queue.list"
	MQueueSteer      = "queue.steer"
	MQueueCancel     = "queue.cancel"
	MRosterGet       = "roster.get"
	MModelsList      = "models.list"
	MModelsSet       = "models.set"
	MModeGet         = "mode.get"
	MModeSet         = "mode.set"
	MApprovalsList   = "approvals.list"
	MApprovalsAnswer = "approvals.answer"
	MChangesGet      = "changes.get"
	MDevicesList     = "devices.list"
	MDevicesRevoke   = "devices.revoke"
)

// Limits the server enforces and the client should respect.
const (
	MaxImageBytes  = 8 << 20
	MaxFrameBytes  = 12 << 20
	MaxOutputBytes = 8 << 10
	MaxDiffBytes   = 256 << 10
	MaxRawBytes    = 4 << 10
)

// Request/response bodies for methods that take or return more than a list.
type (
	SessionsCreateReq struct {
		Title string `json:"title,omitempty"`
	}
	SessionsRenameReq struct {
		ID    string `json:"id"`
		Title string `json:"title"`
	}
	// SessionIDReq is the body of methods that act on one session's state.
	SessionIDReq struct {
		SessionID string `json:"session_id"`
	}
	SessionRef struct {
		ID string `json:"id"`
	}
	// SessionsFocusReq with an empty ID unfocuses.
	SessionsFocusReq struct {
		ID string `json:"id"`
	}
	MessagesListReq struct {
		SessionID string `json:"session_id"`
		Before    int64  `json:"before,omitempty"`
		Limit     int    `json:"limit,omitempty"`
	}
	// MessagesListRes is a page of rows, oldest first; More says older rows exist.
	MessagesListRes struct {
		Messages []Message `json:"messages"`
		More     bool      `json:"more"`
	}
	ImageUpload struct {
		Name string `json:"name"`
		Mime string `json:"mime"`
		Data string `json:"data_b64"`
	}
	ChatSendReq struct {
		SessionID      string        `json:"session_id"`
		Text           string        `json:"text"`
		Images         []ImageUpload `json:"images,omitempty"`
		IdempotencyKey string        `json:"idempotency_key,omitempty"`
	}
	// MessageEditReq replaces the text of one of the user's earlier messages.
	MessageEditReq struct {
		SessionID string `json:"session_id"`
		MessageID string `json:"message_id"`
		Text      string `json:"text"`
	}
	// PushSubscription is the browser's PushSubscription.toJSON().
	PushSubscription struct {
		Endpoint string `json:"endpoint"`
		Keys     struct {
			P256dh string `json:"p256dh"`
			Auth   string `json:"auth"`
		} `json:"keys"`
	}
	// PushSubscribeReq registers this phone for notifications.
	PushSubscribeReq struct {
		Subscription PushSubscription `json:"subscription"`
		Approvals    bool             `json:"approvals"`
		Done         bool             `json:"done"`
	}
	// PushStatus says whether this phone is subscribed and for what.
	PushStatus struct {
		Available bool `json:"available"`
		Enabled   bool `json:"enabled"`
		Approvals bool `json:"approvals"`
		Done      bool `json:"done"`
	}
	AgentInterruptReq struct {
		SessionID string `json:"session_id"`
		Agent     string `json:"agent"`
	}
	QueueItemRef struct {
		ItemID string `json:"item_id"`
	}
	ModelsSetReq struct {
		SessionID string `json:"session_id"`
		Kind      string `json:"kind"`
		Model     string `json:"model"`
		Effort    string `json:"effort,omitempty"`
	}
	ModelsListReq struct {
		SessionID string `json:"session_id"`
		Kind      string `json:"kind"`
	}
	ModeBody struct {
		Mode string `json:"mode"`
	}
	ApprovalsAnswerReq struct {
		ID       string `json:"id"`
		Decision string `json:"decision"`
	}
	ApprovalsAnswerRes struct {
		Resolved bool `json:"resolved"`
	}
	ChangesGetReq struct {
		SessionID string `json:"session_id"`
	}
	DevicesRevokeReq struct {
		ID string `json:"id"`
	}
)
