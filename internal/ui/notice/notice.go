// Package notice is Deck's notifications: a history of what happened
// (a push that failed, CI that passed, an agent that finished or needs an
// approval), the toasts that show the newest of them for a few seconds,
// and how a notice is drawn in the Notifications pane and popup.
//
// The store and the toasts are plain values the UI owns and changes in its
// Update loop; code that runs in a tea.Cmd sends a [Msg] instead.
package notice

import (
	"time"
)

// Level is how much a notice matters.
type Level int

const (
	Info Level = iota
	Success
	Warn
	Error
)

// String is the level's name.
func (l Level) String() string {
	switch l {
	case Success:
		return "success"
	case Warn:
		return "warn"
	case Error:
		return "error"
	}
	return "info"
}

// Sources a notice can come from.
const (
	SourceDeck      = "deck"
	SourceGit       = "git"
	SourceCI        = "ci"
	SourceAgent     = "agent"
	SourceApprovals = "approvals"
	SourceTeam      = "team"
)

// Built-in action ids the UI knows how to run. An action can carry its own
// message instead (see [Action.Msg]).
const (
	// ActionGit focuses the git pane.
	ActionGit = "git"
	// ActionGitPR opens a pull request: "git.pr:<number>".
	ActionGitPR = "git.pr:"
	// ActionApproval brings the waiting approval to the front.
	ActionApproval = "approval"
	// ActionNotices opens the notification history.
	ActionNotices = "notices"
)

// Action is something a notice offers to do, e.g. "Open PR #12".
type Action struct {
	// ID names a built-in action (ActionGit, ActionGitPR+"12", …).
	ID string
	// Label is what the action is called, e.g. "Open PR #12".
	Label string
	// Msg, when set, is sent to the UI as a message when the action runs,
	// whatever ID says. It must be a plain value, safe to keep.
	Msg any
}

// Notice is one notification.
type Notice struct {
	ID     int // set by the store
	Level  Level
	Source string // SourceGit, SourceAgent, …
	Title  string
	Body   string
	Time   time.Time // set by the store when zero
	Action *Action
	// Quiet notices go into the history without a toast.
	Quiet bool
}

// Text is the notice's title and body on one line.
func (n Notice) Text() string {
	switch {
	case n.Title == "":
		return n.Body
	case n.Body == "":
		return n.Title
	}
	return n.Title + ": " + n.Body
}

// Msg pushes a notice from a tea.Cmd: return notice.Msg{Notice: …}.
type Msg struct{ Notice Notice }

// MaxHistory is how many notices the store keeps.
const MaxHistory = 200

// Store is the notification history. Its zero value is ready to use.
type Store struct {
	items []Notice // oldest first
	next  int      // the last id handed out
	read  int      // notices up to this id have been seen
}

// Push adds a notice and returns it with its id and time set.
func (s *Store) Push(n Notice) Notice {
	s.next++
	n.ID = s.next
	if n.Time.IsZero() {
		n.Time = time.Now()
	}
	if n.Source == "" {
		n.Source = SourceDeck
	}
	s.items = append(s.items, n)
	if over := len(s.items) - MaxHistory; over > 0 {
		s.items = append(s.items[:0:0], s.items[over:]...)
	}
	return n
}

// List is the history, newest first. The slice is a copy.
func (s *Store) List() []Notice {
	out := make([]Notice, len(s.items))
	for i, n := range s.items {
		out[len(s.items)-1-i] = n
	}
	return out
}

// Len is how many notices the store holds.
func (s *Store) Len() int { return len(s.items) }

// Get finds a notice by id.
func (s *Store) Get(id int) (Notice, bool) {
	for _, n := range s.items {
		if n.ID == id {
			return n, true
		}
	}
	return Notice{}, false
}

// Unread is how many notices arrived since the history was last seen.
func (s *Store) Unread() int {
	c := 0
	for i := len(s.items) - 1; i >= 0 && s.items[i].ID > s.read; i-- {
		c++
	}
	return c
}

// MarkRead marks every notice seen.
func (s *Store) MarkRead() { s.read = s.next }

// Remove drops one notice.
func (s *Store) Remove(id int) {
	for i, n := range s.items {
		if n.ID == id {
			s.items = append(s.items[:i], s.items[i+1:]...)
			return
		}
	}
}

// Clear empties the history.
func (s *Store) Clear() {
	s.items = nil
	s.read = s.next
}
