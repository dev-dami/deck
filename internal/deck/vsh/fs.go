package vsh

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sync/atomic"

	"mvdan.cc/sh/v3/interp"
)

var (
	errOutside  = errors.New("outside the scratch dir: not permitted by contract")
	errQuota    = errors.New("scratch quota exceeded")
	errDirReads = errors.New("globs read too many directories")
)

// maxDirReads caps the directory reads of glob expansion in one run.
const maxDirReads = 256

// scratch confines file access to one directory through an os.Root, which
// refuses .. and symlinks that lead out of it.
type scratch struct {
	dir   string
	root  *os.Root
	quota int64
	used  atomic.Int64
	full  atomic.Bool // a write failed on the quota
	// dirReads counts glob directory reads.
	dirReads atomic.Int64
}

func openScratch(dir string, quota int64) (*scratch, error) {
	dir = filepath.Clean(dir)
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, fmt.Errorf("vsh: %w", err)
	}
	s := &scratch{dir: dir, root: root, quota: quota}
	// Files already in the dir count against the quota.
	err = fs.WalkDir(root.FS(), ".", func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type().IsRegular() {
			fi, err := d.Info()
			if err != nil {
				return err
			}
			s.used.Add(fi.Size())
		}
		return nil
	})
	if err != nil {
		root.Close()
		return nil, fmt.Errorf("vsh: scratch dir: %w", err)
	}
	return s, nil
}

// rel maps a script path to a path relative to the scratch dir, or refuses.
// The os.Root does the final check, also for symlinks.
func (s *scratch) rel(name string) (string, error) {
	if filepath.IsAbs(name) {
		r, err := filepath.Rel(s.dir, name)
		if err != nil {
			return "", errOutside
		}
		name = r
	}
	name = filepath.Clean(name)
	if name != "." && !filepath.IsLocal(name) {
		return "", errOutside
	}
	return name, nil
}

func (s *scratch) open(_ context.Context, name string, flag int, _ os.FileMode) (io.ReadWriteCloser, error) {
	if name == "/dev/null" {
		return devNull{}, nil
	}
	rel, err := s.rel(name)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: name, Err: err}
	}
	if flag&(os.O_WRONLY|os.O_RDWR) == 0 {
		f, err := s.root.Open(rel)
		if err != nil {
			return nil, err
		}
		return f, nil
	}
	var old int64
	if flag&os.O_TRUNC != 0 {
		if fi, err := s.root.Stat(rel); err == nil && fi.Mode().IsRegular() {
			old = fi.Size()
		}
	}
	f, err := s.root.OpenFile(rel, flag, 0o600)
	if err != nil {
		return nil, err
	}
	s.used.Add(-old)
	// Not an *os.File, so the interpreter copies through Write and a shim
	// never gets the file descriptor itself.
	return &quotaFile{f: f, s: s}, nil
}

// openRead opens a file operand of a builtin. "-" is stdin.
func (s *scratch) openRead(name string, stdin io.Reader) (io.ReadCloser, error) {
	if name == "-" {
		return io.NopCloser(stdin), nil
	}
	if name == "/dev/null" {
		return devNull{}, nil
	}
	rel, err := s.rel(name)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: name, Err: err}
	}
	f, err := s.root.Open(rel)
	if err != nil {
		return nil, err
	}
	return f, nil
}

func (s *scratch) readDir(_ context.Context, name string) ([]fs.DirEntry, error) {
	// Globs like */../*/../* grow as a power of the number of subdirs.
	if s.dirReads.Add(1) > maxDirReads {
		return nil, &os.PathError{Op: "readdir", Path: name, Err: errDirReads}
	}
	rel, err := s.rel(name)
	if err != nil {
		return nil, &os.PathError{Op: "readdir", Path: name, Err: err}
	}
	return fs.ReadDir(s.root.FS(), filepath.ToSlash(rel))
}

func (s *scratch) stat(_ context.Context, name string, follow bool) (fs.FileInfo, error) {
	rel, err := s.rel(name)
	if err != nil {
		return nil, &os.PathError{Op: "stat", Path: name, Err: err}
	}
	if follow {
		return s.root.Stat(rel)
	}
	return s.root.Lstat(rel)
}

func (s *scratch) access(ctx context.Context, name string, mode interp.AccessMode) error {
	fi, err := s.stat(ctx, name, true)
	if err != nil {
		return err
	}
	perm := fi.Mode().Perm()
	if mode&interp.AccessWrite != 0 && perm&0o200 == 0 || mode&interp.AccessExec != 0 && perm&0o100 == 0 {
		return fs.ErrPermission
	}
	return nil
}

// quotaFile counts writes against the scratch quota. It has no ReadFrom, so
// io.Copy cannot go around Write.
type quotaFile struct {
	f *os.File
	s *scratch
}

func (q *quotaFile) Read(p []byte) (int, error) { return q.f.Read(p) }
func (q *quotaFile) Close() error               { return q.f.Close() }

func (q *quotaFile) Write(p []byte) (int, error) {
	// After one refused write, all writes fail, so that no file ends with
	// a part of a write, such as the newline of an echo.
	if q.s.full.Load() {
		return 0, errQuota
	}
	if q.s.used.Add(int64(len(p))) > q.s.quota {
		q.s.used.Add(-int64(len(p)))
		q.s.full.Store(true)
		return 0, errQuota
	}
	n, err := q.f.Write(p)
	q.s.used.Add(int64(n - len(p)))
	return n, err
}

type devNull struct{}

func (devNull) Read([]byte) (int, error)    { return 0, io.EOF }
func (devNull) Write(p []byte) (int, error) { return len(p), nil }
func (devNull) Close() error                { return nil }
