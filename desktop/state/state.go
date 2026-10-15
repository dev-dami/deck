// Package state is the app's picture of every open project: sessions,
// messages, approvals and busy flags, kept current by the event stream.
// Readers hold the lock through View; event goroutines write through the
// methods.
package state

import (
	"slices"
	"sync"

	"google.golang.org/protobuf/proto"

	"github.com/dev-dami/deck/pkg/rpc/deckv1"
)

// Project is one open project's state.
type Project struct {
	Key       string // the backend's directory
	Name      string
	Kinds     []string
	Theme     *deckv1.Theme
	Sessions  []*deckv1.Session // newest first
	Messages  map[string][]*deckv1.Message
	Approvals map[string][]*deckv1.Approval
	Queue     map[string][]*deckv1.QueuedItem
	Roster    map[string][]*deckv1.AgentLine
	Changes   map[string][]*deckv1.ChangedFile
	// Models is keyed by session id + "|" + agent kind.
	Models  map[string][]*deckv1.ModelChoice
	Notices []string
}

// Store holds every project. Changed is called after each write, outside
// the lock (the window invalidates itself there).
type Store struct {
	mu       sync.Mutex
	projects []*Project
	Changed  func()
}

func (s *Store) write(f func()) {
	s.mu.Lock()
	f()
	s.mu.Unlock()
	if s.Changed != nil {
		s.Changed()
	}
}

// View runs f with the projects under the lock.
func (s *Store) View(f func(ps []*Project)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f(s.projects)
}

// Get returns the project for key, nil if none; call within View.
func Get(ps []*Project, key string) *Project {
	for _, p := range ps {
		if p.Key == key {
			return p
		}
	}
	return nil
}

// Add registers a project once.
func (s *Store) Add(key string, info *deckv1.InfoResponse) {
	s.write(func() {
		if Get(s.projects, key) != nil {
			return
		}
		s.projects = append(s.projects, &Project{
			Key: key, Name: info.GetProject(), Kinds: info.GetKinds(), Theme: info.GetTheme(),
			Messages: map[string][]*deckv1.Message{}, Approvals: map[string][]*deckv1.Approval{},
			Queue: map[string][]*deckv1.QueuedItem{}, Roster: map[string][]*deckv1.AgentLine{},
			Changes: map[string][]*deckv1.ChangedFile{}, Models: map[string][]*deckv1.ModelChoice{},
		})
	})
}

// Remove drops a project.
func (s *Store) Remove(key string) {
	s.write(func() {
		s.projects = slices.DeleteFunc(s.projects, func(p *Project) bool { return p.Key == key })
	})
}

func (s *Store) with(key string, f func(p *Project)) {
	s.write(func() {
		if p := Get(s.projects, key); p != nil {
			f(p)
		}
	})
}

func sortSessions(ss []*deckv1.Session) {
	slices.SortStableFunc(ss, func(a, b *deckv1.Session) int {
		switch {
		case a.GetUpdatedAt() > b.GetUpdatedAt():
			return -1
		case a.GetUpdatedAt() < b.GetUpdatedAt():
			return 1
		}
		return 0
	})
}

// SetSessions replaces the session list.
func (s *Store) SetSessions(key string, ss []*deckv1.Session) {
	s.with(key, func(p *Project) {
		p.Sessions = slices.Clone(ss)
		sortSessions(p.Sessions)
	})
}

// SetMessages replaces one session's messages.
func (s *Store) SetMessages(key, session string, ms []*deckv1.Message) {
	s.with(key, func(p *Project) { p.Messages[session] = slices.Clone(ms) })
}

// SetApprovals replaces every session's pending approvals.
func (s *Store) SetApprovals(key string, as []*deckv1.Approval) {
	s.with(key, func(p *Project) {
		p.Approvals = map[string][]*deckv1.Approval{}
		for _, a := range as {
			p.Approvals[a.GetSessionId()] = append(p.Approvals[a.GetSessionId()], a)
		}
	})
}

// SetRoster replaces one session's agents.
func (s *Store) SetRoster(key, session string, as []*deckv1.AgentLine) {
	s.with(key, func(p *Project) { p.Roster[session] = as })
}

// SetChanges replaces one session's changed files.
func (s *Store) SetChanges(key, session string, fs []*deckv1.ChangedFile) {
	s.with(key, func(p *Project) { p.Changes[session] = fs })
}

