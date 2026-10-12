package session

import (
	"errors"
	"fmt"
	"strings"

	"github.com/dev-dami/deck/internal/deck/agent"
	"github.com/dev-dami/deck/internal/deck/store"
)

// Task-level steering: redirect whoever owns a task, mid-task, without
// naming the agent. The message goes through the same delivery as queue
// steering (steerDeliver): codex takes it into the running turn, the other
// adapters are interrupted and resumed with it.

// TaskSteeredMsg says a task's owner was sent a steer.
type TaskSteeredMsg struct {
	ThreadID string
	TaskID   string
	AgentID  string // the owner steered
	By       string // the member who steered; "" for the user
}

// SteerTask redirects the owner of a task. by is the member steering, or ""
// for the user; a member may steer only tasks it created.
func (m *Manager) SteerTask(threadID, taskID, by, text string) error {
	if m.isClosed() {
		return errClosed
	}
	t := m.thread(threadID)
	if t == nil {
		return fmt.Errorf("session: no thread %q", threadID)
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return errors.New("session: empty message")
	}

	t.mu.Lock()
	i := store.FindTask(t.tasks, taskID)
	if i < 0 {
		t.mu.Unlock()
		return store.ErrNoTask
	}
	tk := t.tasks[i]
	owner := t.member(tk.Owner)
	switch {
	case tk.Status.Final():
		t.mu.Unlock()
		return fmt.Errorf("session: %s is %s", tk.ID, tk.Status)
	case tk.Owner == "" || owner == nil:
		t.mu.Unlock()
		return fmt.Errorf("session: %s has no owner to steer", tk.ID)
	case owner.status() == StatusExited:
		t.mu.Unlock()
		return fmt.Errorf("session: %s has exited", owner.getName())
	case by != "" && by == tk.Owner:
		t.mu.Unlock()
		return errors.New("session: you cannot steer your own task")
	case by != "" && by != tk.Creator:
		t.mu.Unlock()
		return fmt.Errorf("session: only %s (who handed it over) or the user can steer %s", t.name(tk.Creator), tk.ID)
	}
	var e store.Entry
	if by == "" {
		speaker := m.config().UserName
		if speaker == "" {
			speaker = "user"
		}
		e = store.Entry{
			Type: store.EntryUser, ID: store.NewID(), Speaker: speaker, To: []string{owner.id},
			Steer: true, TaskID: tk.ID, Event: agent.Event{Kind: agent.KindUserText, Text: text},
		}
	} else {
		e = store.Entry{
			Type: store.EntryRelay, ID: store.NewID(), Speaker: by, To: []string{owner.id}, Via: "steer",
			Steer: true, TaskID: tk.ID, Event: eventText(text),
		}
	}
	idx := m.appendLocked(t, e)
	msg := msgFor(t.id, e)
	t.mu.Unlock()
	m.emit(msg)
	m.emit(TaskSteeredMsg{ThreadID: t.id, TaskID: tk.ID, AgentID: owner.id, By: by})

	if err := m.steerDeliver(t, owner); err != nil {
		_ = idx // the entry stays: it is delivered with the owner's next turn
		return fmt.Errorf("session: could not redirect %s now (%v); the message stays in the thread and reaches it after its turn", owner.getName(), err)
	}
	return nil
}

// taskSteerText is the prompt text of a steer aimed at a task. t.mu is held.
func taskSteerText(t *thread, who, taskID, raw string) string {
	head := "[deck] " + who + " is redirecting you mid-task"
	if i := store.FindTask(t.tasks, taskID); i >= 0 {
		head += fmt.Sprintf(" on task %s \"%s\"", t.tasks[i].ID, t.tasks[i].Title)
	}
	return head + ":\n" + clip(strings.TrimSpace(raw), maxEntryChars)
}
