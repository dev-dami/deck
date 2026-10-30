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
//	                from the *.meta.json files when missing or unreadable,
//	                and patched from any that are newer than it
//
// Every write to a file goes through a temp file and a rename, so a crash never
// leaves a half-written index or metadata file.
//
// Appends do not rewrite metadata: the bump to a thread's Updated time stays in
// memory and reaches its .meta.json and the index within flushDelay, at the
// end of a turn, or on Flush. Metadata changes (UpdateMeta) are written to the
// .meta.json at once and to the index on the same schedule. A crash can thus
// lose at most the last second of Updated bumps, nothing else.
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
	"iter"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/dev-dami/deck/internal/deck/agent"
	"github.com/dev-dami/deck/internal/lock"
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
	ID    string `json:"id"`   // Deck's id, unique across the deck: "claude", "claude2", "codex"
	Kind  string `json:"kind"` // "claude" or "codex"
	Name  string `json:"name"` // display name; used for addressing
	Model string `json:"model,omitempty"`
	// Running is the slug Model ran as, as the CLI reported it
	// ("claude-opus-5-5" for "opus"); "" until it has.
	Running   string  `json:"running,omitempty"`
	Effort    string  `json:"effort,omitempty"`
	SessionID string  `json:"session_id,omitempty"` // the CLI's own session/thread id
	CostUSD   float64 `json:"cost_usd,omitempty"`
	// Cursor is how many thread-log entries this member has been briefed on.
	Cursor int `json:"cursor,omitempty"`
	// ContextTokens and ContextWindow are the member's last reported context
	// window use (0 = unknown), so a resumed thread can show the meter at once.
	ContextTokens int `json:"context_tokens,omitempty"`
	ContextWindow int `json:"context_window,omitempty"`
	// ContextSource is where they came from (agent.Usage.ContextSource).
	ContextSource string `json:"context_source,omitempty"`
	// ContextBaseline is how much of the window SessionID filled before its
	// first turn, and DeckTokens how much of it Deck's own text (briefings,
	// steers, instructions) takes, both estimates (session/context.go).
	ContextBaseline int `json:"context_baseline,omitempty"`
	DeckTokens      int `json:"deck_tokens,omitempty"`
	// Briefed: the CLI session SessionID has had Deck's instructions in a
	// prompt, so a resume does not send them again (agent.Spec.Briefed).
	Briefed bool `json:"briefed,omitempty"`
	// Parent, Ephemeral, Dir and Worktree describe a Deck-managed subagent
	// (session/spawn.go): the member that asked for it, whether Deck stops it
	// when its task ends, and the git worktree it works in (Dir is the folder
	// inside it).
	Parent    string    `json:"parent,omitempty"`
	Ephemeral bool      `json:"ephemeral,omitempty"`
	Dir       string    `json:"dir,omitempty"`
	Worktree  *Worktree `json:"worktree,omitempty"`
	// Profile is the named agent (deck.json "profiles") the member was
	// started from, and Role its one-line role and Color its colour then;
	// "" for none.
	Profile string `json:"profile,omitempty"`
	Role    string `json:"role,omitempty"`
	Color   string `json:"color,omitempty"`
	// Wait is set while the member's plan ran out and Deck holds its work
	// until the limit resets (session/waits.go).
	Wait *Wait `json:"wait,omitempty"`
	// Subs are the subagents the member's CLI started with its own tool
	// (Claude Code's Task), newest last (session/subagents.go).
	Subs []SubAgent `json:"subs,omitempty"`
}

// SubAgent is a subagent a member's CLI started with its own tool, not with
// Deck's agent.spawn: Deck shows its work, but cannot message or stop it
// apart from its member.
type SubAgent struct {
	ID          string `json:"id"`   // the id of the tool call that started it
	Name        string `json:"name"` // its lane under the member: "review-pr-296"
	Type        string `json:"type,omitempty"`
	Description string `json:"description,omitempty"`
	// Status is running, done, failed or stopped (agent.SubStatus).
	Status  string    `json:"status"`
	Started time.Time `json:"started,omitzero"`
	Ended   time.Time `json:"ended,omitzero"`
	// Actions counts its tool calls; Error is why it failed.
	Actions int    `json:"actions,omitempty"`
	Error   string `json:"error,omitempty"`
}