// SetModels replaces the model choices of one agent kind in a session.
func (s *Store) SetModels(key, session, kind string, ms []*deckv1.ModelChoice) {
	s.with(key, func(p *Project) { p.Models[session+"|"+kind] = ms })
}

// SetQueue replaces one session's queue.
func (s *Store) SetQueue(key, session string, q []*deckv1.QueuedItem) {
	s.with(key, func(p *Project) { p.Queue[session] = q })
}

func upsertSession(p *Project, ss *deckv1.Session) {
	if i := slices.IndexFunc(p.Sessions, func(x *deckv1.Session) bool { return x.GetId() == ss.GetId() }); i >= 0 {
		p.Sessions[i] = ss
	} else {
		p.Sessions = append(p.Sessions, ss)
	}
	sortSessions(p.Sessions)
}

// editSession changes a copy, so a snapshot taken earlier never sees a
// write: stored values are replaced, never mutated.
func editSession(p *Project, id string, f func(ss *deckv1.Session)) {
	for i, ss := range p.Sessions {
		if ss.GetId() == id {
			c := proto.CloneOf(ss)
			f(c)
			p.Sessions[i] = c
		}
	}
}

func upsertMessage(p *Project, m *deckv1.Message) {
	ms := p.Messages[m.GetSessionId()]
	if i := slices.IndexFunc(ms, func(x *deckv1.Message) bool { return x.GetId() == m.GetId() }); i >= 0 {
		ms[i] = m
		return
	}
	p.Messages[m.GetSessionId()] = append(ms, m)
}

// Apply folds one event into the project's state. It reports whether the
// event asks the UI to refetch (a resync, or the roster changed).
func (s *Store) Apply(key string, ev *deckv1.Event) (refetch bool) {
	s.with(key, func(p *Project) {
		switch k := ev.GetKind().(type) {
		case *deckv1.Event_Ready:
			refetch = true
		case *deckv1.Event_SessionCreated:
			upsertSession(p, k.SessionCreated)
		case *deckv1.Event_SessionUpdated:
			upsertSession(p, k.SessionUpdated)
		case *deckv1.Event_SessionDeleted:
			id := k.SessionDeleted.GetId()
			p.Sessions = slices.DeleteFunc(p.Sessions, func(x *deckv1.Session) bool { return x.GetId() == id })
			delete(p.Messages, id)
			delete(p.Approvals, id)
		case *deckv1.Event_MessageCreated:
			upsertMessage(p, k.MessageCreated)
		case *deckv1.Event_MessageUpdated:
			upsertMessage(p, k.MessageUpdated)
		case *deckv1.Event_MessageDeleted:
			sid, id := k.MessageDeleted.GetSessionId(), k.MessageDeleted.GetId()
			p.Messages[sid] = slices.DeleteFunc(p.Messages[sid], func(x *deckv1.Message) bool { return x.GetId() == id })
		case *deckv1.Event_ApprovalRequested:
			a := k.ApprovalRequested
			if !slices.ContainsFunc(p.Approvals[a.GetSessionId()], func(x *deckv1.Approval) bool { return x.GetId() == a.GetId() }) {
				p.Approvals[a.GetSessionId()] = append(p.Approvals[a.GetSessionId()], a)
			}
		case *deckv1.Event_ApprovalResolved:
			sid, id := k.ApprovalResolved.GetSessionId(), k.ApprovalResolved.GetId()
			p.Approvals[sid] = slices.DeleteFunc(p.Approvals[sid], func(x *deckv1.Approval) bool { return x.GetId() == id })
		case *deckv1.Event_Busy:
			editSession(p, k.Busy.GetSessionId(), func(ss *deckv1.Session) { ss.Busy = k.Busy.GetBusy() })
		case *deckv1.Event_QueueChanged:
			p.Queue[k.QueueChanged.GetSessionId()] = k.QueueChanged.GetItems()
		case *deckv1.Event_RosterChanged:
			refetch = true
		case *deckv1.Event_ModeChanged:
			editSession(p, k.ModeChanged.GetSessionId(), func(ss *deckv1.Session) { ss.Mode = k.ModeChanged.GetMode() })
		case *deckv1.Event_Notice:
			p.Notices = append(p.Notices, k.Notice.GetText())
			if len(p.Notices) > 20 {
				p.Notices = p.Notices[len(p.Notices)-20:]
			}
		case *deckv1.Event_ThemeChanged:
			p.Theme = k.ThemeChanged
		}
	})
	return refetch
}
