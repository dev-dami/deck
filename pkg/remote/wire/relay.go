package wire

import "time"

// Relay protocol relay/v1: the plaintext envelope around the E2E channel.
// The relay sees the cid, device and pair id hashes, sizes, timing and push
// endpoints, never content (contract §5).

// Close codes the relay adds to the v2 set.
const (
	CloseUnknownCID  = 4404
	CloseHostReplace = 4409
)

// Relay limits.
const (
	MaxRelayFrameBytes    = 1 << 20
	MaxMailBytes          = 64 << 10
	MaxMailsPerCID        = 1000
	MaxMailBytesPerCID    = 16 << 20
	MaxLanesPerDevice     = 8
	ReceiptMailTTL        = 7 * 24 * time.Hour
	MaxMailTTL            = 24 * time.Hour
	RelayChallengeContext = "deck-relay-v1|"
)

// Host frame types (HostFrame.T). Frames from the computer to the relay are
// devices, pair_open, pair_close, data, close, mail_ack, snap and push; frames
// from the relay are challenge, ready, open, data, close and mail.
const (
	HostChallenge = "challenge"
	HostAuth      = "auth"
	HostReady     = "ready"
	HostDevices   = "devices"
	HostPairOpen  = "pair_open"
	HostPairClose = "pair_close"
	HostOpen      = "open"
	HostData      = "data"
	HostClose     = "close"
	HostMail      = "mail"
	HostAck       = "ack"
	HostSnap      = "snap"
	HostPush      = "push"
)

// Push urgencies (HostFrame.Urgency), as in RFC 8030.
const (
	UrgencyNormal = "normal"
	UrgencyHigh   = "high"
)

// HostFrame is one JSON text frame on /r/v1/c/:cid/host. Only the fields of
// its type are set.
type HostFrame struct {
	T string `json:"t"`

	// challenge: Nonce. auth: Sig (Ed25519 over RelayChallengeContext+cid+nonce).
	Nonce string `json:"nonce,omitempty"`
	Sig   string `json:"sig,omitempty"`

	// devices: the hashes of every relay token the computer accepts.
	Hashes []string `json:"hashes,omitempty"`

	// pair_open: the hash of the pair id and its expiry.
	PairIDHash string `json:"pair_id_hash,omitempty"`
	Exp        int64  `json:"exp,omitempty"`

	// open, data, close: a lane is one phone connection. open names either
	// a device (its token hash) or a pairing attempt.
	Lane   string `json:"lane,omitempty"`
	Device string `json:"device,omitempty"`
	Pair   bool   `json:"pair,omitempty"`
	Code   int    `json:"code,omitempty"`

	// data, mail, snap, push: an opaque base64 body.
	B64 string `json:"b64,omitempty"`

	// mail, ack: the mailbox entry.
	ID string `json:"id,omitempty"`

	// snap, push: the target device (token hash).
	DeviceHash string `json:"device_hash,omitempty"`

	// push: lifetime in seconds and urgency.
	TTL     int    `json:"ttl,omitempty"`
	Urgency string `json:"urgency,omitempty"`
}

// Relay HTTP and first-frame bodies.
type (
	// RegisterRequest is POST /r/v1/register.
	RegisterRequest struct {
		RelayPub string `json:"relay_pub"`
	}

	// RegisterResponse carries the computer id, b64url(sha256(pub))[:22].
	RegisterResponse struct {
		CID string `json:"cid"`
	}

	// PhoneAuth is the first text frame on /r/v1/c/:cid/dev; never in the URL.
	PhoneAuth struct {
		Token string `json:"token"`
	}

	// PairOpen is the first text frame on /r/v1/c/:cid/pair.
	PairOpen struct {
		PairID string `json:"pair_id"`
	}

	// MailPost is POST /r/v1/c/:cid/mail (bearer relay token). ExpiresAt is
	// the sealed action's expiry, copied out so the relay can drop it.
	MailPost struct {
		ID        string    `json:"id"`
		ExpiresAt time.Time `json:"expires_at"`
		B64       string    `json:"b64"`
	}

	// SnapResponse is GET /r/v1/c/:cid/snap.
	SnapResponse struct {
		B64      string        `json:"b64"`
		Computer ComputerState `json:"computer"`
	}

	// ComputerState is whether the computer is connected and when it last was.
	ComputerState struct {
		Online   bool      `json:"online"`
		LastSeen time.Time `json:"last_seen,omitzero"`
	}

	// PushPut is PUT /r/v1/c/:cid/push (bearer relay token).
	PushPut struct {
		Endpoint string `json:"endpoint"`
	}
)

// FixturesRelay lists the relay fixtures (prefix "relay_").
func FixturesRelay() []Fixture {
	return []Fixture{
		{"relay_host_challenge", HostFrame{}},
		{"relay_host_auth", HostFrame{}},
		{"relay_host_ready", HostFrame{}},
		{"relay_host_devices", HostFrame{}},
		{"relay_host_pair_open", HostFrame{}},
		{"relay_host_open", HostFrame{}},
		{"relay_host_data", HostFrame{}},
		{"relay_host_close", HostFrame{}},
		{"relay_host_mail", HostFrame{}},
		{"relay_host_ack", HostFrame{}},
		{"relay_host_snap", HostFrame{}},
		{"relay_host_push", HostFrame{}},
		{"relay_register_request", RegisterRequest{}},
		{"relay_register_response", RegisterResponse{}},
		{"relay_phone_auth", PhoneAuth{}},
		{"relay_pair_open", PairOpen{}},
		{"relay_mail_post", MailPost{}},
		{"relay_snap_response", SnapResponse{}},
		{"relay_push_put", PushPut{}},
	}
}
