package adapter

import (
	deckagent "github.com/dev-dami/deck/internal/deck/agent"
)

// changedDiff is one changed file under /work for the tests.
func changedDiff() deckagent.FileDiff {
	return deckagent.FileDiff{
		Path: "/work/a/b.go", Added: 1, Removed: 1,
		Hunks: []deckagent.Hunk{{OldStart: 1, OldLines: 1, NewStart: 1, NewLines: 1, Lines: []deckagent.DiffLine{
			{Op: deckagent.OpDel, Text: "old"}, {Op: deckagent.OpAdd, Text: "new"},
		}}},
	}
}
