package remote

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	deckagent "github.com/dev-dami/deck/internal/deck/agent"
	decks "github.com/dev-dami/deck/internal/deck/session"
	"github.com/dev-dami/deck/internal/message"
	"github.com/dev-dami/deck/internal/permission"
	"github.com/dev-dami/deck/internal/pubsub"
	"github.com/dev-dami/deck/internal/remote/wire"
	"github.com/dev-dami/deck/internal/session"
	"github.com/dev-dami/deck/internal/workspace"
)

// stubWS implements only the Workspace methods the adapter's event path uses;
// calling anything else panics through the nil embedded interface.
type stubWS struct {
	workspace.Workspace
	busy     map[string]bool
	sessions map[string]session.Session
	granted  []string
	resolved bool
}

func (s *stubWS) AgentIsSessionBusy(id string) bool { return s.busy[id] }
func (s *stubWS) GetSession(_ context.Context, id string) (session.Session, error) {
	if v, ok := s.sessions[id]; ok {
		return v, nil
	}
	return session.Session{}, context.Canceled
}
func (s *stubWS) PermissionGrant(p permission.PermissionRequest) bool {
	s.granted = append(s.granted, "grant:"+p.ID)
	return s.resolved
}
func (s *stubWS) PermissionGrantPersistent(p permission.PermissionRequest) bool {
	s.granted = append(s.granted, "persist:"+p.ID)
	return s.resolved
}
func (s *stubWS) PermissionDeny(p permission.PermissionRequest) bool {
	s.granted = append(s.granted, "deny:"+p.ID)
	return s.resolved
}

type stubRC struct {
	workspace.RemoteControl
	editable   map[string]bool
	editedRows map[string]bool
	edited     []string
	editErr    error
}

func (r stubRC) EditableMessage(id string) bool { return r.editable[id] }
func (r stubRC) EditedMessage(id string) bool   { return r.editedRows[id] }
func (r *stubRC) EditMessage(_ context.Context, sid, mid, text string) error {
	r.edited = append(r.edited, sid+"/"+mid+"/"+text)
	return r.editErr
}

func newAdapter(ws *stubWS) *WorkspaceBackend {
	return &WorkspaceBackend{ws: ws, rc: &stubRC{}, waiting: map[string]pending{}}
}

type sink struct{ got []Event }

func (s *sink) emit(e Event) { s.got = append(s.got, e) }

func TestStreamingRowsAreCoalescedUntilFlushed(t *testing.T) {
	b := newAdapter(&stubWS{})
	dirty := map[string]message.Message{}
	var out sink

	mk := func(text string, finished bool) message.Message {
		m := message.Message{ID: "m1", SessionID: "s1", Role: message.Assistant, Parts: []message.ContentPart{message.TextContent{Text: text}}}
		if finished {
			m.Parts = append(m.Parts, message.Finish{Reason: message.FinishReasonEndTurn})
		}
		return m
	}
	b.onApp(pubsub.Event[message.Message]{Type: pubsub.CreatedEvent, Payload: mk("h", false)}, dirty, out.emit)
	require.Len(t, out.got, 1, "the first row goes out at once")
	require.Equal(t, wire.EvMessageCreated, out.got[0].Name)
	require.True(t, out.got[0].Scoped)

	for _, text := range []string{"he", "hel", "hell"} {
		b.onApp(pubsub.Event[message.Message]{Type: pubsub.UpdatedEvent, Payload: mk(text, false)}, dirty, out.emit)
	}
	require.Len(t, out.got, 1, "deltas wait for the flush")
	require.Contains(t, dirty, "m1")
	held := dirty["m1"]
	require.Equal(t, "hell", held.Content().Text, "only the latest snapshot is kept")

	b.onApp(pubsub.Event[message.Message]{Type: pubsub.UpdatedEvent, Payload: mk("hello", true)}, dirty, out.emit)
	require.Len(t, out.got, 2, "a finished row goes out at once")
	require.Empty(t, dirty, "and supersedes the pending snapshot")
	require.True(t, out.got[1].Payload.(wire.Message).Finished)
}

func TestUserRowsAreNeverDelayed(t *testing.T) {
	b := newAdapter(&stubWS{})
	var out sink
	b.onApp(pubsub.Event[message.Message]{Type: pubsub.UpdatedEvent,
		Payload: message.Message{ID: "u", SessionID: "s1", Role: message.User}}, map[string]message.Message{}, out.emit)
	require.Len(t, out.got, 1)
}