// Wait is a member's plan running out: since when, until when (zero while
// not known) and which window, when the CLI said.
type Wait struct {
	Since  time.Time `json:"since"`
	Until  time.Time `json:"until,omitzero"`
	Window string    `json:"window,omitempty"`
}

// Scheduled is a message the user wrote to be sent later (when a plan
// limit resets): At is when.
type Scheduled struct {
	ID      string    `json:"id"`
	Text    string    `json:"text"`
	At      time.Time `json:"at"`
	Created time.Time `json:"created"`
}

// Thread is the metadata of one conversation between the user and one or more
// agents (the Members).
//
// Members is authoritative whenever it is non-empty. The flat AgentKind, Name,
// Model and SessionID fields are the pre-shared-thread format: they are read to
// migrate old threads (see Normalize) and mirrored from Members on every write
// so older Deck builds can still list the thread.
type Thread struct {
	ID      string `json:"id"`
	Version int    `json:"version,omitempty"`
	Title   string `json:"title"` // first prompt, truncated; later a model-written title
	// TitleSrc says who wrote Title: "" (the first-prompt placeholder), "auto"
	// (written by the default model) or "user" (renamed by hand). Only "" may be
	// replaced by an automatic title.
	TitleSrc string `json:"title_src,omitempty"`
	Dir      string `json:"dir"`
	// Lead is the member the person talks to: every message of theirs goes
	// to it (session/lead.go). "" for a thread from before leads.
	Lead string `json:"lead,omitempty"`
	// Parent is the thread an agent opened this one from (a sub-thread,
	// session/subthread.go), ParentAgent that agent's member id there, and
	// Asks the entries of this thread that agent waits on an answer to.
	Parent      string   `json:"parent,omitempty"`
	ParentAgent string   `json:"parent_agent,omitempty"`
	Asks        []string `json:"asks,omitempty"`
	Members     []Member `json:"members,omitempty"`
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
	// Tasks is the thread's shared task board (task.go).
	Tasks []Task `json:"tasks,omitempty"`
	// Orchestration is the thread's orchestration level ("off", "assist",
	// "auto" or "full"); "" for a thread stored before levels, which
	// Orchestrate then speaks for: set, it was on (auto); unset, it never
	// chose, and takes the configured default.
	Orchestration string `json:"orchestration,omitempty"`
	Orchestrate   bool   `json:"orchestrate,omitempty"`
	// Contract is the teammate whose code contract runs in this thread: its
	// mode is at most ask.
	Contract string `json:"contract,omitempty"`
	// Ops is set when the thread runs, or ran, a teammate's ops contract:
	// its agents stay locked down to sh.run and contract.result.
	Ops bool `json:"ops,omitempty"`
	// Shares are the teammates the thread is shared with, and their roles.
	Shares []Share `json:"shares,omitempty"`
	// Scheduled are messages waiting for their time (session/waits.go).
	Scheduled []Scheduled `json:"scheduled,omitempty"`
	// Trees are the thread's own worktrees, apart from its agents'
	// (session/work.go); Capsules its named capsules.
	Trees    []*Worktree  `json:"trees,omitempty"`
	Capsules []CapsuleRec `json:"capsules,omitempty"`

	// Legacy mirror of Members[0] (Name: every member's name joined by "+").
	AgentKind string `json:"agent_kind"`
	Name      string `json:"name"`
	Model     string `json:"model"`
	SessionID string `json:"session_id"`

	Created time.Time `json:"created"`
	Updated time.Time `json:"updated"`
	CostUSD float64   `json:"cost_usd"` // sum over members
}

// Share is a thread shared with a teammate's Deck: Role is "observer"
// (reads it), "participant" (also posts) or "editor" (also answers its
// approvals).
type Share struct {
	Peer string `json:"peer"`
	Role string `json:"role"`
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
	// EntryTask: a change to the thread's task board; Task says what changed,
	// Speaker who changed it (a member id, or "" for the user or Deck).
	EntryTask = "task"
)

