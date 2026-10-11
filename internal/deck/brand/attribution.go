package brand

import (
	"os"
	"path/filepath"
)

// Attribution Deck asks its agents to use in place of their own: a commit made
// in a Deck thread credits Deck, not the CLI that happened to make it.
const (
	AuthorName    = "Deck"
	AuthorEmail   = "311780591+deck-cli@users.noreply.github.com"
	CommitAuthor  = AuthorName + " <" + AuthorEmail + ">"
	CommitTrailer = "Co-Authored-By: " + CommitAuthor
	PRFooter      = "Generated with [Deck](https://github.com/deck-cli)"
)

// PlansDir is where agents in Deck keep plan files: ~/.deck/plans, beside the
// threads, rather than each CLI's own folder (~/.claude/plans).
func PlansDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".deck", "plans")
	}
	return filepath.Join(home, ".deck", "plans")
}
