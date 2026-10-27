package sub

import (
	"context"
	"encoding/json"
	"slices"
	"sort"
	"strings"

	"github.com/aymanbagabas/go-udiff"
)

// Capsules for big work: a capsule built from ops whose files are already
// stored (a range of commits, a whole branch), one restacked onto a new
// base, and a capsule's files read a page at a time.

// Build stores ops as a capsule (the last of a chain when there are more
// than MaxFiles) chained onto parent, and returns its id; "" for no ops.
// Its Base is a snapshot of just the files the ops touch, as they were
// before: enough to keep them, and cheap however big the tree.
func (s *Store) Build(ops []Op, parent ID, meta Meta) (ID, error) {
	if len(ops) == 0 {
		return "", nil
	}
	ops = slices.Clone(ops)
	sort.Slice(ops, func(i, j int) bool { return ops[i].Path < ops[j].Path })
	base := &Snapshot{Version: 1, Files: map[string]Entry{}}
	for _, o := range ops {
		if err := validPath(o.Path); err != nil {
			return "", err
		}
		if o.Before != "" {
			base.Files[o.Path] = Entry{File: o.Before, Exec: o.BeforeExec}
		}
	}
	b, err := json.Marshal(base)
	if err != nil {
		return "", err
	}
	bid, err := s.Put(b)
	if err != nil {
		return "", err
	}
	head := parent
	for len(ops) > 0 {
		n := min(len(ops), MaxFiles)
		b, err := json.Marshal(Capsule{Version: 1, Base: bid, Parent: head, Meta: meta, Ops: ops[:n]})
		if err != nil {
			return "", err
		}
		if head, err = s.Put(b); err != nil {
			return "", err
		}
		ops = ops[n:]
	}
	return head, nil
}

// Onto says what a file is on the base a capsule moves to: its file ID, ""
// when the base has no such file.
type Onto func(path string) (ID, error)

// TreeOnto is the work tree at root as a base.
func (s *Store) TreeOnto(root string) Onto {
	sc := s.stats(root)
	return func(p string) (ID, error) {
		if err := validPath(p); err != nil {
			return "", err
		}
		e, ok, err := s.entry(sc, root, p, true)
		if err != nil || !ok {
			return "", err
		}
		return e.File, nil
	}
}

// Rebase moves capsule id (with its chain) onto a new base: each file's
// change is redone against what onto says the file is there, with a
// three-way merge where the base changed it too. It stores the result as a
// new capsule chained onto parent ("" for none; the capsule it is stacked
// on otherwise) and returns it. When files conflict nothing is stored and
// conflicts names them. Files the base already has as the capsule leaves
// them drop out.
func (s *Store) Rebase(ctx context.Context, id ID, onto Onto, parent ID, meta Meta) (ID, []Preview, error) {
	chs, err := s.changes(id)
	if err != nil {
		return "", nil, err
	}
	var ops []Op
	var prev []Preview
	bad := false
	for _, ch := range chs {
		if err := ctx.Err(); err != nil {
			return "", nil, err
		}
		cur, err := onto(ch.Path)
		if err != nil {
			return "", nil, err
		}
		p := Preview{Op: ch.Op}
		switch {
		case cur == ch.After:
			p.State = StateApplied
		case cur == ch.Before:
			p.State = StateFits
			ops = append(ops, ch.Op)
		default:
			st, reason, merged := s.merge3Files(ch.Op, cur)
			p.State, p.Reason = st, reason
			if st == StateConflict {
				bad = true
				break
			}
			mid, err := s.PutFile(merged)
			if err != nil {
				return "", nil, err
			}
			ops = append(ops, Op{Path: ch.Path, Before: cur, After: mid, Exec: ch.Exec, BeforeExec: ch.BeforeExec})
		}
		prev = append(prev, p)
	}
	if bad {
		return "", prev, &ConflictError{Paths: conflicted(prev)}
	}
	nid, err := s.Build(ops, parent, meta)
	return nid, prev, err
}

