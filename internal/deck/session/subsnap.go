package session

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"

	"github.com/dev-dami/deck/internal/deck/agent"
	"github.com/dev-dami/deck/internal/deck/approve"
	"github.com/dev-dami/deck/internal/deck/changes"
	"github.com/dev-dami/deck/internal/deck/sub"
)

// Checkpoints are SUB snapshots: hashing goes through SUB's stat cache, so a
// checkpoint of a tree that has barely changed is a directory walk rather
// than git's full index rebuild. Snapshots logged before SUB (git tree ids)
// still diff and restore through git.

// subStore opens the SUB object store beside the thread store once. It is nil
// when it cannot be opened; checkpoints then fall back to git.
func (m *Manager) subStore() *sub.Store {
	m.subOnce.Do(func() {
		if m.st == nil {
			return
		}
		if s, err := sub.Open(filepath.Join(m.st.Dir(), "sub")); err == nil {
			m.sub = s
		}
	})
	return m.sub
}

func isSubSnap(snap string) bool { return strings.HasPrefix(snap, "blake3:") }

// snapLike records the work tree now, as the same kind of snapshot as like
// (so the two can be diffed); a SUB one when like is "".
func (m *Manager) snapLike(ctx context.Context, r *changes.Repo, like string) (string, error) {
	if s := m.subStore(); s != nil && (like == "" || isSubSnap(like)) {
		id, err := s.Snap(ctx, r.Root)
		return string(id), err
	}
	return r.Snapshot(ctx)
}

// snapExists reports whether snap can still be read.
func (m *Manager) snapExists(ctx context.Context, r *changes.Repo, snap string) bool {
	if isSubSnap(snap) {
		s := m.subStore()
		return s != nil && s.Has(sub.ID(snap))
	}
	return r.Exists(ctx, snap)
}

// diffSnaps is the file diffs from snapshot from to snapshot to, both of the
// same kind.
func (m *Manager) diffSnaps(ctx context.Context, r *changes.Repo, from, to string) ([]agent.FileDiff, error) {
	if !isSubSnap(from) {
		return r.Diff(ctx, from, to)
	}
	s := m.subStore()
	if s == nil {
		return nil, errNoTracking
	}
	ops, err := s.Diff(sub.ID(from), sub.ID(to))
	if err != nil {
		return nil, err
	}
	out := make([]agent.FileDiff, 0, len(ops))
	for _, o := range ops {
		a, err := subText(s, o.Before)
		if err != nil {
			return nil, err
		}
		b, err := subText(s, o.After)
		if err != nil {
			return nil, err
		}
		if bytes.IndexByte(a, 0) >= 0 || bytes.IndexByte(b, 0) >= 0 {
			out = append(out, agent.FileDiff{Path: o.Path, Binary: true, New: o.Before == "", Deleted: o.After == ""})
			continue
		}
		d := approve.BuildEditDiff(o.Path, string(a), string(b))
		d.New, d.Deleted = o.Before == "", o.After == ""
		out = append(out, *d)
	}
	return out, nil
}

func subText(s *sub.Store, id sub.ID) ([]byte, error) {
	if id == "" {
		return nil, nil
	}
	return s.ReadFile(id)
}

// restoreSnap puts paths back the way snap has them.
func (m *Manager) restoreSnap(ctx context.Context, r *changes.Repo, snap string, paths []string) error {
	if !isSubSnap(snap) {
		return r.Restore(ctx, snap, paths)
	}
	s := m.subStore()
	if s == nil {
		return errNoTracking
	}
	return s.Restore(ctx, r.Root, sub.ID(snap), paths)
}
