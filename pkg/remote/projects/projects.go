// Package projects is the opt-in list of projects the phone may see
// (docs/mobile-contract.md §2). The path stays on the computer: the phone
// gets an id and a name only (remote-contract R11).
package projects

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/dev-dami/deck/internal/deck/peer"
	"github.com/dev-dami/deck/pkg/remote/wire"
)

const fileName = "projects.json"

// ErrNotFound is returned for an id that is not registered.
var ErrNotFound = errors.New("projects: no such project")

// Project is one registered project.
type Project struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Path    string `json:"path"`
	Enabled bool   `json:"enabled"`
}

// Registry is the list, persisted at dir/projects.json.
type Registry struct {
	path  string
	mu    sync.Mutex
	list  []Project
	mtime time.Time
}

// Open reads the registry, or starts empty if there is none.
func Open(dir string) (*Registry, error) {
	r := &Registry{path: filepath.Join(dir, fileName)}
	raw, err := os.ReadFile(r.path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return r, nil
	case err != nil:
		return nil, fmt.Errorf("projects: %w", err)
	}
	if err := json.Unmarshal(raw, &r.list); err != nil {
		return nil, fmt.Errorf("projects: %s is not valid: %w", fileName, err)
	}
	if st, err := os.Stat(r.path); err == nil {
		r.mtime = st.ModTime()
	}
	return r, nil
}

// Reload re-reads the file if it changed on disk, for a daemon that shares the
// registry with the CLI. It reports whether the list changed.
func (r *Registry) Reload() (bool, error) {
	st, err := os.Stat(r.path)
	if errors.Is(err, fs.ErrNotExist) {
		r.mu.Lock()
		defer r.mu.Unlock()
		changed := len(r.list) > 0
		r.list, r.mtime = nil, time.Time{}
		return changed, nil
	}
	if err != nil {
		return false, fmt.Errorf("projects: %w", err)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if st.ModTime().Equal(r.mtime) {
		return false, nil
	}
	raw, err := os.ReadFile(r.path)
	if err != nil {
		return false, fmt.Errorf("projects: %w", err)
	}
	var list []Project
	if err := json.Unmarshal(raw, &list); err != nil {
		return false, fmt.Errorf("projects: %s is not valid: %w", fileName, err)
	}
	r.list, r.mtime = list, st.ModTime()
	return true, nil
}

// IDOf is the id of a project directory: a hash of its cleaned absolute path,
// so adding the same folder twice is the same project.
func IDOf(path string) string {
	sum := sha256.Sum256([]byte(filepath.Clean(path)))
	return "prj_" + hex.EncodeToString(sum[:4])
}

// Add registers a directory, enabled. An empty name is the directory's name.
// Adding a registered path renames it and re-enables it.
func (r *Registry) Add(path, name string) (Project, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return Project{}, fmt.Errorf("projects: %w", err)
	}
	if st, err := os.Stat(abs); err != nil || !st.IsDir() {
		return Project{}, errors.New("projects: that is not a directory")
	}
	if name == "" {
		name = filepath.Base(abs)
	}
	p := Project{ID: IDOf(abs), Name: name, Path: abs, Enabled: true}
	r.mu.Lock()
	defer r.mu.Unlock()
	next := slices.Clone(r.list)
	if i := slices.IndexFunc(next, func(e Project) bool { return e.ID == p.ID }); i >= 0 {
		next[i] = p
	} else {
		next = append(next, p)
	}
	return p, r.save(next)
}

// Remove unregisters a project.
func (r *Registry) Remove(id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	next := slices.DeleteFunc(slices.Clone(r.list), func(p Project) bool { return p.ID == id })
	if len(next) == len(r.list) {
		return ErrNotFound
	}
	return r.save(next)
}

// SetEnabled turns a project on or off without forgetting it.
func (r *Registry) SetEnabled(id string, on bool) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	next := slices.Clone(r.list)
	i := slices.IndexFunc(next, func(p Project) bool { return p.ID == id })
	if i < 0 {
		return ErrNotFound
	}
	next[i].Enabled = on
	return r.save(next)
}

// Get returns an enabled project; a disabled one is ErrNotFound to the phone.
func (r *Registry) Get(id string) (Project, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, p := range r.list {
		if p.ID == id && p.Enabled {
			return p, nil
		}
	}
	return Project{}, ErrNotFound
}

// List returns every registered project, enabled or not, for the CLI.
func (r *Registry) List() []Project {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.list)
}

// Refs are the enabled projects as the phone sees them: id, name and whether
// a provider is online. online reports it by id.
func (r *Registry) Refs(online func(id string) bool) []wire.ProjectRef {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := []wire.ProjectRef{}
	for _, p := range r.list {
		if p.Enabled {
			out = append(out, wire.ProjectRef{ID: p.ID, Name: p.Name, Online: online(p.ID)})
		}
	}
	return out
}

func (r *Registry) save(next []Project) error {
	raw, err := json.Marshal(next)
	if err != nil {
		return err
	}
	if err := peer.WritePrivateFile(r.path, raw); err != nil {
		return fmt.Errorf("projects: write: %w", err)
	}
	if st, err := os.Stat(r.path); err == nil {
		r.mtime = st.ModTime()
	}
	r.list = next
	return nil
}
