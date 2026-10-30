package rpc

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	deckconfig "github.com/dev-dami/deck/internal/deck/config"
	decks "github.com/dev-dami/deck/internal/deck/session"
	"github.com/dev-dami/deck/internal/pubsub"
	"github.com/dev-dami/deck/internal/workspace"
	"github.com/dev-dami/deck/pkg/remote"
	"github.com/dev-dami/deck/pkg/remote/remotetest"
	"github.com/dev-dami/deck/pkg/remote/wire"
	"github.com/dev-dami/deck/pkg/rpc/deckv1"
)

// fakeRooms is a workspace with rooms on top of fakeCrew's board: the room
// calls, the agents' statuses, the settings and the stream of changes.
type fakeRooms struct {
	*fakeCrew
	agents  []workspace.AgentLine
	rooms   map[string]workspace.Room
	did     []string
	since   time.Time
	changes *pubsub.Broker[workspace.DeckChange]
	cfg     deckconfig.Config
	failErr error
}

func newFakeRooms() *fakeRooms {
	return &fakeRooms{
		fakeCrew: &fakeCrew{shares: map[string]string{}},
		rooms:    map[string]workspace.Room{},
		changes:  pubsub.NewBroker[workspace.DeckChange](),
	}
}

func (f *fakeRooms) Roster(string) []workspace.AgentLine { return f.agents }

func (f *fakeRooms) known(sid string) error {
	if sid != "s1" {
		return workspace.ErrNotFound
	}
	return f.failErr
}

func (f *fakeRooms) Room(sid string) workspace.Room { return f.rooms[sid] }

func (f *fakeRooms) OpenSub(_ context.Context, parent, title string) (string, error) {
	return "", errors.New("no sub-threads in this fake")
}
func (f *fakeRooms) Isolate(_ context.Context, sid string) error {
	if err := f.known(sid); err != nil {
		return err
	}
	f.did = append(f.did, "isolate "+sid)
	f.rooms[sid] = workspace.Room{Dir: f.rooms[sid].Dir, Branch: "deck/" + sid}
	return nil
}

func (f *fakeRooms) ApplyIsolated(_ context.Context, sid string) ([]string, error) {
	if err := f.known(sid); err != nil {
		return nil, err
	}
	if f.rooms[sid].Branch == "" {
		return nil, errors.New("session: this thread is not isolated")
	}
	return []string{"a.go"}, nil
}

func (f *fakeRooms) Unisolate(_ context.Context, sid string) error {
	if err := f.known(sid); err != nil {
		return err
	}
	f.did = append(f.did, "unisolate "+sid)
	f.rooms[sid] = workspace.Room{}
	return nil
}

func (f *fakeRooms) RevertFiles(_ context.Context, sid string, since time.Time, paths []string) ([]string, error) {
	if err := f.known(sid); err != nil {
		return nil, err
	}
	f.since = since
	return append(paths, "old-name.go"), nil
}

func (f *fakeRooms) UndoRevert(_ context.Context, sid string) error {
	if err := f.known(sid); err != nil {
		return err
	}
	f.did = append(f.did, "undo "+sid)
	return nil
}

func (f *fakeRooms) Archive(_ context.Context, sid string) error {
	if err := f.known(sid); err != nil {
		return err
	}
	f.rooms[sid] = workspace.Room{Archived: true}
	return nil
}

func (f *fakeRooms) UndoApply(_ context.Context, sid string) ([]string, error) {
	if err := f.known(sid); err != nil {
		return nil, err
	}
	f.did = append(f.did, "undo apply "+sid)
	return []string{"a.go"}, nil
}

func (f *fakeRooms) Unarchive(_ context.Context, sid string) error {
	if err := f.known(sid); err != nil {
		return err
	}
	if !f.rooms[sid].Archived {
		return errors.New("this session is not archived")
	}
	f.rooms[sid] = workspace.Room{}
	return nil
}

