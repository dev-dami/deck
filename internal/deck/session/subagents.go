package session

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/dev-dami/deck/internal/deck/agent"
	"github.com/dev-dami/deck/internal/deck/store"
)

// A member's CLI-native subagents: the ones its CLI started with its own
// tool (Claude Code's Task, OpenCode's task, Codex's spawn_agent), not with
// Deck's agent.spawn. They are not members: nothing can message one, and it
// stops with its member. Deck keeps each one's name, task and state with the
// member (store.Member.Subs), so a room shows them after a restart, and
// passes their work on to the UI tagged with them (agent.Event.Sub), apart
// from the member's own turn: no reply, no relay, not in the thread's log.

// maxSubs is how many subagents a member keeps; the oldest ended ones go.
const maxSubs = 64

// SubPath is a subagent's lane id: its member's id, a slash and its name
// ("claude/review-pr-296"). No member id holds a slash.
func SubPath(member, name string) string { return member + "/" + name }

// onSubagent folds a subagent's event into its member's subagents and
// passes it on. It is the whole of onEvent for such an event.
func (m *Manager) onSubagent(a *agentState, ev agent.Event) {
	t := a.thread
	id := ev.Sub
	if ev.Kind == agent.KindSubagent {
		if ev.Subagent == nil {
			return
		}
		id = ev.Subagent.ID
	}
	a.mu.Lock()
	s, started := a.subLocked(id, ev.Time)
	changed := started
	switch ev.Kind {
	case agent.KindSubagent:
		in := ev.Subagent
		if in.Type != "" && in.Type != s.Type {
			s.Type, changed = in.Type, true
		}
		if in.Description != "" && in.Description != s.Description {
			s.Description, changed = in.Description, true
			if started {
				s.Name = a.subNameLocked(s)
			}
		}
		if st := string(in.Status); st != "" && st != s.Status {
			s.Status, changed = st, true
			s.Error = in.Error
			if in.Status != agent.SubRunning {
				s.Ended = ev.Time
			}
		}
	case agent.KindToolCall:
		s.Actions++
	}
	snap := subagentOf(*s)
	a.mu.Unlock()
	if ev.Kind == agent.KindSubagent {
		snap.Result = ev.Subagent.Result
	}
	ev.Subagent = &snap
	if changed {
		m.persistMeta(t)
	}
	if ev.Kind == agent.KindFileChange && ev.Diff != nil {
		m.noteEdit(t, a, ev.Diff) // the member's CLI made the edit
	}
	m.emit(EventMsg{Event: ev, ThreadID: t.id})
}

// subLocked is the member's subagent id, started at at when it is new
// (a.mu held).
func (a *agentState) subLocked(id string, at time.Time) (s *store.SubAgent, started bool) {
	for i := range a.subs {
		if a.subs[i].ID == id {
			return &a.subs[i], false
		}
	}
	if len(a.subs) >= maxSubs {
		// The oldest that ended goes; with none ended, the oldest.
		drop := slices.IndexFunc(a.subs, func(s store.SubAgent) bool { return s.Status != string(agent.SubRunning) })
		a.subs = slices.Delete(a.subs, max(drop, 0), max(drop, 0)+1)
	}
	a.subs = append(a.subs, store.SubAgent{ID: id, Status: string(agent.SubRunning), Started: at})
	s = &a.subs[len(a.subs)-1]
	s.Name = a.subNameLocked(s)
	return s, true
}

// subNameLocked names s from its task, unique among the member's
// subagents: "Review PR #296" is review-pr-296, a second one of that task
// review-pr-296-2.
func (a *agentState) subNameLocked(s *store.SubAgent) string {
	base := subSlug(s.Description, 4, 28)
	if base == "" && s.Type != "general-purpose" {
		base = subSlug(s.Type, 2, 20)
	}
	if base == "" {
		base = "subagent"
	}
	taken := func(n string) bool {
		for i := range a.subs {
			if &a.subs[i] != s && a.subs[i].Name == n {
				return true
			}
		}
		return false
	}
	name := base
	for n := 2; taken(name); n++ {
		name = fmt.Sprintf("%s-%d", base, n)
	}
	return name
}

// subSlug is slug(s) cut to its first n words and max bytes.
func subSlug(s string, n, maxLen int) string {
	var out string
	for i, w := range strings.Split(slug(s), "-") {
		if i == n {
			break
		}
		next := w
		if out != "" {
			next = out + "-" + w
		}
		if len(next) > maxLen {
			if out == "" {
				out = w[:maxLen]
			}
			break
		}
		out = next
	}
	return out
}

// stopSubsLocked ends the member's subagents still running: its CLI ended
// without them reporting back (a.mu held). It returns those it ended.
func (a *agentState) stopSubsLocked(at time.Time) []store.SubAgent {
	var stopped []store.SubAgent
	for i := range a.subs {
		if a.subs[i].Status == string(agent.SubRunning) {
			a.subs[i].Status, a.subs[i].Ended = string(agent.SubStopped), at
			stopped = append(stopped, a.subs[i])
		}
	}
	return stopped
}

// subagentOf is a stored subagent as an event carries it.
func subagentOf(s store.SubAgent) agent.Subagent {
	return agent.Subagent{
		ID: s.ID, Name: s.Name, Type: s.Type, Description: s.Description,
		Status: agent.SubStatus(s.Status), Error: s.Error,
	}
}

// subRoster is the lines thread.roster shows for a member's subagents
// under its own: those running, then how many ended.
func subRoster(memberName string, subs []store.SubAgent) []string {
	var lines []string
	ended := 0
	for _, s := range subs {
		if s.Status != string(agent.SubRunning) {
			ended++
			continue
		}
		line := "  ↳ " + SubPath(memberName, s.Name) + " · " + memberName + "'s own subagent"
		if s.Type != "" {
			line += " (" + s.Type + ")"
		}
		if d := oneLine(s.Description); d != "" {
			line += " · " + clip(d, 60)
		}
		lines = append(lines, line+" · running · not addressable")
	}
	if ended > 0 {
		lines = append(lines, fmt.Sprintf("  ↳ %d more of %s's own subagents ended", ended, memberName))
	}
	return lines
}
