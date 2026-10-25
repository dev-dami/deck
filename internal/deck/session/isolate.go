package session

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/dev-dami/deck/internal/deck/agent"
	"github.com/dev-dami/deck/internal/deck/changes"
	"github.com/dev-dami/deck/internal/deck/store"
)

// Isolation is opt-in, per thread: the shared tree stays the default. An
// isolated thread works on its own git worktree and branch; /apply brings its
// changes back to the shared tree as uncommitted edits, /unisolate moves it
// back and removes the worktree.

// UnappliedError is Unisolate's refusal when the worktree has changes that
// were never applied to the shared tree.
type UnappliedError struct{ Files int }

func (e UnappliedError) Error() string {
	return fmt.Sprintf("the worktree has %d changed files not applied to your tree", e.Files)
}

const gitTimeout = 2 * time.Minute

// Isolate moves a thread onto a new worktree and branch (deck/<id>) made from
// the repository's last commit; edits not yet committed stay in the shared
// tree. Its agents restart there like after Move.
func (m *Manager) Isolate(threadID string) (ThreadInfo, []tea.Msg, error) {
	t := m.thread(threadID)
	if t == nil {
		return ThreadInfo{}, nil, fmt.Errorf("session: no thread %q", threadID)
	}
	if m.st == nil {
		return ThreadInfo{}, nil, errors.New("session: no store")
	}
	t.mu.Lock()
	wt, dir := t.worktree, t.dir
	t.mu.Unlock()
	if wt != nil {
		return ThreadInfo{}, nil, fmt.Errorf("session: already isolated on %s", wt.Branch)
	}
	ctx, cancel := context.WithTimeout(m.ctx, gitTimeout)
	defer cancel()
	r, err := changes.Open(ctx, dir)
	if err != nil {
		return ThreadInfo{}, nil, errors.New("session: isolating needs a git repository")
	}
	id := threadID
	if len(id) > 6 {
		id = id[:6]
	}
	path := filepath.Join(filepath.Dir(m.st.Dir()), "worktrees", filepath.Base(r.Root)+"-"+id)
	branch := "deck/" + id
	base, err := r.AddWorktree(ctx, path, branch)
	if err != nil {
		return ThreadInfo{}, nil, err
	}
	target := path // the thread's subfolder of the repository, in the worktree
	if rel, err := filepath.Rel(r.Root, dir); err == nil && rel != "." && !strings.HasPrefix(rel, "..") {
		target = filepath.Join(path, rel)
	}
	t.mu.Lock()
	t.worktree = &store.Worktree{Path: path, Branch: branch, Main: dir, Base: base}
	t.mu.Unlock()
	info, hist, err := m.Move(threadID, target)
	if err != nil {
		t.mu.Lock()
		t.worktree = nil
		t.mu.Unlock()
		_ = r.RemoveWorktree(ctx, path, branch)
		return ThreadInfo{}, nil, err
	}
	return info, hist, nil
}

// isolated opens both trees of an isolated thread.
func (m *Manager) isolated(ctx context.Context, threadID string) (*thread, *store.Worktree, *changes.Repo, *changes.Repo, error) {
	t := m.thread(threadID)
	if t == nil {
		return nil, nil, nil, nil, fmt.Errorf("session: no thread %q", threadID)
	}
	t.mu.Lock()
	wt := t.worktree
	t.mu.Unlock()
	if wt == nil {
		return nil, nil, nil, nil, errors.New("session: this thread is not isolated")
	}
	main, err := changes.Open(ctx, wt.Main)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	tree, err := changes.Open(ctx, wt.Path)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	return t, wt, main, tree, nil
}

// ApplyIsolated writes what an isolated thread changed since it started (or
// since the last apply) into the shared tree as uncommitted edits, whole or
// not at all, and returns the files.
func (m *Manager) ApplyIsolated(threadID string) ([]string, error) {
	ctx, cancel := context.WithTimeout(m.ctx, gitTimeout)
	defer cancel()
	t, wt, main, tree, err := m.isolated(ctx, threadID)
	if err != nil {
		return nil, err
	}
	patch, files, now, err := main.Pending(ctx, tree, wt.Base)
	if err != nil || len(files) == 0 {
		return nil, err
	}
	before, err := main.Snapshot(ctx)
	if err != nil {
		return nil, err
	}
	if err := main.Apply(ctx, patch); err != nil {
		return nil, fmt.Errorf("the changes do not apply cleanly to your tree (it changed the same lines): %w", err)
	}
	next := &store.Worktree{Path: wt.Path, Branch: wt.Branch, Main: wt.Main, Base: now, SubBase: wt.SubBase, SubParent: wt.SubParent}
	// The apply is done; without its after-snapshot it just cannot be undone.
	if after, err := main.Snapshot(ctx); err == nil {
		if diffs, err := main.Diff(ctx, before, after); err == nil {
			next.Applied = &store.Applied{Before: before, After: after, Files: diffPaths(diffs), Base: wt.Base}
		}
	}
	t.mu.Lock()
	t.worktree = next
	t.mu.Unlock()
	m.persistMeta(t)
	m.emit(NoticeMsg{ThreadID: threadID, Text: fmt.Sprintf("applied %d changed files to %s", len(files), wt.Main)})
	return files, nil
}

