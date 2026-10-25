package workspace

import (
	"context"
	"errors"
	"slices"
	"time"

	decks "github.com/dev-dami/deck/internal/deck/session"
)

// Rooms is what the desktop's room view does to a session's thread beyond
// the remote control: isolation, reverts, archive and races
// (internal/deck/session isolate.go, changes.go, lifecycle.go, race.go).
type Rooms interface {
	// Room is the session's thread as a room shows it; zero when the
	// session has no thread.
	Room(sessionID string) Room
	Isolate(ctx context.Context, sessionID string) error
	ApplyIsolated(ctx context.Context, sessionID string) ([]string, error)
	// Unisolate moves the thread back to the shared tree and discards its
	// worktree, applied or not.
	Unisolate(ctx context.Context, sessionID string) error
	// RevertFiles puts paths back the way they were before the first
	// message at or after since (zero: the checkpoint ChangedFiles measures
	// from) and returns the files it put back.
	RevertFiles(ctx context.Context, sessionID string, since time.Time, paths []string) ([]string, error)
	// UndoRevert undoes the thread's latest revert.
	UndoRevert(ctx context.Context, sessionID string) error
	Archive(ctx context.Context, sessionID string) error
	// StartRace has count agents of kind do brief, each on a worktree of
	// its own, with one more of kind to rank them when judge is set. It
	// returns the race's task.
	StartRace(ctx context.Context, sessionID, brief, kind string, count int, judge bool) (string, error)
}

// Room is a session's thread as a room shows it.
type Room struct {
	Dir      string // the thread's folder: in its worktree when isolated
	Branch   string // the worktree branch when isolated
	Archived bool
}

var _ Rooms = (*DeckWorkspace)(nil)

var errNoThread = errors.New("no agents have run in this session yet")

// roomThread checks that sessionID names a top-level session, as the remote
// adapter does, and returns its live thread, resumed when it is stored. With
// open, a session that has no thread gets one.
func (w *DeckWorkspace) roomThread(ctx context.Context, sessionID string, open bool) (string, error) {
	s, err := w.app.Sessions.Get(ctx, sessionID)
	if err != nil || s.ParentSessionID != "" {
		return "", ErrNotFound
	}
	w.mu.Lock()
	_, ok := w.threads[sessionID]
	w.mu.Unlock()
	if !ok && !open {
		return "", errNoThread
	}
	return w.thread(sessionID)
}

// Room implements [Rooms]. A thread that is not live is read from the
// store, so listing sessions starts no agents.
func (w *DeckWorkspace) Room(sessionID string) Room {
	w.mu.Lock()
	id, ok := w.threads[sessionID]
	w.mu.Unlock()
	if !ok {
		return Room{}
	}
	if th, live := w.mgr().Thread(id); live {
		return Room{Dir: th.Dir, Branch: th.Branch}
	}
	for _, t := range w.mgr().Saved() {
		if t.ID == id {
			r := Room{Dir: t.Dir, Archived: t.Archived}
			if t.Worktree != nil {
				r.Branch = t.Worktree.Branch
			}
			return r
		}
	}
	return Room{}
}

// Isolate implements [Rooms].
func (w *DeckWorkspace) Isolate(ctx context.Context, sessionID string) error {
	tid, err := w.roomThread(ctx, sessionID, true)
	if err != nil {
		return err
	}
	_, _, err = w.mgr().Isolate(tid)
	return err
}

// ApplyIsolated implements [Rooms].
func (w *DeckWorkspace) ApplyIsolated(ctx context.Context, sessionID string) ([]string, error) {
	tid, err := w.roomThread(ctx, sessionID, false)
	if err != nil {
		return nil, err
	}
	return w.mgr().ApplyIsolated(tid)
}

// Unisolate implements [Rooms].
func (w *DeckWorkspace) Unisolate(ctx context.Context, sessionID string) error {
	tid, err := w.roomThread(ctx, sessionID, false)
	if err != nil {
		return err
	}
	_, _, err = w.mgr().Unisolate(tid, true)
	return err
}

// RevertFiles implements [Rooms].
func (w *DeckWorkspace) RevertFiles(ctx context.Context, sessionID string, since time.Time, paths []string) ([]string, error) {
	tid, err := w.roomThread(ctx, sessionID, false)
	if err != nil {
		return nil, err
	}
	cs, err := w.mgr().Changes(ctx, tid, "")
	if err != nil {
		return nil, err
	}
	if !since.IsZero() {
		i := slices.IndexFunc(cs.Checkpoints, func(c decks.Checkpoint) bool { return !c.Time.Before(since) })
		if i < 0 {
			return nil, errors.New("no message at or after that time to revert to")
		}
		if cs, err = w.mgr().Changes(ctx, tid, cs.Checkpoints[i].EntryID); err != nil {
			return nil, err
		}
	}
	// A renamed file goes back under both its names.
	var files []string
	for _, p := range paths {
		i := slices.IndexFunc(cs.Files, func(f decks.ChangedFile) bool { return f.Path == p })
		if i < 0 {
			files = append(files, p)
			continue
		}
		files = append(files, cs.Files[i].Paths()...)
	}
	if _, err := w.mgr().Revert(ctx, tid, cs.Since, files); err != nil {
		return nil, err
	}
	return files, nil
}

// UndoRevert implements [Rooms].
func (w *DeckWorkspace) UndoRevert(ctx context.Context, sessionID string) error {
	tid, err := w.roomThread(ctx, sessionID, false)
	if err != nil {
		return err
	}
	return w.mgr().UndoRevert(ctx, tid, "")
}

// Archive implements [Rooms]. The store keeps only threads that had
// content, so an empty one cannot be archived.
func (w *DeckWorkspace) Archive(ctx context.Context, sessionID string) error {
	tid, err := w.roomThread(ctx, sessionID, false)
	if err != nil {
		return err
	}
	if err := w.mgr().Archive(tid); err != nil {
		return err
	}
	w.mgr().Flush() // Room reads the store
	if !w.Room(sessionID).Archived {
		return errNoThread
	}
	return nil
}

// StartRace implements [Rooms].
func (w *DeckWorkspace) StartRace(ctx context.Context, sessionID, brief, kind string, count int, judge bool) (string, error) {
	tid, err := w.roomThread(ctx, sessionID, true)
	if err != nil {
		return "", err
	}
	sp := decks.RaceSpec{Brief: brief, Kinds: slices.Repeat([]string{kind}, max(count, 0))}
	if judge {
		sp.Judge = kind
	}
	res, err := w.mgr().Race(tid, "", sp)
	return res.TaskID, err
}
