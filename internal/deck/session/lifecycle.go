package session

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/dev-dami/deck/internal/deck/agent"
	"github.com/dev-dami/deck/internal/deck/store"
)

// ThreadChangedMsg says a thread was renamed, pinned, resolved, woken,
// archived or deleted. Gone is true when it left the live set (archive,
// delete); the UI re-reads Threads either way.
type ThreadChangedMsg struct {
	ThreadID string
	Gone     bool
}

// RenameThread sets a thread's title.
func (m *Manager) RenameThread(threadID, title string) error {
	title = oneLine(title)
	if title == "" {
		return errors.New("session: empty title")
	}
	return m.change(threadID, func(t *thread) { t.title, t.titleSrc = title, titleUser })
}

// SetPinned pins a thread to the top of the list, or unpins it.
func (m *Manager) SetPinned(threadID string, on bool) error {
	return m.change(threadID, func(t *thread) { t.pinned = on })
}

// Resolve settles a thread: whatever its members are doing is interrupted and
// it is marked resolved until the next message wakes it.
func (m *Manager) Resolve(threadID string) error {
	t := m.thread(threadID)
	if t == nil {
		return fmt.Errorf("session: no thread %q", threadID)
	}
	t.mu.Lock()
	members := append([]*agentState(nil), t.members...)
	t.mu.Unlock()
	for _, a := range members {
		if s := a.status(); s != StatusIdle && s != StatusExited && s != StatusError {
			_ = m.Interrupt(a.id)
		}
	}
	return m.change(threadID, func(t *thread) { t.resolved = true })
}

// wake clears a thread's resolved mark when a new message arrives.
func (m *Manager) wake(t *thread) {
	t.mu.Lock()
	was := t.resolved
	t.resolved = false
	t.mu.Unlock()
	if was {
		m.persistMeta(t)
		m.emit(ThreadChangedMsg{ThreadID: t.id})
	}
}

// change applies f to a live thread under its lock, stores the result and
// tells the UI.
func (m *Manager) change(threadID string, f func(*thread)) error {
	t := m.thread(threadID)
	if t == nil {
		return fmt.Errorf("session: no thread %q", threadID)
	}
	t.mu.Lock()
	f(t)
	t.mu.Unlock()
	m.persistMeta(t)
	m.emit(ThreadChangedMsg{ThreadID: threadID})
	return nil
}

// Archive stops a thread's agents and takes it off the live list. It stays in
// the store, marked archived, and /resume or Unarchive brings it back.
func (m *Manager) Archive(threadID string) error {
	t, err := m.drop(threadID)
	if err != nil {
		return err
	}
	t.mu.Lock()
	t.archived = true
	t.mu.Unlock()
	if m.st != nil {
		th := m.snapshot(t)
		m.w.push(func() {
			if t.created {
				_ = m.st.UpdateMeta(th)
				return
			}
			// A thread that never had content was never stored: store its
			// metadata alone, so the mark has somewhere to live.
			_, _ = m.st.Create(th)
		})
	}
	m.emit(ThreadChangedMsg{ThreadID: threadID, Gone: true})
	return nil
}

// Unarchive brings an archived thread back to the list without starting its
// agents; its next message resumes it. A thread archived before anyone wrote
// in it was stored only to hold the mark, so it leaves the store again.
func (m *Manager) Unarchive(threadID string) error {
	if m.st == nil {
		return errors.New("session: no store")
	}
	if m.thread(threadID) != nil {
		return errors.New("session: the thread is not archived")
	}
	errc := make(chan error, 1)
	m.w.push(func() { // after the archive's own write
		ts, err := m.st.List()
		if err != nil {
			errc <- err
			return
		}
		i := slices.IndexFunc(ts, func(t store.Thread) bool { return t.ID == threadID })
		switch {
		case i < 0:
			errc <- fmt.Errorf("session: no thread %q", threadID)
		case !ts[i].Archived:
			errc <- errors.New("session: the thread is not archived")
		case len(ts[i].Members) == 0:
			errc <- m.st.Delete(threadID)
		default:
			ts[i].Archived = false
			errc <- m.st.UpdateMeta(ts[i])
		}
	})
	if err := <-errc; err != nil {
		return err
	}
	m.emit(ThreadChangedMsg{ThreadID: threadID})
	return nil
}

// Delete stops a thread's agents and removes it, log and all.
func (m *Manager) Delete(threadID string) error {
	t, err := m.drop(threadID)
	if err != nil {
		return err
	}
	if m.st != nil {
		m.w.push(func() {
			if t.created {
				_ = m.st.Delete(t.id)
			}
		})
	}
	t.mu.Lock()
	wts := []*store.Worktree{t.worktree}
	for _, a := range t.members {
		wts = append(wts, a.getWorktree()) // a subagent's own worktree (spawn.go)
	}
	t.mu.Unlock()
	for _, wt := range wts {
		m.dropWorktree(wt)
	}
	m.emit(ThreadChangedMsg{ThreadID: threadID, Gone: true})
	return nil
}

