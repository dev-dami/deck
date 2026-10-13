package sub

import (
	"bytes"
	"context"
	"fmt"
	"slices"

	"github.com/aymanbagabas/go-udiff"
)

// State is how one op of a capsule stands against a work tree.
type State string

const (
	// StateFits: the file is what the capsule was captured against.
	StateFits State = "fits"
	// StateApplied: the file already has the capsule's change.
	StateApplied State = "applied"
	// StateMerge: the file changed since, but on other lines; a three-way
	// merge combines both.
	StateMerge State = "merge"
	// StateConflict: both sides changed the same lines, or the file is
	// binary, or one side deleted what the other edited.
	StateConflict State = "conflict"
)

// Preview is one op and how it would apply.
type Preview struct {
	Op
	State  State
	Reason string // for StateConflict
	merged []byte // for StateMerge
}

// Strategy says what MergeApply does with a conflicting file.
type Strategy string

const (
	// Refuse writes nothing when any file conflicts (the default).
	Refuse Strategy = ""
	// Theirs takes the capsule's version of a conflicting file.
	Theirs Strategy = "theirs"
	// Ours keeps the work tree's version of a conflicting file and applies
	// the rest.
	Ours Strategy = "ours"
)

// PreviewMerge reports, file by file, how the capsule (with its chain) would
// apply to the tree at root. Nothing is written.
func (s *Store) PreviewMerge(ctx context.Context, root string, id ID) ([]Preview, error) {
	chs, err := s.changes(id)
	if err != nil {
		return nil, err
	}
	sc := s.stats(root)
	defer sc.save()
	out := make([]Preview, 0, len(chs))
	for _, ch := range chs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		cur, err := s.current(sc, root, ch.Path)
		if err != nil {
			return nil, err
		}
		p := Preview{Op: ch.Op}
		switch {
		case cur == ch.After:
			p.State = StateApplied
		case slices.Contains(ch.states, cur):
			p.State = StateFits
			p.Before = cur
		default:
			p.State, p.Reason, p.merged = s.merge3(root, ch.Op)
		}
		out = append(out, p)
	}
	return out, nil
}

// merge3 tries a line-level three-way merge of op's change into the file
// as it is now.
func (s *Store) merge3(root string, o Op) (State, string, []byte) {
	switch {
	case o.Before == "":
		return StateConflict, "added here and in the capsule", nil
	case o.After == "":
		return StateConflict, "the capsule deletes a file changed here", nil
	}
	cur, err := readTree(root, o.Path)
	if err != nil {
		return StateConflict, "deleted here, changed in the capsule", nil
	}
	base, err := s.ReadFile(o.Before)
	if err != nil {
		return StateConflict, err.Error(), nil
	}
	theirs, err := s.ReadFile(o.After)
	if err != nil {
		return StateConflict, err.Error(), nil
	}
	if isBinary(base) || isBinary(theirs) || isBinary(cur) {
		return StateConflict, "binary file changed on both sides", nil
	}
	merged, ok := Merge3(string(base), string(cur), string(theirs))
	if !ok {
		return StateConflict, "both sides changed the same lines", nil
	}
	return StateMerge, "", []byte(merged)
}

// Merge3 merges the line changes base→ours and base→theirs. ok is false when
// they touch the same lines differently.
func Merge3(base, ours, theirs string) (string, bool) {
	x, y := udiff.Lines(base, ours), udiff.Lines(base, theirs)
	// Merge puts two different insertions at one point side by side; for
	// source that is a conflict, not a merge.
	for _, a := range x {
		for _, b := range y {
			if a.Start == a.End && b.Start == b.End && a.Start == b.Start && a.New != b.New {
				return "", false
			}
		}
	}
	edits, ok := udiff.Merge(x, y)
	if !ok {
		return "", false
	}
	out, err := udiff.Apply(base, edits)
	if err != nil {
		return "", false
	}
	return out, true
}

// MergeApply applies the capsule (with its chain) to the tree at root,
// merging files that changed since on other lines. Conflicts are settled by
// strategy; with Refuse any conflict writes nothing and returns a
// *ConflictError. It returns what happened to each file.
func (s *Store) MergeApply(ctx context.Context, root string, id ID, strategy Strategy) ([]Preview, error) {
	if strategy != Refuse && strategy != Theirs && strategy != Ours {
		return nil, fmt.Errorf("sub: unknown strategy %q (theirs, ours or none)", strategy)
	}
	prev, err := s.PreviewMerge(ctx, root, id)
	if err != nil {
		return nil, err
	}
	var bad []string
	var ops []Op
	for _, p := range prev {
		switch p.State {
		case StateFits:
			ops = append(ops, p.Op)
		case StateMerge:
			mid, err := s.PutFile(p.merged)
			if err != nil {
				return nil, err
			}
			ops = append(ops, Op{Path: p.Path, After: mid, Exec: p.Exec})
		case StateConflict:
			switch strategy {
			case Theirs:
				ops = append(ops, p.Op)
			case Refuse:
				bad = append(bad, p.Path)
			}
		}
	}
	if len(bad) > 0 {
		return prev, &ConflictError{Paths: bad}
	}
	return prev, s.write(ctx, root, ops)
}

func isBinary(b []byte) bool { return bytes.IndexByte(b, 0) >= 0 }
