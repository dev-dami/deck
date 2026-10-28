package workspace

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/dev-dami/deck/internal/deck/highlights"
	decks "github.com/dev-dami/deck/internal/deck/session"
	"github.com/dev-dami/deck/internal/message"
	pb "github.com/dev-dami/deck/pkg/rpc/deckv1"
	"github.com/stretchr/testify/require"
)

func TestHighlightPinValidatesSourceAndPersistsOriginalAuthor(t *testing.T) {
	w, a, sid := newAskWorkspace(t)
	w.path = filepath.Join(t.TempDir(), "deck-threads.json")
	msg, err := a.Messages.Create(t.Context(), sid, message.CreateMessageParams{Role: message.Assistant, Model: "claude", Parts: []message.ContentPart{message.TextContent{Text: "The important result"}}})
	require.NoError(t, err)
	sub := w.SubscribeDeck(t.Context())
	req := &pb.HighlightActionRequest{SessionId: sid, Action: "pin", Title: "My useful excerpt", Source: &pb.HighlightSource{MessageId: msg.ID}}
	item, err := w.HighlightAction(t.Context(), req)
	require.NoError(t, err)
	require.Equal(t, "claude", item.Agent)
	require.Equal(t, msg.ID, item.Source.MessageId)
	select {
	case event := <-sub:
		require.Equal(t, ChangeHighlight, event.Payload.Kind)
		require.Equal(t, item.Id, event.Payload.Highlight.Id)
	case <-time.After(time.Second):
		t.Fatal("highlight event missing")
	}
	again, err := w.HighlightAction(t.Context(), req)
	require.NoError(t, err)
	require.Equal(t, item.Id, again.Id)
	other, err := a.Sessions.Create(t.Context(), "another room")
	require.NoError(t, err)
	req.SessionId = other.ID
	_, err = w.HighlightAction(t.Context(), req)
	require.ErrorIs(t, err, ErrNotFound)
	req.SessionId = sid
	req.Source.TurnId = "forged"
	_, err = w.HighlightAction(t.Context(), req)
	require.ErrorIs(t, err, highlights.ErrInvalid)
	user, err := a.Messages.Create(t.Context(), sid, message.CreateMessageParams{Role: message.User})
	require.NoError(t, err)
	req.Source = &pb.HighlightSource{MessageId: user.ID}
	_, err = w.HighlightAction(t.Context(), req)
	require.ErrorIs(t, err, highlights.ErrInvalid)
	disk, err := highlights.New(filepath.Join(filepath.Dir(w.path), "highlights"), nil).Snapshot(sid)
	require.NoError(t, err)
	require.Len(t, disk.Items, 1)
	require.Equal(t, "My useful excerpt", disk.Items[0].Title)
}

func TestHighlightAgentSourceStampTaskValidationAndSummary(t *testing.T) {
	w, a, sid := newAskWorkspace(t)
	tid, err := w.mgr().NewThread(t.TempDir())
	require.NoError(t, err)
	w.threads[sid] = tid
	w.back[tid] = sid
	task, err := w.mgr().CreateTask(tid, decks.TaskSpec{Title: "A task"})
	require.NoError(t, err)
	raw := json.RawMessage(`{"key":"k","kind":"needs_you","title":"Choose an option","source":{"turn_id":"forged"}}`)
	text, err := w.runHighlights(t.Context(), tid, "codex", "actual-turn", "highlight.add", raw)
	require.NoError(t, err)
	var item pb.HighlightItem
	require.NoError(t, json.Unmarshal([]byte(text), &item))
	require.Equal(t, "codex", item.Agent)
	require.Equal(t, "actual-turn", item.Source.TurnId)
	_, err = w.runHighlights(t.Context(), tid, "claude", "other-turn", "highlight.resolve", json.RawMessage(`{"id":"`+item.Id+`"}`))
	require.ErrorIs(t, err, highlights.ErrForbidden)
	msg, err := a.Messages.Create(t.Context(), sid, message.CreateMessageParams{Role: message.Assistant, Model: "codex"})
	require.NoError(t, err)
	w.bindHighlightTurn(sid, "codex", "actual-turn", msg.ID)
	text, err = w.runHighlights(t.Context(), tid, "codex", "actual-turn", "summary.update", json.RawMessage(`{"text":"Waiting on a decision"}`))
	require.NoError(t, err)
	var summary pb.AgentSummary
	require.NoError(t, json.Unmarshal([]byte(text), &summary))
	require.Equal(t, msg.ID, summary.Source.MessageId)
	_, err = w.runHighlights(t.Context(), tid, "codex", "actual-turn", "highlight.update", json.RawMessage(`{"id":"`+item.Id+`","source":{"task_id":"`+task.ID+`"}}`))
	require.NoError(t, err)
	_, err = w.runHighlights(t.Context(), tid, "codex", "actual-turn", "highlight.add", json.RawMessage(`{"key":"bad","title":"Bad task source","source":{"task_id":"missing"}}`))
	require.ErrorIs(t, err, ErrNotFound)
	list, err := w.Highlights(t.Context(), sid)
	require.NoError(t, err)
	require.Len(t, list.Items, 1)
	require.Len(t, list.Summaries, 1)
	require.Equal(t, task.ID, list.Items[0].Source.TaskId)
}

func TestHighlightsDeleteRoomRemovesPersistence(t *testing.T) {
	w, a, sid := newAskWorkspace(t)
	w.path = filepath.Join(t.TempDir(), "deck-threads.json")
	msg, err := a.Messages.Create(t.Context(), sid, message.CreateMessageParams{Role: message.Assistant, Model: "codex"})
	require.NoError(t, err)
	_, err = w.HighlightAction(t.Context(), &pb.HighlightActionRequest{SessionId: sid, Action: "pin", Title: "Pinned", Source: &pb.HighlightSource{MessageId: msg.ID}})
	require.NoError(t, err)
	require.NoError(t, w.DeleteSession(t.Context(), sid))
	_, err = w.Highlights(t.Context(), sid)
	require.ErrorIs(t, err, ErrNotFound)
	disk, err := highlights.New(filepath.Join(filepath.Dir(w.path), "highlights"), nil).Snapshot(sid)
	require.NoError(t, err)
	require.Empty(t, disk.Items)
}
