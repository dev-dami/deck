package session

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/dev-dami/deck/internal/deck/agent"
	"github.com/dev-dami/deck/internal/deck/changes"
	"github.com/dev-dami/deck/internal/deck/store"
)

// Checkpoint is a point the changes of a thread can be measured from: the work
// tree just before one of the user's messages reached anyone.
type Checkpoint struct {
	EntryID string // the user message
	Text    string // the message, on one line
	Time    time.Time
}

// ChangedFile is one file that differs from the checkpoint.
type ChangedFile struct {
	agent.FileDiff
	// Agents are the members that reported changing the file since the
	// checkpoint, in thread order. A file changed by a shell command, or by
	// you, may have none.
	Agents []string
}

// Paths are the work-tree paths a revert of the file puts back: the file, and
// for a rename the name it had.
func (f ChangedFile) Paths() []string {
	if f.OldPath != "" && f.OldPath != f.Path {
		return []string{f.Path, f.OldPath}
	}
	return []string{f.Path}
}

// ChangeSet is what changed in a thread's work tree since a checkpoint.
type ChangeSet struct {
	ThreadID string
	// Since is the checkpoint the set starts at ("" when the thread has none).
	Since string
	// Checkpoints are every checkpoint of the thread, oldest first.
	Checkpoints []Checkpoint
	// Git is true when Files is the real difference between the checkpoint and
	// the work tree now, and can be reverted. When false, Files is only what
	// the agents reported changing (for example outside a git repository) and
	// Note says why.
	Git   bool
	Root  string // the repository root, when Git
	Files []ChangedFile
	Note  string
}

// Added and Removed total the line counts of every file.
func (c ChangeSet) Added() (n int) {
	for _, f := range c.Files {
		n += f.Added
	}
	return n
}

func (c ChangeSet) Removed() (n int) {
	for _, f := range c.Files {
		n += f.Removed
	}
	return n
}

// RevertedMsg says the user put files back (Manager.Revert), or undid that
// (Manager.UndoRevert, Undo true). Since is the text of the message the files
// went back to.
type RevertedMsg struct {
	ThreadID string
	EntryID  string // the revert, for UndoRevert
	Files    []string
	Since    string
	Undo     bool
}

// errNoTracking is what Revert says when a thread has nothing to revert to.
var errNoTracking = errors.New("changes are not tracked here: not a git repository, or track_changes is off")

// repoOf opens the thread's git work tree once. It returns nil outside a git
// repository or when tracking is off.
func (m *Manager) repoOf(t *thread) *changes.Repo {
	if !m.config().TrackChanges {
		return nil
	}
	t.repoOnce.Do(func() {
		ctx, cancel := context.WithTimeout(m.ctx, 10*time.Second)
		defer cancel()
		if r, err := changes.Open(ctx, t.dir); err == nil {
			t.repo = r
		}
	})
	return t.repo
}

// checkpoint records the work tree as the user message entryID found it. It
// runs after the message is on screen and before anyone is given it.
func (m *Manager) checkpoint(t *thread, entryID string) {
	r := m.repoOf(t)
	if r == nil {
		return
	}
	tree, err := m.snapLike(m.ctx, r, "")
	if err != nil {
		return
	}
	t.mu.Lock()
	m.appendLocked(t, store.Entry{Type: store.EntrySnap, Ref: entryID, Snap: tree})
	t.mu.Unlock()
}

// checkpointsLocked lists the thread's user messages oldest first, with the
// index of each in the log and its snapshot, from the log's index. t.mu is
// held.
func (t *thread) checkpointsLocked() (cps []Checkpoint, idx []int, snaps []string) {
	for _, h := range t.index.heads {
		if h.typ != store.EntryUser || h.steer {
			continue
		}
		text := h.text
		if s, ok := t.index.edited[h.id]; ok {
			text = s
		}
		cps = append(cps, Checkpoint{EntryID: h.id, Text: oneLine(text), Time: h.time})
		idx = append(idx, h.i)
		snaps = append(snaps, t.index.snaps[h.id])
	}
	return cps, idx, snaps
}

