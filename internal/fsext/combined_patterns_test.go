package fsext

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestCombinedPatternsMatchFullWalk checks that building a directory's
// patterns from its parent's gives the same list, in the same order, as
// reading every level from the root.
func TestCombinedPatternsMatchFullWalk(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	dirs := []string{"a", "a/b", "a/b/c", "a/d", "e"}
	for _, d := range dirs {
		require.NoError(t, os.MkdirAll(filepath.Join(root, d), 0o755))
	}
	require.NoError(t, os.WriteFile(filepath.Join(root, ".gitignore"), []byte("*.root\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "a", ".gitignore"), []byte("*.a\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "a", "b", "c", ".deckignore"), []byte("*.c\n"), 0o644))

	dl := NewDirectoryLister(root)
	for _, d := range append([]string{"."}, dirs...) {
		dir := filepath.Join(root, d)
		rel, _ := filepath.Rel(root, dir)
		want := dl.collectPatterns(pathToComponents(rel))
		got := dl.getCombinedPatterns(dir)
		require.Equal(t, len(want), len(got), "dir %s", d)
		for i := range want {
			require.Equal(t, want[i], got[i], "dir %s pattern %d", d, i)
		}
	}

	require.True(t, dl.shouldIgnore(filepath.Join(root, "a", "b", "c", "x.c"), nil, false))
	require.True(t, dl.shouldIgnore(filepath.Join(root, "a", "d", "x.a"), nil, false))
	require.False(t, dl.shouldIgnore(filepath.Join(root, "e", "x.a"), nil, false))
	require.False(t, dl.shouldIgnore(filepath.Join(root, "a", "d", "x.c"), nil, false))
}