// Entry is one line of a thread log. The embedded Event keeps old logs (bare
// agent.Event lines) readable: they load as Entries with every other field
// empty. For a user, relay, member, pause, continue or notice entry the
// message text lives in Event.Text.
type Entry struct {
	agent.Event
	// TurnID links explicit findings to the same turn during history replay.
	TurnID  string   `json:"turn_id,omitempty"`
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
	// Task is the change an EntryTask records.
	Task *TaskEvent `json:"task,omitempty"`
	// TaskID ties a relay (a handover under a contract) to the task it hands over.
	TaskID string `json:"task_id,omitempty"`
}

// flushDelay is how long metadata and index writes are held back so a burst
// of appends costs one write instead of one per event.
const flushDelay = time.Second

// Store is a directory of threads. It is safe for concurrent use.
type Store struct {
	dir string

	mu      sync.Mutex // guards threads, locks, the flush state and every write to index.json
	threads map[string]*Thread
	locks   map[string]*sync.Mutex // per-thread lock serialising log appends

	// Writes held back until the next flush (see flushDelay).
	dirty      map[string]bool // threads whose .meta.json lags behind memory
	indexDirty bool            // index.json lags behind memory
	timer      *time.Timer     // the pending flush; nil when none
	closed     bool            // after Close every change is written at once
	delay      time.Duration
}

// DefaultDir returns ~/.deck/threads.
func DefaultDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".deck", "threads"), nil
}

// lockFile is the file in the threads directory that a Deck engine locks
// while it uses the store.
const lockFile = "deck.lock"

// Lock takes the cross-process lock on the threads directory dir ("" means
// DefaultDir). Terminal Decks take it shared, so several run at once as
// before; `deck serve` takes it exclusive, so it never writes the store
// while another Deck does. When it cannot be had, Lock fails at once and
// the error names the lock file. The returned func drops the lock.
func Lock(dir string, exclusive bool) (func(), error) {
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
	path := filepath.Join(dir, lockFile)
	try := lock.TryShared
	if exclusive {
		try = lock.TryFile
	}
	release, err := try(path)
	switch {
	case errors.Is(err, lock.ErrContended) && exclusive:
		return nil, fmt.Errorf("another Deck uses the threads in %s: stop it first, or wait for it to stop (lock file %s)", dir, path)
	case errors.Is(err, lock.ErrContended):
		owner := ""
		if b, _ := os.ReadFile(path); len(bytes.TrimSpace(b)) > 0 {
			owner = " (pid " + string(bytes.TrimSpace(b)) + ")"
		}
		return nil, fmt.Errorf("deck serve%s uses the threads in %s: stop it first (lock file %s)", owner, dir, path)
	case err != nil:
		return nil, err
	}
	if exclusive {
		// The pid only helps the message above; the flock is the lock.
		_ = os.WriteFile(path, []byte(strconv.Itoa(os.Getpid())+"\n"), 0o600)
	}
	return release, nil
}

