// Package store keeps Deck's threads on disk.
//
// Layout under the threads directory (default ~/.deck/threads):
//
//	<id>.jsonl      one JSON line per agent.Event (streaming deltas are dropped)
//	<id>.meta.json  the Thread metadata; the source of truth for that thread
//	index.json      cache of every thread's metadata, sorted on read; rebuilt
//	                from the *.meta.json files when missing or unreadable
//
// Every write to a file goes through a temp file and a rename, so a crash never
// leaves a half-written index or metadata file.
package store

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/dev-dami/deck/internal/agent"
)

// ErrNotFound is returned for a thread id the store does not know.
var ErrNotFound = errors.New("store: thread not found")

// ErrExists is returned by Create when the id is already in use.
var ErrExists = errors.New("store: thread already exists")

// maxTitleRunes is the longest thread title kept, counting the ellipsis.
const maxTitleRunes = 60

var validID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$`)

// Thread is the metadata of one conversation with one agent.
type Thread struct {
	ID        string    `json:"id"`
	AgentKind string    `json:"agent_kind"` // "claude" or "codex"
	Name      string    `json:"name"`       // display name, e.g. "claude2"
	Title     string    `json:"title"`      // first prompt, truncated
	Dir       string    `json:"dir"`
	Model     string    `json:"model"`
	SessionID string    `json:"session_id"` // the CLI's own session/thread id
	Created   time.Time `json:"created"`
	Updated   time.Time `json:"updated"`
	CostUSD   float64   `json:"cost_usd"`
}

// Store is a directory of threads. It is safe for concurrent use.
type Store struct {
	dir string

	mu      sync.Mutex // guards threads, locks and every write to index.json
	threads map[string]*Thread
	locks   map[string]*sync.Mutex // per-thread lock serialising log appends
}

// DefaultDir returns ~/.deck/threads.
func DefaultDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".deck", "threads"), nil
}

// New opens the store in dir, creating it if needed. An empty dir means
// DefaultDir. The index is rebuilt from the thread metadata files when
// index.json is missing or unreadable.
func New(dir string) (*Store, error) {
	if dir == "" {
		d, err := DefaultDir()
		if err != nil {
			return nil, err
		}
		dir = d
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("store: create %s: %w", dir, err)
	}
	s := &Store{
		dir:     dir,
		threads: make(map[string]*Thread),
		locks:   make(map[string]*sync.Mutex),
	}

	if threads, err := readIndex(s.indexPath()); err == nil {
		for i := range threads {
			t := threads[i]
			s.threads[t.ID] = &t
		}
		return s, nil
	}

	threads, err := scanMeta(dir)
	if err != nil {
		return nil, err
	}
	for i := range threads {
		t := threads[i]
		s.threads[t.ID] = &t
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.writeIndexLocked(); err != nil {
		return nil, err
	}
	return s, nil
}

// Create stores a new thread and returns it with ID, Created, Updated and
// Title filled in. An empty ID is generated. The title is trimmed to one line
// and truncated to 60 runes.
func (s *Store) Create(t Thread) (Thread, error) {
	if t.ID == "" {
		t.ID = newID()
	} else if !validID.MatchString(t.ID) {
		return Thread{}, fmt.Errorf("store: invalid thread id %q", t.ID)
	}
	t.Title = truncateTitle(t.Title)
	now := time.Now()
	if t.Created.IsZero() {
		t.Created = now
	}
	if t.Updated.IsZero() {
		t.Updated = t.Created
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.threads[t.ID]; ok {
		return Thread{}, ErrExists
	}
	f, err := os.OpenFile(s.logPath(t.ID), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		if os.IsExist(err) {
			return Thread{}, ErrExists
		}
		return Thread{}, fmt.Errorf("store: create log: %w", err)
	}
	if err := f.Close(); err != nil {
		return Thread{}, err
	}
	s.threads[t.ID] = &t
	if err := s.saveLocked(t); err != nil {
		return Thread{}, err
	}
	return t, nil
}

// Append writes one event to the thread's log and bumps its Updated time.
// Streaming deltas (Delta == true) are dropped to keep logs small. Events that
// cannot be encoded have their invalid raw JSON removed rather than failing.
func (s *Store) Append(threadID string, ev agent.Event) error {
	if ev.Delta {
		return nil
	}
	ev = sanitize(ev)
	line, err := json.Marshal(ev)
	if err != nil {
		return fmt.Errorf("store: encode event: %w", err)
	}
	line = append(line, '\n')

	s.mu.Lock()
	if _, ok := s.threads[threadID]; !ok {
		s.mu.Unlock()
		return ErrNotFound
	}
	tl := s.threadLockLocked(threadID)
	s.mu.Unlock()

	tl.Lock()
	err = appendFile(s.logPath(threadID), line)
	tl.Unlock()
	if err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.threads[threadID]
	if !ok {
		return ErrNotFound
	}
	t.Updated = time.Now()
	return s.saveLocked(*t)
}

// UpdateMeta replaces the stored metadata of t.ID with t's fields (Name,
// Title, Dir, Model, SessionID, AgentKind, CostUSD). Created is kept and
// Updated is set to now.
func (s *Store) UpdateMeta(t Thread) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cur, ok := s.threads[t.ID]
	if !ok {
		return ErrNotFound
	}
	t.Created = cur.Created
	t.Updated = time.Now()
	t.Title = truncateTitle(t.Title)
	s.threads[t.ID] = &t
	return s.saveLocked(t)
}

// List returns every thread, most recently updated first.
func (s *Store) List() ([]Thread, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sortedLocked(), nil
}

// Load returns a thread's events in the order they were appended, along with
// its metadata. Malformed lines (for example a torn final write) are skipped.
func (s *Store) Load(id string) ([]agent.Event, Thread, error) {
	s.mu.Lock()
	cur, ok := s.threads[id]
	if !ok {
		s.mu.Unlock()
		return nil, Thread{}, ErrNotFound
	}
	meta := *cur
	tl := s.threadLockLocked(id)
	s.mu.Unlock()

	tl.Lock()
	defer tl.Unlock()
	f, err := os.Open(s.logPath(id))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, meta, nil
		}
		return nil, meta, err
	}
	defer f.Close()

	var events []agent.Event
	r := bufio.NewReader(f)
	for {
		line, err := r.ReadBytes('\n')
		if trimmed := bytes.TrimSpace(line); len(trimmed) > 0 {
			var ev agent.Event
			if json.Unmarshal(trimmed, &ev) == nil {
				events = append(events, ev)
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, meta, err
		}
	}
	return events, meta, nil
}

func (s *Store) threadLockLocked(id string) *sync.Mutex {
	l, ok := s.locks[id]
	if !ok {
		l = &sync.Mutex{}
		s.locks[id] = l
	}
	return l
}

func (s *Store) sortedLocked() []Thread {
	out := make([]Thread, 0, len(s.threads))
	for _, t := range s.threads {
		out = append(out, *t)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Updated.Equal(out[j].Updated) {
			return out[i].ID < out[j].ID
		}
		return out[i].Updated.After(out[j].Updated)
	})
	return out
}

// saveLocked writes one thread's metadata file and then the index. Caller holds s.mu.
func (s *Store) saveLocked(t Thread) error {
	data, err := json.MarshalIndent(t, "", "  ")
	if err != nil {
		return err
	}
	if err := writeFileAtomic(s.metaPath(t.ID), data); err != nil {
		return err
	}
	return s.writeIndexLocked()
}

func (s *Store) writeIndexLocked() error {
	data, err := json.MarshalIndent(s.sortedLocked(), "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(s.indexPath(), data)
}

func (s *Store) indexPath() string         { return filepath.Join(s.dir, "index.json") }
func (s *Store) logPath(id string) string  { return filepath.Join(s.dir, id+".jsonl") }
func (s *Store) metaPath(id string) string { return filepath.Join(s.dir, id+".meta.json") }

func readIndex(path string) ([]Thread, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var threads []Thread
	if err := json.Unmarshal(data, &threads); err != nil {
		return nil, err
	}
	return threads, nil
}

// scanMeta rebuilds the thread list from the *.meta.json files in dir.
// Unreadable or id-less files are skipped.
func scanMeta(dir string) ([]Thread, error) {
	matches, err := filepath.Glob(filepath.Join(dir, "*.meta.json"))
	if err != nil {
		return nil, err
	}
	var threads []Thread
	for _, path := range matches {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var t Thread
		if err := json.Unmarshal(data, &t); err != nil || t.ID == "" {
			continue
		}
		threads = append(threads, t)
	}
	return threads, nil
}

func appendFile(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("store: open log: %w", err)
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return fmt.Errorf("store: write log: %w", err)
	}
	return f.Close()
}

// writeFileAtomic writes data to a temp file beside path and renames it over
// path. Readers see either the old file or the new one, never a partial file.
// It does not fsync: a power cut can lose the latest index write, which List
// tolerates by rebuilding from the metadata files, and fsync on every event
// append is too slow on Windows.
func writeFileAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpName) }

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		cleanup()
		return err
	}
	return nil
}

// sanitize drops raw JSON the encoder would reject, without touching the
// caller's copy of the event.
func sanitize(ev agent.Event) agent.Event {
	if len(ev.Raw) > 0 && !json.Valid(ev.Raw) {
		ev.Raw = nil
	}
	if ev.Tool != nil && len(ev.Tool.Input) > 0 && !json.Valid(ev.Tool.Input) {
		tool := *ev.Tool
		tool.Input = nil
		ev.Tool = &tool
	}
	return ev
}

func newID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand does not fail in practice; fall back to the clock.
		return fmt.Sprintf("t%x", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}

// truncateTitle collapses a title onto one line and cuts it to maxTitleRunes,
// ending with an ellipsis when it is shortened.
func truncateTitle(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	r := []rune(s)
	if len(r) <= maxTitleRunes {
		return s
	}
	return string(r[:maxTitleRunes-1]) + "…"
}
