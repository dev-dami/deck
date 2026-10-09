package session

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/dev-dami/deck/internal/agent"
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
