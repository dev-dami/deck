package workspace

import (
	"context"
	"errors"

	decks "github.com/dev-dami/deck/internal/deck/session"
)

// TeamDesk is what the sidebar's Capsules and Team chat panes drive: the
// session's capsules and whether teammates may fetch them, and the team
// chat. Every method may read files or use the network: call them off the
// UI loop. The chat and the capsule requests themselves come with the peer
// link's Snapshot.
type TeamDesk interface {
	// Capsules lists the session's capsules, newest first, then any other
	// published one.
	Capsules(sessionID string) []decks.CapsuleInfo
	// SetCapsulePublic publishes a capsule or makes it private again.
	SetCapsulePublic(id string, public bool) error
	// SendTeamChat posts to every paired teammate's team chat and returns
	// the ones it reached.
	SendTeamChat(ctx context.Context, text string) ([]string, error)
}

var _ TeamDesk = (*DeckWorkspace)(nil)

// Capsules implements [TeamDesk].
func (w *DeckWorkspace) Capsules(sessionID string) []decks.CapsuleInfo {
	th, ok := w.threadOf(sessionID)
	if !ok {
		return w.mgr().Capsules("")
	}
	return w.mgr().Capsules(th.ID)
}

// SetCapsulePublic implements [TeamDesk].
func (w *DeckWorkspace) SetCapsulePublic(id string, public bool) error {
	return w.mgr().SetCapsulePublic(id, public)
}

// SendTeamChat implements [TeamDesk].
func (w *DeckWorkspace) SendTeamChat(ctx context.Context, text string) ([]string, error) {
	if w.eng == nil || w.eng.Peers() == nil {
		return nil, errors.New("this Deck has no peer link")
	}
	return w.eng.Peers().SendChat(ctx, text)
}
