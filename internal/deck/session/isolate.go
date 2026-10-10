package session

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

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
	if err := main.Apply(ctx, patch); err != nil {
		return nil, fmt.Errorf("the changes do not apply cleanly to your tree (it changed the same lines): %w", err)
	}
	t.mu.Lock()
	t.worktree = &store.Worktree{Path: wt.Path, Branch: wt.Branch, Main: wt.Main, Base: now}
	t.mu.Unlock()
	m.persistMeta(t)
	m.emit(NoticeMsg{ThreadID: threadID, Text: fmt.Sprintf("applied %d changed files to %s", len(files), wt.Main)})
	return files, nil
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
