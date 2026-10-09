package session

import (
	"errors"
	"fmt"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/dev-dami/deck/internal/agent"
	"github.com/dev-dami/deck/internal/store"
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
	return m.change(threadID, func(t *thread) { t.title = title })
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
// the store, marked archived, and /resume brings it back.
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
			if t.created { // a thread that never had content was never stored
				_ = m.st.UpdateMeta(th)
			}
		})
	}
	m.emit(ThreadChangedMsg{ThreadID: threadID, Gone: true})
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
	var wg sync.WaitGroup
	for _, a := range members {
		_ = m.Interrupt(a.id) // denies its pending approvals too
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
				if s, ok := t.editedTexts()[e.ID]; ok {
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
	th := store.Thread{ID: store.NewID(), Title: "fork · " + t.title, Dir: t.dir, Mode: string(t.mode)}
	t.mu.Unlock()
	if len(members) == 0 {
		return ThreadInfo{}, nil, "", errors.New("session: nothing to fork yet")
	}
	for _, a := range members {
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
