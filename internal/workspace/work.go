package workspace

import (
	"errors"
	"strings"

	decks "github.com/dev-dami/deck/internal/deck/session"
	"github.com/dev-dami/deck/internal/deck/sub"
)

// WorkDesk is a session's worktrees and named capsules as the desktop's
// Work tab drives them (internal/deck/session work.go, capsulework.go).
// Conflicts come back in a LandResult or a changes.ConflictError; nothing
// was written then.
type WorkDesk interface {
	Trees(sessionID string) ([]decks.WorktreeInfo, error)
	NamedCapsules(sessionID string) ([]decks.CapsuleView, error)
	CreateTree(sessionID string, sp decks.TreeSpec) (decks.WorktreeInfo, error)
	SyncTree(sessionID, name string, rebase bool) (decks.WorktreeInfo, error)
	// LandTree merges a worktree into the shared tree (into "") or another
	// worktree; check only reports how each file would land.
	LandTree(sessionID, name, into string, check bool, strategy string) (decks.LandResult, error)
	DiscardTree(sessionID, name string) error
	// SpawnInTree starts an agent of kind in the worktree, with brief.
	SpawnInTree(sessionID, name, kind, brief string) (string, error)
	CreateCapsule(sessionID string, sp decks.CapsuleSpec) (string, int, error)
	LandCapsule(sessionID, ref, into string, check bool, strategy string) (decks.LandResult, error)
	RebaseCapsule(sessionID, ref, onto string) (decks.LandResult, error)
	RevertCapsule(sessionID, ref, from string) error
	HandCapsule(sessionID, ref, agent, text string) error
	ReviewCapsule(sessionID, ref, path, comment, tests string) error
	CapsuleFiles(sessionID, ref string, offset, limit int) ([]sub.FileStat, int, error)
	CapsuleFileDiff(sessionID, ref, path string) (string, error)
}

var _ WorkDesk = (*DeckWorkspace)(nil)

func strategyOf(s string) (sub.Strategy, error) {
	switch st := sub.Strategy(strings.ToLower(strings.TrimSpace(s))); st {
	case "", "none":
		return sub.Refuse, nil
	case sub.Theirs, sub.Ours:
		return st, nil
	}
	return "", errors.New("a strategy is none, theirs or ours")
}

// Trees implements [WorkDesk]. A session with no agents yet has none.
func (w *DeckWorkspace) Trees(sessionID string) ([]decks.WorktreeInfo, error) {
	th, ok := w.threadOf(sessionID)
	if !ok {
		return nil, nil
	}
	return w.mgr().Trees(th.ID), nil
}

// NamedCapsules implements [WorkDesk].
func (w *DeckWorkspace) NamedCapsules(sessionID string) ([]decks.CapsuleView, error) {
	th, ok := w.threadOf(sessionID)
	if !ok {
		return nil, nil
	}
	return w.mgr().CapsuleViews(th.ID), nil
}

// CreateTree implements [WorkDesk].
func (w *DeckWorkspace) CreateTree(sessionID string, sp decks.TreeSpec) (decks.WorktreeInfo, error) {
	tid, err := w.boardThread(sessionID)
	if err != nil {
		return decks.WorktreeInfo{}, err
	}
	return w.mgr().CreateTree(tid, sp)
}

// SyncTree implements [WorkDesk].
func (w *DeckWorkspace) SyncTree(sessionID, name string, rebase bool) (decks.WorktreeInfo, error) {
	tid, err := w.boardThread(sessionID)
	if err != nil {
		return decks.WorktreeInfo{}, err
	}
	return w.mgr().SyncTree(tid, name, rebase, "")
}

// LandTree implements [WorkDesk].
func (w *DeckWorkspace) LandTree(sessionID, name, into string, check bool, strategy string) (decks.LandResult, error) {
	tid, err := w.boardThread(sessionID)
	if err != nil {
		return decks.LandResult{}, err
	}
	st, err := strategyOf(strategy)
	if err != nil {
		return decks.LandResult{}, err
	}
	return w.mgr().LandTree(tid, name, into, check, st, "")
}

// DiscardTree implements [WorkDesk].
func (w *DeckWorkspace) DiscardTree(sessionID, name string) error {
	tid, err := w.boardThread(sessionID)
	if err != nil {
		return err
	}
	return w.mgr().DiscardTree(tid, name, "")
}

// SpawnInTree implements [WorkDesk].
func (w *DeckWorkspace) SpawnInTree(sessionID, name, kind, brief string) (string, error) {
	tid, err := w.boardThread(sessionID)
	if err != nil {
		return "", err
	}
	res, err := w.mgr().Spawn(tid, "", decks.SpawnSpec{Kind: kind, Brief: brief, Tree: name})
	return res.AgentID, err
}

// CreateCapsule implements [WorkDesk].
func (w *DeckWorkspace) CreateCapsule(sessionID string, sp decks.CapsuleSpec) (string, int, error) {
	tid, err := w.boardThread(sessionID)
	if err != nil {
		return "", 0, err
	}
	return w.mgr().CreateCapsule(tid, sp)
}

// LandCapsule implements [WorkDesk].
func (w *DeckWorkspace) LandCapsule(sessionID, ref, into string, check bool, strategy string) (decks.LandResult, error) {
	tid, err := w.boardThread(sessionID)
	if err != nil {
		return decks.LandResult{}, err
	}
	st, err := strategyOf(strategy)
	if err != nil {
		return decks.LandResult{}, err
	}
	return w.mgr().LandCapsule(tid, ref, into, check, st, "")
}

// RebaseCapsule implements [WorkDesk].
func (w *DeckWorkspace) RebaseCapsule(sessionID, ref, onto string) (decks.LandResult, error) {
	tid, err := w.boardThread(sessionID)
	if err != nil {
		return decks.LandResult{}, err
	}
	return w.mgr().RebaseCapsule(tid, ref, onto, "")
}

// RevertCapsule implements [WorkDesk].
func (w *DeckWorkspace) RevertCapsule(sessionID, ref, from string) error {
	tid, err := w.boardThread(sessionID)
	if err != nil {
		return err
	}
	return w.mgr().RevertCapsule(tid, ref, from, "")
}

// HandCapsule implements [WorkDesk].
func (w *DeckWorkspace) HandCapsule(sessionID, ref, agent, text string) error {
	tid, err := w.boardThread(sessionID)
	if err != nil {
		return err
	}
	return w.mgr().HandCapsule(tid, ref, agent, text)
}

// ReviewCapsule implements [WorkDesk].
func (w *DeckWorkspace) ReviewCapsule(sessionID, ref, path, comment, tests string) error {
	tid, err := w.boardThread(sessionID)
	if err != nil {
		return err
	}
	return w.mgr().ReviewCapsule(tid, ref, "", path, comment, tests, "")
}

// CapsuleFiles implements [WorkDesk].
func (w *DeckWorkspace) CapsuleFiles(sessionID, ref string, offset, limit int) ([]sub.FileStat, int, error) {
	tid, err := w.boardThread(sessionID)
	if err != nil {
		return nil, 0, err
	}
	return w.mgr().CapsuleFiles(tid, ref, offset, limit)
}

// CapsuleFileDiff implements [WorkDesk].
func (w *DeckWorkspace) CapsuleFileDiff(sessionID, ref, path string) (string, error) {
	tid, err := w.boardThread(sessionID)
	if err != nil {
		return "", err
	}
	return w.mgr().CapsuleFileDiff(tid, ref, path)
}