// UndoApply takes the shared tree back to how it was before the thread's last
// apply, and the worktree's base with it, and returns the files it put back.
// It refuses when one of those files changed in the shared tree since.
func (m *Manager) UndoApply(threadID string) ([]string, error) {
	ctx, cancel := context.WithTimeout(m.ctx, gitTimeout)
	defer cancel()
	t, wt, main, _, err := m.isolated(ctx, threadID)
	if err != nil {
		return nil, err
	}
	a := wt.Applied
	if a == nil {
		return nil, errors.New("session: there is no apply to undo")
	}
	now, err := main.Snapshot(ctx)
	if err != nil {
		return nil, err
	}
	diffs, err := main.Diff(ctx, a.After, now)
	if err != nil {
		return nil, err
	}
	var changed []string
	for _, p := range diffPaths(diffs) {
		if slices.Contains(a.Files, p) {
			changed = append(changed, p)
		}
	}
	if len(changed) > 0 {
		return nil, fmt.Errorf("session: cannot undo the apply: %s changed in your tree since", strings.Join(changed, ", "))
	}
	if err := main.Restore(ctx, a.Before, a.Files); err != nil {
		return nil, err
	}
	t.mu.Lock()
	t.worktree = &store.Worktree{Path: wt.Path, Branch: wt.Branch, Main: wt.Main, Base: a.Base, SubBase: wt.SubBase, SubParent: wt.SubParent}
	t.mu.Unlock()
	m.persistMeta(t)
	m.emit(NoticeMsg{ThreadID: threadID, Text: fmt.Sprintf("undid the apply of %d files to %s", len(a.Files), wt.Main)})
	return a.Files, nil
}

// diffPaths are the paths a diff touches, both names of a rename.
func diffPaths(diffs []agent.FileDiff) []string {
	var out []string
	for _, d := range diffs {
		for _, p := range []string{d.Path, d.OldPath} {
			if p != "" && !slices.Contains(out, p) {
				out = append(out, p)
			}
		}
	}
	return out
}

// Unisolate moves an isolated thread back to the shared tree and removes its
// worktree and branch. With changes never applied it refuses (UnappliedError)
// unless discard is true.
func (m *Manager) Unisolate(threadID string, discard bool) (ThreadInfo, []tea.Msg, error) {
	ctx, cancel := context.WithTimeout(m.ctx, gitTimeout)
	defer cancel()
	t, wt, main, tree, err := m.isolated(ctx, threadID)
	if err != nil {
		return ThreadInfo{}, nil, err
	}
	if !discard {
		if _, files, _, err := main.Pending(ctx, tree, wt.Base); err == nil && len(files) > 0 {
			return ThreadInfo{}, nil, UnappliedError{Files: len(files)}
		}
	}
	t.mu.Lock()
	t.worktree = nil
	t.mu.Unlock()
	info, hist, err := m.Move(threadID, wt.Main)
	if err != nil {
		return ThreadInfo{}, nil, err
	}
	if err := main.RemoveWorktree(ctx, wt.Path, wt.Branch); err != nil {
		m.emit(NoticeMsg{ThreadID: threadID, Text: "could not remove the worktree " + wt.Path + ": " + err.Error()})
	}
	return info, hist, nil
}

// dropWorktree removes a deleted thread's worktree, best effort.
func (m *Manager) dropWorktree(wt *store.Worktree) {
	if wt == nil {
		return
	}
	ctx, cancel := context.WithTimeout(m.ctx, gitTimeout)
	defer cancel()
	if main, err := changes.Open(ctx, wt.Main); err == nil {
		_ = main.RemoveWorktree(ctx, wt.Path, wt.Branch)
	}
}
