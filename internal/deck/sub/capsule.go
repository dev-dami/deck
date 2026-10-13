package sub

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// MaxFiles is the most files one capsule changes. A larger change is a
// chain of capsules, each naming the one before it as Parent.
const MaxFiles = 200

// Op is one file's change. An empty Before means the file did not exist; an
// empty After means it is deleted. A rename is a delete and an add, which
// costs nothing extra since the added file's chunks are already stored.
type Op struct {
	Path   string `json:"path"`
	Before ID     `json:"before,omitempty"`
	After  ID     `json:"after,omitempty"`
	Exec   bool   `json:"exec,omitempty"`
}

// Kind is "add", "modify" or "delete".
func (o Op) Kind() string {
	switch {
	case o.Before == "":
		return "add"
	case o.After == "":
		return "delete"
	}
	return "modify"
}

// Meta says who made a capsule and for what.
type Meta struct {
	Source string `json:"source,omitempty"` // agent id
	Task   string `json:"task,omitempty"`
	Note   string `json:"note,omitempty"`
}

// Capsule is an immutable change set: from Base (a snapshot) apply Ops.
type Capsule struct {
	Version int  `json:"version"`
	Base    ID   `json:"base"`
	Parent  ID   `json:"parent,omitempty"`
	Meta    Meta `json:"meta"`
	Ops     []Op `json:"ops"`
}

// Capsule reads a stored capsule.
func (s *Store) Capsule(id ID) (*Capsule, error) {
	b, err := s.Get(id)
	if err != nil {
		return nil, err
	}
	var c Capsule
	if err := json.Unmarshal(b, &c); err != nil || c.Version == 0 {
		return nil, fmt.Errorf("sub: %s is not a capsule", id.Short())
	}
	return &c, nil
}

