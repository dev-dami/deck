package changes

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// Worktrees let one thread work apart from the shared tree (opt-in): a git
// worktree on its own branch, whose changes come back to the main tree as
// uncommitted edits. The user's own branch, index and stash are never touched.

// AddWorktree creates a worktree at path on a new branch from HEAD and returns
// the commit it starts from. Edits not yet committed in the main tree are not
// in it.
func (r *Repo) AddWorktree(ctx context.Context, path, branch string) (base string, err error) {
	out, err := r.run(ctx, r.Root, nil, nil, "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil {
		return "", errors.New("changes: the repository has no commit to start a worktree from")
	}
	base = strings.TrimSpace(out)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	if _, err := r.run(ctx, r.Root, nil, nil, "worktree", "add", "-q", "-b", branch, path, base); err != nil {
		return "", err
	}
	return base, nil
}

// RemoveWorktree removes a worktree and its branch, uncommitted work and all.
func (r *Repo) RemoveWorktree(ctx context.Context, path, branch string) error {
	_, err := r.run(ctx, r.Root, nil, nil, "worktree", "remove", "--force", path)
	if branch != "" {
		if _, berr := r.run(ctx, r.Root, nil, nil, "branch", "-D", branch); err == nil {
			err = berr
		}
	}
	_, _ = r.run(ctx, r.Root, nil, nil, "worktree", "prune")
	return err
}

// Pending is the diff of a worktree's tree as it is now against base (the
// commit it started from, or the tree last applied), the files it touches and
// the tree it was taken at; empty when nothing changed.
func (r *Repo) Pending(ctx context.Context, wt *Repo, base string) (patch string, files []string, now string, err error) {
	now, err = wt.Snapshot(ctx)
	if err != nil {
		return "", nil, "", err
	}
	patch, err = r.run(ctx, r.Root, nil, nil, "diff", "--binary", "--no-color", "--no-ext-diff", "--no-textconv", "-M", base, now, "--")
	if err != nil {
		return "", nil, "", err
	}
	names, err := r.run(ctx, r.Root, nil, nil, "diff", "--name-only", "-z", "-M", base, now, "--")
	if err != nil {
		return "", nil, "", err
	}
	for _, n := range strings.Split(names, nul) {
		if n != "" {
			files = append(files, n)
		}
	}
	return patch, files, now, nil
}

// Apply writes a patch from Pending into this work tree as uncommitted edits.
// It applies whole or not at all; the index is not touched.
func (r *Repo) Apply(ctx context.Context, patch string) error {
	if strings.TrimSpace(patch) == "" {
		return nil
	}
	_, err := r.run(ctx, r.Root, nil, strings.NewReader(patch), "apply", "--whitespace=nowarn", "-")
	return err
}
