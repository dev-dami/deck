package workspace

import (
	"context"
	"log/slog"
	"sort"
	"time"

	deckagent "github.com/dev-dami/deck/internal/deck/agent"
	decks "github.com/dev-dami/deck/internal/deck/session"
	"github.com/dev-dami/deck/internal/message"
)

// A member's CLI-native subagents (session/subagents.go) in the session:
// each one's work is a run of messages of its own, under its lane id
// ("claude/review-pr-296") as the message's agent, so the conversation can
// fold it into one block and a lane can show it alone; its calls in flight
// count as its own in the activity report, and SubAgents lists them for the
// agents tree.

// onSubEvent stores an event of a subagent's work, or its start or end.
func (w *DeckWorkspace) onSubEvent(sid string, ev deckagent.Event) {
	s := ev.Subagent
	if s == nil || s.Name == "" {
		return
	}
	key := decks.SubPath(ev.AgentID, s.Name)
	kind := ev.AgentID
	if a, ok := w.mgr().Agent(ev.AgentID); ok {
		kind = a.Kind
	}
	// Its calls in flight are its own; they still go when its member's
	// turn or run ends.
	w.act.observe(sid, agentRef{id: ev.AgentID, name: key, kind: kind}, ev, time.Now())
	ctx := context.Background()
	t := w.turns[key]
	if ev.Kind == deckagent.KindSubagent {
		if s.Status == deckagent.SubRunning {
			if t == nil {
				w.publish(DeckChange{Kind: ChangeRoster, SessionID: sid})
			}
			return
		}
		w.act.dropTools(sid, key)
		if t != nil && t.open {
			w.dbw.wait(sid)
			if s.Status == deckagent.SubFailed {
				t.msg.AddFinish(message.FinishReasonError, "Error", s.Error)
			} else {
				t.msg.AddFinish(message.FinishReasonEndTurn, "", "")
			}
			_ = w.app.Messages.Update(ctx, t.msg)
		}
		delete(w.turns, key)
		w.publish(DeckChange{Kind: ChangeRoster, SessionID: sid})
		return
	}
	if t == nil {
		t = &turn{tools: map[string]bool{}, diffs: map[string]deckagent.FileDiff{}}
		w.turns[key] = t
	}
	open := func() bool {
		if t.open {
			return true
		}
		w.dbw.wait(sid) // rows keep the order of the events
		m, err := w.app.Messages.Create(ctx, sid, message.CreateMessageParams{Role: message.Assistant, Model: key, Provider: kind})
		if err != nil {
			slog.Error("deck: create subagent message", "error", err)
			return false
		}
		t.msg, t.open = m, true
		return true
	}
	// Text after a call starts a new message, as in a member's turn.
	fresh := func() bool {
		if t.open && t.msg.HasToolCalls() {
			t.msg.AddFinish(message.FinishReasonToolUse, "", "")
			_ = w.app.Messages.Update(ctx, t.msg)
			t.open = false
		}
		return open()
	}
	switch ev.Kind {
	case deckagent.KindAssistantText:
		if ev.Delta || !fresh() {
			return // a subagent's text comes whole
		}
		setText(&t.msg, ev.Text, false)
		_ = w.app.Messages.Update(ctx, t.msg)
	case deckagent.KindReasoning:
		if ev.Delta || !fresh() {
			return
		}
		t.msg.AppendReasoningContent(ev.Text)
		t.msg.FinishThinking()
		_ = w.app.Messages.Update(ctx, t.msg)
	case deckagent.KindToolCall:
		if ev.Tool == nil || !open() {
			return
		}
		name, input := deckTool(ev.Tool)
		t.msg.AddToolCall(message.ToolCall{ID: ev.Tool.ID, Name: name, Input: input, Finished: true})
		t.tools[ev.Tool.ID] = true
		_ = w.app.Messages.Update(ctx, t.msg)
	case deckagent.KindFileChange:
		if ev.Diff != nil {
			t.diffs[ev.Diff.Path] = *ev.Diff
		}
	case deckagent.KindToolResult:
		if ev.Tool == nil {
			return
		}
		name, input := deckTool(ev.Tool)
		if !t.tools[ev.Tool.ID] && open() {
			t.msg.AddToolCall(message.ToolCall{ID: ev.Tool.ID, Name: name, Input: input, Finished: true})
			t.tools[ev.Tool.ID] = true
			_ = w.app.Messages.Update(ctx, t.msg)
		}
		res := t.resultAs(ev.Tool, name, input)
		w.dbw.do(sid, func() {
			_, err := w.app.Messages.Create(context.Background(), sid, message.CreateMessageParams{
				Role:  message.Tool,
				Parts: []message.ContentPart{res},
			})
			if err != nil {
				slog.Error("deck: subagent tool result", "error", err)
			}
		})
	}
}

// SubAgentLine is one CLI-native subagent as the agents tree shows it.
type SubAgentLine struct {
	// Name is its lane id, "claude/review-pr-296"; Parent its member's
	// name in the roster and ParentID that member's id.
	Name, Parent, ParentID string
	Kind                   string // its member's kind
	Type, Description      string
	// Status is running, done, failed or stopped; Error why it failed.
	Status, Error  string
	Started, Ended time.Time
	Actions        int
	ToolID         string // the call that started it
}

// SubAgents lists the session's CLI-native subagents: per member, those
// running and the newest that ended, at most keepSubs of them, oldest first.
type SubAgents interface {
	SubAgents(sessionID string) []SubAgentLine
}

var _ SubAgents = (*DeckWorkspace)(nil)

// keepSubs is how many of a member's subagents the tree shows.
const keepSubs = 8

// SubAgents implements [SubAgents].
func (w *DeckWorkspace) SubAgents(sessionID string) []SubAgentLine {
	th, ok := w.threadOf(sessionID)
	if !ok {
		return nil
	}
	var out []SubAgentLine
	for _, a := range th.Members {
		var lines []SubAgentLine
		for i := len(a.Subs) - 1; i >= 0; i-- {
			s := a.Subs[i]
			if len(lines) >= keepSubs && s.Status != string(deckagent.SubRunning) {
				continue
			}
			lines = append(lines, SubAgentLine{
				Name: decks.SubPath(a.ID, s.Name), Parent: a.Name, ParentID: a.ID, Kind: a.Kind,
				Type: s.Type, Description: s.Description, Status: s.Status, Error: s.Error,
				Started: s.Started, Ended: s.Ended, Actions: s.Actions, ToolID: s.ID,
			})
		}
		sort.SliceStable(lines, func(i, j int) bool { return lines[i].Started.Before(lines[j].Started) })
		out = append(out, lines...)
	}
	return out
}
