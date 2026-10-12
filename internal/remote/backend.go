package remote

import (
	"context"
	"errors"

	"github.com/dev-dami/deck/internal/remote/wire"
)

// ErrNotFound is returned by a Backend for an unknown session, item or approval.
var ErrNotFound = errors.New("not found")

// ErrInvalid marks a request the backend refuses as malformed or not allowed,
// as opposed to one that failed.
var ErrInvalid = errors.New("invalid request")

// Info describes the running Deck to a phone.
type Info struct {
	Version string
	Project string
	Cwd     string
	Kinds   []string
}

// Image is an attachment uploaded from the phone, already decoded.
type Image struct {
	Name string
	Mime string
	Data []byte
}

// Event is something the phone should hear about. Scoped events (message
// rows) reach only connections focused on SessionID.
type Event struct {
	Name      string
	SessionID string
	Scoped    bool
	Payload   any
}

// Backend is everything the server needs from Deck, kept narrow so the server
// is tested against a fake. The workspace adapter implements it for real.
type Backend interface {
	Info() Info

	Sessions(ctx context.Context) ([]wire.Session, error)
	CreateSession(ctx context.Context, title string) (wire.Session, error)
	RenameSession(ctx context.Context, id, title string) error
	DeleteSession(ctx context.Context, id string) error
	// Messages returns a session's rows, oldest first.
	Messages(ctx context.Context, sessionID string) ([]wire.Message, error)

	Send(ctx context.Context, sessionID, text string, images []Image) error
	// EditMessage rewrites one of the user's earlier messages. It fails with
	// ErrInvalid when the message cannot be edited.
	EditMessage(ctx context.Context, sessionID, messageID, text string) error
	Cancel(sessionID string)
	Interrupt(sessionID, agent string) error
	Continue(sessionID string) error

	Queue(sessionID string) []wire.QueuedItem
	Steer(itemID string) error
	CancelQueued(itemID string) error

	Roster(sessionID string) []wire.AgentLine
	Models(ctx context.Context, sessionID, kind string) ([]wire.ModelChoice, error)
	SetModel(sessionID, kind, model, effort string) error

	Mode() string
	SetMode(mode string) error

	Approvals() []wire.Approval
	// Answer resolves a pending approval. resolved is false when another UI
	// got there first; ErrNotFound when the id was never pending.
	Answer(id, decision string) (resolved bool, err error)

	Changes(ctx context.Context, sessionID string) ([]wire.ChangedFile, error)

	// Events streams until ctx ends. The backend coalesces streaming message
	// updates itself.
	Events(ctx context.Context) <-chan Event
}