func (f *fakeRooms) ReadImage(_ context.Context, sid, mid string, index int) ([]byte, string, error) {
	if err := f.known(sid); err != nil {
		return nil, "", err
	}
	switch {
	case mid != "m1" || index > 1:
		return nil, "", workspace.ErrNotFound
	case index == 1:
		return make([]byte, maxImageRead+1), "image/png", nil
	}
	return []byte("PNG"), "image/png", nil
}

func (f *fakeRooms) SearchMessages(_ context.Context, query string, limit int) ([]workspace.MessageHit, error) {
	f.did = append(f.did, "search "+query+" "+strconv.Itoa(limit))
	return []workspace.MessageHit{{SessionID: "s1", MessageID: "m1", Agent: "claude", Snippet: "…the lockout…", At: time.UnixMilli(1760000000123)}}, nil
}

func (f *fakeRooms) StartRace(_ context.Context, sid, brief string, entrants []workspace.RaceEntrant, judge bool) (string, error) {
	if err := f.known(sid); err != nil {
		return "", err
	}
	if len(entrants) < 2 {
		return "", errors.New("session: a race has 2 to 6 entries, not 1")
	}
	var who []string
	for _, e := range entrants {
		who = append(who, strings.TrimRight(e.Agent+"/"+e.Model+"/"+e.Effort, "/"))
	}
	f.did = append(f.did, strings.Join([]string{"race", brief, strings.Join(who, ","), map[bool]string{true: "judged"}[judge]}, " "))
	return "t7", nil
}

func (f *fakeRooms) DeckSettings() deckconfig.Config                { return f.cfg }
func (f *fakeRooms) SetDeckSettings(func(*deckconfig.Config)) error { return nil }
func (f *fakeRooms) AgentKinds() []string                           { return nil }
func (f *fakeRooms) SubscribeDeck(ctx context.Context) <-chan pubsub.Event[workspace.DeckChange] {
	return f.changes.Subscribe(ctx)
}

// roomRig serves a fake backend and f, with the project in dir, and runs
// the event pump.
func roomRig(t *testing.T, f *fakeRooms, dir string) (*remotetest.Fake, deckv1.DeckClient) {
	t.Helper()
	if f == nil {
		return workspaceRig(t, nil, dir)
	}
	return workspaceRig(t, f, dir)
}

// workspaceRig is roomRig for any workspace (nil for none).
func workspaceRig(t *testing.T, ws any, dir string) (*remotetest.Fake, deckv1.DeckClient) {
	t.Helper()
	fake := remotetest.New()
	srv := New(fake, "secret").WithProject(dir, nil)
	if ws != nil {
		srv.WithWorkspace(ws)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go srv.Run(ctx)
	l, sock, closeSock, err := Listen(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	g := srv.GRPC()
	go func() { _ = g.Serve(l) }()
	t.Cleanup(func() { g.Stop(); closeSock(); srv.Close() })
	cc, c, err := Dial(sock, "secret")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cc.Close() })
	return fake, c
}

// events subscribes and drops the Ready.
func events(t *testing.T, c deckv1.DeckClient) deckv1.Deck_EventsClient {
	t.Helper()
	st, err := c.Events(ctxT(t), &deckv1.Subscribe{})
	if err != nil {
		t.Fatal(err)
	}
	recv(t, st)
	return st
}

// until reads events until one passes ok.
func until(t *testing.T, st deckv1.Deck_EventsClient, ok func(*deckv1.Event) bool) *deckv1.Event {
	t.Helper()
	for {
		if ev := recv(t, st); ok(ev) {
			return ev
		}
	}
}

