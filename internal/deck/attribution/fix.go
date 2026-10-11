package attribution

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/dev-dami/deck/internal/deck/brand"
)

// foreign matches the lines the guard removes: other AI co-author trailers and
// "Generated with <AI>" footers. It mirrors the commit-msg hook's patterns.
var foreign = regexp.MustCompile(`(?im)^[^\S\n]*(co-authored-by:.*(claude|anthropic|codex|openai|chatgpt|opencode|gemini|antigravity|copilot|cursor|aider|devin|windsurf)|.*generated (with|by) .*(claude|codex|openai|chatgpt|opencode|gemini|antigravity|copilot|cursor|aider)).*$\n?`)

// ErrPushed means a commit with other attribution is already on a remote, so it
// is left alone: rewriting it would diverge from what others have.
var ErrPushed = errors.New("already pushed")

// Clean returns msg without other AI attribution. It reports whether anything
// was removed.
func Clean(msg string) (string, bool) {
	out := foreign.ReplaceAllString(msg, "")
	return out, out != msg
}

// Fix is the watcher for commits that skipped the hooks (git commit
// --no-verify): it rewrites the unpushed commits made since `since` in dir that
// carry other AI attribution, giving them Deck's trailer instead. The trees are
// unchanged, so the work tree and index are untouched; only the branch moves.
// It returns how many commits it rewrote.
func Fix(ctx context.Context, dir string, since time.Time) (int, error) {
	if _, err := git(ctx, dir, "rev-parse", "--verify", "-q", "HEAD"); err != nil {
		return 0, nil // not a repository, or no commits yet
	}
	head, err := git(ctx, dir, "rev-parse", "HEAD")
	if err != nil {
		return 0, err
	}
	head = strings.TrimSpace(head)
	// Unpushed commits, newest first: hash, parents, committer time, message.
	raw, err := git(ctx, dir, "log", "-z", "-n", "200", "--format=%H%x1f%P%x1f%ct%x1f%B", "HEAD", "--not", "--remotes")
	if err != nil {
		return 0, err
	}
	type commit struct {
		hash, parent, msg string
		at                time.Time
		merge             bool
	}
	var list []commit
	for _, rec := range strings.Split(strings.TrimRight(raw, "\x00"), "\x00") {
		f := strings.SplitN(rec, "\x1f", 4)
		if len(f) < 4 {
			continue
		}
		var secs int64
		fmt.Sscan(f[2], &secs)
		parents := strings.Fields(f[1])
		c := commit{hash: strings.TrimSpace(f[0]), msg: f[3], at: time.Unix(secs, 0), merge: len(parents) > 1}
		if len(parents) > 0 {
			c.parent = parents[0]
		}
		list = append(list, c)
	}
	// The oldest dirty commit made during the turn; everything above it is
	// rewritten onto it (with its own message cleaned only if dirty).
	start := -1
	for i, c := range list {
		if c.at.Before(since.Add(-time.Minute)) {
			break
		}
		if _, dirty := Clean(c.msg); dirty {
			start = i
		}
	}
	if start < 0 {
		// Nothing unpushed to fix; was it pushed already?
		if pushedDirty(ctx, dir, since) {
			return 0, ErrPushed
		}
		return 0, nil
	}
	for _, c := range list[:start+1] {
		if c.merge {
			return 0, fmt.Errorf("a merge commit is in the way")
		}
	}

	parent := list[start].parent
	fixed := 0
	for i := start; i >= 0; i-- {
		c := list[i]
		msg, dirty := Clean(c.msg)
		if dirty {
			if msg, err = addTrailer(ctx, dir, msg); err != nil {
				return 0, err
			}
			fixed++
		}
		meta, err := git(ctx, dir, "log", "-1", "--format=%an%x1f%ae%x1f%aI%x1f%cn%x1f%ce%x1f%cI%x1f%T", c.hash)
		if err != nil {
			return 0, err
		}
		m := strings.Split(strings.TrimSpace(meta), "\x1f")
		if len(m) != 7 {
			return 0, fmt.Errorf("unexpected commit metadata for %s", c.hash)
		}
		args := []string{"commit-tree", m[6]}
		if parent != "" {
			args = append(args, "-p", parent)
		}
		cmd := exec.CommandContext(ctx, "git", args...)
		cmd.Dir = dir
		cmd.Env = append(gitEnv(),
			"GIT_AUTHOR_NAME="+m[0], "GIT_AUTHOR_EMAIL="+m[1], "GIT_AUTHOR_DATE="+m[2],
			"GIT_COMMITTER_NAME="+m[3], "GIT_COMMITTER_EMAIL="+m[4], "GIT_COMMITTER_DATE="+m[5])
		cmd.Stdin = strings.NewReader(msg)
		out, err := cmd.Output()
		if err != nil {
			return 0, fmt.Errorf("commit-tree: %w", err)
		}
		parent = strings.TrimSpace(string(out))
	}
	// Move the branch only if nobody committed meanwhile.
	if _, err := git(ctx, dir, "update-ref", "-m", "deck: commit attribution", "HEAD", parent, head); err != nil {
		return 0, err
	}
	return fixed, nil
}

// pushedDirty reports whether a commit made since `since` and already on a
// remote carries other AI attribution.
func pushedDirty(ctx context.Context, dir string, since time.Time) bool {
	raw, err := git(ctx, dir, "log", "-z", "-n", "50", "--since", since.Add(-time.Minute).Format(time.RFC3339), "--format=%B", "HEAD")
	if err != nil {
		return false
	}
	for _, msg := range strings.Split(raw, "\x00") {
		if _, dirty := Clean(msg); dirty {
			return true
		}
	}
	return false
}

func addTrailer(ctx context.Context, dir, msg string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", "interpret-trailers", "--if-exists", "addIfDifferent", "--trailer", brand.CommitTrailer)
	cmd.Dir = dir
	cmd.Env = gitEnv()
	cmd.Stdin = strings.NewReader(msg)
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("interpret-trailers: %w", err)
	}
	return string(out), nil
}

func git(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	cmd.Env = gitEnv()
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return "", fmt.Errorf("git %s: %s", args[0], msg)
		}
		return "", err
	}
	return string(out), nil
}

// gitEnv is Deck's own environment for git, without the guard's override and
// without prompts.
func gitEnv() []string {
	var env []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(strings.ToUpper(kv), EnvKey+"=") {
			env = append(env, kv)
		}
	}
	return append(env, "GIT_TERMINAL_PROMPT=0")
}
