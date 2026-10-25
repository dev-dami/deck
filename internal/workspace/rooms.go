package workspace

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"
	"unicode"

	decks "github.com/dev-dami/deck/internal/deck/session"
	"github.com/dev-dami/deck/internal/message"
)

// Rooms is what the desktop's room view does to a session's thread beyond
// the remote control: isolation, reverts, archive and races
// (internal/deck/session isolate.go, changes.go, lifecycle.go, race.go), and
// what it reads of the messages: image bytes and a search across sessions.
type Rooms interface {
	// Room is the session's thread as a room shows it; zero when the
	// session has no thread.
	Room(sessionID string) Room
	Isolate(ctx context.Context, sessionID string) error
	ApplyIsolated(ctx context.Context, sessionID string) ([]string, error)
	// UndoApply takes the shared tree back to how it was before the last
	// apply and returns the files it put back.
	UndoApply(ctx context.Context, sessionID string) ([]string, error)
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
	Unarchive(ctx context.Context, sessionID string) error
	// StartRace has count agents of kind do brief, each on a worktree of
	// its own, with one more of kind to rank them when judge is set. It
	// returns the race's task.
	StartRace(ctx context.Context, sessionID, brief, kind string, count int, judge bool) (string, error)
	// ReadImage returns the bytes and type of the index'th image part of a
	// message of the session.
	ReadImage(ctx context.Context, sessionID, messageID string, index int) ([]byte, string, error)
	// SearchMessages finds query, ignoring case, in the text of the
	// messages of every session that is not archived: up to limit hits,
	// newest first.
	SearchMessages(ctx context.Context, query string, limit int) ([]MessageHit, error)
}

// MessageHit is a message SearchMessages found, with the text around the
// match.
type MessageHit struct {
	SessionID, MessageID, Agent, Snippet string
	At                                   time.Time
}

// Room is a session's thread as a room shows it.
type Room struct {
	Dir      string // the thread's folder: in its worktree when isolated
	Branch   string // the worktree branch when isolated
	Archived bool
}

var _ Rooms = (*DeckWorkspace)(nil)

var (
	errNoThread    = errors.New("no agents have run in this session yet")
	errNotArchived = errors.New("this session is not archived")
	errNotImage    = errors.New("that part of the message is not an image")
)

// topSession checks that sessionID names a top-level session, as the remote
// adapter does.
func (w *DeckWorkspace) topSession(ctx context.Context, sessionID string) error {
	s, err := w.app.Sessions.Get(ctx, sessionID)
	if err != nil || s.ParentSessionID != "" {
		return ErrNotFound
	}
	return nil
}

