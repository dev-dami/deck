package changes

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// Branch work: a worktree as a long-lived line of work of its own, with
// commits, a base it keeps up with and a check of how it would merge. Every
// command here runs in the worktree or reads objects; the user's own branch,
// index and stash are still never touched.

// ConflictError is a sync or merge check that stopped on files both sides
// changed. A sync that hit it was aborted: the worktree is as it was.
type ConflictError struct {
	Paths []string
}

func (e *ConflictError) Error() string {
	n := len(e.Paths)
	show := e.Paths[:min(n, 5)]
	s := "changes: conflicts in " + strings.Join(show, ", ")
	if n > len(show) {
		s += fmt.Sprintf(" and %d more", n-len(show))
	}
	return s
}

// deckIdent is who commits in a worktree when git has no user configured.
var deckIdent = []string{"GIT_AUTHOR_NAME=deck", "GIT_AUTHOR_EMAIL=deck@localhost", "GIT_COMMITTER_NAME=deck", "GIT_COMMITTER_EMAIL=deck@localhost"}

// AddWorktreeAt is AddWorktree from ref (a branch, tag or commit; "" is
// HEAD) instead of HEAD.
func (r *Repo) AddWorktreeAt(ctx context.Context, path, branch, ref string) (base string, err error) {
	if ref == "" {
		return r.AddWorktree(ctx, path, branch)
	}
	base, err = r.Commit(ctx, ref)
	if err != nil {
		return "", fmt.Errorf("changes: no commit %q to start a worktree from", ref)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	if _, err := r.run(ctx, r.Root, nil, nil, "worktree", "add", "-q", "-b", branch, path, base); err != nil {
		return "", err
	}
	return base, nil
}

// Commit is the commit ref names.
func (r *Repo) Commit(ctx context.Context, ref string) (string, error) {
	if strings.HasPrefix(ref, "-") {
		return "", fmt.Errorf("changes: bad ref %q", ref)
	}
	out, err := r.run(ctx, r.Root, nil, nil, "rev-parse", "--verify", "--end-of-options", ref+"^{commit}")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// Branch is the branch checked out in this work tree, "" when detached.
func (r *Repo) Branch(ctx context.Context) string {
	out, err := r.run(ctx, r.Root, nil, nil, "symbolic-ref", "-q", "--short", "HEAD")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// Divergence counts the commits head has that base does not (ahead) and
// the other way round (behind).
func (r *Repo) Divergence(ctx context.Context, base, head string) (ahead, behind int, err error) {
	out, err := r.run(ctx, r.Root, nil, nil, "rev-list", "--left-right", "--count", "--end-of-options", base+"..."+head)
	if err != nil {
		return 0, 0, err
	}
	f := strings.Fields(out)
	if len(f) != 2 {
		return 0, 0, fmt.Errorf("changes: rev-list said %q", out)
	}
	behind, _ = strconv.Atoi(f[0])
	ahead, _ = strconv.Atoi(f[1])
	return ahead, behind, nil
}

// Dirty lists the files of the work tree that differ from its HEAD:
// changed, staged, deleted or untracked and not ignored.
func (r *Repo) Dirty(ctx context.Context) ([]string, error) {
	out, err := r.run(ctx, r.Root, nil, nil, "status", "--porcelain=v1", "-z", "--untracked-files=all")
	if err != nil {
		return nil, err
	}
	var files []string
	recs := strings.Split(out, "\x00")
	for i := 0; i < len(recs); i++ {
		rec := recs[i]
		if len(rec) < 4 {
			continue
		}
		files = append(files, rec[3:])
		if rec[0] == 'R' || rec[0] == 'C' {
			i++ // the old name follows
		}
	}
	return files, nil
}

// CommitAll commits everything in the work tree that is not ignored, with
// msg, and returns the new commit; "" when there was nothing to commit.
// Without a configured git identity the commit is Deck's.
func (r *Repo) CommitAll(ctx context.Context, msg string) (string, error) {
	if _, err := r.run(ctx, r.Root, nil, nil, "add", "-A"); err != nil {
		return "", err
	}
	if _, err := r.run(ctx, r.Root, nil, nil, "diff", "--cached", "--quiet"); err == nil {
		return "", nil
	}
	if _, err := r.run(ctx, r.Root, r.ident(ctx), nil, "-c", "commit.gpgsign=false", "commit", "-q", "--no-verify", "-m", msg); err != nil {
		return "", err
	}
	return r.Commit(ctx, "HEAD")
}

// ident is the environment a commit needs: none when git knows who the
// user is, Deck's name otherwise.
func (r *Repo) ident(ctx context.Context) []string {
	if out, err := r.run(ctx, r.Root, nil, nil, "config", "user.email"); err == nil && strings.TrimSpace(out) != "" {
		return nil
	}
	return deckIdent
}

// Sync brings ref into this work tree's branch: its uncommitted work is
// committed first (with wip as the message), then ref is merged in, or the
// branch rebased onto it. On a conflict nothing changes and a
// *ConflictError names the files.
func (r *Repo) Sync(ctx context.Context, ref, wip string, rebase bool) error {
	if _, err := r.Commit(ctx, ref); err != nil {
		return fmt.Errorf("changes: no commit %q to sync with", ref)
	}
	if _, err := r.CommitAll(ctx, wip); err != nil {
		return err
	}
	env := r.ident(ctx)
	args := []string{"-c", "commit.gpgsign=false", "merge", "--no-edit", "-q", "--end-of-options", ref}
	abort := []string{"merge", "--abort"}
	if rebase {
		args = []string{"-c", "commit.gpgsign=false", "rebase", "-q", "--end-of-options", ref}
		abort = []string{"rebase", "--abort"}
	}
	if _, err := r.run(ctx, r.Root, env, nil, args...); err != nil {
		out, _ := r.run(ctx, r.Root, nil, nil, "diff", "--name-only", "-z", "--diff-filter=U")
		_, _ = r.run(ctx, r.Root, nil, nil, abort...)
		if paths := splitZ(out); len(paths) > 0 {
			return &ConflictError{Paths: paths}
		}
		return err
	}
	return nil
}

// MergeCheck reports the files a merge of theirs into ours would conflict
// on, without touching any work tree (git merge-tree, git 2.38 or later).
func (r *Repo) MergeCheck(ctx context.Context, ours, theirs string) ([]string, error) {
	out, code, err := r.runCode(ctx, "merge-tree", "--write-tree", "--name-only", "--no-messages", "-z", "--end-of-options", ours, theirs)
	switch {
	case err != nil:
		return nil, err
	case code == 0:
		return nil, nil
	case code != 1:
		return nil, fmt.Errorf("changes: merge-tree failed (git 2.38 or later is needed)")
	}
	paths := splitZ(out)
	if len(paths) > 0 {
		paths = paths[1:] // the tree it wrote
	}
	return paths, nil
}

// CommitRef is one commit of a range.
type CommitRef struct {
	SHA, Subject, Author string
}

// maxCommits bounds a listed range.
const maxCommits = 500

// Commits lists from..to, newest first, at most maxCommits.
func (r *Repo) Commits(ctx context.Context, from, to string) ([]CommitRef, error) {
	out, err := r.run(ctx, r.Root, nil, nil, "log", "-z", "--format=%H%x1f%s%x1f%an", "-n", strconv.Itoa(maxCommits), "--end-of-options", from+".."+to)
	if err != nil {
		return nil, err
	}
	var cs []CommitRef
	for _, rec := range splitZ(out) {
		f := strings.SplitN(strings.TrimLeft(rec, "\n"), "\x1f", 3)
		if len(f) == 3 {
			cs = append(cs, CommitRef{SHA: f[0], Subject: f[1], Author: f[2]})
		}
	}
	return cs, nil
}

// RawChange is one file a diff of two trees touches. A blob of all zeros
// (or "") means the file is not on that side; Exec is the new side's mode.
type RawChange struct {
	Path             string
	OldBlob, NewBlob string
	OldExec, NewExec bool
	// Link or submodule on either side: not a regular file.
	Special bool
}

// Changed is what turns tree-ish from into tree-ish to, file by file,
// renames as a delete and an add.
func (r *Repo) Changed(ctx context.Context, from, to string) ([]RawChange, error) {
	out, err := r.run(ctx, r.Root, nil, nil, "-c", "core.quotePath=false", "diff-tree", "-r", "-z", "--no-renames", "--end-of-options", from, to)
	if err != nil {
		return nil, err
	}
	var chs []RawChange
	recs := strings.Split(out, "\x00")
	for i := 0; i+1 < len(recs); i += 2 {
		meta, p := recs[i], recs[i+1]
		f := strings.Fields(strings.TrimPrefix(meta, ":"))
		if len(f) < 5 {
			continue
		}
		ch := RawChange{Path: p, OldBlob: nullBlob(f[2]), NewBlob: nullBlob(f[3]), OldExec: f[0] == "100755", NewExec: f[1] == "100755"}
		ch.Special = special(f[0]) || special(f[1])
		chs = append(chs, ch)
	}
	return chs, nil
}

func nullBlob(s string) string {
	if strings.Trim(s, "0") == "" {
		return ""
	}
	return s
}

// special is a mode that is neither absent nor a regular file.
func special(mode string) bool {
	return mode != "000000" && mode != "100644" && mode != "100755"
}

// Blobs reads the blobs named by ids in one git process, calling fn with
// each in order. Large sets stream: one blob is in memory at a time.
func (r *Repo) Blobs(ctx context.Context, ids []string, fn func(id string, b []byte) error) error {
	if len(ids) == 0 {
		return nil
	}
	cmd := exec.CommandContext(ctx, r.git, "cat-file", "--batch")
	cmd.Dir = r.Root
	cmd.Env = cleanEnv()
	hide(cmd)
	in, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	outp, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() {
		w := bufio.NewWriter(in)
		for _, id := range ids {
			fmt.Fprintln(w, id)
		}
		w.Flush()
		in.Close()
	}()
	br := bufio.NewReaderSize(outp, 64<<10)
	var ferr error
	for _, id := range ids {
		head, err := br.ReadString('\n')
		if err != nil {
			ferr = err
			break
		}
		f := strings.Fields(head)
		if len(f) != 3 {
			ferr = fmt.Errorf("changes: no blob %s", id)
			break
		}
		n, _ := strconv.Atoi(f[2])
		b := make([]byte, n+1) // and the newline after it
		if _, err := io.ReadFull(br, b); err != nil {
			ferr = err
			break
		}
		if err := fn(id, b[:n]); err != nil {
			ferr = err
			break
		}
	}
	if ferr != nil {
		_ = cmd.Process.Kill()
	}
	werr := cmd.Wait()
	if ferr != nil {
		return ferr
	}
	return werr
}

// runCode runs git in the work tree and returns its output and exit code;
// err is only for git not running at all.
func (r *Repo) runCode(ctx context.Context, args ...string) (string, int, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, r.git, args...)
	cmd.Dir = r.Root
	cmd.Env = cleanEnv()
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	hide(cmd)
	err := cmd.Run()
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return out.String(), ee.ExitCode(), nil
	}
	if err != nil {
		return "", 0, err
	}
	return out.String(), 0, nil
}

func splitZ(s string) []string {
	var out []string
	for _, p := range strings.Split(s, "\x00") {
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
