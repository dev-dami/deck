package gitutil

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// helper to check if git is available.
func gitAvailable() bool {
	_, err := exec.LookPath("git")
	return err == nil
}

// helper to run git commands in a directory.
func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	gitArgs := append([]string{"-c", "commit.gpgsign=false", "-c", "tag.gpgsign=false"}, args...)
	cmd := exec.CommandContext(context.Background(), "git", gitArgs...)
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	require.NoError(t, err, "git %v failed: %s", args, string(output))
}

func TestCurrentBranch(t *testing.T) {
	if !gitAvailable() {
		t.Skip("git is not available")
	}

	t.Run("returns branch name for normal checkout", func(t *testing.T) {
		testDir := t.TempDir()
		runGit(t, testDir, "init")
		runGit(t, testDir, "config", "user.email", "test@test.com")
		runGit(t, testDir, "config", "user.name", "Test User")

		testFile := filepath.Join(testDir, "test.txt")
		require.NoError(t, os.WriteFile(testFile, []byte("test"), 0o644))
		runGit(t, testDir, "add", ".")
		runGit(t, testDir, "commit", "-m", "initial commit")

		// Ensure we're on 'main' regardless of default branch name.
		cmd := exec.CommandContext(context.Background(), "git", "branch", "--show-current")
		cmd.Dir = testDir
		out, _ := cmd.Output()
		currentBranch := string(out[:len(out)-1])
		if currentBranch != "main" {
			runGit(t, testDir, "checkout", "-b", "main")
		}

		branch := CurrentBranch(testDir)
		require.Equal(t, "main", branch)
	})

	t.Run("returns branch name for feature branch", func(t *testing.T) {
		testDir := t.TempDir()
		runGit(t, testDir, "init")
		runGit(t, testDir, "config", "user.email", "test@test.com")
		runGit(t, testDir, "config", "user.name", "Test User")

		testFile := filepath.Join(testDir, "test.txt")
		require.NoError(t, os.WriteFile(testFile, []byte("test"), 0o644))
		runGit(t, testDir, "add", ".")
		runGit(t, testDir, "commit", "-m", "initial commit")
		runGit(t, testDir, "checkout", "-b", "feature/git-branch-display")

		branch := CurrentBranch(testDir)
		require.Equal(t, "feature/git-branch-display", branch)
	})

	t.Run("returns empty string for detached HEAD", func(t *testing.T) {
		testDir := t.TempDir()
		runGit(t, testDir, "init")
		runGit(t, testDir, "config", "user.email", "test@test.com")
		runGit(t, testDir, "config", "user.name", "Test User")

		testFile := filepath.Join(testDir, "test.txt")
		require.NoError(t, os.WriteFile(testFile, []byte("test"), 0o644))
		runGit(t, testDir, "add", ".")
		runGit(t, testDir, "commit", "-m", "initial commit")

		cmd := exec.CommandContext(context.Background(), "git", "rev-parse", "HEAD")
		cmd.Dir = testDir
		output, err := cmd.Output()
		require.NoError(t, err)
		commitHash := string(output[:len(output)-1])

		runGit(t, testDir, "checkout", commitHash)

		branch := CurrentBranch(testDir)
		require.Empty(t, branch)
	})

	t.Run("returns empty string for non-git directory", func(t *testing.T) {
		testDir := t.TempDir()

		branch := CurrentBranch(testDir)
		require.Empty(t, branch)
	})

	t.Run("finds git directory from subdirectory", func(t *testing.T) {
		testDir := t.TempDir()
		runGit(t, testDir, "init")
		runGit(t, testDir, "config", "user.email", "test@test.com")
		runGit(t, testDir, "config", "user.name", "Test User")

		testFile := filepath.Join(testDir, "test.txt")
		require.NoError(t, os.WriteFile(testFile, []byte("test"), 0o644))
		runGit(t, testDir, "add", ".")
		runGit(t, testDir, "commit", "-m", "initial commit")
		runGit(t, testDir, "checkout", "-b", "develop")

		subDir := filepath.Join(testDir, "src", "internal", "pkg")
		require.NoError(t, os.MkdirAll(subDir, 0o755))

		branch := CurrentBranch(subDir)
		require.Equal(t, "develop", branch)
	})

	t.Run("ignores unusual branch config entries", func(t *testing.T) {
		testDir := t.TempDir()
		runGit(t, testDir, "init")
		runGit(t, testDir, "config", "user.email", "test@test.com")
		runGit(t, testDir, "config", "user.name", "Test User")

		testFile := filepath.Join(testDir, "test.txt")
		require.NoError(t, os.WriteFile(testFile, []byte("test"), 0o644))
		runGit(t, testDir, "add", ".")
		runGit(t, testDir, "commit", "-m", "initial commit")
		runGit(t, testDir, "checkout", "-b", "odd-config")

		// Some repositories have branch entries that point at non-branch
		// refs, which config parsers can reject outright.
		runGit(t, testDir, "config", "branch.odd-config.merge", "refs/pull/1234/head")

		branch := CurrentBranch(testDir)
		require.Equal(t, "odd-config", branch)
	})

	t.Run("follows gitdir pointer in worktree", func(t *testing.T) {
		root := t.TempDir()
		gitDir := filepath.Join(root, "repo", ".git", "worktrees", "wt")
		require.NoError(t, os.MkdirAll(gitDir, 0o755))
		require.NoError(t, os.WriteFile(
			filepath.Join(gitDir, "HEAD"),
			[]byte("ref: refs/heads/worktree-branch\n"),
			0o644,
		))

		worktree := filepath.Join(root, "wt")
		require.NoError(t, os.MkdirAll(worktree, 0o755))
		require.NoError(t, os.WriteFile(
			filepath.Join(worktree, ".git"),
			[]byte("gitdir: ../repo/.git/worktrees/wt\n"),
			0o644,
		))

		branch := CurrentBranch(worktree)
		require.Equal(t, "worktree-branch", branch)
	})
}

func TestRepoRoot(t *testing.T) {
	if !gitAvailable() {
		t.Skip("git is not available")
	}
	root := t.TempDir()
	runGit(t, root, "init")
	runGit(t, root, "config", "user.email", "test@test.com")
	runGit(t, root, "config", "user.name", "Test User")
	require.NoError(t, os.WriteFile(filepath.Join(root, "a.txt"), []byte("a"), 0o644))
	runGit(t, root, "add", ".")
	runGit(t, root, "commit", "-m", "init")
	sub := filepath.Join(root, "sub")
	require.NoError(t, os.Mkdir(sub, 0o755))
	wt := filepath.Join(t.TempDir(), "wt")
	runGit(t, root, "worktree", "add", wt)

	same := func(want, got string) {
		t.Helper()
		w, _ := filepath.EvalSymlinks(want)
		g, _ := filepath.EvalSymlinks(got)
		require.Equal(t, w, g)
	}
	same(root, RepoRoot(root))
	same(root, RepoRoot(sub))
	same(root, RepoRoot(wt))

	plain := t.TempDir()
	same(plain, RepoRoot(plain))
}