// roomThread checks the session and returns its live thread, resumed when it
// is stored. With open, a session that has no thread gets one.
func (w *DeckWorkspace) roomThread(ctx context.Context, sessionID string, open bool) (string, error) {
	if err := w.topSession(ctx, sessionID); err != nil {
		return "", err
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

// UndoApply implements [Rooms].
func (w *DeckWorkspace) UndoApply(ctx context.Context, sessionID string) ([]string, error) {
	tid, err := w.roomThread(ctx, sessionID, false)
	if err != nil {
		return nil, err
	}
	return w.mgr().UndoApply(tid)
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

// Archive implements [Rooms]. A session nobody wrote in yet gets a thread,
// so the store has a row to mark.
func (w *DeckWorkspace) Archive(ctx context.Context, sessionID string) error {
	tid, err := w.roomThread(ctx, sessionID, true)
	if err != nil {
		return err
	}
	if err := w.mgr().Archive(tid); err != nil {
		return err
	}
	w.mgr().Flush() // Room reads the store
	return nil
}

// Unarchive implements [Rooms]. It starts no agents; the next message does.
func (w *DeckWorkspace) Unarchive(ctx context.Context, sessionID string) error {
	if err := w.topSession(ctx, sessionID); err != nil {
		return err
	}
	w.mu.Lock()
	tid, ok := w.threads[sessionID]
	w.mu.Unlock()
	if !ok {
		return errNotArchived
	}
	return w.mgr().Unarchive(tid)
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

// ReadImage implements [Rooms]. index counts the message's image parts as
// the remote adapter converts them.
func (w *DeckWorkspace) ReadImage(ctx context.Context, sessionID, messageID string, index int) ([]byte, string, error) {
	if err := w.topSession(ctx, sessionID); err != nil {
		return nil, "", err
	}
	m, err := w.app.Messages.Get(ctx, messageID)
	if err != nil || m.SessionID != sessionID {
		return nil, "", ErrNotFound
	}
	n := 0
	for _, p := range m.Parts {
		switch v := p.(type) {
		case message.BinaryContent:
			if n == index {
				if !strings.HasPrefix(v.MIMEType, "image/") {
					return nil, "", errNotImage
				}
				return v.Data, v.MIMEType, nil
			}
			n++
		case message.ImageURLContent:
			if n == index {
				return nil, "", errNotImage // a link, not bytes Deck keeps
			}
			n++
		}
	}
	return nil, "", ErrNotFound
}

// snippetRunes is how much text a search hit keeps on each side of the match.
const snippetRunes = 60

// SearchMessages implements [Rooms]. It reads the message store, so no
// thread is resumed to search it.
func (w *DeckWorkspace) SearchMessages(ctx context.Context, query string, limit int) ([]MessageHit, error) {
	q := []rune(strings.TrimSpace(query))
	if len(q) == 0 || limit <= 0 {
		return nil, nil
	}
	for i, r := range q {
		q[i] = unicode.ToLower(r)
	}
	if err := w.app.Messages.FlushAll(ctx); err != nil {
		return nil, err
	}
	sessions, err := w.app.Sessions.List(ctx)
	if err != nil {
		return nil, err
	}
	archived := map[string]bool{}
	for _, t := range w.mgr().Saved() {
		archived[t.ID] = t.Archived
	}
	var hits []MessageHit
	for _, s := range sessions {
		w.mu.Lock()
		tid := w.threads[s.ID]
		w.mu.Unlock()
		if _, live := w.mgr().Thread(tid); s.ParentSessionID != "" || archived[tid] && !live {
			continue
		}
		msgs, err := w.app.Messages.List(ctx, s.ID)
		if err != nil {
			return nil, err
		}
		for _, m := range msgs {
			for _, p := range m.Parts {
				t, ok := p.(message.TextContent)
				if !ok || t.Hidden {
					continue
				}
				if snip, ok := snippet(t.Text, q); ok {
					h := MessageHit{SessionID: s.ID, MessageID: m.ID, Snippet: snip, At: time.Unix(m.CreatedAt, 0)}
					if m.Role == message.Assistant && m.Provider != "deck" {
						h.Agent = m.Model
					}
					hits = append(hits, h)
					break
				}
			}
		}
	}
	slices.SortStableFunc(hits, func(a, b MessageHit) int { return b.At.Compare(a.At) })
	return hits[:min(len(hits), limit)], nil
}

// snippet finds q (lower case) in text, ignoring case, and returns the text
// around the first match on one line.
func snippet(text string, q []rune) (string, bool) {
	rs := []rune(text)
	at := -1
	for i := 0; i+len(q) <= len(rs) && at < 0; i++ {
		at = i
		for j, r := range q {
			if unicode.ToLower(rs[i+j]) != r {
				at = -1
				break
			}
		}
	}
	if at < 0 {
		return "", false
	}
	from, to := max(0, at-snippetRunes), min(len(rs), at+len(q)+snippetRunes)
	out := strings.Join(strings.Fields(string(rs[from:to])), " ")
	if from > 0 {
		out = "…" + out
	}
	if to < len(rs) {
		out += "…"
	}
	return out, true
}
