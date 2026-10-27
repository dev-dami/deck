// Package remote is the desktop side of Deck's phone remote control: the
// Backend interface the server speaks to (docs/remote-contract.md §4), its
// sentinels, and the QR renderer the pairing commands share. The server,
// hub, auth store and workspace adapter are its subpackages.
package remote

import (
	"context"
	"errors"

	"github.com/dev-dami/deck/pkg/remote/wire"
)

// ErrNotFound is returned by a Backend for an unknown session, or for an
// item, message or approval that does not belong to the session named.
var ErrNotFound = errors.New("not found")

// ErrInvalid marks a request the backend refuses as malformed or not
// allowed, as opposed to one that failed.
var ErrInvalid = errors.New("invalid request")

// Info describes the running Deck to a phone. Project is a name, never a
// path; Theme is the TUI's palette as of the call.
type Info struct {
	Version string
	Project string
	Kinds   []string
	Theme   wire.Theme
}

// Image is an attachment uploaded from the phone, already decoded.
type Image struct {
	Name string
	Mime string
	Data []byte
}

// Event is something the phone should hear about. Scoped events reach only
// connections focused on SessionID and are never replayed.
type Event struct {
	Name      string
	SessionID string
	Scoped    bool
	Payload   any
}

// Backend is everything the server needs from Deck, kept narrow so the
// server is tested against a fake. Every method that names a session
// returns ErrNotFound for an unknown session and for an id that belongs to
// another session.
type Backend interface {
	Info() Info

	Sessions(ctx context.Context) ([]wire.Session, error)
	CreateSession(ctx context.Context, title string) (wire.Session, error)
	RenameSession(ctx context.Context, id, title string) error
	DeleteSession(ctx context.Context, id string) error
	// Messages returns up to limit rows older than the before cursor (""
	// for the newest), oldest first, whether older rows exist, and the
	// cursor that fetches them.
	Messages(ctx context.Context, sessionID, before string, limit int) (msgs []wire.Message, more bool, next string, err error)

	// Send posts a message and returns the id of the row it created.
	Send(ctx context.Context, sessionID, text string, images []Image) (messageID string, err error)
	// EditMessage rewrites one of the user's earlier messages. It fails
	// with ErrInvalid when the message cannot be edited.
	EditMessage(ctx context.Context, sessionID, messageID, text string) error
	Cancel(sessionID string) error
	Interrupt(sessionID, agent string) error
	Continue(sessionID string) error

	Queue(sessionID string) ([]wire.QueuedItem, error)
	Steer(sessionID, itemID string) error
	CancelQueued(sessionID, itemID string) error

	Roster(sessionID string) ([]wire.AgentLine, error)
	// AgentKinds lists the agent CLIs that can join the session.
	AgentKinds(sessionID string) ([]wire.AgentKind, error)
	// AddAgent starts an agent of kind in the session without sending it
	// anything. An empty model means the one configured for the kind; an
	// unknown kind is ErrInvalid.
	AddAgent(sessionID, kind, model string) (wire.AgentLine, error)
	// Models lists the models of kind. An empty sessionID asks apart from
	// any thread: the same list, none of it selected.
	Models(ctx context.Context, sessionID, kind string) ([]wire.ModelChoice, error)
	// SetModel makes model what agents of kind run; "" is the CLI's default.
	SetModel(sessionID, kind, model, effort string) error

	Mode(sessionID string) (string, error)
	SetMode(sessionID, mode string) error

	// Approvals lists the pending approvals of one session, or of every
	// session when sessionID is "".
	Approvals(sessionID string) ([]wire.Approval, error)
	// Answer resolves a pending approval. resolved is false when another UI
	// got there first; ErrNotFound when the id is not pending in the session.
	Answer(sessionID, id, decision string) (resolved bool, err error)

	Changes(ctx context.Context, sessionID string) ([]wire.ChangedFile, error)

	// Events streams until ctx ends. The backend coalesces streaming
	// message updates itself and yields theme.changed when the palette
	// changes.
	Events(ctx context.Context) <-chan Event
}

// Racer is a Backend that starts races: entrants do one brief, each on a
// worktree of its own, and the user keeps one. It returns the race's task.
// A refusal the person should read (no git repository, too many entrants)
// is a *wire.Error.
type Racer interface {
	StartRace(ctx context.Context, sessionID, brief string, entrants []wire.RaceEntrant, judge bool) (string, error)
}

// ModelRefresher is a Backend whose model lists can be asked for again:
// RefreshModels forgets them, so the next Models asks the agent CLIs afresh.
type ModelRefresher interface {
	RefreshModels()
}
