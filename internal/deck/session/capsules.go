package session

import (
	"errors"
	"fmt"

	"github.com/dev-dami/deck/internal/deck/sub"
)

// CapsuleInfo is one capsule as the UI's Capsules pane lists it.
type CapsuleInfo struct {
	ID        string
	Task      string // the task it is on; "" for one only published
	TaskTitle string
	Files     int // how many files it changes
	Add, Mod  int
	Del       int
	Source    string // the agent that made it
	Note      string
	Public    bool // published: paired teammates can fetch it
}

// Capsules lists the capsules on the thread's task board, newest first,
// then any other published capsule this Deck holds.
func (m *Manager) Capsules(threadID string) []CapsuleInfo {
	s := m.subStore()
	if s == nil {
		return nil
	}
	type ref struct{ id, task, title string }
	var refs []ref
	if t := m.thread(threadID); t != nil {
		t.mu.Lock()
		for _, tk := range t.tasks {
			for _, c := range tk.Capsules {
				refs = append(refs, ref{c, tk.ID, tk.Title})
			}
		}
		t.mu.Unlock()
	}
	seen := map[string]bool{}
	var out []CapsuleInfo
	for i := len(refs) - 1; i >= 0; i-- {
		r := refs[i]
		if seen[r.id] {
			continue
		}
		seen[r.id] = true
		if ci, ok := capsuleInfo(s, sub.ID(r.id)); ok {
			ci.Task, ci.TaskTitle = r.task, r.title
			out = append(out, ci)
		}
	}
	for _, id := range s.Published() {
		if seen[string(id)] {
			continue
		}
		if ci, ok := capsuleInfo(s, id); ok {
			out = append(out, ci)
		}
	}
	return out
}

func capsuleInfo(s *sub.Store, id sub.ID) (CapsuleInfo, bool) {
	c, err := s.Capsule(id)
	if err != nil {
		return CapsuleInfo{}, false
	}
	ci := CapsuleInfo{ID: string(id), Source: c.Meta.Source, Note: c.Meta.Note, Public: s.IsPublished(id)}
	if ops, err := s.Ops(id); err == nil {
		ci.Files = len(ops)
		for _, o := range ops {
			switch o.Kind() {
			case "add":
				ci.Add++
			case "delete":
				ci.Del++
			default:
				ci.Mod++
			}
		}
	}
	return ci, true
}

// SetCapsulePublic publishes a capsule, so paired teammates can fetch it,
// or makes it private again.
func (m *Manager) SetCapsulePublic(id string, public bool) error {
	s := m.subStore()
	if s == nil {
		return errors.New("no SUB store")
	}
	cid := sub.ID(id)
	if _, err := s.Capsule(cid); err != nil {
		return fmt.Errorf("no capsule %s here", cid.Short())
	}
	if public {
		_, err := s.Publish(cid)
		return err
	}
	return s.Unpublish(cid)
}
