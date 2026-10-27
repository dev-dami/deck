package session

import (
	"fmt"
	"strings"

	"github.com/dev-dami/deck/internal/deck/config"
	"github.com/dev-dami/deck/internal/deck/store"
)

// Orchestration levels (config.Levels). A thread has one: it sets how deep
// and wide its members may spawn (spawnRoom), whether they may race (Race),
// whether the scheduler runs its board (schedule), how much work goes out
// per user message (takeWork), who reviews its helpers' work (reviewOf), and
// what its members' instructions tell them about all that (levelText).

// viaLevel is EntryNotice.Via of a level change, which briefings show: the
// members' instructions name the level they started at.
const viaLevel = "level"

// SetLevel sets a thread's orchestration level. Its members hear of the
// change with their next prompt.
func (m *Manager) SetLevel(threadID, level string) error {
	if !config.ValidLevel(level) {
		return fmt.Errorf("session: no orchestration level %q (%s)", level, strings.Join(config.LevelNames, ", "))
	}
	var was string
	err := m.change(threadID, func(t *thread) { was, t.level = t.level, level })
	if err != nil || was == level {
		return err
	}
	t := m.thread(threadID)
	if t == nil {
		return nil
	}
	// Members start with the level in their instructions; those already
	// here hear of the change.
	t.mu.Lock()
	tell := len(t.members) > 0
	t.mu.Unlock()
	if tell {
		e := store.Entry{Type: store.EntryNotice, Via: viaLevel, Event: eventText("The user set orchestration to " + m.levelText(t))}
		t.mu.Lock()
		m.appendLocked(t, e)
		t.mu.Unlock()
		m.emit(msgFor(t.id, e))
	}
	m.kick(t)
	return nil
}

// StartLevel is the level new threads start at.
func (m *Manager) StartLevel() string { return m.config().StartLevel() }

// storedLevel is the level of a stored thread: its own, else auto for one
// stored with orchestration on, else the configured default, as it never
// chose.
func storedLevel(th store.Thread, cfg config.Config) string {
	switch {
	case config.ValidLevel(th.Orchestration):
		return th.Orchestration
	case th.Orchestrate:
		return config.LevelAuto
	}
	return cfg.StartLevel()
}

// levelText tells a member what t's level lets it do, for its instructions
// and the notice of a change: "<level>: ...".
func (m *Manager) levelText(t *thread) string {
	t.mu.Lock()
	name, lv := t.level, m.levelLocked(t)
	t.mu.Unlock()
	var b strings.Builder
	b.WriteString(name + ": ")
	switch {
	case lv.AskFirst:
		fmt.Fprintf(&b, "start helpers only when the user asks you to split work or spin up agents (at most %d); otherwise do the work yourself.", lv.MaxChildren)
	case lv.MaxDepth <= 1:
		fmt.Fprintf(&b, "you may start up to %d helpers when that clearly saves time; do small jobs yourself.", lv.MaxChildren)
	default:
		fmt.Fprintf(&b, "plan bigger work on the task board (deps), split it and start helpers (%d each, %d in the thread, %d levels deep); Deck runs ready tasks. Hand off and review each other's work.",
			lv.MaxChildren, lv.MaxSubagents, lv.MaxDepth)
	}
	if lv.Race {
		b.WriteString(" task.race {brief, agents} races worktrees.")
	}
	if lv.Review != "" && lv.Review != store.ReviewNone && m.config().Review == "" {
		b.WriteString(" Helpers' work is reviewed.")
	}
	return b.String()
}
