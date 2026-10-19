package wire

import (
	"encoding/json"
	"time"
)

// ProtocolV3 is the version of the Noise-secured, multi-project channel of
// docs/mobile-contract.md. It reuses every v2 method, event and entity; the
// types below are what it adds or changes. v2 stays frozen.
const ProtocolV3 = 3

// Error codes added in v3 (Error.Code).
const (
	CodeAssertionRequired = "assertion_required"
	CodeAssertionInvalid  = "assertion_invalid"
	CodeExpired           = "expired"
)

// Limits of v3 (§4, §5 of the contract).
const (
	MaxQueuedPerDevice    = 20
	DefaultQueueExpiry    = 8 * time.Hour
	MaxQueueExpiry        = 24 * time.Hour
	AppliedQIDRetention   = 7 * 24 * time.Hour
	MaxSnapshotBytes      = 256 << 10
	MaxSnapshotReceipts   = 50
	MaxInboxSummaryRunes  = 280
	MaxChangeSummaryBytes = 4 << 10
	MaxPushPayloadBytes   = 3 << 10
	QueueVetoWindow       = 30 * time.Second
	PairFailuresToBurn    = 3
	NoiseRekeyMessages    = 10000
	NoiseRekeyInterval    = time.Hour
	ClockSkewTolerance    = 5 * time.Minute
)

// Features announced in WelcomeV3.Server.Features beyond the v2 ones.
const (
	FeatureInbox        = "inbox"
	FeatureOfflineQueue = "offline_queue"
	FeaturePasskey      = "passkey"
	FeaturePushE2E      = "push_e2e"
)

// Computer-level methods (no pj).
const (
	MProjectsList     = "projects.list"
	MInboxGet         = "inbox.get"
	MInboxAck         = "inbox.ack"
	MPasskeyBegin     = "passkey.begin"
	MPasskeyFinish    = "passkey.finish"
	MPasskeyList      = "passkey.list"
	MPasskeyRemove    = "passkey.remove"
	MAssertChallenge  = "assert.challenge"
	MQueueStatus      = "queue.status"
	MPushRegister     = "push.register"
	EvInboxChanged    = "inbox.changed"
	EvReceiptsChanged = "receipts.changed"
)

// Approval tiers (ApprovalV3.Tier).
const (
	TierNormal      = "normal"
	TierDestructive = "destructive"
)

// Inbox states (InboxItem.State).
const (
	InboxNeedsYou = "needs_you"
	InboxRunning  = "running"
	InboxDone     = "done"
)

// Frame3 is the envelope inside the Noise channel. It differs from Frame in
// carrying the project id (PJ) and in having no token in the hello.
type Frame3 struct {
	T string `json:"t"`

	*Hello3
	*Welcome3

	ID  string          `json:"id,omitempty"`
	M   string          `json:"m,omitempty"`
	PJ  string          `json:"pj,omitempty"`
	OK  *bool           `json:"ok,omitempty"`
	R   json.RawMessage `json:"r,omitempty"`
	Err *Error          `json:"err,omitempty"`

	*Ev

	P json.RawMessage `json:"p,omitempty"`
}

// Hello3 is the first frame the phone sends once the Noise handshake is
// done. The device is known from the handshake's static key.
type Hello3 struct {
	V      int      `json:"v"`
	Resume *Resume3 `json:"resume,omitempty"`
}

// Resume3 asks for a replay after Seq if Epoch still matches, and restores
// the focused session.
type Resume3 struct {
	Epoch string    `json:"epoch"`
	Seq   uint64    `json:"seq"`
	Focus *FocusRef `json:"focus,omitempty"`
}

// FocusRef names one session of one project.
type FocusRef struct {
	PJ        string `json:"project_id"`
	SessionID string `json:"session_id"`
}

// Welcome3 answers a hello.
type Welcome3 struct {
	Epoch  string      `json:"epoch"`
	Replay bool        `json:"replay"`
	Server ServerInfo3 `json:"server"`
	Device DeviceRef   `json:"device"`
}

