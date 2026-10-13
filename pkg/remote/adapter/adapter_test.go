package adapter

import (
	"context"
	"errors"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/require"

	decks "github.com/dev-dami/deck/internal/deck/session"
	"github.com/dev-dami/deck/internal/message"
	"github.com/dev-dami/deck/internal/permission"
	"github.com/dev-dami/deck/internal/pubsub"
	"github.com/dev-dami/deck/internal/session"
	tools "github.com/dev-dami/deck/internal/toolspec"
	"github.com/dev-dami/deck/internal/workspace"
	"github.com/dev-dami/deck/pkg/remote"
	"github.com/dev-dami/deck/pkg/remote/wire"
)

// fakeCore is the slice of the workspace the adapter reads and writes.
type fakeCore struct {
	sessions map[string]session.Session
	messages map[string][]message.Message
	busy     map[string]bool
	granted  []string
	resolved bool
	cancel   []string
}

func newCore() *fakeCore {
	return &fakeCore{
		sessions: map[string]session.Session{"s1": {ID: "s1", Title: "One", UpdatedAt: 100}, "s2": {ID: "s2", Title: "Two", UpdatedAt: 200}, "task": {ID: "task", ParentSessionID: "s1"}},
		messages: map[string][]message.Message{},
		busy:     map[string]bool{},
		resolved: true,
	}
}

func (c *fakeCore) CreateSession(_ context.Context, title string) (session.Session, error) {
	s := session.Session{ID: "new", Title: title}
	c.sessions[s.ID] = s
	return s, nil
}

func (c *fakeCore) GetSession(_ context.Context, id string) (session.Session, error) {
	if s, ok := c.sessions[id]; ok {
		return s, nil
	}
	return session.Session{}, errors.New("no such session")
}

func (c *fakeCore) ListSessions(context.Context) ([]session.Session, error) {
	var out []session.Session
	for _, s := range c.sessions {
		out = append(out, s)
	}
	return out, nil
}

func (c *fakeCore) SaveSession(_ context.Context, s session.Session) (session.Session, error) {
	c.sessions[s.ID] = s
	return s, nil
}

func (c *fakeCore) DeleteSession(_ context.Context, id string) error {
	delete(c.sessions, id)
	return nil
}

func (c *fakeCore) ListMessages(_ context.Context, sid string) ([]message.Message, error) {
	return append([]message.Message(nil), c.messages[sid]...), nil
}

func (c *fakeCore) AgentCancel(sid string)             { c.cancel = append(c.cancel, sid) }
func (c *fakeCore) AgentIsSessionBusy(sid string) bool { return c.busy[sid] }
func (c *fakeCore) PermissionGrant(p permission.PermissionRequest) bool {
	c.granted = append(c.granted, "grant:"+p.ID)
	return c.resolved
}

func (c *fakeCore) PermissionGrantPersistent(p permission.PermissionRequest) bool {
	c.granted = append(c.granted, "persist:"+p.ID)
	return c.resolved
}

func (c *fakeCore) PermissionDeny(p permission.PermissionRequest) bool {
	c.granted = append(c.granted, "deny:"+p.ID)
	return c.resolved
}
func (c *fakeCore) WorkingDir() string { return "/work" }

// fakeRC is the RemoteControl of a workspace with one queued item and one
// editable message in s1.
type fakeRC struct {
	modes    map[string]string
	queue    map[string][]decks.QueuedItem
	editable map[string]string // message id -> session
	calls    []string
	changes  chan pubsub.Event[workspace.DeckChange]
	theme    workspace.Theme
}

func newRC() *fakeRC {
	return &fakeRC{
		modes:    map[string]string{"s1": "ask"},
		queue:    map[string][]decks.QueuedItem{"s1": {{ID: "q1", AgentID: "claude1", Text: "later"}}},
		editable: map[string]string{"m1": "s1"},
		changes:  make(chan pubsub.Event[workspace.DeckChange], 16),
		theme:    workspace.Theme{Palette: map[string]string{"primary": "#111111"}, Kinds: map[string]workspace.KindTone{"claude": {Tint: "#d97757", Mark: "✻"}}},
	}
}

func (r *fakeRC) RemoteMode(sid string) string { return r.modes[sid] }
func (r *fakeRC) SetRemoteMode(sid, mode string) error {
	r.modes[sid] = mode
	return nil
}
func (r *fakeRC) QueueItems(sid string) []decks.QueuedItem { return r.queue[sid] }
func (r *fakeRC) has(sid, item string) bool {
	for _, q := range r.queue[sid] {
		if q.ID == item {
			return true
		}
	}
	return false
}