// Unused reports whether no Deck holds the lock on the threads directory
// dir ("" means DefaultDir) now. Call it before Lock: Lock's own hold makes
// it false.
func Unused(dir string) bool {
	if dir == "" {
		d, err := DefaultDir()
		if err != nil {
			return false
		}
		dir = d
	}
	release, err := lock.TryFile(filepath.Join(dir, lockFile))
	if err != nil {
		return false
	}
	release()
	return true
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
		dirty:   make(map[string]bool),
		delay:   flushDelay,
	}

	if threads, err := readIndex(s.indexPath()); err == nil {
		for i := range threads {
			t := threads[i]
			s.threads[t.ID] = &t
		}
		// The index is written lazily, so a crash can leave it behind the
		// metadata files: take any written since.
		stale := false
		for _, t := range newerMeta(dir, s.indexPath()) {
			if cur, ok := s.threads[t.ID]; ok && reflect.DeepEqual(*cur, t) {
				continue
			}
			s.threads[t.ID] = &t
			stale = true
		}
		if stale {
			s.mu.Lock()
			defer s.mu.Unlock()
			if err := s.writeIndexLocked(); err != nil {
				return nil, err
			}
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
	return s.CreateFrom(t, slices.Values(entries))
}

// createChunk is how much of a new log CreateFrom encodes before writing it.
const createChunk = 256 << 10

// CreateFrom is CreateWith for entries handed over one at a time: they are
// encoded and written a part at a time, so a long log is never held as one
// buffer. On an error writing the log the thread stays, with the part of
// the log written, and is returned with the error.
func (s *Store) CreateFrom(t Thread, entries iter.Seq[Entry]) (Thread, error) {
	t, err := s.Create(t)
	if err != nil {
		return Thread{}, err
	}
	s.mu.Lock()
	tl := s.threadLockLocked(t.ID)
	s.mu.Unlock()
	tl.Lock()
	defer tl.Unlock()
	f, err := os.OpenFile(s.logPath(t.ID), os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return t, fmt.Errorf("store: open log: %w", err)
	}
	var buf []byte
	for e := range entries {
		if buf, _, err = encodeEntry(buf, e); err != nil {
			break
		}
		if len(buf) >= createChunk {
			if _, err = f.Write(buf); err != nil {
				break
			}
			buf = buf[:0]
		}
	}
	if err == nil && len(buf) > 0 {
		_, err = f.Write(buf)
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return t, fmt.Errorf("store: write log: %w", err)
	}
	return t, nil
}

// Append writes one plain agent event to the thread's log and bumps its Updated
// time. It is AppendEntry for an event with no speaker information.
func (s *Store) Append(threadID string, ev agent.Event) error {
	return s.AppendEntry(threadID, Entry{Event: ev})
}

// AppendEntry writes one entry to the thread's log and bumps its Updated time.
// Streaming deltas (Event.Delta == true) are dropped to keep logs small, and so
// is the raw protocol line of every event agent.KeepsRaw does not keep. Events
// that cannot be encoded have their invalid raw JSON removed rather than
// failing.
func (s *Store) AppendEntry(threadID string, e Entry) error {
	return s.AppendEntries(threadID, []Entry{e})
}

// AppendEntries is AppendEntry for several entries, written to the log in one
// go and in order.
func (s *Store) AppendEntries(threadID string, entries []Entry) error {
	_, _, err := s.AppendEntriesAt(threadID, entries)
	return err
}

// AppendEntriesAt is AppendEntries that also says where in the log the new
// lines went: from byte offset start to end, the log's size after them. A
// reader that knows where each entry is can read part of the log back
// (ScanEntries) instead of keeping it.
func (s *Store) AppendEntriesAt(threadID string, entries []Entry) (start, end int64, err error) {
	buf, ended, err := encodeEntries(entries)
	if err != nil || len(buf) == 0 {
		return -1, -1, err
	}

	s.mu.Lock()
	if _, ok := s.threads[threadID]; !ok {
		s.mu.Unlock()
		return -1, -1, ErrNotFound
	}
	tl := s.threadLockLocked(threadID)
	s.mu.Unlock()

	tl.Lock()
	start, end, err = appendFileAt(s.logPath(threadID), buf)
	tl.Unlock()
	if err != nil {
		return -1, -1, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.threads[threadID]
	if !ok {
		return start, end, ErrNotFound
	}
	t.Updated = time.Now()
	s.dirty[threadID] = true
	s.indexDirty = true
	if ended {
		// A turn ended: a good moment to bring the files up to date.
		return start, end, s.flushLocked()
	}
	s.scheduleLocked()
	return start, end, nil
}

// encodeEntries renders the log lines for entries, dropping streaming deltas
// and the raw lines the log does not keep. ended reports an entry that ends a
// turn.
func encodeEntries(entries []Entry) (buf []byte, ended bool, err error) {
	for _, e := range entries {
		var end bool
		if buf, end, err = encodeEntry(buf, e); err != nil {
			return nil, false, err
		}
		ended = ended || end
	}
	return buf, ended, nil
}

// encodeEntry is encodeEntries for one entry, appended to buf.
func encodeEntry(buf []byte, e Entry) (_ []byte, ended bool, err error) {
	if e.Delta {
		return buf, false, nil
	}
	e.Event = slim(sanitize(e.Event))
	line, err := json.Marshal(e)
	if err != nil {
		return buf, false, fmt.Errorf("store: encode entry: %w", err)
	}
	buf = append(append(buf, line...), '\n')
	return buf, e.Type == EntryEvent && (e.Kind == agent.KindTurnDone || e.Kind == agent.KindExited), nil
}

// slim drops the raw protocol line of an event that does not keep it (see
// agent.KeepsRaw), and the explicit "Raw":null of older logs, so an old line
// loads the same as one that leaves Raw out.
func slim(ev agent.Event) agent.Event {
	if len(ev.Raw) > 0 && (!agent.KeepsRaw(ev.Kind) || string(ev.Raw) == "null") {
		ev.Raw = nil
	}
	return ev
}

// UpdateMeta replaces the stored metadata of t.ID with t (Title, Dir, Members;
// a Thread with no Members is migrated from the legacy single-agent fields
// first). Created is kept and Updated is set to now. The thread's .meta.json is
// written before UpdateMeta returns; the index follows within flushDelay.
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
	if err := s.writeMetaLocked(t); err != nil {
		return err
	}
	delete(s.dirty, t.ID)
	s.indexDirty = true
	s.scheduleLocked()
	return nil
}

// Flush writes every held-back metadata and index change now.
func (s *Store) Flush() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.flushLocked()
}