// drop interrupts and closes every member of a thread and forgets it.
func (m *Manager) drop(threadID string) (*thread, error) {
	t := m.thread(threadID)
	if t == nil {
		return nil, fmt.Errorf("session: no thread %q", threadID)
	}
	t.mu.Lock()
	members := append([]*agentState(nil), t.members...)
	t.mu.Unlock()
	for _, a := range members {
		_ = m.Interrupt(a.id) // denies its pending approvals too
	}
	t.gone.Store(true) // from here its agents' events and writes are dropped
	var wg sync.WaitGroup
	for _, a := range members {
		run := a.getRun()
		if run == nil {
			continue
		}
		wg.Add(1)
		go func(run agent.Run) {
			defer wg.Done()
			_ = run.Close()
		}(run)
	}
	wait(&wg, 3*time.Second)
	m.forget(t)
	return t, nil
}

// Fork starts a new thread from threadID's history up to (not including) the
// user message entryID, or from all of it when entryID is "". The new thread
// has the same members on fresh CLI sessions, each briefed on that history at
// its first turn. It returns the new thread like Resume does, plus the text of
// the message forked at (for the composer).
func (m *Manager) Fork(threadID, entryID string) (ThreadInfo, []tea.Msg, string, error) {
	if m.st == nil {
		return ThreadInfo{}, nil, "", errors.New("session: no store")
	}
	t := m.thread(threadID)
	if t == nil {
		return ThreadInfo{}, nil, "", fmt.Errorf("session: no thread %q", threadID)
	}
	t.mu.Lock()
	cut, text := len(t.log), ""
	if entryID != "" {
		cut = -1
		for i, e := range t.log {
			if e.ID == entryID && e.Type == store.EntryUser {
				cut, text = i, e.Text
				if s, ok := t.editedFrom(i)[e.ID]; ok {
					text = s
				}
				break
			}
		}
	}
	if cut < 0 {
		t.mu.Unlock()
		return ThreadInfo{}, nil, "", errors.New("session: no such message in this thread")
	}
	entries := append([]store.Entry(nil), t.log[:cut]...)
	members := append([]*agentState(nil), t.members...)
	th := store.Thread{ID: store.NewID(), Title: "fork · " + t.title, TitleSrc: titleUser, Dir: t.dir, Mode: string(t.mode)}
	t.mu.Unlock()
	if len(members) == 0 {
		return ThreadInfo{}, nil, "", errors.New("session: nothing to fork yet")
	}
	for _, a := range members {
		if a.isEphemeral() {
			continue // a subagent belongs to its own thread's tasks
		}
		mm := a.memberMeta()
		mm.SessionID, mm.Cursor, mm.CostUSD = "", 0, 0 // a fresh session, briefed on everything
		mm.ContextTokens, mm.ContextWindow = 0, 0
		th.Members = append(th.Members, mm)
	}
	if _, err := m.st.CreateWith(th, entries); err != nil {
		return ThreadInfo{}, nil, "", err
	}
	info, hist, err := m.Resume(th.ID)
	return info, hist, text, err
}

// Move re-homes a thread in dir. A CLI's session belongs to the folder it ran
// in, so the members restart there on fresh sessions, each briefed on the
// thread so far at its next turn. The thread keeps its id and history.
func (m *Manager) Move(threadID, dir string) (ThreadInfo, []tea.Msg, error) {
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		return ThreadInfo{}, nil, fmt.Errorf("session: %s is not a directory", dir)
	}
	dir = filepath.Clean(dir)
	t := m.thread(threadID)
	if t == nil {
		return ThreadInfo{}, nil, fmt.Errorf("session: no thread %q", threadID)
	}
	t.mu.Lock()
	same, empty := t.dir == dir, len(t.members) == 0
	if empty && !same { // nobody has run yet: just point it elsewhere
		t.dir = dir
		t.repoOnce, t.repo = sync.Once{}, nil
	}
	t.mu.Unlock()
	switch {
	case same:
		return ThreadInfo{}, nil, errors.New("session: the thread is already there")
	case empty:
		m.emit(ThreadChangedMsg{ThreadID: threadID})
		return m.threadInfo(t), nil, nil
	case m.st == nil:
		return ThreadInfo{}, nil, errors.New("session: no store")
	}
	th := m.snapshot(t)
	th.Dir = dir
	for i := range th.Members {
		th.Members[i].SessionID, th.Members[i].Cursor = "", 0
		th.Members[i].ContextTokens, th.Members[i].ContextWindow = 0, 0
	}
	if _, err := m.drop(threadID); err != nil {
		return ThreadInfo{}, nil, err
	}
	errc := make(chan error, 1)
	m.w.push(func() { // after every write the thread had queued
		if t.created {
			errc <- m.st.UpdateMeta(th)
			return
		}
		_, err := m.st.Create(th)
		errc <- err
	})
	if err := <-errc; err != nil {
		return ThreadInfo{}, nil, err
	}
	return m.Resume(threadID)
}
