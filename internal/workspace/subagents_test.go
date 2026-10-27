package workspace

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	deckagent "github.com/dev-dami/deck/internal/deck/agent"
	"github.com/dev-dami/deck/internal/message"
)

// TestSubagentWorkIsItsOwnRun: a CLI-native subagent's work is stored as
// messages under its lane id, apart from its member's turn; its calls in
// flight are its own; when it ends its message is finished.
func TestSubagentWorkIsItsOwnRun(t *testing.T) {
	w := roomWorkspace(t)
	w.turns = map[string]*turn{}
	w.dbw = newDBWriter()
	ctx := context.Background()
	s, err := w.app.Sessions.Create(ctx, "reviews")
	require.NoError(t, err)
	tid, err := w.mgr().NewThread(t.TempDir())
	require.NoError(t, err)
	w.threads[s.ID], w.back[tid] = tid, s.ID
	_, err = w.mgr().AddMember(tid, "claude", "")
	require.NoError(t, err)

	sub := &deckagent.Subagent{ID: "toolu_b", Name: "review-pr-296", Description: "Review PR #296", Status: deckagent.SubRunning}
	ev := func(k deckagent.Kind, f func(*deckagent.Event)) {
		e := deckagent.Event{AgentID: "claude", Time: time.Now(), Kind: k, Sub: "toolu_b", Subagent: sub}
		if k == deckagent.KindSubagent {
			e.Sub = ""
		}
		if f != nil {
			f(&e)
		}
		w.onEvent(tid, e)
	}
	ev(deckagent.KindSubagent, nil)
	ev(deckagent.KindAssistantText, func(e *deckagent.Event) { e.Text = "Looking at the diff." })
	ev(deckagent.KindToolCall, func(e *deckagent.Event) {
		e.Tool = &deckagent.Tool{ID: "b1", Name: "Bash", Title: "gh pr diff 296", Input: raw(`{"command":"gh pr diff 296"}`), Status: deckagent.ToolRunning}
	})
	act := w.Activity(s.ID)
	require.Len(t, act.Tools, 1)
	require.Equal(t, "claude/review-pr-296", act.Tools[0].AgentName)
	require.Equal(t, "claude", act.Tools[0].AgentID)

	ev(deckagent.KindToolResult, func(e *deckagent.Event) {
		e.Tool = &deckagent.Tool{ID: "b1", Name: "Bash", Status: deckagent.ToolError, Output: "rate limit"}
	})
	failed := *sub
	failed.Status, failed.Error = deckagent.SubFailed, "API error"
	sub = &failed
	ev(deckagent.KindSubagent, nil)
	require.True(t, w.dbw.flush(5*time.Second))

	msgs, err := w.app.Messages.List(ctx, s.ID)
	require.NoError(t, err)
	var own []message.Message
	results := 0
	for _, m := range msgs {
		switch m.Role {
		case message.Assistant:
			own = append(own, m)
		case message.Tool:
			results++
		}
	}
	require.Len(t, own, 1)
	m := own[0]
	require.Equal(t, "claude/review-pr-296", m.Model)
	require.Equal(t, "claude", m.Provider)
	require.Equal(t, "Looking at the diff.", m.Content().Text)
	require.Len(t, m.ToolCalls(), 1)
	require.True(t, m.IsFinished())
	require.Equal(t, message.FinishReasonError, m.FinishReason())
	require.Equal(t, 1, results)
	require.Empty(t, w.Activity(s.ID).Tools)
	_, open := w.turns["claude/review-pr-296"]
	require.False(t, open)
}