// Close flushes the store. The store stays usable, but from then on every
// change is written at once instead of being held back.
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	return s.flushLocked()
}

// scheduleLocked arranges for held-back writes to be flushed. Caller holds s.mu.
func (s *Store) scheduleLocked() {
	if s.closed || s.delay <= 0 {
		_ = s.flushLocked()
		return
	}
	if s.timer == nil {
		s.timer = time.AfterFunc(s.delay, func() { _ = s.Flush() })
	}
}

// flushLocked writes the metadata files that lag behind memory and then the
// index. What fails to be written stays pending for the next flush. Caller
// holds s.mu.
func (s *Store) flushLocked() error {
	if s.timer != nil {
		s.timer.Stop()
		s.timer = nil
	}
	var first error
	for id := range s.dirty {
		t, ok := s.threads[id]
		if !ok {
			delete(s.dirty, id)
			continue
		}
		if err := s.writeMetaLocked(*t); err != nil {
			if first == nil {
				first = err
			}
			continue
		}
		delete(s.dirty, id)
	}
	if s.indexDirty {
		if err := s.writeIndexLocked(); err != nil && first == nil {
			first = err
		}
	}
	return first
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
	entries, _, meta, err := s.LoadEntriesFrom(id, 0, 0)
	return entries, meta, err
}

// LoadEntriesFrom is LoadEntries for the part of the log from byte offset off
// on, for a reader that already holds the n entries before it (so positional
// IDs carry on from n). Logs are only ever appended to, so such a reader can
// keep up with a thread without decoding its whole log again. end is where
// the next read should start: the size of the log read, or -1 when the log
// does not end with a whole line (a torn write), which must be read afresh.
func (s *Store) LoadEntriesFrom(id string, off int64, n int) (entries []Entry, end int64, meta Thread, err error) {
	end, meta, err = s.ScanEntries(id, off, n, 0, func(e Entry, _ int64) bool {
		entries = append(entries, e)
		return true
	})
	if err != nil {
		return nil, -1, meta, err
	}
	return entries, end, meta, nil
}

// Meta returns a thread's metadata, as LoadEntries does, without its log.
func (s *Store) Meta(id string) (Thread, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cur, ok := s.threads[id]
	if !ok {
		return Thread{}, ErrNotFound
	}
	meta := *cur
	meta.Members = append([]Member(nil), cur.Members...)
	return meta, nil
}

// LogSize is the size of a thread's log in bytes.
func (s *Store) LogSize(id string) (int64, error) {
	s.mu.Lock()
	_, ok := s.threads[id]
	tl := s.threadLockLocked(id)
	s.mu.Unlock()
	if !ok {
		return -1, ErrNotFound
	}
	tl.Lock()
	defer tl.Unlock()
	fi, err := os.Stat(s.logPath(id))
	if err != nil {
		return -1, err
	}
	return fi.Size(), nil
}