func (r *fakeRC) SteerQueued(sid, item string) error {
	if !r.has(sid, item) {
		return workspace.ErrNotFound
	}
	r.calls = append(r.calls, "steer:"+item)
	return nil
}

func (r *fakeRC) CancelQueued(sid, item string) error {
	if !r.has(sid, item) {
		return workspace.ErrNotFound
	}
	r.calls = append(r.calls, "cancel:"+item)
	return nil
}

func (r *fakeRC) InterruptAgent(sid, agent string) error {
	r.calls = append(r.calls, "interrupt:"+sid+"/"+agent)
	return nil
}

func (r *fakeRC) ContinueThread(sid string) error {
	r.calls = append(r.calls, "continue:"+sid)
	return nil
}

func (r *fakeRC) ChangedFiles(context.Context, string) (decks.ChangeSet, error) {
	return decks.ChangeSet{Root: "/work", Files: []decks.ChangedFile{{FileDiff: changedDiff()}}}, nil
}

func (r *fakeRC) PostMessage(_ context.Context, sid, prompt string, atts ...message.Attachment) (string, error) {
	r.calls = append(r.calls, "post:"+sid+"/"+prompt)
	return "row1", nil
}

func (r *fakeRC) EditableMessage(sid, mid string) bool { return r.editable[mid] == sid }
func (r *fakeRC) EditMessage(_ context.Context, sid, mid, text string) error {
	if r.editable[mid] != sid {
		return workspace.ErrNotEditable
	}
	r.calls = append(r.calls, "edit:"+mid+"="+text)
	return nil
}

func (r *fakeRC) SubscribeDeck(context.Context) <-chan pubsub.Event[workspace.DeckChange] {
	return r.changes
}
func (r *fakeRC) Theme() workspace.Theme { return r.theme }

type fakeRoster struct{}

func (fakeRoster) Roster(sid string) []workspace.AgentLine {
	if sid == "" {
		return []workspace.AgentLine{{ID: "claude", Kind: "claude", Status: "ready"}}
	}
	return []workspace.AgentLine{{ID: "claude1", Kind: "claude", Model: "opus", Status: "working"}}
}

type rig struct {
	core   *fakeCore
	rc     *fakeRC
	app    chan pubsub.Event[tea.Msg]
	b      *Backend
	events <-chan remote.Event
	cancel context.CancelFunc
}

func newRig(t *testing.T) *rig {
	t.Helper()
	r := &rig{core: newCore(), rc: newRC(), app: make(chan pubsub.Event[tea.Msg], 16)}
	events := func(context.Context) <-chan pubsub.Event[tea.Msg] { return r.app }
	r.b = NewWith(r.core, r.rc, nil, fakeRoster{}, events, "v", "proj")
	r.b.themePoll = 20 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	r.cancel = cancel
	t.Cleanup(cancel)
	r.events = r.b.Events(ctx)
	return r
}

// next reads the next event, failing after a second.
func (r *rig) next(t *testing.T) remote.Event {
	t.Helper()
	select {
	case ev := <-r.events:
		return ev
	case <-time.After(time.Second):
		t.Fatal("no event")
		return remote.Event{}
	}
}

func (r *rig) app_(msg tea.Msg) {
	r.app <- pubsub.Event[tea.Msg]{Type: pubsub.UpdatedEvent, Payload: msg}
}

func TestR10_ForeignIdsAreNotFound(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	require.ErrorIs(t, r.b.Steer("s2", "q1"), remote.ErrNotFound)
	require.ErrorIs(t, r.b.CancelQueued("s2", "q1"), remote.ErrNotFound)
	require.ErrorIs(t, r.b.Steer("ghost", "q1"), remote.ErrNotFound)
	require.ErrorIs(t, r.b.Steer("task", "q1"), remote.ErrNotFound, "task sessions are not addressable")
	require.ErrorIs(t, r.b.EditMessage(ctx, "s2", "m1", "x"), remote.ErrInvalid)
	require.ErrorIs(t, r.b.EditMessage(ctx, "ghost", "m1", "x"), remote.ErrNotFound)
	_, err := r.b.Mode("ghost")
	require.ErrorIs(t, err, remote.ErrNotFound)
	require.ErrorIs(t, r.b.Cancel("ghost"), remote.ErrNotFound)
	_, _, _, err = r.b.Messages(ctx, "ghost", "", 10)
	require.ErrorIs(t, err, remote.ErrNotFound)
	require.Empty(t, r.rc.calls)

	require.NoError(t, r.b.Steer("s1", "q1"))
	require.NoError(t, r.b.EditMessage(ctx, "s1", "m1", "x"))
	mode, err := r.b.Mode("s1")
	require.NoError(t, err)
	require.Equal(t, "ask", mode)
	require.Equal(t, []string{"steer:q1", "edit:m1=x"}, r.rc.calls)
}