// Changes reports what changed in the thread's work tree since the checkpoint
// since (a user message's entry id; "" for the first one that has a
// snapshot). In a git work tree with tracking on it compares that snapshot
// with the tree as it is now, whoever made the changes; otherwise it falls
// back to the changes the agents reported.
func (m *Manager) Changes(ctx context.Context, threadID, since string) (ChangeSet, error) {
	t := m.thread(threadID)
	if t == nil {
		return ChangeSet{}, fmt.Errorf("session: no thread %q", threadID)
	}
	t.mu.Lock()
	cps, idx, snaps := t.checkpointsLocked()
	t.mu.Unlock()
	cs := ChangeSet{ThreadID: threadID, Checkpoints: cps}
	if len(cps) == 0 {
		return cs, nil
	}

	at := -1 // the checkpoint the set starts at
	for i, c := range cps {
		if (since == "" && snaps[i] != "") || (since != "" && c.EntryID == since) {
			at = i
			break
		}
	}
	if at < 0 && since == "" {
		at = 0 // no snapshot anywhere: the agents' reports since the first message
	}
	if at < 0 {
		return cs, fmt.Errorf("session: no message %q in this thread", since)
	}
	cs.Since = cps[at].EntryID

	r := m.repoOf(t)
	switch {
	case r == nil && !m.config().TrackChanges:
		cs.Note = "track_changes is off: showing what the agents reported"
	case r == nil:
		cs.Note = "not a git repository: showing what the agents reported"
	case snaps[at] == "":
		cs.Note = "no snapshot for that message: showing what the agents reported"
	default:
		now, err := m.snapLike(ctx, r, snaps[at])
		if err != nil {
			return cs, err
		}
		diffs, err := m.diffSnaps(ctx, r, snaps[at], now)
		if err != nil {
			if m.snapExists(ctx, r, snaps[at]) {
				return cs, err
			}
			cs.Note = "that snapshot is gone (git gc): showing what the agents reported"
			break
		}
		t.mu.Lock()
		v := t.view()
		t.mu.Unlock()
		touched := t.touched(v, idx[at], r.Root)
		cs.Git, cs.Root = true, r.Root
		for _, d := range diffs {
			f := ChangedFile{FileDiff: d}
			f.Agents = touched[pathKey(d.Path)]
			if f.OldPath != "" {
				f.Agents = mergeIDs(f.Agents, touched[pathKey(d.OldPath)])
			}
			cs.Files = append(cs.Files, f)
		}
		return cs, nil
	}

	t.mu.Lock()
	v := t.view()
	t.mu.Unlock()
	cs.Files = t.reported(v, idx[at])
	return cs, nil
}

// touched maps each file the agents reported changing from log index from
// on in v (as a pathKey of its path under root) to those agents.
func (t *thread) touched(v logView, from int, root string) map[string][]string {
	out := map[string][]string{}
	root = realPath(root)
	relOf := relMemo(root, t.dir)
	// A part of the log that cannot be read back is left out.
	_ = v.each(from, v.size(), func(_ int, e store.Entry) bool {
		if e.Type != store.EntryEvent || e.Kind != agent.KindFileChange || e.Diff == nil || e.AgentID == "" {
			return true
		}
		for _, p := range []string{e.Diff.Path, e.Diff.OldPath} {
			if p == "" {
				continue
			}
			rel, ok := relOf(p)
			if !ok {
				continue
			}
			k := pathKey(rel)
			out[k] = mergeIDs(out[k], []string{e.AgentID})
		}
		return true
	})
	return out
}

// reported gathers the file changes the agents reported from log index from
// on in v, one entry per file with its hunks in the order they were made.
func (t *thread) reported(v logView, from int) []ChangedFile {
	var out []ChangedFile
	at := map[string]int{}
	relOf := relMemo(realPath(t.dir), t.dir)
	_ = v.each(from, v.size(), func(_ int, e store.Entry) bool {
		if e.Type != store.EntryEvent || e.Kind != agent.KindFileChange || e.Diff == nil {
			return true
		}
		d := *e.Diff
		path := d.Path
		if rel, ok := relOf(path); ok {
			path = rel
		}
		k := pathKey(path)
		i, seen := at[k]
		if !seen {
			d.Path = path
			d.Hunks = append([]agent.Hunk(nil), d.Hunks...)
			out = append(out, ChangedFile{FileDiff: d, Agents: []string{e.AgentID}})
			at[k] = len(out) - 1
			return true
		}
		f := &out[i]
		f.Hunks = append(f.Hunks, d.Hunks...)
		f.Added += d.Added
		f.Removed += d.Removed
		f.Deleted = d.Deleted
		f.Agents = mergeIDs(f.Agents, []string{e.AgentID})
		return true
	})
	return out
}

// Revert puts the given work-tree paths (as a ChangeSet names them) back the
// way they were at checkpoint since, logs it so every agent hears of it at
// its next turn, and returns the revert's entry id for UndoRevert.
func (m *Manager) Revert(ctx context.Context, threadID, since string, paths []string) (string, error) {
	t := m.thread(threadID)
	if t == nil {
		return "", fmt.Errorf("session: no thread %q", threadID)
	}
	if len(paths) == 0 {
		return "", errors.New("session: nothing to revert")
	}
	r := m.repoOf(t)
	if r == nil {
		return "", errNoTracking
	}
	t.mu.Lock()
	cps, _, snaps := t.checkpointsLocked()
	t.mu.Unlock()
	base := ""
	for i, c := range cps {
		if c.EntryID == since {
			base = snaps[i]
		}
	}
	if base == "" {
		return "", errors.New("session: that message has no snapshot to revert to")
	}
	pre, err := m.snapLike(ctx, r, "")
	if err != nil {
		return "", err
	}
	if err := m.restoreSnap(ctx, r, base, paths); err != nil {
		return "", err
	}
	e := store.Entry{Type: store.EntryRevert, ID: store.NewID(), Ref: since, Snap: pre, Files: append([]string(nil), paths...)}
	m.logRevert(t, e)
	return e.ID, nil
}