// ScanEntries is LoadEntriesFrom handing the entries to fn one at a time
// instead of returning them, each with next, the byte offset just past its
// line, where a later read can start. It stops early, with end at the last
// next, when fn returns false. fn runs while appends to the log wait: it
// must not call the store.
//
// The entries of the lines that start before byte full come without their
// event's payload (Tool, Diff, Usage, Limits, Raw): enough to index the log
// at a fraction of the cost of decoding it. A full of 0 has every entry
// whole.
func (s *Store) ScanEntries(id string, off int64, n int, full int64, fn func(e Entry, next int64) bool) (end int64, meta Thread, err error) {
	s.mu.Lock()
	cur, ok := s.threads[id]
	if !ok {
		s.mu.Unlock()
		return -1, Thread{}, ErrNotFound
	}
	meta = *cur
	meta.Members = append([]Member(nil), cur.Members...)
	tl := s.threadLockLocked(id)
	s.mu.Unlock()

	tl.Lock()
	defer tl.Unlock()
	f, err := os.Open(s.logPath(id))
	if err != nil {
		if os.IsNotExist(err) {
			if off == 0 {
				return 0, meta, nil // no log yet: an empty one
			}
			return -1, meta, nil
		}
		return -1, meta, err
	}
	defer f.Close()
	if off > 0 {
		if _, err := f.Seek(off, io.SeekStart); err != nil {
			return -1, meta, err
		}
	}

	end = off
	r := bufio.NewReaderSize(f, 64<<10)
	var long []byte // a line longer than r's buffer
	for {
		line, err := r.ReadSlice('\n')
		if err == bufio.ErrBufferFull {
			long = append(long[:0], line...)
			for err == bufio.ErrBufferFull {
				line, err = r.ReadSlice('\n')
				long = append(long, line...)
			}
			line = long
		}
		start := end
		end += int64(len(line))
		trimmed := bytes.TrimSpace(line)
		decoded := false
		if len(trimmed) > 0 {
			var e Entry
			if e, decoded = decodeEntry(trimmed, start >= full); decoded {
				if e.ID == "" {
					e.ID = "e" + strconv.Itoa(n)
				}
				n++
				if !fn(e, end) {
					return end, meta, nil
				}
			}
		}
		if err == io.EOF {
			// A last line with no newline is unfinished unless it is a
			// whole entry (cut just before its newline): the next append
			// starts a line of its own after it (appendFileAt).
			if len(trimmed) > 0 && !decoded {
				end = -1
			}
			break
		}
		if err != nil {
			return -1, meta, err
		}
	}
	return end, meta, nil
}

// decodeEntry decodes one log line, whole or (whole false) as an entryHead.
// A line is an entry exactly when its head decodes: one whose payload alone
// is broken loads without it, so a read of part of the log counts the same
// entries as a read of all of it.
func decodeEntry(line []byte, whole bool) (Entry, bool) {
	if whole {
		var e Entry
		if json.Unmarshal(line, &e) == nil {
			// Older logs kept every event's raw line; it is not worth
			// holding in memory.
			e.Event = slim(e.Event)
			return e, true
		}
	}
	var h entryHead
	if json.Unmarshal(line, &h) != nil {
		return Entry{}, false
	}
	return h.entry(), true
}

// entryHead is an Entry without the payload of its event (Tool, Diff, Usage,
// Limits, LimitHit, Raw), which is most of the bytes of a log and none of what an
// index of it needs. The fields and their JSON names are Entry's.
type entryHead struct {
	TurnID    string     `json:"turn_id,omitempty"`
	AgentID   string     `json:",omitempty"`
	Time      time.Time  `json:",omitzero"`
	Kind      agent.Kind `json:",omitempty"`
	Text      string     `json:",omitempty"`
	Delta     bool       `json:",omitempty"`
	SessionID string     `json:",omitempty"`

	Type    string     `json:"type,omitempty"`
	Speaker string     `json:"speaker,omitempty"`
	To      []string   `json:"to,omitempty"`
	Via     string     `json:"via,omitempty"`
	Held    bool       `json:"held,omitempty"`
	ID      string     `json:"id,omitempty"`
	Images  []string   `json:"images,omitempty"`
	Steer   bool       `json:"steer,omitempty"`
	Ref     string     `json:"ref,omitempty"`
	Old     string     `json:"old,omitempty"`
	Snap    string     `json:"snap,omitempty"`
	Files   []string   `json:"files,omitempty"`
	Task    *TaskEvent `json:"task,omitempty"`
	TaskID  string     `json:"task_id,omitempty"`
}