func TestRoomCalls(t *testing.T) {
	f := newFakeRooms()
	_, c := roomRig(t, f, "")
	ctx := ctxT(t)
	st := events(t, c)

	if _, err := c.IsolateSession(ctx, &deckv1.SessionRef{SessionId: "s1"}); err != nil {
		t.Fatal(err)
	}
	ev := until(t, st, func(e *deckv1.Event) bool { return e.GetSessionUpdated() != nil })
	if ev.GetSessionUpdated().GetBranch() != "deck/s1" || ev.GetSessionUpdated().GetTitle() != "One" {
		t.Fatalf("after isolating: %v", ev)
	}
	if r, err := c.ApplyIsolated(ctx, &deckv1.SessionRef{SessionId: "s1"}); err != nil || len(r.GetFiles()) != 1 {
		t.Fatalf("apply %v %v", r, err)
	}
	if _, err := c.UnisolateSession(ctx, &deckv1.SessionRef{SessionId: "s1"}); err != nil {
		t.Fatal(err)
	}
	if ev := until(t, st, func(e *deckv1.Event) bool { return e.GetSessionUpdated() != nil }); ev.GetSessionUpdated().GetBranch() != "" {
		t.Fatalf("after unisolating: %v", ev)
	}
	// The engine's own words come back.
	_, err := c.ApplyIsolated(ctx, &deckv1.SessionRef{SessionId: "s1"})
	if status.Code(err) != codes.FailedPrecondition || status.Convert(err).Message() != "session: this thread is not isolated" {
		t.Fatalf("apply when not isolated: %v", err)
	}

	r, err := c.RevertFiles(ctx, &deckv1.RevertFilesRequest{SessionId: "s1", Since: 1760000000123, Paths: []string{"a.go"}})
	if err != nil || len(r.GetFiles()) != 2 || f.since.UnixMilli() != 1760000000123 {
		t.Fatalf("revert %v %v since %v", r, err, f.since)
	}
	if _, err := c.RevertFiles(ctx, &deckv1.RevertFilesRequest{SessionId: "s1"}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("revert without paths: %v", err)
	}
	if _, err := c.RevertFiles(ctx, &deckv1.RevertFilesRequest{SessionId: "s1", Paths: []string{"a.go"}}); err != nil || !f.since.IsZero() {
		t.Fatalf("revert from the changes' checkpoint: %v %v", err, f.since)
	}
	if _, err := c.UndoRevert(ctx, &deckv1.SessionRef{SessionId: "s1"}); err != nil {
		t.Fatal(err)
	}

	tr, err := c.StartRace(ctx, &deckv1.StartRaceRequest{SessionId: "s1", Brief: "fix it", Kind: "claude", Count: 3, Judge: true})
	if err != nil || tr.GetTaskId() != "t7" {
		t.Fatalf("race %v %v", tr, err)
	}
	// Entrants mix kinds and repeat one, on the same model.
	mixed := []*deckv1.RaceEntrant{{Agent: "codex"}, {Agent: "claude", Model: "opus", Effort: "high"}, {Agent: "claude", Model: "opus"}}
	if _, err := c.StartRace(ctx, &deckv1.StartRaceRequest{SessionId: "s1", Brief: "fix it", Entrants: mixed, Kind: "pi", Count: 5}); err != nil {
		t.Fatalf("mixed race: %v", err)
	}
	if _, err := c.StartRace(ctx, &deckv1.StartRaceRequest{SessionId: "s1", Brief: "fix it", Kind: "claude", Count: 1}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("a race of one: %v", err)
	}
	for _, bad := range []*deckv1.StartRaceRequest{
		{SessionId: "s1", Kind: "claude", Count: 2},
		{SessionId: "s1", Brief: "fix it", Count: 2},
		{SessionId: "s1", Brief: "fix it", Kind: "claude", Count: 1 << 30},
		{SessionId: "s1", Brief: "fix it", Entrants: []*deckv1.RaceEntrant{{Agent: "claude"}, {Model: "opus"}}},
	} {
		if _, err := c.StartRace(ctx, bad); status.Code(err) != codes.InvalidArgument {
			t.Fatalf("race %v: %v", bad, err)
		}
	}

	if _, err := c.ArchiveSession(ctx, &deckv1.SessionRef{SessionId: "s1"}); err != nil {
		t.Fatal(err)
	}
	if ev := until(t, st, func(e *deckv1.Event) bool { return e.GetSessionUpdated() != nil }); !ev.GetSessionUpdated().GetArchived() {
		t.Fatalf("after archiving: %v", ev)
	}
	want := []string{"isolate s1", "unisolate s1", "undo s1", "race fix it claude,claude,claude judged", "race fix it codex,claude/opus/high,claude/opus "}
	if !slices.Equal(f.did, want) {
		t.Fatalf("did %q, want %q", f.did, want)
	}

	// Every call checks its session.
	if _, err := c.ArchiveSession(ctx, &deckv1.SessionRef{}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("no session: %v", err)
	}
	if _, err := c.UndoRevert(ctx, &deckv1.SessionRef{SessionId: "nope"}); status.Code(err) != codes.NotFound {
		t.Fatalf("unknown session: %v", err)
	}
}