// UndoRevert takes the files of the revert revertID back to how they were
// just before it. An empty revertID means the latest revert not undone yet.
func (m *Manager) UndoRevert(ctx context.Context, threadID, revertID string) error {
	t := m.thread(threadID)
	if t == nil {
		return fmt.Errorf("session: no thread %q", threadID)
	}
	r := m.repoOf(t)
	if r == nil {
		return errNoTracking
	}
	var rev store.Entry
	t.mu.Lock()
	if revertID == "" {
		revertID = t.index.revert // the newest revert not undone
	}
	if i, ok := t.index.at[revertID]; ok && revertID != "" {
		if e, ok := t.entryLocked(i); ok && e.Type == store.EntryRevert {
			rev = e
		}
	}
	t.mu.Unlock()
	if rev.ID == "" || rev.Snap == "" {
		return errors.New("session: no such revert")
	}
	pre, err := m.snapLike(ctx, r, "")
	if err != nil {
		return err
	}
	if err := m.restoreSnap(ctx, r, rev.Snap, rev.Files); err != nil {
		return err
	}
	m.logRevert(t, store.Entry{Type: store.EntryUnrevert, ID: store.NewID(), Ref: rev.ID, Snap: pre, Files: rev.Files})
	return nil
}

func (m *Manager) logRevert(t *thread, e store.Entry) {
	t.mu.Lock()
	m.appendLocked(t, e)
	msg := t.revertMsgLocked(e)
	t.mu.Unlock()
	m.emit(msg)
}

// revertMsgLocked is the message for a revert entry of the log. t.mu is held.
func (t *thread) revertMsgLocked(e store.Entry) RevertedMsg {
	return RevertedMsg{ThreadID: t.id, EntryID: e.ID, Files: e.Files, Undo: e.Type == store.EntryUnrevert, Since: t.revertSinceLocked(e)}
}

// revertSinceLocked is the text of the message a revert (or the revert an
// undo undid) went back to, as last edited. t.mu is held.
func (t *thread) revertSinceLocked(e store.Entry) string {
	ref := e.Ref
	if e.Type == store.EntryUnrevert {
		if i, ok := t.index.at[e.Ref]; ok {
			if rev, ok := t.entryLocked(i); ok {
				ref = rev.Ref
			}
		}
	}
	i, ok := t.index.at[ref]
	h, isMsg := t.index.head(i)
	if !ok || !isMsg || h.typ != store.EntryUser {
		return ""
	}
	text := h.text
	if h.steer {
		u, ok := t.entryLocked(i)
		if !ok {
			return ""
		}
		text = u.Text
	}
	if s, ok := t.index.edited[ref]; ok {
		text = s
	}
	return oneLine(text)
}

// revertText is how a briefing tells an agent about a revert.
func revertText(e store.Entry, since string) string {
	files := listMax(e.Files, 8)
	if e.Type == store.EntryUnrevert {
		return "[deck] the user undid their revert, putting back the newer versions of: " + files + ". Read them again before editing them."
	}
	when := "an earlier message"
	if since != "" {
		when = "the message \"" + clip(since, 120) + "\""
	}
	return "[deck] the user reverted " + plural(len(e.Files), "file") + " to how they were before " + when + ": " + files +
		". Any changes to them since then are gone; read them again before editing them."
}

// relTo makes p (absolute, or relative to dir) relative to root, a realPath,
// with forward slashes. It fails for a path outside root.
func relTo(root, dir, p string) (string, bool) {
	p = filepath.FromSlash(p)
	if !filepath.IsAbs(p) {
		p = filepath.Join(dir, p)
	}
	rel, err := filepath.Rel(root, realPath(filepath.Clean(p)))
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	return filepath.ToSlash(rel), true
}

// relMemo is relTo(root, dir, p) remembering each path's answer: resolving a
// path costs a file-system lookup per component, and an agent reports the
// same few files over and over.
func relMemo(root, dir string) func(p string) (string, bool) {
	type res struct {
		rel string
		ok  bool
	}
	memo := map[string]res{}
	return func(p string) (string, bool) {
		r, seen := memo[p]
		if !seen {
			r.rel, r.ok = relTo(root, dir, p)
			memo[p] = r
		}
		return r.rel, r.ok
	}
}

// realPath resolves symlinks (and on Windows short 8.3 names) so that a path an
// agent reports compares with the root git gives. A file that is gone is
// resolved through its directory.
func realPath(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	if r, err := filepath.EvalSymlinks(filepath.Dir(p)); err == nil {
		return filepath.Join(r, filepath.Base(p))
	}
	return p
}

// pathKey compares work-tree paths the way the file system does.
func pathKey(p string) string {
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		return strings.ToLower(p)
	}
	return p
}

// mergeIDs adds the ids of b missing from a, keeping a's order first.
func mergeIDs(a, b []string) []string {
	for _, id := range b {
		found := false
		for _, x := range a {
			if x == id {
				found = true
				break
			}
		}
		if !found {
			a = append(a, id)
		}
	}
	return a
}
