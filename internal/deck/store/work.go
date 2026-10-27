package store

import "time"

// Line is a worktree as a line of work: who works in it, what it keeps up
// with, and when it was landed. Every field is optional, so worktrees
// stored before lines existed read as they were.
type Line struct {
	// Fork is the commit the worktree started from; Base may move past it
	// (to a tree) as its changes are applied.
	Fork string `json:"fork,omitempty"`
	// BaseRef is the branch it keeps up with (sync): the shared tree's
	// branch when it was made, or the base it was made from.
	BaseRef string `json:"base_ref,omitempty"`
	// Owner is the member working in it; Task the task it is for.
	Owner   string    `json:"owner,omitempty"`
	Task    string    `json:"task,omitempty"`
	Created time.Time `json:"created,omitzero"`
	// Landed is the worktree's tree when it was last merged into Main, and
	// LandedAt when; one that has not changed since may be pruned.
	Landed   string    `json:"landed,omitempty"`
	LandedAt time.Time `json:"landed_at,omitzero"`
}

// CapsuleRec is a named capsule: a unit of change kept in versions, each a
// SUB capsule, with where it came from, who made it, its tests and review.
type CapsuleRec struct {
	Name string `json:"name"`
	// Source is "worktree", "commits" or "diff"; Worktree the branch of the
	// worktree it came from.
	Source   string           `json:"source"`
	Worktree string           `json:"worktree,omitempty"`
	Authors  []string         `json:"authors,omitempty"`
	Note     string           `json:"note,omitempty"`
	Versions []CapsuleVersion `json:"versions"`
	// Tests is "pass", "fail" or "" (not run), with what ran.
	Tests    string           `json:"tests,omitempty"`
	TestNote string           `json:"test_note,omitempty"`
	Comments []CapsuleComment `json:"comments,omitempty"`
	// Landings are where versions were applied, or reverted from.
	Landings []CapsuleLanding `json:"landings,omitempty"`
	Created  time.Time        `json:"created"`
	Updated  time.Time        `json:"updated"`
}

// CapsuleVersion is one version of a named capsule.
type CapsuleVersion struct {
	ID string `json:"id"` // the SUB capsule
	// Fork and Head are the commits it spans, when it came from git;
	// Commits the newest of them, CommitsTotal how many in all.
	Fork         string      `json:"fork,omitempty"`
	Head         string      `json:"head,omitempty"`
	Commits      []CommitRef `json:"commits,omitempty"`
	CommitsTotal int         `json:"commits_total,omitempty"`
	// Onto is what it was rebased or stacked on, for a version made so.
	Onto    string    `json:"onto,omitempty"`
	Files   int       `json:"files"`
	Add     int       `json:"add,omitempty"`
	Mod     int       `json:"mod,omitempty"`
	Del     int       `json:"del,omitempty"`
	By      string    `json:"by,omitempty"`
	Note    string    `json:"note,omitempty"`
	Created time.Time `json:"created"`
}

// CommitRef is one commit a capsule version holds.
type CommitRef struct {
	SHA     string `json:"sha"`
	Subject string `json:"subject"`
	Author  string `json:"author,omitempty"`
}

// CapsuleComment is a review comment on a capsule, on one file or all.
type CapsuleComment struct {
	By   string    `json:"by"`
	Path string    `json:"path,omitempty"`
	Text string    `json:"text"`
	At   time.Time `json:"at"`
}

// CapsuleLanding is a version applied to a tree ("main" or a worktree's
// name), or reverted from it.
type CapsuleLanding struct {
	Version  int       `json:"version"`
	Into     string    `json:"into"`
	At       time.Time `json:"at"`
	By       string    `json:"by,omitempty"`
	Reverted bool      `json:"reverted,omitempty"`
}