func TestCreateIsolatedSession(t *testing.T) {
	f := newFakeRooms()
	fake, c := roomRig(t, f, "")
	ctx := ctxT(t)
	// The fake knows only s1, so isolating the new session fails and the
	// session goes again.
	_, err := c.CreateSession(ctx, &deckv1.CreateSessionRequest{Title: "Two", Isolate: true})
	if status.Code(err) != codes.NotFound {
		t.Fatalf("create isolated: %v", err)
	}
	list, _ := c.ListSessions(ctx, &deckv1.Empty{})
	if len(list.GetSessions()) != 1 || len(f.did) != 0 {
		t.Fatalf("left behind: %v, did %v", list, f.did)
	}
	if fake.Get("s1") == nil {
		t.Fatal("s1 is gone")
	}
}

func TestRoomCallsWithoutAWorkspace(t *testing.T) {
	_, c := roomRig(t, nil, "")
	ctx := ctxT(t)
	if _, err := c.IsolateSession(ctx, &deckv1.SessionRef{SessionId: "s1"}); status.Code(err) != codes.Unimplemented {
		t.Fatalf("isolate without a workspace: %v", err)
	}
	if _, err := c.CreateSession(ctx, &deckv1.CreateSessionRequest{Isolate: true}); status.Code(err) != codes.Unimplemented {
		t.Fatalf("create isolated without a workspace: %v", err)
	}
	if _, err := c.ListFiles(ctx, &deckv1.ListFilesRequest{SessionId: "s1"}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("files without a project: %v", err)
	}
	info, err := c.Info(ctx, &deckv1.Empty{})
	if err != nil || len(info.GetCapabilities()) != 0 || len(info.GetClis()) != 0 {
		t.Fatalf("info %v %v", info, err)
	}
}

func TestInfoListsCLIsAndCapabilities(t *testing.T) {
	f := newFakeRooms()
	f.cfg = deckconfig.Config{ClaudeBinary: "sh", CodexBinary: "no-such-cli-for-deck"}
	_, c := roomRig(t, f, t.TempDir())
	info, err := c.Info(ctxT(t), &deckv1.Empty{})
	if err != nil {
		t.Fatal(err)
	}
	clis := map[string]*deckv1.AgentCli{}
	for _, x := range info.GetClis() {
		clis[x.GetKind()] = x
	}
	if cl := clis["claude"]; !cl.GetFound() || cl.GetPath() == "" || cl.LoggedIn != nil {
		t.Fatalf("claude %v", cl)
	}
	if cx := clis["codex"]; cx.GetFound() || cx.GetPath() != "" {
		t.Fatalf("codex %v", cx)
	}
	for _, m := range []string{"IsolateSession", "StartRace", "ListFiles", "UnarchiveSession", "UndoApply", "ReadImage", "SearchMessages"} {
		if !slices.Contains(info.GetCapabilities(), m) {
			t.Fatalf("capabilities %v lack %s", info.GetCapabilities(), m)
		}
	}
}

