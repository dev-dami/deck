package workspace

import (
	"context"

	deckconfig "github.com/dev-dami/deck/internal/deck/config"
	"github.com/dev-dami/deck/internal/deck/peer"
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
}

var _ PeerLink = (*peer.Service)(nil)

// Peers gives the UI the peer link. The UI type-asserts its Workspace to it.
type Peers interface {
	// PeerLink is the link, or nil when this Deck has none.
	PeerLink() PeerLink
}

var _ Peers = (*DeckWorkspace)(nil)

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
