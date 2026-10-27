package codex

import (
	"strings"

	"github.com/dev-dami/deck/internal/deck/agent"
)

// Codex's own subagents (its multi-agent "collab" tools): a
// collabAgentToolCall item with tool spawnAgent starts one on a thread of its
// own (receiverThreadIds), and the collab calls after it (wait, sendInput,
// closeAgent) report each target's state in agentsStates. Deck shows each
// spawned agent as a subagent of the codex member (agent.KindSubagent); items
// of a spawned thread that reach this connection are its work (Sub).

// collabState is CollabAgentState: a target agent's last known status
// (pendingInit, running, completed, errored, shutdown, notFound) and message.
type collabState struct {
	Status  string  `json:"status"`
	Message *string `json:"message"`
}

// collabSpawn is the tool name of the collab call that starts an agent.
const collabSpawn = "spawnAgent"

// collabStarted handles a collabAgentToolCall item that started.
func (t *translator) collabStarted(it threadItem, raw rawLine) []agent.Event {
	if it.Tool != collabSpawn || t.subs[it.ID] != nil {
		return nil
	}
	s := &agent.Subagent{ID: it.ID, Status: agent.SubRunning, Description: promptLine(it.Prompt)}
	if it.Model != nil {
		s.Type = *it.Model
	}
	t.subs[it.ID] = s
	return []agent.Event{t.subEvent(*s, raw)}
}

// collabCompleted handles a collabAgentToolCall item that ended: a spawn
// learns its thread, and any call updates the states of its targets.
func (t *translator) collabCompleted(it threadItem, raw rawLine) []agent.Event {
	var out []agent.Event
	if it.Tool == collabSpawn {
		if t.subs[it.ID] == nil {
			out = append(out, t.collabStarted(it, raw)...)
		}
		if it.Status == "failed" {
			return append(out, t.endSub(it.ID, agent.SubFailed, "", "the spawn failed", raw)...)
		}
		for _, th := range it.ReceiverThreadIDs {
			t.child[th] = it.ID
		}
	}
	for th, st := range it.AgentsStates {
		id := t.child[th]
		if id == "" {
			continue
		}
		msg := ""
		if st.Message != nil {
			msg = *st.Message
		}
		switch st.Status {
		case "completed", "shutdown":
			out = append(out, t.endSub(id, agent.SubDone, msg, "", raw)...)
		case "errored":
			out = append(out, t.endSub(id, agent.SubFailed, "", msg, raw)...)
		case "notFound":
			out = append(out, t.endSub(id, agent.SubStopped, "", msg, raw)...)
		}
	}
	return out
}

// endSub ends a running subagent, once.
func (t *translator) endSub(id string, status agent.SubStatus, result, failure string, raw rawLine) []agent.Event {
	s := t.subs[id]
	if s == nil || s.Status != agent.SubRunning {
		return nil
	}
	s.Status, s.Result, s.Error = status, clipText(result), clipText(failure)
	return []agent.Event{t.subEvent(*s, raw)}
}

func (t *translator) subEvent(s agent.Subagent, raw rawLine) agent.Event {
	e := t.ev(agent.KindSubagent, raw)
	e.Subagent = &s
	return e
}

// childOf is the subagent whose thread threadID is, "" for the agent's own.
func (t *translator) childOf(threadID string) string {
	if threadID == "" {
		return ""
	}
	return t.child[threadID]
}

// promptLine is the first line of a spawn's prompt, as its description.
func promptLine(p *string) string {
	if p == nil {
		return ""
	}
	s := strings.TrimSpace(*p)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if r := []rune(s); len(r) > 80 {
		s = string(r[:79]) + "…"
	}
	return s
}

func clipText(s string) string {
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > 600 {
		s = string(r[:600]) + "…"
	}
	return s
}
