package gitutil

import (
	"os"
	"path/filepath"
	"strings"
)

// RepoRoot returns the main working tree of the repository dir is in, so a
// linked worktree and its subdirectories all name the same project. It
// returns dir, made absolute, when dir is not in a repository or the root
// can't be told.
func RepoRoot(dir string) string {
	abs, err := filepath.Abs(dir)
	if err != nil {
		abs = dir
	}
	gitDir := findGitDir(abs)
	if gitDir == "" {
		return abs
	}
	// A linked worktree's git dir is <root>/.git/worktrees/<name>, and its
	// commondir file points back at <root>/.git.
	if common, err := os.ReadFile(filepath.Join(gitDir, "commondir")); err == nil {
		c := strings.TrimSpace(string(common))
		if !filepath.IsAbs(c) {
			c = filepath.Join(gitDir, c)
		}
		gitDir = filepath.Clean(c)
	}
	if filepath.Base(gitDir) != ".git" {
		return abs
	}
	return filepath.Dir(gitDir)
}