// Stack restacks capsule id on capsule under: each file under changes is
// taken as under leaves it, every other as id expects, and the result chains
// onto under, so applying it applies both.
func (s *Store) Stack(ctx context.Context, id, under ID, meta Meta) (ID, []Preview, error) {
	below, err := s.changes(under)
	if err != nil {
		return "", nil, err
	}
	chs, err := s.changes(id)
	if err != nil {
		return "", nil, err
	}
	at := make(map[string]ID, len(chs)+len(below))
	for _, ch := range chs {
		at[ch.Path] = ch.Before
	}
	for _, ch := range below {
		at[ch.Path] = ch.After
	}
	return s.Rebase(ctx, id, func(p string) (ID, error) { return at[p], nil }, under, meta)
}

// merge3Files merges op's change into a base whose file is now cur.
func (s *Store) merge3Files(o Op, cur ID) (State, string, []byte) {
	switch {
	case o.Before == "" && cur != "":
		return StateConflict, "added on both sides", nil
	case cur == "":
		return StateConflict, "the base deleted a file the capsule changes", nil
	case o.After == "":
		return StateConflict, "the capsule deletes a file the base changed", nil
	}
	base, err := s.ReadFile(o.Before)
	if err != nil {
		return StateConflict, err.Error(), nil
	}
	ours, err := s.ReadFile(cur)
	if err != nil {
		return StateConflict, err.Error(), nil
	}
	theirs, err := s.ReadFile(o.After)
	if err != nil {
		return StateConflict, err.Error(), nil
	}
	if isBinary(base) || isBinary(theirs) || isBinary(ours) {
		return StateConflict, "binary file changed on both sides", nil
	}
	merged, ok := Merge3(string(base), string(ours), string(theirs))
	if !ok {
		return StateConflict, "both sides changed the same lines", nil
	}
	return StateMerge, "", []byte(merged)
}

func conflicted(prev []Preview) []string {
	var out []string
	for _, p := range prev {
		if p.State == StateConflict {
			out = append(out, p.Path)
		}
	}
	return out
}

// FileStat is one file of a capsule with its line counts; Binary when
// either side is.
type FileStat struct {
	Op
	Added, Removed int
	Binary         bool
}

// Page is files [offset, offset+limit) of a capsule's net change, in path
// order, with line counts, and how many files it changes in all. Only the
// page's files are read, so a capsule of thousands of files stays cheap.
func (s *Store) Page(id ID, offset, limit int) ([]FileStat, int, error) {
	ops, err := s.Ops(id)
	if err != nil {
		return nil, 0, err
	}
	sort.Slice(ops, func(i, j int) bool { return ops[i].Path < ops[j].Path })
	total := len(ops)
	offset = min(max(offset, 0), total)
	end := min(offset+max(limit, 0), total)
	out := make([]FileStat, 0, end-offset)
	for _, o := range ops[offset:end] {
		out = append(out, s.stat(o))
	}
	return out, total, nil
}

func (s *Store) stat(o Op) FileStat {
	fs := FileStat{Op: o}
	a, err1 := s.text(o.Before)
	b, err2 := s.text(o.After)
	if err1 != nil || err2 != nil || isBinary(a) || isBinary(b) {
		fs.Binary = true
		return fs
	}
	for _, e := range udiff.Lines(string(a), string(b)) {
		fs.Removed += strings.Count(string(a[e.Start:e.End]), "\n")
		if e.End > e.Start && !strings.HasSuffix(string(a[e.Start:e.End]), "\n") {
			fs.Removed++
		}
		fs.Added += strings.Count(e.New, "\n")
		if e.New != "" && !strings.HasSuffix(e.New, "\n") {
			fs.Added++
		}
	}
	return fs
}

// text is a file's bytes; nil for none.
func (s *Store) text(id ID) ([]byte, error) {
	if id == "" {
		return nil, nil
	}
	return s.ReadFile(id)
}