func TestSessionAttention(t *testing.T) {
	f := newFakeRooms()
	f.rooms["s1"] = workspace.Room{Branch: "deck/s1"}
	fake, c := roomRig(t, f, "")
	ctx := ctxT(t)
	attention := func() *deckv1.Session {
		t.Helper()
		list, err := c.ListSessions(ctx, &deckv1.Empty{})
		if err != nil {
			t.Fatal(err)
		}
		return list.GetSessions()[0]
	}
	if s := attention(); s.GetAttention() != "" || s.GetBranch() != "deck/s1" {
		t.Fatalf("calm: %v", s)
	}
	f.board.Tasks = []workspace.BoardTask{{ID: "t1", Status: "failed", Entry: true}, {ID: "t2", Status: "blocked"}}
	if got := attention().GetAttention(); got != "blocked" {
		t.Fatalf("a blocked task: %q", got)
	}
	f.agents = []workspace.AgentLine{{ID: "claude", Status: "error"}}
	if got := attention().GetAttention(); got != "error" {
		t.Fatalf("an agent in error: %q", got)
	}
	fake.Get("s1").Approvals = []wire.Approval{{ID: "a1", SessionID: "s1", Kind: wire.ApprovalOther}}
	if got := attention().GetAttention(); got != "question" {
		t.Fatalf("an agent asks: %q", got)
	}
	fake.Get("s1").Approvals = append(fake.Get("s1").Approvals, wire.Approval{ID: "a2", SessionID: "s1", Kind: wire.ApprovalCommand})
	if got := attention().GetAttention(); got != "approval" {
		t.Fatalf("a command to approve: %q", got)
	}

	// An agent's status moves with roster_changed alone: the room follows.
	fake.Get("s1").Approvals = nil
	f.board.Tasks, f.agents = nil, nil
	attention()
	st := events(t, c)
	f.agents = []workspace.AgentLine{{ID: "claude", Status: "approval"}}
	fake.Inject(remote.Event{Name: wire.EvRosterChanged, SessionID: "s1", Payload: wire.RosterChanged{SessionID: "s1"}})
	ev := until(t, st, func(e *deckv1.Event) bool { return e.GetSessionUpdated() != nil })
	if ev.GetSessionUpdated().GetAttention() != "approval" {
		t.Fatalf("after the roster changed: %v", ev)
	}
	// A session event carries the room too.
	fake.Inject(remote.Event{Name: wire.EvSessionUpdated, SessionID: "s1", Payload: wire.Session{ID: "s1", Title: "One"}})
	ev = until(t, st, func(e *deckv1.Event) bool { return e.GetSessionUpdated() != nil })
	if ev.GetSessionUpdated().GetAttention() != "approval" || ev.GetSessionUpdated().GetBranch() != "deck/s1" {
		t.Fatalf("session event: %v", ev)
	}
}

func TestLeaseAndOverlapEvents(t *testing.T) {
	f := newFakeRooms()
	fake, c := roomRig(t, f, "")
	st := events(t, c)
	waitUntil(t, func() bool { return f.changes.GetSubscriberCount() > 0 })

	f.changes.Publish(pubsub.UpdatedEvent, workspace.DeckChange{
		Kind: workspace.ChangeLease, SessionID: "s1",
		Lease: decks.LeasesChangedMsg{Agent: "claude", Paths: []string{"a.go"}, Exclusive: true},
	})
	f.changes.Publish(pubsub.UpdatedEvent, workspace.DeckChange{Kind: workspace.ChangeBusy, SessionID: "s1"}) // not a lease
	f.changes.Publish(pubsub.UpdatedEvent, workspace.DeckChange{
		Kind: workspace.ChangeLease, SessionID: "s1",
		Lease: decks.LeasesChangedMsg{Agent: "claude", Paths: []string{"a.go"}, Released: true},
	})
	taken := recv(t, st).GetLeaseChanged()
	if taken.GetAgent() != "claude" || !taken.GetExclusive() || taken.GetReleased() || taken.GetSessionId() != "s1" || taken.GetPaths()[0] != "a.go" {
		t.Fatalf("taken %v", taken)
	}
	if gone := recv(t, st).GetLeaseChanged(); !gone.GetReleased() {
		t.Fatalf("released %v", gone)
	}

	fake.Inject(remote.Event{Name: wire.EvNotice, SessionID: "s1", Payload: wire.Notice{SessionID: "s1", Text: "claude and codex both changed a.go", Via: "overlap"}})
	if n := recv(t, st).GetNotice(); n.GetVia() != "overlap" {
		t.Fatalf("notice %v", n)
	}
}

