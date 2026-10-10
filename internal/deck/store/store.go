// Package store keeps Deck's threads on disk.
//
// Layout under the threads directory (default ~/.deck/threads):
//
//	<id>.jsonl      one JSON line per Entry (an agent.Event plus the speaker, the
//	                addressees and how it was relayed); streaming deltas are
//	                dropped. Old logs of bare agent.Event lines load unchanged.
//	                Besides messages and events the log holds the user's edits of
//	                earlier messages (EntryEdit) and cancelled queued messages
//	                (EntryCancel); user entries may carry image paths and a steer flag.
//	                With change tracking on it also holds git snapshots of the
//	                work tree (EntrySnap) and reverts of files (EntryRevert,
//	                EntryUnrevert).
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
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/dev-dami/deck/internal/deck/agent"
)

// ErrNotFound is returned for a thread id the store does not know.
var ErrNotFound = errors.New("store: thread not found")

// ErrExists is returned by Create when the id is already in use.
var ErrExists = errors.New("store: thread already exists")

// maxTitleRunes is the longest thread title kept, counting the ellipsis.
const maxTitleRunes = 60

var validID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$`)

// CurrentVersion is the metadata format written by this package. Version 0
// (absent) marks a thread written before shared threads existed: one agent,
// described by the legacy AgentKind/Name/Model/SessionID fields.
const CurrentVersion = 2

// Member is one agent taking part in a thread.
type Member struct {
	ID        string  `json:"id"`   // Deck's id, unique across the deck: "claude", "claude2", "codex"
	Kind      string  `json:"kind"` // "claude" or "codex"
	Name      string  `json:"name"` // display name; used for addressing
	Model     string  `json:"model,omitempty"`
	Effort    string  `json:"effort,omitempty"`
	SessionID string  `json:"session_id,omitempty"` // the CLI's own session/thread id
	CostUSD   float64 `json:"cost_usd,omitempty"`
	// Cursor is how many thread-log entries this member has been briefed on.
	Cursor int `json:"cursor,omitempty"`
	// ContextTokens and ContextWindow are the member's last reported context
	// window use (0 = unknown), so a resumed thread can show the meter at once.
	ContextTokens int `json:"context_tokens,omitempty"`
	ContextWindow int `json:"context_window,omitempty"`
}

// Thread is the metadata of one conversation between the user and one or more
// agents (the Members).
//
// Members is authoritative whenever it is non-empty. The flat AgentKind, Name,
// Model and SessionID fields are the pre-shared-thread format: they are read to
// migrate old threads (see Normalize) and mirrored from Members on every write
// so older Deck builds can still list the thread.
type Thread struct {
	ID      string   `json:"id"`
	Version int      `json:"version,omitempty"`
	Title   string   `json:"title"` // first prompt, truncated
	Dir     string   `json:"dir"`
	Members []Member `json:"members,omitempty"`
	// Mode is the thread's permission mode: "plan", "ask" or "yolo". Threads
	// stored before modes existed (and any unknown value) read as "ask".
	Mode string `json:"mode,omitempty"`
	// Pinned threads sort first; Resolved ones are settled (shown apart until
	// a new message wakes them); Archived ones are off the sidebar.
	Pinned   bool `json:"pinned,omitempty"`
	Resolved bool `json:"resolved,omitempty"`
	Archived bool `json:"archived,omitempty"`
	// Worktree is set while the thread works apart from the shared tree.
	Worktree *Worktree `json:"worktree,omitempty"`

	// Legacy mirror of Members[0] (Name: every member's name joined by "+").
	AgentKind string `json:"agent_kind"`
	Name      string `json:"name"`
	Model     string `json:"model"`
	SessionID string `json:"session_id"`

	Created time.Time `json:"created"`
	Updated time.Time `json:"updated"`
	CostUSD float64   `json:"cost_usd"` // sum over members
}

// Normalize migrates a pre-shared-thread record in place: when there are no
// Members but a legacy AgentKind, the legacy fields become one Member. It then
// refreshes the legacy mirror and CostUSD from Members. A missing or unknown
// Mode becomes "ask". Version is left as is,
// so a caller can tell a migrated thread (Version < CurrentVersion) from a new
// one.
func (t *Thread) Normalize() {
	switch t.Mode {
	case "plan", "ask", "yolo":
	default:
		t.Mode = "ask"
	}
	if len(t.Members) == 0 && t.AgentKind != "" {
		id := t.Name
		if id == "" || strings.ContainsAny(id, "+ ") {
			id = t.AgentKind
		}
		t.Members = []Member{{
			ID: id, Kind: t.AgentKind, Name: id, Model: t.Model,
			SessionID: t.SessionID, CostUSD: t.CostUSD,
		}}
	}
	if len(t.Members) == 0 {
		return
	}
	t.Members = append([]Member(nil), t.Members...)
	names := make([]string, 0, len(t.Members))
	cost := 0.0
	for _, m := range t.Members {
		n := m.Name
		if n == "" {
			n = m.ID
		}
		names = append(names, n)
		cost += m.CostUSD
	}
	first := t.Members[0]
	t.AgentKind, t.Model, t.SessionID = first.Kind, first.Model, first.SessionID
	t.Name = strings.Join(names, "+")
	t.CostUSD = cost
}

// Entry types. The zero value is a plain agent event.
const (
	EntryEvent    = ""         // an agent.Event; Event.AgentID is the speaker
	EntryUser     = "user"     // the user spoke; Speaker is their name, To the members addressed
	EntryRelay    = "relay"    // an agent message to other members; Speaker is the sender, Via "tool" or "mention"
	EntryMember   = "member"   // Speaker joined the thread; Event.Text holds its kind
	EntryPause    = "pause"    // agent-to-agent relays paused; Event.Text holds the hop count
	EntryContinue = "continue" // the user released a pause
	EntryNotice   = "notice"   // Deck's own note (e.g. a dropped duplicate relay)
	// EntryEdit: the user edited an earlier message. Ref is that message's entry
	// id, Old the text it had before this edit, Event.Text the new text, To the
	// members the edit was delivered to (empty when the original had not been
	// delivered yet, so the edit just replaced its text).
	EntryEdit = "edit"
	// EntryCancel: the user cancelled a queued message before it was delivered.
	// Ref is the message's entry id, To the member it was queued for.
	EntryCancel = "cancel"
	// EntrySnap: Snap is the work tree just before the user message Ref
	// reached anyone; the changes panel measures from it. Never shown.
	EntrySnap = "snap"
	// EntryRevert: the user put Files back the way they were just before the
	// message Ref (see EntrySnap). Snap is the work tree right before the
	// revert, which EntryUnrevert restores from.
	EntryRevert = "revert"
	// EntryUnrevert: the user undid the revert Ref, taking Files back to that
	// revert's Snap. Snap is the work tree right before the undo.
	EntryUnrevert = "unrevert"
)

// Entry is one line of a thread log. The embedded Event keeps old logs (bare
// agent.Event lines) readable: they load as Entries with every other field
// empty. For a user, relay, member, pause, continue or notice entry the
// message text lives in Event.Text.
type Entry struct {
	agent.Event
	Type    string   `json:"type,omitempty"`
	Speaker string   `json:"speaker,omitempty"`
	To      []string `json:"to,omitempty"` // member ids
	Via     string   `json:"via,omitempty"`
	// Held marks a relay that was held by a pause (informational).
	Held bool `json:"held,omitempty"`

	// ID identifies the entry within its thread. Deck assigns one to every entry
	// it writes; LoadEntries gives entries from older logs a stable one ("e<n>",
	// n = position in the log).
	ID string `json:"id,omitempty"`
	// Images (user entries) are the paths of the images attached to the message.
	Images []string `json:"images,omitempty"`
	// Steer marks a user entry that redirected a busy agent mid-task (the
	// message was queued and the user then steered with it).
	Steer bool `json:"steer,omitempty"`
	// Ref is the entry this one refers to (EntryEdit, EntryCancel; for a Steer
	// entry the queued message it delivered).
	Ref string `json:"ref,omitempty"`
	// Old is the text before an edit (EntryEdit); on a Steer entry it is set when
	// the steer delivered an edit, and holds the text before that edit.
	Old string `json:"old,omitempty"`
	// Snap is a git tree of the thread's work tree (EntrySnap, EntryRevert,
	// EntryUnrevert).
	Snap string `json:"snap,omitempty"`
	// Files are the work-tree paths (relative to the repository root, forward
	// slashes) an EntryRevert or EntryUnrevert put back.
	Files []string `json:"files,omitempty"`
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
	t.Normalize()
	t.Version = CurrentVersion
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

// CreateWith stores a new thread whose log starts with entries (a fork).
// Streaming deltas are dropped as AppendEntry would.
func (s *Store) CreateWith(t Thread, entries []Entry) (Thread, error) {
	t, err := s.Create(t)
	if err != nil {
		return Thread{}, err
	}
	var buf []byte
	for _, e := range entries {
		if e.Delta {
			continue
		}
		e.Event = sanitize(e.Event)
		line, err := json.Marshal(e)
		if err != nil {
			return Thread{}, fmt.Errorf("store: encode entry: %w", err)
		}
		buf = append(append(buf, line...), '\n')
	}
	s.mu.Lock()
	tl := s.threadLockLocked(t.ID)
	s.mu.Unlock()
	tl.Lock()
	defer tl.Unlock()
	if err := appendFile(s.logPath(t.ID), buf); err != nil {
		return Thread{}, err
	}
	return t, nil
}

// Append writes one plain agent event to the thread's log and bumps its Updated
// time. It is AppendEntry for an event with no speaker information.
func (s *Store) Append(threadID string, ev agent.Event) error {
	return s.AppendEntry(threadID, Entry{Event: ev})
}

// AppendEntry writes one entry to the thread's log and bumps its Updated time.
// Streaming deltas (Event.Delta == true) are dropped to keep logs small. Events
// that cannot be encoded have their invalid raw JSON removed rather than failing.
func (s *Store) AppendEntry(threadID string, e Entry) error {
	if e.Delta {
		return nil
	}
	e.Event = sanitize(e.Event)
	line, err := json.Marshal(e)
	if err != nil {
		return fmt.Errorf("store: encode entry: %w", err)
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

// UpdateMeta replaces the stored metadata of t.ID with t (Title, Dir, Members;
// a Thread with no Members is migrated from the legacy single-agent fields
// first). Created is kept and Updated is set to now.
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
	t.Normalize()
	t.Version = CurrentVersion
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
// its metadata (migrated to the shared-thread shape, see Thread.Normalize).
// Malformed lines (for example a torn final write) are skipped. Entries that
// are not agent events (user messages, relays, ...) are included with their text
// in Event.Text; use LoadEntries to tell them apart.
func (s *Store) Load(id string) ([]agent.Event, Thread, error) {
	entries, meta, err := s.LoadEntries(id)
	if err != nil {
		return nil, meta, err
	}
	var events []agent.Event
	for _, e := range entries {
		events = append(events, e.Event)
	}
	return events, meta, nil
}

// LoadEntries returns a thread's log entries in append order along with its
// metadata. Old logs (bare agent.Event lines) come back as EntryEvent entries.
// Every returned entry has an ID (see Entry.ID).
func (s *Store) LoadEntries(id string) ([]Entry, Thread, error) {
	s.mu.Lock()
	cur, ok := s.threads[id]
	if !ok {
		s.mu.Unlock()
		return nil, Thread{}, ErrNotFound
	}
	meta := *cur
	meta.Members = append([]Member(nil), cur.Members...)
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

	var entries []Entry
	r := bufio.NewReader(f)
	for {
		line, err := r.ReadBytes('\n')
		if trimmed := bytes.TrimSpace(line); len(trimmed) > 0 {
			var e Entry
			if json.Unmarshal(trimmed, &e) == nil {
				if e.ID == "" {
					e.ID = "e" + strconv.Itoa(len(entries))
				}
				entries = append(entries, e)
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, meta, err
		}
	}
	return entries, meta, nil
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

// Worktree is an isolated thread's git worktree.
type Worktree struct {
	Path   string `json:"path"`   // where the thread's agents work
	Branch string `json:"branch"` // its branch, deck/<id>
	Main   string `json:"main"`   // the shared work tree it came from
	Base   string `json:"base"`   // the commit it started from
}

// Dir is the folder the store keeps threads in.
func (s *Store) Dir() string { return s.dir }

// Delete removes a thread for good: its metadata, its log and its index entry.
func (s *Store) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.threads[id]; !ok {
		return ErrNotFound
	}
	tl := s.threadLockLocked(id)
	tl.Lock()
	for _, p := range []string{s.logPath(id), s.metaPath(id)} {
		if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
			tl.Unlock()
			return err
		}
	}
	tl.Unlock()
	delete(s.threads, id)
	return s.writeIndexLocked()
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
	for i := range threads {
		threads[i].Normalize()
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
		t.Normalize()
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

// NewID returns a fresh random thread id, valid for Create.
func NewID() string { return newID() }

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