// ServerInfo3 describes the computer. CID is the computer id; Projects lists
// the enabled projects by id and name, never by path.
type ServerInfo3 struct {
	Version   string       `json:"version"`
	CID       string       `json:"cid"`
	Name      string       `json:"name"`
	Projects  []ProjectRef `json:"projects"`
	Kinds     []string     `json:"kinds"`
	AllowYolo bool         `json:"allow_yolo"`
	Features  []string     `json:"features"`
	Protocol  int          `json:"protocol"`
	Tunnel    string       `json:"tunnel"`
	PushKey   string       `json:"push_key,omitempty"`
	Theme     Theme        `json:"theme"`
}

// ProjectRef is one project a computer exposes.
type ProjectRef struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Online bool   `json:"online"`
}

// InboxItem is one session as the inbox shows it.
type InboxItem struct {
	PJ           string    `json:"pj"`
	SessionID    string    `json:"session_id"`
	Title        string    `json:"title"`
	State        string    `json:"state"`
	Approvals    int       `json:"approvals"`
	LastActivity time.Time `json:"last_activity"`
	Summary      string    `json:"summary"`
	Unread       bool      `json:"unread"`
}

// ApprovalV3 is an Approval with the tier the computer assigned and the
// digest a queued answer binds to (sha256 of kind|command|path|diff, hex).
type ApprovalV3 struct {
	Approval
	Tier   string `json:"tier"`
	Digest string `json:"digest"`
}

// Assertion is a WebAuthn assertion; every field is base64url.
type Assertion struct {
	CredentialID      string `json:"credential_id"`
	AuthenticatorData string `json:"authenticator_data"`
	ClientDataJSON    string `json:"client_data_json"`
	Signature         string `json:"signature"`
}

// Method params and results added or changed in v3.
type (
	// InboxAckParams is the params of inbox.ack.
	InboxAckParams struct {
		PJ        string `json:"pj"`
		SessionID string `json:"session_id"`
	}

	// ApprovalsAnswer3Params is approvals.answer with a deny reason and the
	// assertion a destructive allow needs.
	ApprovalsAnswer3Params struct {
		SessionID string     `json:"session_id"`
		ID        string     `json:"id"`
		Decision  string     `json:"decision"`
		Reason    string     `json:"reason,omitempty"`
		Assertion *Assertion `json:"assertion,omitempty"`
	}

	// ModeSet3Params is mode.set with the assertion yolo needs.
	ModeSet3Params struct {
		SessionID string     `json:"session_id"`
		Mode      string     `json:"mode"`
		Assertion *Assertion `json:"assertion,omitempty"`
	}

	// ChangesGet3Params is changes.get; Summary asks for per-file counts and
	// the first hunk, at most MaxChangeSummaryBytes.
	ChangesGet3Params struct {
		SessionID string `json:"session_id"`
		Summary   bool   `json:"summary,omitempty"`
	}

	// PasskeyBeginResult is what the phone passes to navigator.credentials.create.
	PasskeyBeginResult struct {
		Challenge string `json:"challenge"`
		RPID      string `json:"rp_id"`
		UserID    string `json:"user_id"`
	}

	// PasskeyFinishParams carries the credential of an attestation "none".
	PasskeyFinishParams struct {
		CredentialID      string `json:"credential_id"`
		ClientDataJSON    string `json:"client_data_json"`
		AttestationObject string `json:"attestation_object"`
	}

	// PasskeyInfo is one registered passkey.
	PasskeyInfo struct {
		ID        string    `json:"id"`
		CreatedAt time.Time `json:"created_at"`
	}

	// PasskeyRemoveParams is the params of passkey.remove.
	PasskeyRemoveParams struct {
		ID string `json:"id"`
	}

	// AssertChallengeParams asks for a nonce to sign over ActionDigest.
	AssertChallengeParams struct {
		ActionDigest string `json:"action_digest"`
	}

	// AssertChallengeResult carries the server-issued nonce.
	AssertChallengeResult struct {
		Nonce string `json:"nonce"`
	}

	// PushRegisterParams hands the computer the keys it needs to encrypt a
	// push (RFC 8291); the endpoint goes to the relay, not here.
	PushRegisterParams struct {
		P256dh    string `json:"p256dh"`
		Auth      string `json:"auth"`
		Approvals bool   `json:"approvals"`
		Done      bool   `json:"done"`
	}
)

