package sub

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Entry is one file in a snapshot.
type Entry struct {
	File ID   `json:"file"`
	Exec bool `json:"exec,omitempty"`
}

// Snapshot is a work tree's files at one moment, by path relative to the
// root with forward slashes.
type Snapshot struct {
	Version int              `json:"version"`
	Files   map[string]Entry `json:"files"`
}

// Snap records the work tree at root and returns the snapshot's ID. Files
// whose size and modification time match the stat cache are not read again,
// so a snapshot of a mostly unchanged tree costs a directory walk.
func (s *Store) Snap(ctx context.Context, root string) (ID, error) {
	snap, err := s.scan(ctx, root, true)
	if err != nil {
		return "", err
	}
	b, err := json.Marshal(snap) // map keys marshal sorted: stable bytes
	if err != nil {
		return "", err
	}
	return s.Put(b)
}

// Snapshot reads a stored snapshot.
func (s *Store) Snapshot(id ID) (*Snapshot, error) {
	b, err := s.Get(id)
	if err != nil {
		return nil, err
	}
	var snap Snapshot
	if err := json.Unmarshal(b, &snap); err != nil || snap.Files == nil {
		return nil, fmt.Errorf("sub: %s is not a snapshot", id.Short())
	}
	return &snap, nil
}

// scan hashes the tree at root; with store set, new contents go into the
// store too (a snapshot or capture needs them, a verify does not).
func (s *Store) scan(ctx context.Context, root string, store bool) (*Snapshot, error) {
	paths, err := listFiles(ctx, root)
	if err != nil {
		return nil, err
	}
	sc := s.stats(root)
	snap := &Snapshot{Version: 1, Files: make(map[string]Entry, len(paths))}
	for _, p := range paths {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		e, ok, err := s.entry(sc, root, p, store)
		if err != nil {
			return nil, err
		}
		if ok {
			snap.Files[p] = e
		}
	}
	sc.save()
	return snap, nil
}

// entry is the file at root/p; ok is false when there is no regular file.
func (s *Store) entry(sc *statCache, root, p string, store bool) (Entry, bool, error) {
	full := filepath.Join(root, filepath.FromSlash(p))
	fi, err := os.Lstat(full)
	if err != nil || !fi.Mode().IsRegular() {
		return Entry{}, false, nil
	}
	e := Entry{Exec: fi.Mode()&0o111 != 0}
	if id, ok := sc.get(p, fi); ok && (!store || s.Has(id)) {
		e.File = id
		return e, true, nil
	}
	b, err := os.ReadFile(full)
	if err != nil {
		return Entry{}, false, err
	}
	if store {
		e.File, err = s.PutFile(b)
		if err != nil {
			return Entry{}, false, err
		}
	} else {
		e.File = FileID(b)
	}
	sc.put(p, fi, e.File)
	return e, true, nil
}

// listFiles is every file under root that is not ignored: git's view when
// root is in a repository (tracked plus untracked, minus .gitignore), a plain
// walk without .git directories otherwise.
func listFiles(ctx context.Context, root string) ([]string, error) {
	if git, err := exec.LookPath("git"); err == nil {
		cmd := exec.CommandContext(ctx, git, "-c", "core.quotePath=false",
			"ls-files", "-z", "--cached", "--others", "--exclude-standard")
		cmd.Dir = root
		hide(cmd)
		if out, err := cmd.Output(); err == nil {
			seen := map[string]bool{}
			var ps []string
			for _, p := range strings.Split(string(out), "\x00") {
				if p != "" && !seen[p] {
					seen[p] = true
					ps = append(ps, p)
				}
			}
			sort.Strings(ps)
			return ps, nil
		}
	}
	var ps []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && d.Name() == ".git" {
			return filepath.SkipDir
		}
		if d.Type().IsRegular() {
			rel, _ := filepath.Rel(root, p)
			ps = append(ps, filepath.ToSlash(rel))
		}
		return nil
	})
	sort.Strings(ps)
	return ps, err
}

// statCache remembers each path's file ID by size and modification time.
type statCache struct {
	file  string
	mu    sync.Mutex
	m     map[string]statEntry
	dirty bool
}

type statEntry struct {
	Size  int64 `json:"s"`
	MTime int64 `json:"t"`
	File  ID    `json:"f"`
}

// racy is how recent a modification time must be for the cache to not trust
// it: a file written again within the clock's granularity keeps its mtime.
const racy = 2 * time.Second

var statCaches sync.Map // root → *statCache

func (s *Store) stats(root string) *statCache {
	abs, _ := filepath.Abs(root)
	if c, ok := statCaches.Load(abs); ok {
		return c.(*statCache)
	}
	c := &statCache{
		file: filepath.Join(s.dir, "stat", string(Sum([]byte(abs))[len(idPrefix):][:24])+".json"),
		m:    map[string]statEntry{},
	}
	if b, err := os.ReadFile(c.file); err == nil {
		_ = json.Unmarshal(b, &c.m)
	}
	actual, _ := statCaches.LoadOrStore(abs, c)
	return actual.(*statCache)
}

func (c *statCache) get(p string, fi fs.FileInfo) (ID, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.m[p]
	if !ok || e.Size != fi.Size() || e.MTime != fi.ModTime().UnixNano() {
		return "", false
	}
	return e.File, true
}

func (c *statCache) put(p string, fi fs.FileInfo, id ID) {
	if time.Since(fi.ModTime()) < racy {
		return
	}
	c.mu.Lock()
	c.m[p] = statEntry{Size: fi.Size(), MTime: fi.ModTime().UnixNano(), File: id}
	c.dirty = true
	c.mu.Unlock()
}

func (c *statCache) forget(p string) {
	c.mu.Lock()
	if _, ok := c.m[p]; ok {
		delete(c.m, p)
		c.dirty = true
	}
	c.mu.Unlock()
}

func (c *statCache) save() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.dirty {
		return
	}
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(c.m); err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(c.file), 0o755); err != nil {
		return
	}
	if err := writeAtomic(c.file, buf.Bytes(), 0o644); err != nil && !errors.Is(err, fs.ErrExist) {
		return
	}
	c.dirty = false
}