func TestDeletedRow(t *testing.T) {
	b := newAdapter(&stubWS{})
	dirty := map[string]message.Message{"m1": {ID: "m1"}}
	var out sink
	b.onApp(pubsub.Event[message.Message]{Type: pubsub.DeletedEvent, Payload: message.Message{ID: "m1", SessionID: "s1"}}, dirty, out.emit)
	require.Empty(t, dirty)
	require.Equal(t, wire.EvMessageDeleted, out.got[0].Name)
}

func TestSessionEventsSkipTaskSessions(t *testing.T) {
	ws := &stubWS{busy: map[string]bool{"s1": true}}
	b := newAdapter(ws)
	var out sink
	b.onApp(pubsub.Event[session.Session]{Type: pubsub.UpdatedEvent, Payload: session.Session{ID: "child", ParentSessionID: "s1"}}, nil, out.emit)
	require.Empty(t, out.got)

	b.onApp(pubsub.Event[session.Session]{Type: pubsub.CreatedEvent, Payload: session.Session{ID: "s1", Title: "T"}}, nil, out.emit)
	require.Equal(t, wire.EvSessionCreated, out.got[0].Name)
	got := out.got[0].Payload.(wire.Session)
	require.True(t, got.Busy)
	require.Equal(t, "T", got.Title)
}

func TestApprovalLifecycle(t *testing.T) {
	ws := &stubWS{sessions: map[string]session.Session{"s1": {ID: "s1"}}, resolved: true}
	b := newAdapter(ws)
	var out sink
	req := permission.PermissionRequest{ID: "p1", SessionID: "s1", ToolCallID: "c1", Description: "run"}

	b.onApp(pubsub.Event[permission.PermissionRequest]{Type: pubsub.CreatedEvent, Payload: req}, nil, out.emit)
	require.Equal(t, wire.EvApprovalRequested, out.got[0].Name)
	require.Len(t, b.Approvals(), 1)
	require.Equal(t, 1, out.got[1].Payload.(wire.Session).Pending, "the session summary shows the pending count")

	resolved, err := b.Answer("p1", wire.DecisionAllowSession)
	require.NoError(t, err)
	require.True(t, resolved)
	require.Equal(t, []string{"persist:p1"}, ws.granted)

	out.got = nil
	b.onApp(pubsub.Event[permission.PermissionNotification]{Type: pubsub.CreatedEvent, Payload: permission.PermissionNotification{ToolCallID: "c1", Granted: true}}, nil, out.emit)
	require.Equal(t, wire.EvApprovalResolved, out.got[0].Name)
	require.Empty(t, b.Approvals(), "answered elsewhere (the TUI) clears it here too")
	require.Equal(t, 0, out.got[1].Payload.(wire.Session).Pending)
}

func TestAnswerDecisions(t *testing.T) {
	ws := &stubWS{resolved: false}
	b := newAdapter(ws)
	b.waiting["p1"] = pending{req: permission.PermissionRequest{ID: "p1"}}

	for decision, want := range map[string]string{wire.DecisionAllow: "grant:p1", wire.DecisionDeny: "deny:p1"} {
		ws.granted = nil
		resolved, err := b.Answer("p1", decision)
		require.NoError(t, err)
		require.False(t, resolved, "another UI already answered")
		require.Equal(t, []string{want}, ws.granted)
	}
	_, err := b.Answer("p1", "maybe")
	require.Error(t, err)
	_, err = b.Answer("missing", wire.DecisionAllow)
	require.ErrorIs(t, err, ErrNotFound)
}

func TestApprovalsAreOrdered(t *testing.T) {
	b := newAdapter(&stubWS{})
	b.waiting["b"] = pending{req: permission.PermissionRequest{ID: "b"}, at: 20}
	b.waiting["a"] = pending{req: permission.PermissionRequest{ID: "a"}, at: 10}
	got := b.Approvals()
	require.Equal(t, "a", got[0].ID)
	require.Equal(t, "b", got[1].ID)
}

