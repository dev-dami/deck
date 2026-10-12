package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dev-dami/deck/internal/app"
	deckagent "github.com/dev-dami/deck/internal/deck/agent"
	decks "github.com/dev-dami/deck/internal/deck/session"
	"github.com/dev-dami/deck/internal/message"
	"github.com/dev-dami/deck/internal/permission"
	"github.com/dev-dami/deck/internal/pubsub"
)

// newRemoteWorkspace is a picker workspace that also has the permission
// service the mode switches go through.
func newRemoteWorkspace(t *testing.T) *DeckWorkspace {
	t.Helper()
	w := newPickerWorkspace(t, pickerCfg())
	w.busy = map[string]bool{}
	w.AppWorkspace = &AppWorkspace{app: &app.App{Permissions: permission.NewPermissionService(t.TempDir(), false, nil)}}
	return w
}

func joinThread(t *testing.T, w *DeckWorkspace, sid string) string {
	t.Helper()
	tid, err := w.mgr().NewThread(t.TempDir())
	require.NoError(t, err)
	w.threads[sid], w.back[tid] = tid, sid
	return tid
}

func TestSetRemoteModeRoundTrips(t *testing.T) {
	w := newRemoteWorkspace(t)
	tid := joinThread(t, w, "s1")
	require.Equal(t, RemoteModeAsk, w.RemoteMode())

	for _, tc := range []struct {
		set  string
		want deckagent.Mode
	}{
		{RemoteModePlan, deckagent.ModePlan},
		{RemoteModeYolo, deckagent.ModeYolo},
		{RemoteModeAsk, deckagent.ModeAsk},
	} {
		require.NoError(t, w.SetRemoteMode(tc.set), tc.set)
		require.Equal(t, string(tc.want), w.RemoteMode(), tc.set)
		require.Equal(t, tc.want, w.mgr().Mode(tid), "live thread follows %s", tc.set)
	}
}

func TestSetRemoteModeLeavesPlanWhenAskedForYolo(t *testing.T) {
	w := newRemoteWorkspace(t)
	require.NoError(t, w.SetRemoteMode(RemoteModePlan))
	require.NoError(t, w.SetRemoteMode(RemoteModeYolo))
	require.Equal(t, RemoteModeYolo, w.RemoteMode())
	require.True(t, w.PermissionSkipRequests())

	require.NoError(t, w.SetRemoteMode(RemoteModeAsk))
	require.False(t, w.PermissionSkipRequests(), "ask must switch yolo off")
}

func TestSetRemoteModeRejectsUnknown(t *testing.T) {
	w := newRemoteWorkspace(t)
	require.Error(t, w.SetRemoteMode("turbo"))
	require.Equal(t, RemoteModeAsk, w.RemoteMode())
}

func TestInterruptAgentUnknown(t *testing.T) {
	w := newRemoteWorkspace(t)
	require.ErrorContains(t, w.InterruptAgent("nosession", "claude"), "no running agents")

	tid := joinThread(t, w, "s1")
	_, err := w.mgr().AddMember(tid, "codex", "")
	require.NoError(t, err)
	require.ErrorContains(t, w.InterruptAgent("s1", "ghost"), `no agent "ghost"`)
}

func TestQueueItemsWithoutThread(t *testing.T) {
	w := newRemoteWorkspace(t)
	require.Empty(t, w.QueueItems("none"))
	require.Error(t, w.SteerQueued("missing"))
	require.Error(t, w.CancelQueued("missing"))
}

func TestContinueAndChangesWithoutThread(t *testing.T) {
	w := newRemoteWorkspace(t)
	require.Error(t, w.ContinueThread("none"))
	cs, err := w.ChangedFiles(context.Background(), "none")
	require.NoError(t, err)
	require.Empty(t, cs.Files)
}