// Chain is the capsule id and its ancestors, oldest first.
func (s *Store) Chain(id ID) ([]*Capsule, error) {
	var out []*Capsule
	for seen := 0; id != ""; seen++ {
		if seen > 1000 {
			return nil, errors.New("sub: capsule chain too long")
		}
		c, err := s.Capsule(id)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
		id = c.Parent
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, nil
}

// Capture records how the tree at root differs from snapshot base and
// returns the capsule holding it (the last of a chain when it changes more
// than MaxFiles files); empty when nothing changed.
func (s *Store) Capture(ctx context.Context, root string, base ID, meta Meta) (ID, error) {
	from, err := s.Snapshot(base)
	if err != nil {
		return "", err
	}
	now, err := s.scan(ctx, root, true)
	if err != nil {
		return "", err
	}
	ops := diff(from, now)
	var head ID
	for len(ops) > 0 {
		n := min(len(ops), MaxFiles)
		b, err := json.Marshal(Capsule{Version: 1, Base: base, Parent: head, Meta: meta, Ops: ops[:n]})
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

// diff is the ops that turn a into b, by path.
func diff(a, b *Snapshot) []Op {
	var ops []Op
	for p, eb := range b.Files {
		if ea, ok := a.Files[p]; !ok || ea != eb {
			ops = append(ops, Op{Path: p, Before: a.Files[p].File, After: eb.File, Exec: eb.Exec})
		}
	}
	for p, ea := range a.Files {
		if _, ok := b.Files[p]; !ok {
			ops = append(ops, Op{Path: p, Before: ea.File})
		}
	}
	sort.Slice(ops, func(i, j int) bool { return ops[i].Path < ops[j].Path })
	return ops
}

// ConflictError is a capsule that does not fit the tree: each path's file is
// not what the capsule was captured against.
type ConflictError struct {
	Paths []string
}

func (e *ConflictError) Error() string {
	n := len(e.Paths)
	show := e.Paths[:min(n, 5)]
	s := "sub: tree does not match the capsule's base at " + strings.Join(show, ", ")
	if n > len(show) {
		s += fmt.Sprintf(" and %d more", n-len(show))
	}
	return s
}

// ops is a capsule chain's ops in order.
func (s *Store) ops(id ID) ([]Op, error) {
	chain, err := s.Chain(id)
	if err != nil {
		return nil, err
	}
	var ops []Op
	for _, c := range chain {
		ops = append(ops, c.Ops...)
	}
	return ops, nil
}

// Verify checks the capsule (with its chain) fits the tree at root: every
// file it touches is still what it was captured against. Only the touched
// files are looked at.
func (s *Store) Verify(ctx context.Context, root string, id ID) error {
	ops, err := s.ops(id)
	if err != nil {
		return err
	}
	return s.check(root, ops)
}

func (s *Store) check(root string, ops []Op) error {
	sc := s.stats(root)
	defer sc.save()
	var bad []string
	for _, o := range ops {
		if err := validPath(o.Path); err != nil {
			return err
		}
		e, ok, err := s.entry(sc, root, o.Path, false)
		if err != nil {
			return err
		}
		var cur ID
		if ok {
			cur = e.File
		}
		if cur != o.Before && cur != o.After { // already applied counts as fitting
			bad = append(bad, o.Path)
		}
	}
	if len(bad) > 0 {
		return &ConflictError{Paths: bad}
	}
	return nil
}

// Apply verifies the capsule (with its chain) against the tree at root and
// writes it: all of it or, on any conflict or failure, none of it.
func (s *Store) Apply(ctx context.Context, root string, id ID) error {
	ops, err := s.ops(id)
	if err != nil {
		return err
	}
	if err := s.check(root, ops); err != nil {
		return err
	}
	return s.write(ctx, root, ops)
}

// Revert undoes an applied capsule: the same ops with before and after
// swapped, under the same all-or-nothing check.
func (s *Store) Revert(ctx context.Context, root string, id ID) error {
	ops, err := s.ops(id)
	if err != nil {
		return err
	}
	rev := make([]Op, len(ops))
	for i, o := range ops {
		rev[len(ops)-1-i] = Op{Path: o.Path, Before: o.After, After: o.Before, Exec: o.Exec}
	}
	if err := s.check(root, rev); err != nil {
		return err
	}
	return s.write(ctx, root, rev)
}

// Restore puts each path back the way snapshot snap has it: its content
// when snap has it, gone when it does not. Paths are relative to root with
// forward slashes. It does not check what is there now.
func (s *Store) Restore(ctx context.Context, root string, snap ID, paths []string) error {
	sn, err := s.Snapshot(snap)
	if err != nil {
		return err
	}
	ops := make([]Op, 0, len(paths))
	for _, p := range paths {
		if err := validPath(p); err != nil {
			return err
		}
		e := sn.Files[p]
		ops = append(ops, Op{Path: p, After: e.File, Exec: e.Exec})
	}
	return s.write(ctx, root, ops)
}

// write makes the ops' After states real. New contents are staged in
// temporary files first, then the old files are moved aside and the new
// ones moved in; any failure moves everything back.
func (s *Store) write(ctx context.Context, root string, ops []Op) error {
	type step struct {
		full, tmp, old string
	}
	var steps []step
	cleanup := func() {
		for _, st := range steps {
			if st.tmp != "" {
				os.Remove(st.tmp)
			}
		}
	}
	for _, o := range ops {
		if err := ctx.Err(); err != nil {
			cleanup()
			return err
		}
		st := step{full: filepath.Join(root, filepath.FromSlash(o.Path))}
		if o.After != "" {
			b, err := s.ReadFile(o.After)
			if err == nil {
				err = os.MkdirAll(filepath.Dir(st.full), 0o755)
			}
			if err == nil {
				mode := os.FileMode(0o644)
				if o.Exec {
					mode = 0o755
				}
				st.tmp = st.full + ".sub-new"
				err = os.WriteFile(st.tmp, b, mode)
			}
			if err != nil {
				cleanup()
				return err
			}
		}
		steps = append(steps, st)
	}
	var done []step
	rollback := func() {
		for i := len(done) - 1; i >= 0; i-- {
			st := done[i]
			if st.tmp != "" {
				os.Remove(st.full)
			}
			if st.old != "" {
				os.Rename(st.old, st.full)
			}
		}
		cleanup()
	}
	for _, st := range steps {
		if _, err := os.Lstat(st.full); err == nil {
			st.old = st.full + ".sub-old"
			if err := os.Rename(st.full, st.old); err != nil {
				rollback()
				return err
			}
		}
		if st.tmp != "" {
			if err := os.Rename(st.tmp, st.full); err != nil {
				done = append(done, st)
				rollback()
				return err
			}
		}
		done = append(done, st)
	}
	sc := s.stats(root)
	for i, st := range done {
		if st.old != "" {
			os.Remove(st.old)
		}
		sc.forget(ops[i].Path)
	}
	sc.save()
	return nil
}

func validPath(p string) error {
	if p == "" || strings.HasPrefix(p, "/") || strings.Contains(p, "\\") ||
		strings.Contains("/"+p+"/", "/../") || strings.Contains("/"+p+"/", "/.git/") || filepath.IsAbs(p) {
		return fmt.Errorf("sub: bad path %q", p)
	}
	return nil
}

// Diff is the ops that turn snapshot a into snapshot b.
func (s *Store) Diff(a, b ID) ([]Op, error) {
	sa, err := s.Snapshot(a)
	if err != nil {
		return nil, err
	}
	sb, err := s.Snapshot(b)
	if err != nil {
		return nil, err
	}
	return diff(sa, sb), nil
}