func TestDeckChangesBecomeEvents(t *testing.T) {
	ws := &stubWS{sessions: map[string]session.Session{"s1": {ID: "s1"}}}
	b := newAdapter(ws)
	var out sink

	b.onDeck(workspace.DeckChange{Kind: workspace.ChangeMode, Mode: "plan"}, out.emit)
	require.Equal(t, Event{Name: wire.EvModeChanged, Payload: wire.ModeBody{Mode: "plan"}}, out.got[0])

	out.got = nil
	b.onDeck(workspace.DeckChange{Kind: workspace.ChangeQueue, SessionID: "s1", Queue: []decks.QueuedItem{{ID: "q", AgentID: "claude", Text: "next"}}}, out.emit)
	items := out.got[0].Payload.(map[string]any)["items"].([]wire.QueuedItem)
	require.Equal(t, "next", items[0].Text)

	out.got = nil
	b.onDeck(workspace.DeckChange{Kind: workspace.ChangeBusy, SessionID: "s1", Busy: true}, out.emit)
	require.Equal(t, wire.EvBusy, out.got[0].Name)
	require.Equal(t, wire.EvSessionUpdated, out.got[1].Name)

	out.got = nil
	b.onDeck(workspace.DeckChange{Kind: workspace.ChangeNotice, SessionID: "s1", Text: "joined"}, out.emit)
	require.Equal(t, wire.EvNotice, out.got[0].Name)
	b.onDeck(workspace.DeckChange{Kind: workspace.ChangeRoster, SessionID: "s1"}, out.emit)
	require.Equal(t, wire.EvRosterChanged, out.got[1].Name)
}

func TestUnifiedDiffText(t *testing.T) {
	d := deckagent.FileDiff{Path: "a.go", Hunks: []deckagent.Hunk{{
		OldStart: 1, OldLines: 2, NewStart: 1, NewLines: 2,
		Lines: []deckagent.DiffLine{
			{Op: deckagent.OpContext, Text: "keep\n"},
			{Op: deckagent.OpDel, Text: "old"},
			{Op: deckagent.OpAdd, Text: "new\n"},
		},
	}}}
	require.Equal(t, "@@ -1,2 +1,2 @@\n keep\n-old\n+new\n", unified(d))
}

func TestNewWorkspaceBackendRejectsForeignWorkspace(t *testing.T) {
	_, err := NewWorkspaceBackend(&stubWS{}, Info{})
	require.Error(t, err)
}

func TestBusyStatus(t *testing.T) {
	for s, want := range map[string]bool{"working": true, "tool": true, "approval": true, "idle": false, "ready": false, "error": false} {
		require.Equal(t, want, busyStatus(s), s)
	}
}

func TestEditableRowsAreMarked(t *testing.T) {
	rc := &stubRC{editable: map[string]bool{"u1": true}}
	b := &WorkspaceBackend{ws: &stubWS{}, rc: rc, waiting: map[string]pending{}}

	require.True(t, b.decorate(wire.Message{ID: "u1", Role: wire.RoleUser}).Editable)
	require.False(t, b.decorate(wire.Message{ID: "u2", Role: wire.RoleUser}).Editable, "no log entry recorded for it")
	require.False(t, b.decorate(wire.Message{ID: "u1", Role: wire.RoleAssistant}).Editable, "agents' rows are never editable")

	rc.editedRows = map[string]bool{"u1": true}
	require.True(t, b.decorate(wire.Message{ID: "u1", Role: wire.RoleUser}).Edited)
	require.False(t, b.decorate(wire.Message{ID: "u2", Role: wire.RoleUser}).Edited)

	var out sink
	b.onApp(pubsub.Event[message.Message]{Type: pubsub.CreatedEvent,
		Payload: message.Message{ID: "u1", SessionID: "s1", Role: message.User}}, map[string]message.Message{}, out.emit)
	require.True(t, out.got[0].Payload.(wire.Message).Editable, "live events carry the flag too")
}

func TestEditMessageMapsNotEditableToInvalid(t *testing.T) {
	rc := &stubRC{editErr: workspace.ErrNotEditable}
	b := &WorkspaceBackend{ws: &stubWS{}, rc: rc, waiting: map[string]pending{}}
	err := b.EditMessage(t.Context(), "s1", "m1", "x")
	require.ErrorIs(t, err, ErrInvalid)
	require.Equal(t, []string{"s1/m1/x"}, rc.edited)

	rc.editErr = errors.New("disk full")
	err = b.EditMessage(t.Context(), "s1", "m1", "x")
	require.NotErrorIs(t, err, ErrInvalid)
	require.ErrorContains(t, err, "disk full")
}
