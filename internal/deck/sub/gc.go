package sub

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// GC deletes the objects no root reaches that are older than grace. Roots
// are snapshot and capsule IDs still in use (checkpoints in thread logs,
// capsules on tasks and in messages); published capsules are always roots.
// The grace period keeps objects a capture or snapshot is writing right now,
// before anything refers to them. It returns how many objects went.
func (s *Store) GC(roots []ID, grace time.Duration) (int, error) {
	live := map[ID]bool{}
	file := func(f ID) {
		if f == "" || live[f] {
			return
		}
		live[f] = true
		if chunks, err := s.Chunks(f); err == nil {
			for _, c := range chunks {
				live[c] = true
			}
		}
	}
	snapshot := func(id ID) bool {
		sn, err := s.Snapshot(id)
		if err != nil {
			return false
		}
		live[id] = true
		for _, e := range sn.Files {
			file(e.File)
		}
		return true
	}
	var root func(id ID)
	root = func(id ID) {
		if id == "" || live[id] || !s.Has(id) {
			return
		}
		b, err := s.Get(id)
		if err != nil {
			return
		}
		var probe struct {
			Ops   json.RawMessage `json:"ops"`
			Files json.RawMessage `json:"files"`
		}
		if json.Unmarshal(b, &probe) != nil {
			return
		}
		switch {
		case probe.Ops != nil:
			c, err := s.Capsule(id)
			if err != nil {
				return
			}
			live[id] = true
			for _, o := range c.Ops {
				file(o.Before)
				file(o.After)
			}
			snapshot(c.Base)
			root(c.Parent)
		case probe.Files != nil:
			snapshot(id)
		}
	}
	for _, id := range append(roots, s.Published()...) {
		root(id)
	}
	cutoff := time.Now().Add(-grace)
	removed := 0
	err := filepath.WalkDir(filepath.Join(s.dir, "objects"), func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || strings.HasPrefix(d.Name(), ".sub-") {
			return nil
		}
		id := ID(idPrefix + filepath.Base(filepath.Dir(p)) + d.Name())
		if live[id] {
			return nil
		}
		if fi, err := d.Info(); err != nil || fi.ModTime().After(cutoff) {
			return nil
		}
		_ = os.Chmod(p, 0o644) // objects are read-only; Windows will not remove them otherwise
		if os.Remove(p) == nil {
			removed++
		}
		return nil
	})
	return removed, err
}
