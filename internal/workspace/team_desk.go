package workspace

import (
	decks "github.com/dev-dami/deck/internal/deck/session"
)

// TeamDesk is what the sidebar's Capsules pane drives: the session's
// capsules and whether teammates may fetch them. Every method may read files:
// call them off the UI loop. The capsule requests themselves come with the
// peer link's Snapshot.
type TeamDesk interface {
	// Capsules lists the session's capsules, newest first, then any other
	// published one.
	Capsules(sessionID string) []decks.CapsuleInfo
	// SetCapsulePublic publishes a capsule or makes it private again.
	SetCapsulePublic(id string, public bool) error
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
