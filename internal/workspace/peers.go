package workspace

import (
	"context"
	"errors"
	"time"

	deckconfig "github.com/dev-dami/deck/internal/deck/config"
	"github.com/dev-dami/deck/internal/deck/peer"
	decks "github.com/dev-dami/deck/internal/deck/session"
)

// PeerLink is the team link between Deck instances (docs/peers-design.md)
// as the Settings dialog's Team page drives it; *peer.Service implements it.
// Every method may read or write files or use the network: call them off
// the UI loop. Each change is saved to the config and in effect when it
// returns.
type PeerLink interface {
	// Snapshot is the link as it is now; nothing in it is secret.
	Snapshot() peer.Snapshot
	// Wait blocks until the link changed after the snapshot whose Version
	// is since, or ctx ends.
	Wait(ctx context.Context, since uint64) uint64
	// Reload puts a config edited outside Deck in effect.
	Reload() error

	SetEnabled(on bool) error
	SetName(name string) error
	SetListen(enabled bool, addr string) error
	SetRole(name, role string) error
	SetDial(name string, dial bool) error
	Remove(name string) error
	Ping(ctx context.Context, name string) (peer.PingResult, error)

	// StartPairListen waits for another Deck with a fresh one-time code, on
	// this Deck's own listener when it runs (same port as the link).
	StartPairListen(ctx context.Context) (*peer.PairWait, error)
	// PairListenAll moves a wait that only this machine can reach to every
	// interface (Listen on), keeping its code when it can.
	PairListenAll(w *peer.PairWait) (*peer.PairWait, error)
	// PairDial proves a code to a Deck waiting at addr; expectFP, from an
	// invite, is the fingerprint it must have ("" for none).
	PairDial(ctx context.Context, addr, code, expectFP string) (peer.PairResult, error)
	// Pin pins a Deck that proved the code, once the person confirmed the
	// fingerprints.
	Pin(r peer.PinRequest) error
	// AnswerContract accepts a contract a teammate proposed, with an agent
	// of kind agent, or declines it. Accepting fetches the teammate's tree:
	// call it off the UI loop.
	AnswerContract(name, id string, accept bool, agent string) error
	// ProposeContract proposes contract c (an ops contract the person
	// confirmed) to teammate name. A teammate that cannot be reached gets
	// it later: the error is then a peer.QueuedError. It uses the network:
	// call it off the UI loop.
	ProposeContract(ctx context.Context, name string, c peer.Contract) (peer.Contract, error)

	// ReadShared reads a thread teammate name shares with this Deck, from
	// log entry after on, waiting up to wait for something new.
	ReadShared(ctx context.Context, name, thread string, after int, wait time.Duration) (peer.Mirror, error)
	// PostShared posts to it, as participant or editor.
	PostShared(ctx context.Context, name, thread, text string) error
	// AnswerShared answers one of its approvals, as editor.
	AnswerShared(ctx context.Context, name, thread, approval string, allow bool) error
}

// Sharing is sharing this Deck's threads with teammates.
type Sharing interface {
	// ShareThread shares the session's thread with teammate name in role
	// (observer, participant or editor), or stops sharing it ("").
	ShareThread(sessionID, name, role string) error
	// ThreadShares are the teammates the session's thread is shared with,
	// and their roles.
	ThreadShares(sessionID string) map[string]string
}

var _ Sharing = (*DeckWorkspace)(nil)

// ShareThread implements [Sharing].
func (w *DeckWorkspace) ShareThread(sessionID, name, role string) error {
	th, ok := w.threadOf(sessionID)
	if !ok {
		return errors.New("no thread for this session")
	}
	return w.mgr().SetShare(th.ID, name, role)
}

// ThreadShares implements [Sharing].
func (w *DeckWorkspace) ThreadShares(sessionID string) map[string]string {
	th, ok := w.threadOf(sessionID)
	if !ok {
		return nil
	}
	return w.mgr().Shares(th.ID)
}

var _ PeerLink = (*peer.Service)(nil)

// Peers gives the UI the peer link. The UI type-asserts its Workspace to it.
type Peers interface {
	// PeerLink is the link, or nil when this Deck has none.
	PeerLink() PeerLink
}

var _ Peers = (*DeckWorkspace)(nil)

// Contracts are code contracts as the person drives them: asking a
// teammate for one, and which agents may run one here.
type Contracts interface {
	// ProposeContract asks teammate name to do brief for the session's
	// thread, and returns the task it becomes on the board. queued is set
	// when the teammate cannot be reached and the request waits for its
	// link. It snapshots the tree and uses the network: call it off the UI
	// loop.
	ProposeContract(sessionID, name, brief string) (taskID string, queued bool, err error)
	// ContractKinds are the agent kinds that may run a teammate's contract.
	ContractKinds() []string
}

var _ Contracts = (*DeckWorkspace)(nil)

// ProposeContract implements [Contracts].
func (w *DeckWorkspace) ProposeContract(sessionID, name, brief string) (string, bool, error) {
	th, ok := w.threadOf(sessionID)
	if !ok {
		return "", false, errors.New("no thread for this session")
	}
	tk, queued, err := w.mgr().Contract(th.ID, "", decks.ContractSpec{Peer: name, Brief: brief})
	return tk.ID, queued, err
}

// ContractKinds implements [Contracts].
func (w *DeckWorkspace) ContractKinds() []string { return w.mgr().ContractKinds() }

// PeerLink implements [Peers].
func (w *DeckWorkspace) PeerLink() PeerLink {
	if w.eng == nil || w.eng.Peers() == nil {
		return nil
	}
	return w.eng.Peers()
}

// saveKeepingPeers writes the settings with the peers section that is on
// disk now: Settings' Team page and `deck peer` change that section on their
// own, and the copy the engine loaded at startup must not undo them.
func saveKeepingPeers(c deckconfig.Config) error {
	path, err := deckconfig.Path()
	if err != nil {
		return err
	}
	if p, err := deckconfig.LoadPeers(path); err == nil {
		c.Peers = p
	}
	return deckconfig.SaveTo(path, c)
}