func waitUntil(t *testing.T, ok func() bool) {
	t.Helper()
	for end := time.Now().Add(5 * time.Second); !ok(); {
		if time.Now().After(end) {
			t.Fatal("timed out")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestListFiles(t *testing.T) {
	dir := t.TempDir()
	for _, p := range []string{".gitignore", "main.go", "internal/store/store.go", "internal/store/store_test.go", "docs/readme.md", "skip.log", "node_modules/x/index.js"} {
		body := "x\n"
		if p == ".gitignore" {
			body = "*.log\n"
		}
		if err := os.MkdirAll(filepath.Join(dir, filepath.Dir(p)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, p), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	f := newFakeRooms()
	_, c := roomRig(t, f, t.TempDir()) // the project; s1's thread is elsewhere
	f.rooms["s1"] = workspace.Room{Dir: dir}
	ctx := ctxT(t)

	all, err := c.ListFiles(ctx, &deckv1.ListFilesRequest{SessionId: "s1"})
	if err != nil {
		t.Fatal(err)
	}
	if got := all.GetPaths(); !slices.Equal(got, []string{".gitignore", "docs/readme.md", "internal/store/store.go", "internal/store/store_test.go", "main.go"}) || all.GetTruncated() {
		t.Fatalf("all files %q (truncated %v)", got, all.GetTruncated())
	}
	q, err := c.ListFiles(ctx, &deckv1.ListFilesRequest{SessionId: "s1", Query: "storego"})
	if err != nil || len(q.GetPaths()) == 0 || q.GetPaths()[0] != "internal/store/store.go" {
		t.Fatalf("fuzzy %v %v", q, err)
	}
	one, err := c.ListFiles(ctx, &deckv1.ListFilesRequest{SessionId: "s1", Query: "store", Limit: 1})
	if err != nil || len(one.GetPaths()) != 1 || !one.GetTruncated() {
		t.Fatalf("limited %v %v", one, err)
	}
	if _, err := c.ListFiles(ctx, &deckv1.ListFilesRequest{SessionId: "s1", Limit: -1}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("negative limit: %v", err)
	}
	if _, err := c.ListFiles(ctx, &deckv1.ListFilesRequest{}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("no session: %v", err)
	}
}

func TestUndoApplyAndUnarchive(t *testing.T) {
	f := newFakeRooms()
	_, c := roomRig(t, f, "")
	ctx := ctxT(t)
	st := events(t, c)

	if r, err := c.UndoApply(ctx, &deckv1.SessionRef{SessionId: "s1"}); err != nil || len(r.GetFiles()) != 1 {
		t.Fatalf("undo apply %v %v", r, err)
	}
	// The engine's own words come back.
	_, err := c.UnarchiveSession(ctx, &deckv1.SessionRef{SessionId: "s1"})
	if status.Code(err) != codes.FailedPrecondition || status.Convert(err).Message() != "this session is not archived" {
		t.Fatalf("unarchive a live session: %v", err)
	}
	if _, err := c.ArchiveSession(ctx, &deckv1.SessionRef{SessionId: "s1"}); err != nil {
		t.Fatal(err)
	}
	until(t, st, func(e *deckv1.Event) bool { return e.GetSessionUpdated().GetArchived() })
	if _, err := c.UnarchiveSession(ctx, &deckv1.SessionRef{SessionId: "s1"}); err != nil {
		t.Fatal(err)
	}
	if ev := until(t, st, func(e *deckv1.Event) bool { return e.GetSessionUpdated() != nil }); ev.GetSessionUpdated().GetArchived() {
		t.Fatalf("after unarchiving: %v", ev)
	}
	if !slices.Equal(f.did, []string{"undo apply s1"}) {
		t.Fatalf("did %q", f.did)
	}
	for _, call := range []func(*deckv1.SessionRef) error{
		func(r *deckv1.SessionRef) error { _, err := c.UndoApply(ctx, r); return err },
		func(r *deckv1.SessionRef) error { _, err := c.UnarchiveSession(ctx, r); return err },
	} {
		if err := call(&deckv1.SessionRef{}); status.Code(err) != codes.InvalidArgument {
			t.Fatalf("no session: %v", err)
		}
		if err := call(&deckv1.SessionRef{SessionId: "nope"}); status.Code(err) != codes.NotFound {
			t.Fatalf("unknown session: %v", err)
		}
	}
}

func TestReadImage(t *testing.T) {
	_, c := roomRig(t, newFakeRooms(), "")
	ctx := ctxT(t)
	r, err := c.ReadImage(ctx, &deckv1.ReadImageRequest{SessionId: "s1", MessageId: "m1"})
	if err != nil || string(r.GetData()) != "PNG" || r.GetMime() != "image/png" {
		t.Fatalf("read %v %v", r, err)
	}
	for _, tc := range []struct {
		req  *deckv1.ReadImageRequest
		code codes.Code
	}{
		{&deckv1.ReadImageRequest{SessionId: "s1", MessageId: "m1", Index: 1}, codes.ResourceExhausted},
		{&deckv1.ReadImageRequest{SessionId: "s1", MessageId: "m2"}, codes.NotFound},
		{&deckv1.ReadImageRequest{SessionId: "nope", MessageId: "m1"}, codes.NotFound},
		{&deckv1.ReadImageRequest{MessageId: "m1"}, codes.InvalidArgument},
		{&deckv1.ReadImageRequest{SessionId: "s1"}, codes.InvalidArgument},
		{&deckv1.ReadImageRequest{SessionId: "s1", MessageId: "m1", Index: -1}, codes.InvalidArgument},
	} {
		if _, err := c.ReadImage(ctx, tc.req); status.Code(err) != tc.code {
			t.Fatalf("read %v: %v, want %v", tc.req, err, tc.code)
		}
	}
}

func TestSearchMessages(t *testing.T) {
	f := newFakeRooms()
	_, c := roomRig(t, f, "")
	ctx := ctxT(t)
	r, err := c.SearchMessages(ctx, &deckv1.SearchMessagesRequest{Query: "lockout"})
	if err != nil || len(r.GetHits()) != 1 {
		t.Fatalf("search %v %v", r, err)
	}
	if h := r.GetHits()[0]; h.GetSessionId() != "s1" || h.GetMessageId() != "m1" || h.GetAgent() != "claude" || h.GetSnippet() != "…the lockout…" || h.GetAt() != 1760000000123 {
		t.Fatalf("hit %v", h)
	}
	if _, err := c.SearchMessages(ctx, &deckv1.SearchMessagesRequest{Query: "lockout", Limit: 1000}); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(f.did, []string{"search lockout 30", "search lockout 200"}) {
		t.Fatalf("did %q", f.did)
	}
	for _, bad := range []*deckv1.SearchMessagesRequest{{Query: "  "}, {Query: "x", Limit: -1}} {
		if _, err := c.SearchMessages(ctx, bad); status.Code(err) != codes.InvalidArgument {
			t.Fatalf("search %v: %v", bad, err)
		}
	}
}

func TestMessageCallsWithoutAWorkspace(t *testing.T) {
	_, c := roomRig(t, nil, "")
	ctx := ctxT(t)
	if _, err := c.ReadImage(ctx, &deckv1.ReadImageRequest{SessionId: "s1", MessageId: "m1"}); status.Code(err) != codes.Unimplemented {
		t.Fatalf("read image: %v", err)
	}
	if _, err := c.SearchMessages(ctx, &deckv1.SearchMessagesRequest{Query: "x"}); status.Code(err) != codes.Unimplemented {
		t.Fatalf("search: %v", err)
	}
	if _, err := c.UnarchiveSession(ctx, &deckv1.SessionRef{SessionId: "s1"}); status.Code(err) != codes.Unimplemented {
		t.Fatalf("unarchive: %v", err)
	}
	if _, err := c.UndoApply(ctx, &deckv1.SessionRef{SessionId: "s1"}); status.Code(err) != codes.Unimplemented {
		t.Fatalf("undo apply: %v", err)
	}
}