func TestApprovalsAreSessionScoped(t *testing.T) {
	r := newRig(t)
	req := permission.PermissionRequest{ID: "p1", SessionID: "s1", ToolCallID: "c1", Description: "claude1 wants to run: ls",
		Params: tools.BashPermissionsParams{Command: "ls -la", Description: "claude1"}}
	r.app_(pubsub.Event[permission.PermissionRequest]{Type: pubsub.CreatedEvent, Payload: req})
	ev := r.next(t)
	require.Equal(t, wire.EvApprovalRequested, ev.Name)
	a := ev.Payload.(wire.Approval)
	require.Equal(t, wire.ApprovalCommand, a.Kind)
	require.Equal(t, "ls -la", a.Command)
	require.Equal(t, "claude1", a.Agent)
	ev = r.next(t)
	require.Equal(t, wire.EvSessionUpdated, ev.Name)
	require.Equal(t, 1, ev.Payload.(wire.Session).Pending)

	_, err := r.b.Answer("s2", "p1", wire.DecisionAllow)
	require.ErrorIs(t, err, remote.ErrNotFound)
	list, err := r.b.Approvals("s2")
	require.NoError(t, err)
	require.Empty(t, list)
	list, err = r.b.Approvals("")
	require.NoError(t, err)
	require.Len(t, list, 1)

	resolved, err := r.b.Answer("s1", "p1", wire.DecisionAllowSession)
	require.NoError(t, err)
	require.True(t, resolved)
	require.Equal(t, []string{"persist:p1"}, r.core.granted)

	// The notification clears the waiting set at once (R14).
	r.app_(pubsub.Event[permission.PermissionNotification]{Type: pubsub.UpdatedEvent, Payload: permission.PermissionNotification{ToolCallID: "c1", Granted: true}})
	ev = r.next(t)
	require.Equal(t, wire.EvApprovalResolved, ev.Name)
	require.Equal(t, wire.ApprovalResolved{ID: "p1", SessionID: "s1", Granted: true}, ev.Payload)
	ev = r.next(t)
	require.Equal(t, 0, ev.Payload.(wire.Session).Pending)
	_, err = r.b.Answer("s1", "p1", wire.DecisionAllow)
	require.ErrorIs(t, err, remote.ErrNotFound)
}

func TestMessagesPageByCursor(t *testing.T) {
	r := newRig(t)
	for i := 0; i < 7; i++ {
		r.core.messages["s1"] = append(r.core.messages["s1"], message.Message{
			ID: "m" + string(rune('0'+i)), SessionID: "s1", Role: message.User, CreatedAt: int64(1000 + i),
			Parts: []message.ContentPart{message.TextContent{Text: "t"}},
		})
	}
	ctx := context.Background()
	page, more, next, err := r.b.Messages(ctx, "s1", "", 3)
	require.NoError(t, err)
	require.Len(t, page, 3)
	require.Equal(t, "m4", page[0].ID)
	require.True(t, more)
	page, more, next, err = r.b.Messages(ctx, "s1", next, 3)
	require.NoError(t, err)
	require.Equal(t, "m1", page[0].ID)
	require.True(t, more)
	require.True(t, page[0].Editable, "m1 is the editable row")
	page, more, _, err = r.b.Messages(ctx, "s1", next, 3)
	require.NoError(t, err)
	require.Len(t, page, 1)
	require.False(t, more)
	_, _, _, err = r.b.Messages(ctx, "s1", "bogus", 3)
	require.ErrorIs(t, err, remote.ErrInvalid)
}