// Queued action kinds (QueuedAction.Kind).
const (
	QKindChatSend     = "chat.send"
	QKindSessionNew   = "session.new"
	QKindChatContinue = "chat.continue"
	QKindModelsSet    = "models.set"
	QKindApproval     = "approvals.answer"
	QKindCancel       = "queue.cancel"
)

// Receipt statuses (Receipt.Status).
const (
	ReceiptApplied = "applied"
	ReceiptDropped = "dropped"
	ReceiptFailed  = "failed"
)

// Receipt reasons (Receipt.Reason) a phone can rely on.
const (
	ReasonExpired         = "expired"
	ReasonSessionGone     = "session_gone"
	ReasonApprovalGone    = "approval_gone"
	ReasonDigestMismatch  = "digest_mismatch"
	ReasonCancelled       = "cancelled"
	ReasonVetoed          = "vetoed"
	ReasonBusy            = "busy"
	ReasonProjectDisabled = "project_disabled"
	ReasonAssertion       = "assertion_invalid"
	ReasonDuplicate       = "duplicate"
)

type (
	// QueuedAction is an action taken while the computer was away; it is
	// sealed (Noise K) before it leaves the phone.
	QueuedAction struct {
		QID       string          `json:"qid"`
		DeviceID  string          `json:"device_id"`
		CreatedAt time.Time       `json:"created_at"`
		ExpiresAt time.Time       `json:"expires_at"`
		PJ        string          `json:"pj,omitempty"`
		SessionID string          `json:"session_id,omitempty"`
		Kind      string          `json:"kind"`
		Payload   json.RawMessage `json:"payload"`
		Precond   *Precond        `json:"precond,omitempty"`
		Assertion *Assertion      `json:"assertion,omitempty"`
	}

	// Precond binds a queued approval to the exact request the person saw.
	Precond struct {
		ApprovalID string `json:"approval_id"`
		Digest     string `json:"digest"`
	}

	// Receipt is the computer's account of one queued action.
	Receipt struct {
		QID    string    `json:"qid"`
		Status string    `json:"status"`
		Reason string    `json:"reason,omitempty"`
		At     time.Time `json:"at"`
	}

	// QueueCancelPayload is the payload of a QKindCancel action.
	QueueCancelPayload struct {
		QID string `json:"qid"`
	}

	// SessionNewPayload is the payload of a QKindSessionNew action.
	SessionNewPayload struct {
		Title  string `json:"title,omitempty"`
		Text   string `json:"text"`
		Kind   string `json:"kind,omitempty"`
		Model  string `json:"model,omitempty"`
		Effort string `json:"effort,omitempty"`
	}
)

// Pairing (QR blob and the payloads inside the pairing handshake).
type (
	// QRBlob is what the QR code carries after "#p=" (base64url JSON).
	QRBlob struct {
		V     int      `json:"v"`
		CID   string   `json:"cid"`
		Relay string   `json:"relay"`
		S     string   `json:"s"`
		PSK   string   `json:"psk"`
		Exp   int64    `json:"exp"`
		Name  string   `json:"name"`
		LAN   []string `json:"lan,omitempty"`
		FP    string   `json:"fp,omitempty"`
	}

	// PairHello is the phone's payload in the first pairing message.
	PairHello struct {
		DeviceName string `json:"device_name"`
	}

	// PairWelcome is the computer's payload in the second pairing message.
	PairWelcome struct {
		DeviceID   string    `json:"device_id"`
		RelayToken string    `json:"relay_token"`
		ExpiresAt  time.Time `json:"expires_at"`
		Name       string    `json:"name"`
	}
)

// Push message kinds (PushMessage.Kind).
const (
	PushApproval = "approval"
	PushDone     = "done"
)

// PushMessage is the plaintext of an end-to-end encrypted web push: what the
// service worker shows on the lock screen. For a normal-tier approval it also
// carries what the worker needs to answer (approval id and digest).
type PushMessage struct {
	CID        string    `json:"cid,omitempty"`
	Kind       string    `json:"kind"`
	PJ         string    `json:"pj"`
	SessionID  string    `json:"session_id"`
	Title      string    `json:"title"`
	Body       string    `json:"body"`
	ApprovalID string    `json:"approval_id,omitempty"`
	Digest     string    `json:"digest,omitempty"`
	Tier       string    `json:"tier,omitempty"`
	At         time.Time `json:"at"`
}

