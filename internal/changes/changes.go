// Package changes tracks what happens to a thread's working tree, using git
// without touching the user's branch, index, stash or refs.
//
// A snapshot is a git tree object of the whole work tree as it is on disk:
// tracked files with their edits, and untracked files that are not ignored.
// It is built in a throwaway index seeded from the repository's own, so only
// files whose stat data changed are hashed again. The tree's objects land in
// the repository's object store, unreferenced; git's garbage collection keeps
// them for its prune grace period (two weeks by default).
package changes

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/dev-dami/deck/internal/agent"
)

// ErrNoRepo is returned by Open for a directory outside any git work tree, or
// when git itself is missing.
var ErrNoRepo = errors.New("not a git repository")

// timeout bounds every git command.
const timeout = 30 * time.Second

// Repo is the git work tree a thread works in.
type Repo struct {
	// Root is the top directory of the work tree; the paths of a diff are
	// relative to it, with forward slashes.
	Root  string
	git   string
	index string // the repository's own index file (read, never written)
}

// Open finds the work tree containing dir.
func Open(ctx context.Context, dir string) (*Repo, error) {
	bin, err := exec.LookPath("git")
	if err != nil {
		return nil, ErrNoRepo
	}
	r := &Repo{git: bin}
	out, err := r.run(ctx, dir, nil, nil, "rev-parse", "--show-toplevel", "--git-path", "index")
	if err != nil {
		return nil, ErrNoRepo
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) < 2 || lines[0] == "" {
		return nil, ErrNoRepo
	}
	r.Root = filepath.Clean(filepath.FromSlash(lines[0]))
	idx := filepath.FromSlash(lines[1])
	if !filepath.IsAbs(idx) {
		idx = filepath.Join(dir, idx)
	}
	r.index = idx
	return r, nil
}

// Snapshot records the work tree as it is now and returns the tree's id.
func (r *Repo) Snapshot(ctx context.Context) (string, error) {
	tmp, err := os.MkdirTemp("", "deck-snap-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmp)
	idx := filepath.Join(tmp, "index")
	if err := copyIndex(r.index, idx); err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	env := []string{"GIT_INDEX_FILE=" + idx}
	// core.fscache (Git for Windows) answers lstat from directory listings,
	// which can show a just-written file's old stat data; a same-size edit
	// would then look clean and drop out of the snapshot.
	add := []string{"-c", "core.fscache=false", "-c", "core.safecrlf=false", "-c", "advice.addEmbeddedRepo=false", "add", "-A", "--", "."}
	if _, err := r.run(ctx, r.Root, env, nil, add...); err != nil {
		// a copied index git cannot use on its own (a split index, say):
		// hash everything into a fresh one instead
		_ = os.Remove(idx)
		if _, err := r.run(ctx, r.Root, env, nil, add...); err != nil {
			return "", err
		}
	}
	out, err := r.run(ctx, r.Root, env, nil, "write-tree")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// Diff is every file that differs between two snapshots, renames detected.
func (r *Repo) Diff(ctx context.Context, from, to string) ([]agent.FileDiff, error) {
	out, err := r.run(ctx, r.Root, nil, nil, "-c", "core.quotePath=false",
		"diff", "--no-color", "--no-ext-diff", "--no-textconv", "-M",
		"--src-prefix=a/", "--dst-prefix=b/", from, to, "--")
	if err != nil {
		return nil, err
	}
	return agent.ParseUnified(out), nil
}

// Exists reports whether a snapshot is still in the object store.
func (r *Repo) Exists(ctx context.Context, tree string) bool {
	_, err := r.run(ctx, r.Root, nil, nil, "cat-file", "-e", tree+"^{tree}")
	return err == nil
}

// Restore puts each path back the way it is in tree: its content and mode
// when tree has it, gone when it does not. Paths are relative to Root with
// forward slashes, as a Diff names them. The user's index is not touched.
// Content is written the way git checkout would write it, so line endings
// follow the repository's core.autocrlf and attributes.
func (r *Repo) Restore(ctx context.Context, tree string, paths []string) error {
	for _, p := range paths {
		if p == "" || strings.HasPrefix(p, "/") || strings.Contains("/"+p+"/", "/../") {
			return fmt.Errorf("changes: bad path %q", p)
		}
	}
	// which of them tree has; this also fails, before anything is touched,
	// when tree is gone
	out, err := r.run(ctx, r.Root, nil, nil, append([]string{"ls-tree", "-z", "--name-only", "--full-tree", tree, "--"}, paths...)...)
	if err != nil {
		return err
	}
	in := map[string]bool{}
	for _, p := range strings.Split(out, nul) {
		in[p] = true
	}
	var keep []string
	for _, p := range paths {
		if in[p] {
			keep = append(keep, p)
			continue
		}
		full := filepath.Join(r.Root, filepath.FromSlash(p))
		if err := os.Remove(full); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	if len(keep) == 0 {
		return nil
	}
	tmp, err := os.MkdirTemp("", "deck-restore-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	// checkout writes through a throwaway index, which also applies the
	// repository's line-ending and filter settings to what it writes
	env := []string{"GIT_INDEX_FILE=" + filepath.Join(tmp, "index")}
	args := []string{"checkout", tree, "--pathspec-from-file=-", "--pathspec-file-nul"}
	_, err = r.run(ctx, r.Root, env, strings.NewReader(strings.Join(literal(keep), nul)), args...)
	return err
}

func literal(paths []string) []string {
	out := make([]string, len(paths))
	for i, p := range paths {
		out[i] = ":(literal)" + p
	}
	return out
}

func (r *Repo) run(ctx context.Context, dir string, env []string, stdin io.Reader, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, r.git, args...)
	cmd.Dir = dir
	cmd.Env = append(cleanEnv(), env...)
	cmd.Stdin = stdin
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	hide(cmd)
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errb.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("git %s: %s", args[0], firstLine(msg))
	}
	return out.String(), nil
}

// cleanEnv is the environment without anything that would point git at
// another repository or index, plus settings that keep it from prompting or
// taking optional locks the user's own git commands might want.
func cleanEnv() []string {
	var out []string
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		switch strings.ToUpper(k) {
		case "GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE", "GIT_OBJECT_DIRECTORY", "GIT_COMMON_DIR", "GIT_PREFIX":
			continue
		}
		out = append(out, kv)
	}
	return append(out, "GIT_OPTIONAL_LOCKS=0", "GIT_TERMINAL_PROMPT=0")
}

func firstLine(s string) string {
	l, _, _ := strings.Cut(s, "\n")
	return strings.TrimSpace(l)
}

// copyIndex copies an index file and its modification time. git decides which
// entries' stat data it cannot trust (the "racy git" check) by comparing them
// with the index file's own mtime; a copy stamped now would make an edit that
// kept a file's size within the second of the last index write look clean.
func copyIndex(src, dst string) error {
	fi, err := os.Stat(src)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	if err := os.WriteFile(dst, data, 0o600); err != nil {
		return err
	}
	return os.Chtimes(dst, fi.ModTime(), fi.ModTime())
}

// nul separates paths in git's -z output and --pathspec-file-nul input.
const nul = "\x00"