func TestBusyEdgeIsPublishedWithoutAProgram(t *testing.T) {
	w := newRemoteWorkspace(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch := w.SubscribeDeck(ctx)

	w.busy["s1"] = true // the session was busy; nothing is running now
	require.Nil(t, w.prog)
	w.edge("s1", false)

	select {
	case ev := <-ch:
		require.Equal(t, pubsub.UpdatedEvent, ev.Type)
		require.Equal(t, DeckChange{Kind: ChangeBusy, SessionID: "s1", Busy: false}, ev.Payload)
	case <-time.After(2 * time.Second):
		t.Fatal("no busy change published")
	}
}

func TestNoEdgeNoPublish(t *testing.T) {
	w := newRemoteWorkspace(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch := w.SubscribeDeck(ctx)

	w.edge("s1", false) // idle before, idle now
	select {
	case ev := <-ch:
		t.Fatalf("unexpected change %+v", ev)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestModeChangeIsPublishedFromTheEngine(t *testing.T) {
	w := newRemoteWorkspace(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch := w.SubscribeDeck(ctx)
	go w.pump()

	joinThread(t, w, "s1")
	require.NoError(t, w.SetRemoteMode(RemoteModePlan))

	deadline := time.After(2 * time.Second)
	for {
		select {
		case ev := <-ch:
			if ev.Payload.Kind == ChangeMode && ev.Payload.Mode == string(deckagent.ModePlan) {
				return
			}
		case <-deadline:
			t.Fatal("no mode change published")
		}
	}
}

// fakeMessages is the slice of message.Service EditMessage uses.
type fakeMessages struct {
	message.Service
	rows    map[string]message.Message
	updated []message.Message
}

func (f *fakeMessages) Get(_ context.Context, id string) (message.Message, error) {
	m, ok := f.rows[id]
	if !ok {
		return message.Message{}, errors.New("no row")
	}
	return m, nil
}

func (f *fakeMessages) Update(_ context.Context, m message.Message) error {
	f.rows[m.ID] = m
	f.updated = append(f.updated, m)
	return nil
}

// editRig has one session with a claude member, one user message that was
// posted (so it has a log entry), and the row the UI shows for it.
func editRig(t *testing.T) (*DeckWorkspace, *fakeMessages) {
	t.Helper()
	w := newRemoteWorkspace(t)
	w.app = app.NewForTest(t.Context())
	fm := &fakeMessages{rows: map[string]message.Message{}}
	w.app.Messages = fm
	w.entries = map[string]string{}
	w.path = filepath.Join(t.TempDir(), "deck-threads.json")

	tid := joinThread(t, w, "s1")
	_, err := w.mgr().AddMember(tid, "claude", "")
	require.NoError(t, err)
	// A joining agent plays its opening turn first; post once it is free.
	var res decks.PostResult
	require.Eventually(t, func() bool {
		var err error
		res, err = w.mgr().PostWith(tid, "first draft", nil)
		return err == nil
	}, 3*time.Second, 20*time.Millisecond)
	require.NotEmpty(t, res.EntryID)

	fm.rows["m1"] = message.Message{ID: "m1", SessionID: "s1", Role: message.User,
		Parts: []message.ContentPart{
			message.TextContent{Text: "first draft"},
			message.BinaryContent{Path: "a.png", MIMEType: "image/png"},
		}}
	w.recordEntry("m1", res.EntryID)
	return w, fm
}

func TestEditMessageUpdatesTheRowAndKeepsAttachments(t *testing.T) {
	w, fm := editRig(t)
	require.True(t, w.EditableMessage("m1"))

	require.NoError(t, w.EditMessage(t.Context(), "s1", "m1", "  second draft  "))

	require.Len(t, fm.updated, 1)
	require.True(t, w.EditedMessage("m1"), "the row is marked edited")
	row := fm.rows["m1"]
	require.Equal(t, "second draft", row.Content().Text)
	require.Len(t, row.Parts, 2, "the attached image stays")
	require.IsType(t, message.BinaryContent{}, row.Parts[1])
}

func TestEditMessageRefusals(t *testing.T) {
	w, fm := editRig(t)
	ctx := t.Context()

	require.ErrorIs(t, w.EditMessage(ctx, "s1", "unknown", "x"), ErrNotEditable)
	require.ErrorIs(t, w.EditMessage(ctx, "other", "m1", "x"), ErrNotEditable, "a row of another session")
	require.ErrorContains(t, w.EditMessage(ctx, "s1", "m1", "   "), "empty")

	fm.rows["a1"] = message.Message{ID: "a1", SessionID: "s1", Role: message.Assistant}
	w.recordEntry("a1", "e-fake")
	require.ErrorIs(t, w.EditMessage(ctx, "s1", "a1", "x"), ErrNotEditable, "only the user's own rows")

	require.Empty(t, fm.updated, "a refused edit changes nothing")
	require.False(t, w.EditableMessage("unknown"))
}

func TestEditMessageWithAnUnknownEntryFails(t *testing.T) {
	w, fm := editRig(t)
	w.recordEntry("m2", "no-such-entry")
	fm.rows["m2"] = message.Message{ID: "m2", SessionID: "s1", Role: message.User,
		Parts: []message.ContentPart{message.TextContent{Text: "old"}}}
	require.Error(t, w.EditMessage(t.Context(), "s1", "m2", "new"))
	require.Equal(t, "old", func() string { r := fm.rows["m2"]; return r.Content().Text }(), "the row is not touched when the thread refuses")
}

func TestEntryIndexSurvivesARestart(t *testing.T) {
	w, _ := editRig(t)
	data, err := os.ReadFile(w.entriesPath())
	require.NoError(t, err)

	var idx editIndex
	require.NoError(t, json.Unmarshal(data, &idx))
	fresh := &DeckWorkspace{path: w.path, entries: idx.Entries, edited: idx.Edited}
	require.True(t, fresh.EditableMessage("m1"))
	require.False(t, fresh.EditedMessage("m1"))

	w.markEdited("m1", true)
	data, err = os.ReadFile(w.entriesPath())
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(data, &idx))
	require.True(t, idx.Edited["m1"], "the edited mark is saved too")
}

func TestRecordEntryIgnoresEmpty(t *testing.T) {
	w := newRemoteWorkspace(t)
	w.recordEntry("", "e")
	w.recordEntry("m", "")
	require.False(t, w.EditableMessage("m"))
	require.Empty(t, w.entriesPath(), "no path, nothing is written to the working directory")
}