// Snapshot is the encrypted state a computer leaves on the relay for one
// device; the phone shows it greyed while the computer is away.
type Snapshot struct {
	At        time.Time         `json:"at"`
	Inbox     []InboxItem       `json:"inbox"`
	Approvals []PendingApproval `json:"approvals"`
	Receipts  []Receipt         `json:"receipts"`
}

// PendingApproval is an approval with its project.
type PendingApproval struct {
	PJ       string     `json:"pj"`
	Approval ApprovalV3 `json:"approval"`
}

// FixturesV3 lists the v3 fixtures (prefix "v3_"); the same rules as
// Fixtures apply.
func FixturesV3() []Fixture {
	return []Fixture{
		{"v3_frame_hello", Frame3{}},
		{"v3_frame_welcome", Frame3{}},
		{"v3_frame_req", Frame3{}},
		{"v3_frame_res_err", Frame3{}},
		{"v3_frame_ev", Frame3{}},
		{"v3_entity_inbox_item", InboxItem{}},
		{"v3_entity_approval", ApprovalV3{}},
		{"v3_entity_assertion", Assertion{}},
		{"v3_entity_queued_action", QueuedAction{}},
		{"v3_entity_queued_approval", QueuedAction{}},
		{"v3_entity_receipt", Receipt{}},
		{"v3_entity_snapshot", Snapshot{}},
		{"v3_entity_qr_blob", QRBlob{}},
		{"v3_pair_hello", PairHello{}},
		{"v3_pair_welcome", PairWelcome{}},
		{"v3_req_inbox.ack", InboxAckParams{}},
		{"v3_req_approvals.answer", ApprovalsAnswer3Params{}},
		{"v3_req_mode.set", ModeSet3Params{}},
		{"v3_req_changes.get", ChangesGet3Params{}},
		{"v3_res_passkey.begin", PasskeyBeginResult{}},
		{"v3_req_passkey.finish", PasskeyFinishParams{}},
		{"v3_res_passkey.list", []PasskeyInfo{}},
		{"v3_req_assert.challenge", AssertChallengeParams{}},
		{"v3_res_assert.challenge", AssertChallengeResult{}},
		{"v3_req_push.register", PushRegisterParams{}},
		{"v3_res_projects.list", []ProjectRef{}},
		{"v3_res_inbox.get", []InboxItem{}},
		{"v3_res_queue.status", []Receipt{}},
		{"v3_event_inbox.changed", []InboxItem{}},
		{"v3_push_approval", PushMessage{}},
		{"v3_push_done", PushMessage{}},
	}
}

// Validate checks that V is ProtocolV3.
func (h *Hello3) Validate() error {
	if h == nil {
		return invalid("missing hello")
	}
	if h.V != ProtocolV3 {
		return invalid("unsupported protocol version")
	}
	return nil
}

// Validate checks that the action is one that may be queued and carries what
// its kind needs. The queue matrix of contract §4 is enforced here so both
// sides share it.
func (a QueuedAction) Validate() error {
	if a.QID == "" || a.DeviceID == "" {
		return invalid("missing qid or device")
	}
	if !a.ExpiresAt.After(a.CreatedAt) || a.ExpiresAt.Sub(a.CreatedAt) > MaxQueueExpiry {
		return invalid("expiry out of range")
	}
	switch a.Kind {
	case QKindChatSend, QKindChatContinue, QKindModelsSet:
		if a.PJ == "" || a.SessionID == "" {
			return invalid("missing session")
		}
	case QKindSessionNew:
		if a.PJ == "" {
			return invalid("missing project")
		}
	case QKindApproval:
		if a.PJ == "" || a.SessionID == "" || a.Precond == nil || a.Precond.ApprovalID == "" || a.Precond.Digest == "" {
			return invalid("an approval needs its approval id and digest")
		}
	case QKindCancel:
	default:
		return invalid("this action cannot be queued")
	}
	return nil
}
