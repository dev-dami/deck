package workspace

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dev-dami/deck/internal/deck/store"
)

func TestEntryStats(t *testing.T) {
	t.Parallel()
	start := time.Unix(1000, 0)
	tk := store.Task{
		Status: store.TaskDone, Started: start, Updated: start.Add(4*time.Minute + 20*time.Second),
		Diff: &store.DiffStat{Files: []string{"a.go", "b.go"}, Added: 10, Removed: 3}, Spent: store.Spend{Tokens: 12_400},
	}
	require.Equal(t, "2 files +10 -3 · 12k tokens · 4 min", entryStats(tk))
	// Running: nothing changed yet, and the time is not final.
	require.Equal(t, "", entryStats(store.Task{Status: store.TaskRunning, Started: start}))
}