func TestStreamingRowsAreCoalesced(t *testing.T) {
	r := newRig(t)
	mk := func(text string, finished bool) message.Message {
		m := message.Message{ID: "a1", SessionID: "s1", Role: message.Assistant, Model: "claude1", Provider: "claude", Parts: []message.ContentPart{message.TextContent{Text: text}}}
		if finished {
			m.Parts = append(m.Parts, message.Finish{Reason: message.FinishReasonEndTurn})
		}
		return m
	}
	r.app_(pubsub.Event[message.Message]{Type: pubsub.CreatedEvent, Payload: mk("h", false)})
	ev := r.next(t)
	require.Equal(t, wire.EvMessageCreated, ev.Name)
	require.True(t, ev.Scoped)
	require.Equal(t, "claude1", ev.Payload.(wire.Message).Agent)

	for _, text := range []string{"he", "hel", "hell"} {
		r.app_(pubsub.Event[message.Message]{Type: pubsub.UpdatedEvent, Payload: mk(text, false)})
	}
	ev = r.next(t)
	require.Equal(t, wire.EvMessageUpdated, ev.Name, "one flush for three deltas")
	require.Equal(t, "hell", ev.Payload.(wire.Message).Parts[0].Text)
	select {
	case ev := <-r.events:
		t.Fatalf("unexpected event %+v", ev)
	case <-time.After(2 * coalesceEvery):
	}

	r.app_(pubsub.Event[message.Message]{Type: pubsub.UpdatedEvent, Payload: mk("hello", true)})
	ev = r.next(t)
	require.True(t, ev.Payload.(wire.Message).Finished, "a finished row goes out at once")

	r.app_(pubsub.Event[message.Message]{Type: pubsub.DeletedEvent, Payload: mk("", true)})
	ev = r.next(t)
	require.Equal(t, wire.EvMessageDeleted, ev.Name)
	require.Equal(t, wire.MessageDeleted{ID: "a1", SessionID: "s1"}, ev.Payload)
}

func TestDeckChangesAndTheme(t *testing.T) {
	r := newRig(t)
	r.core.busy["s1"] = true
	r.rc.changes <- pubsub.Event[workspace.DeckChange]{Payload: workspace.DeckChange{Kind: workspace.ChangeBusy, SessionID: "s1", Busy: true}}
	ev := r.next(t)
	require.Equal(t, wire.EvBusy, ev.Name)
	require.False(t, ev.Scoped)
	ev = r.next(t)
	require.Equal(t, wire.EvSessionUpdated, ev.Name)
	require.True(t, ev.Payload.(wire.Session).Busy)
	require.Equal(t, "ask", ev.Payload.(wire.Session).Mode)

	r.rc.changes <- pubsub.Event[workspace.DeckChange]{Payload: workspace.DeckChange{Kind: workspace.ChangeQueue, SessionID: "s1", Queue: []decks.QueuedItem{{ID: "q1", AgentID: "claude1", Text: "x"}}}}
	ev = r.next(t)
	require.Equal(t, wire.EvQueueChanged, ev.Name)
	require.True(t, ev.Scoped)
	require.Equal(t, "q1", ev.Payload.(wire.QueueChanged).Items[0].ID)

	r.rc.changes <- pubsub.Event[workspace.DeckChange]{Payload: workspace.DeckChange{Kind: workspace.ChangeMode, SessionID: "s1", Mode: "plan"}}
	ev = r.next(t)
	require.Equal(t, wire.ModeChanged{SessionID: "s1", Mode: "plan"}, ev.Payload)
	r.next(t) // the session summary

	r.rc.changes <- pubsub.Event[workspace.DeckChange]{Payload: workspace.DeckChange{Kind: workspace.ChangeNotice, SessionID: "s1", Text: "hi"}}
	ev = r.next(t)
	require.Equal(t, wire.EvNotice, ev.Name)
	require.True(t, ev.Scoped)

	r.rc.theme = workspace.Theme{Palette: map[string]string{"primary": "#222222"}, Kinds: map[string]workspace.KindTone{}}
	ev = r.next(t)
	require.Equal(t, wire.EvThemeChanged, ev.Name)
	require.Equal(t, "#222222", ev.Payload.(wire.Theme).Palette["primary"])

	info := r.b.Info()
	require.Equal(t, "proj", info.Project)
	require.Equal(t, []string{"claude"}, info.Kinds)
	require.Equal(t, "#222222", info.Theme.Palette["primary"])
}

