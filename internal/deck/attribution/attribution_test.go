package attribution

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dev-dami/deck/internal/deck/brand"
)

func TestClean(t *testing.T) {
	for in, want := range map[string]string{
		"fix: x\n\nCo-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>\n":        "fix: x\n\n",
		"fix: x\n\nco-authored-by: Codex <codex@openai.com>\n":                       "fix: x\n\n",
		"fix: x\n\n🤖 Generated with [Claude Code](https://claude.com/claude-code)\n": "fix: x\n\n",
		"fix: x\n\nCo-Authored-By: Ada Lovelace <ada@example.com>\n":                 "fix: x\n\nCo-Authored-By: Ada Lovelace <ada@example.com>\n",
		"fix: x\n\n" + brand.CommitTrailer + "\n":                                    "fix: x\n\n" + brand.CommitTrailer + "\n",
	} {
		if got, _ := Clean(in); got != want {
			t.Errorf("Clean(%q) = %q, want %q", in, got, want)
		}
	}
}

// repo makes a git repository with the guard's hooks installed in a fake home,
// and returns its dir and the environment an agent's git would have.
func repo(t *testing.T) (string, []string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	home := t.TempDir()
	old := homeDir
	homeDir = func() (string, error) { return home, nil }
	t.Cleanup(func() { homeDir = old })
	hooks, err := install()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	run(t, dir, nil, "init", "-q", "-b", "main")
	run(t, dir, nil, "config", "user.name", "Tester")
	run(t, dir, nil, "config", "user.email", "tester@example.com")
	run(t, dir, nil, "config", "commit.gpgsign", "false")
	env := os.Environ()
	for k, v := range envFor(hooks, false) {
		env = append(env, k+"="+v)
	}
	return dir, env
}

func run(t *testing.T, dir string, env []string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

func message(t *testing.T, dir string) string {
	return run(t, dir, nil, "log", "-1", "--format=%B")
}

func TestHookReplacesOtherAttribution(t *testing.T) {
	dir, env := repo(t)
	// The repository's own commit-msg hook still runs, before the guard's.
	own := filepath.Join(dir, ".git", "hooks", "commit-msg")
	if err := os.WriteFile(own, []byte("#!/bin/sh\necho 'Reviewed-By: own-hook' >> \"$1\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	run(t, dir, env, "commit", "-q", "--allow-empty", "-m",
		"feat: x\n\nCo-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>\n\n🤖 Generated with [Claude Code](https://claude.com/claude-code)")
	msg := message(t, dir)
	if strings.Contains(msg, "Claude") {
		t.Errorf("other attribution kept:\n%s", msg)
	}
	if !strings.Contains(msg, brand.CommitTrailer) {
		t.Errorf("no Deck trailer:\n%s", msg)
	}
	if !strings.Contains(msg, "Reviewed-By: own-hook") {
		t.Errorf("the repository's own hook did not run:\n%s", msg)
	}
	// A second commit already carrying the trailer does not get it twice.
	run(t, dir, env, "commit", "-q", "--allow-empty", "-m", "chore: y\n\n"+brand.CommitTrailer)
	if n := strings.Count(message(t, dir), brand.CommitTrailer); n != 1 {
		t.Errorf("trailer appears %d times", n)
	}
}

func TestFixRewritesNoVerifyCommits(t *testing.T) {
	dir, env := repo(t)
	run(t, dir, env, "commit", "-q", "--allow-empty", "-m", "base")
	since := time.Now()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(t, dir, nil, "add", "a.txt")
	run(t, dir, env, "commit", "-q", "--no-verify", "-m", "feat: a\n\nCo-Authored-By: Codex <codex@openai.com>")
	run(t, dir, env, "commit", "-q", "--allow-empty", "-m", "feat: b")
	tree := run(t, dir, nil, "rev-parse", "HEAD^{tree}")

	n, err := Fix(context.Background(), dir, since)
	if err != nil || n != 1 {
		t.Fatalf("Fix = %d, %v", n, err)
	}
	if got := run(t, dir, nil, "rev-parse", "HEAD^{tree}"); got != tree {
		t.Error("the tree changed")
	}
	log := run(t, dir, nil, "log", "--format=%B%x00")
	if strings.Contains(log, "Codex") || !strings.Contains(log, brand.CommitTrailer) || !strings.Contains(log, "feat: b") {
		t.Errorf("log after Fix:\n%s", log)
	}
	if n, err := Fix(context.Background(), dir, since); err != nil || n != 0 {
		t.Errorf("second Fix = %d, %v", n, err)
	}
}

func TestFixLeavesPushedCommits(t *testing.T) {
	dir, env := repo(t)
	remote := t.TempDir()
	run(t, remote, nil, "init", "-q", "--bare")
	run(t, dir, nil, "remote", "add", "origin", remote)
	since := time.Now()
	run(t, dir, env, "commit", "-q", "--allow-empty", "--no-verify", "-m", "feat: a\n\nCo-Authored-By: Claude <noreply@anthropic.com>")
	run(t, dir, nil, "push", "-q", "origin", "main")
	before := run(t, dir, nil, "rev-parse", "HEAD")
	if _, err := Fix(context.Background(), dir, since); !errors.Is(err, ErrPushed) {
		t.Fatalf("Fix err = %v, want ErrPushed", err)
	}
	if run(t, dir, nil, "rev-parse", "HEAD") != before {
		t.Error("a pushed commit was rewritten")
	}
}

func TestFixOutsideRepository(t *testing.T) {
	if n, err := Fix(context.Background(), t.TempDir(), time.Now()); n != 0 || err != nil {
		t.Errorf("Fix = %d, %v", n, err)
	}
}

func TestAuthorAsDeck(t *testing.T) {
	env := envFor("hooks", true)
	if env["GIT_AUTHOR_NAME"] != brand.AuthorName || env["GIT_AUTHOR_EMAIL"] != brand.AuthorEmail {
		t.Fatalf("env %v", env)
	}
	if _, ok := envFor("hooks", false)["GIT_AUTHOR_EMAIL"]; ok {
		t.Fatal("author set while off")
	}

	dir, base := repo(t)
	env2 := append(base, "GIT_AUTHOR_NAME="+brand.AuthorName, "GIT_AUTHOR_EMAIL="+brand.AuthorEmail)
	run(t, dir, env2, "commit", "-q", "--allow-empty", "-m", "feat: x\n\nCo-Authored-By: Claude <noreply@anthropic.com>")
	who := run(t, dir, nil, "log", "-1", "--format=%an <%ae>|%cn <%ce>")
	if strings.TrimSpace(who) != brand.CommitAuthor+"|Tester <tester@example.com>" {
		t.Errorf("author|committer = %q", who)
	}
	msg := message(t, dir)
	if strings.Contains(msg, "Claude") || strings.Contains(msg, "Co-Authored-By: Deck") {
		t.Errorf("message:\n%s", msg)
	}
}