func (h entryHead) entry() Entry {
	return Entry{
		TurnID: h.TurnID,
		Event:  agent.Event{AgentID: h.AgentID, Time: h.Time, Kind: h.Kind, Text: h.Text, Delta: h.Delta, SessionID: h.SessionID},
		Type:   h.Type, Speaker: h.Speaker, To: h.To, Via: h.Via, Held: h.Held, ID: h.ID, Images: h.Images,
		Steer: h.Steer, Ref: h.Ref, Old: h.Old, Snap: h.Snap, Files: h.Files, Task: h.Task, TaskID: h.TaskID,
	}
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
	if err := s.writeMetaLocked(t); err != nil {
		return err
	}
	delete(s.dirty, t.ID)
	return s.writeIndexLocked()
}

// writeMetaLocked writes one thread's metadata file. Caller holds s.mu.
func (s *Store) writeMetaLocked(t Thread) error {
	data, err := json.MarshalIndent(t, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(s.metaPath(t.ID), data)
}

// writeIndexLocked writes index.json, compact: it is only a cache for New, and
// it holds every thread. Caller holds s.mu.
func (s *Store) writeIndexLocked() error {
	data, err := json.Marshal(s.sortedLocked())
	if err != nil {
		return err
	}
	if err := writeFileAtomic(s.indexPath(), data); err != nil {
		return err
	}
	s.indexDirty = false
	return nil
}

// Worktree is an isolated thread's git worktree.
type Worktree struct {
	Path   string `json:"path"`   // where the thread's agents work
	Branch string `json:"branch"` // its branch, deck/<id>
	Main   string `json:"main"`   // the shared work tree it came from
	Base   string `json:"base"`   // the commit it started from
	// SubBase is the SUB snapshot of the worktree as it was made, the base
	// its agent's capsules are captured from.
	SubBase string `json:"sub_base,omitempty"`
	// SubParent is the capsule SubBase already includes (applied there
	// before it was taken), which its agent's capsules chain onto.
	SubParent string `json:"sub_parent,omitempty"`
	// Applied is the last apply to Main, kept until it is undone or the
	// next apply replaces it.
	Applied *Applied `json:"applied,omitempty"`
	// Line is what Deck tracks of it as a line of work (work.go).
	Line
}

// Applied is what undoing an apply needs: Main as snapshots before and
// after it, the files it wrote there (both names of a rename), and the
// Base it moved on from.
type Applied struct {
	Before string   `json:"before"`
	After  string   `json:"after"`
	Files  []string `json:"files"`
	Base   string   `json:"base"`
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
	delete(s.dirty, id)
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

// newerMeta reads the *.meta.json files in dir modified no earlier than the
// index file (equal times count: the clock may be coarse). Unreadable or
// id-less files are skipped.
func newerMeta(dir, index string) []Thread {
	fi, err := os.Stat(index)
	if err != nil {
		return nil
	}
	since := fi.ModTime()
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var threads []Thread
	for _, de := range ents {
		if de.IsDir() || !strings.HasSuffix(de.Name(), ".meta.json") {
			continue
		}
		info, err := de.Info()
		if err != nil || info.ModTime().Before(since) {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, de.Name()))
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
	return threads
}

// appendFileAt appends the log lines in data to the file at path and returns
// the file's size before and after them. When the file does not end in a
// newline (a write was cut short), data goes on a line of its own: glued to
// the cut line it would make both unreadable. The newline sits between start
// and the first line, where a reader skips it as a blank line.
func appendFileAt(path string, data []byte) (start, end int64, err error) {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return -1, -1, fmt.Errorf("store: open log: %w", err)
	}
	fail := func(err error) (int64, int64, error) {
		f.Close()
		return -1, -1, fmt.Errorf("store: write log: %w", err)
	}
	if start, err = f.Seek(0, io.SeekEnd); err != nil {
		return fail(err)
	}
	if start > 0 {
		last := make([]byte, 1)
		if _, err := f.ReadAt(last, start-1); err != nil {
			return fail(err)
		}
		if last[0] != '\n' {
			data = append([]byte{'\n'}, data...)
		}
	}
	if _, err := f.Write(data); err != nil {
		return fail(err)
	}
	// An append-mode write leaves the offset at the end of the file.
	end, err = f.Seek(0, io.SeekCurrent)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return -1, -1, fmt.Errorf("store: write log: %w", err)
	}
	return start, end, nil
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

// Sanitize is what the store does to every event before writing it: raw JSON
// the encoder would reject is dropped. The caller's copy is not touched.
func Sanitize(ev agent.Event) agent.Event { return sanitize(ev) }

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