func TestSessionsAndSend(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	list, err := r.b.Sessions(ctx)
	require.NoError(t, err)
	require.Len(t, list, 2, "task sessions are hidden")
	require.Equal(t, "s2", list[0].ID, "newest first")

	id, err := r.b.Send(ctx, "s1", "hello", []remote.Image{{Name: "a.png", Mime: "image/png", Data: []byte("x")}})
	require.NoError(t, err)
	require.Equal(t, "row1", id)
	require.Equal(t, []string{"post:s1/hello"}, r.rc.calls)

	roster, err := r.b.Roster("s1")
	require.NoError(t, err)
	require.Equal(t, wire.AgentLine{ID: "claude1", Name: "claude1", Kind: "claude", Model: "opus", Busy: true}, roster[0])

	changes, err := r.b.Changes(ctx, "s1")
	require.NoError(t, err)
	require.Equal(t, "a/b.go", changes[0].Path, "relative to the root")
	require.Contains(t, changes[0].Diff, "@@ -1,1 +1,1 @@\n-old\n+new\n")

	require.NoError(t, r.b.SetMode("s1", "plan"))
	require.Equal(t, "plan", r.rc.modes["s1"])
}

func TestConvertKeepsNamesNotPaths(t *testing.T) {
	m := toMessage(message.Message{ID: "u", SessionID: "s", Role: message.User, Parts: []message.ContentPart{
		message.TextContent{Text: "see"},
		message.BinaryContent{Path: `C:\Users\me\Pictures\shot.png`, MIMEType: "image/png"},
		message.TextContent{Text: "hidden", Hidden: true},
		message.ToolCall{ID: "c", Name: "edit", Input: `{"file_path":"/work/src/y.go"}`},
	}}, "/work")
	require.Len(t, m.Parts, 3)
	require.Equal(t, "src/y.go", m.Parts[2].Summary, "a tool path is relative to the root")
	require.Equal(t, "shot.png", m.Parts[1].Image.Name)
	require.Equal(t, "image/png", m.Parts[1].Image.Mime)
	require.True(t, m.Finished)

	a := toApproval(permission.PermissionRequest{ID: "p", SessionID: "s", Description: "claude1 wants to edit: x",
		Params: tools.EditPermissionsParams{FilePath: "/work/src/x.go", OldContent: "a\n", NewContent: "b\n"}}, time.Unix(1, 0), "/work")
	require.Equal(t, wire.ApprovalEdit, a.Kind)
	require.Equal(t, "src/x.go", a.Path)
	require.Contains(t, a.Diff, "-a\n+b\n")
	require.Equal(t, "claude1", a.Agent)
}

func TestApprovalsLeaveTheWaitingSet(t *testing.T) {
	r := newRig(t)
	ask := func(id, call string) {
		t.Helper()
		r.app_(pubsub.Event[permission.PermissionRequest]{Type: pubsub.CreatedEvent, Payload: permission.PermissionRequest{
			ID: id, SessionID: "s1", ToolCallID: call, Params: tools.BashPermissionsParams{Command: "ls"}}})
		require.Equal(t, wire.EvApprovalRequested, r.next(t).Name)
		r.next(t) // the session summary
	}
	waiting := func() int {
		t.Helper()
		list, err := r.b.Approvals("")
		require.NoError(t, err)
		return len(list)
	}

	// The "about to ask" notification, which may arrive after the request,
	// is not a decision.
	ask("p1", "c1")
	r.app_(pubsub.Event[permission.PermissionNotification]{Type: pubsub.CreatedEvent, Payload: permission.PermissionNotification{ToolCallID: "c1"}})
	r.app_(pubsub.Event[permission.PermissionNotification]{Type: pubsub.CreatedEvent, Payload: permission.PermissionNotification{ToolCallID: "c1", Denied: true}})
	ev := r.next(t)
	require.Equal(t, wire.ApprovalResolved{ID: "p1", SessionID: "s1"}, ev.Payload, "the undecided notification resolved nothing")
	r.next(t)
	require.Zero(t, waiting())

	// An answer the service no longer knows leaves the set too.
	ask("p2", "c2")
	r.core.resolved = false
	won, err := r.b.Answer("s1", "p2", wire.DecisionAllow)
	require.NoError(t, err)
	require.False(t, won)
	require.Zero(t, waiting())

	// A request withdrawn by a cancelled turn sends no notification: the
	// turn ending takes it away.
	ask("p3", "c3")
	r.rc.changes <- pubsub.Event[workspace.DeckChange]{Payload: workspace.DeckChange{Kind: workspace.ChangeBusy, SessionID: "s1", Busy: false}}
	require.Equal(t, wire.EvBusy, r.next(t).Name)
	ev = r.next(t)
	require.Equal(t, wire.ApprovalResolved{ID: "p3", SessionID: "s1"}, ev.Payload)
	require.Equal(t, 0, r.next(t).Payload.(wire.Session).Pending)
	require.Zero(t, waiting())
}
