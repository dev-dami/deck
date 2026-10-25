package workspace

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dev-dami/deck/internal/app"
	"github.com/dev-dami/deck/internal/config"
	"github.com/dev-dami/deck/internal/db"
	"github.com/dev-dami/deck/internal/message"
	"github.com/dev-dami/deck/internal/permission"
	"github.com/dev-dami/deck/internal/session"
)

// roomWorkspace is a Deck workspace with a real session and message store.
func roomWorkspace(t *testing.T) *DeckWorkspace {
	t.Helper()
	conn, err := db.Connect(t.Context(), t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { conn.Close() })
	q := db.New(conn)
	w := newPickerWorkspace(t, pickerCfg())
	w.AppWorkspace = &AppWorkspace{app: &app.App{Sessions: session.NewService(q, conn), Messages: message.NewService(q), Permissions: permission.NewPermissionService(t.TempDir(), false, nil)}, store: config.NewTestStore(&config.Config{})}
	return w
}

func TestRoomReadsLiveAndStoredThreads(t *testing.T) {
	w := newPickerWorkspace(t, pickerCfg())
	require.Equal(t, Room{}, w.Room("s1"))

	tid, err := w.mgr().NewThread(t.TempDir())
	require.NoError(t, err)
	w.threads["s1"], w.back[tid] = tid, "s1"
	_, err = w.mgr().AddMember(tid, "claude", "") // the thread now has content to store
	require.NoError(t, err)
	live := w.Room("s1")
	require.NotEmpty(t, live.Dir)
	require.False(t, live.Archived)

	// Archived, the thread is no longer live: Room reads the store.
	require.NoError(t, w.mgr().Archive(tid))
	w.mgr().Flush()
	require.Equal(t, Room{Dir: live.Dir, Archived: true}, w.Room("s1"))
}

func TestArchiveAnEmptySessionAndUnarchiveIt(t *testing.T) {
	w := roomWorkspace(t)
	ctx := context.Background()
	s, err := w.app.Sessions.Create(ctx, "empty")
	require.NoError(t, err)
	require.ErrorIs(t, w.Unarchive(ctx, s.ID), errNotArchived)

	require.NoError(t, w.Archive(ctx, s.ID))
	require.True(t, w.Room(s.ID).Archived)
	require.NoError(t, w.Unarchive(ctx, s.ID))
	require.False(t, w.Room(s.ID).Archived)
	require.ErrorIs(t, w.Unarchive(ctx, "nope"), ErrNotFound)
}

func TestReadImage(t *testing.T) {
	w := roomWorkspace(t)
	ctx := context.Background()
	s, err := w.app.Sessions.Create(ctx, "one")
	require.NoError(t, err)
	other, err := w.app.Sessions.Create(ctx, "two")
	require.NoError(t, err)
	m, err := w.app.Messages.Create(ctx, s.ID, message.CreateMessageParams{Role: message.User, Parts: []message.ContentPart{
		message.TextContent{Text: "look"},
		message.BinaryContent{Path: "notes.txt", MIMEType: "text/plain", Data: []byte("x")},
		message.BinaryContent{Path: "shot.png", MIMEType: "image/png", Data: []byte("PNG")},
	}})
	require.NoError(t, err)

	data, mime, err := w.ReadImage(ctx, s.ID, m.ID, 1)
	require.NoError(t, err)
	require.Equal(t, "PNG", string(data))
	require.Equal(t, "image/png", mime)
	_, _, err = w.ReadImage(ctx, s.ID, m.ID, 0)
	require.ErrorIs(t, err, errNotImage)
	_, _, err = w.ReadImage(ctx, s.ID, m.ID, 2)
	require.ErrorIs(t, err, ErrNotFound)
	// Another session's message is not found.
	_, _, err = w.ReadImage(ctx, other.ID, m.ID, 1)
	require.ErrorIs(t, err, ErrNotFound)
}

func TestSearchMessages(t *testing.T) {
	w := roomWorkspace(t)
	ctx := context.Background()
	say := func(sid string, role message.MessageRole, model, text string) message.Message {
		m, err := w.app.Messages.Create(ctx, sid, message.CreateMessageParams{Role: role, Model: model, Provider: "claude", Parts: []message.ContentPart{message.TextContent{Text: text}}})
		require.NoError(t, err)
		return m
	}
	a, err := w.app.Sessions.Create(ctx, "a")
	require.NoError(t, err)
	b, err := w.app.Sessions.Create(ctx, "b")
	require.NoError(t, err)
	say(a.ID, message.User, "", "where is the Lockout handled?")
	reply := say(a.ID, message.Assistant, "claude", "The lockout lives in pair.go,\n  - next to the timer - for now.")
	say(b.ID, message.User, "", "nothing to see")
	long := say(b.ID, message.User, "", strings.Repeat("x", 100)+"LOCKOUT"+strings.Repeat("y", 100))

	hits, err := w.SearchMessages(ctx, "lockout", 30)
	require.NoError(t, err)
	require.Len(t, hits, 3)
	ids := map[string]MessageHit{}
	for _, h := range hits {
		ids[h.MessageID] = h
	}
	require.Equal(t, "claude", ids[reply.ID].Agent)
	require.Equal(t, a.ID, ids[reply.ID].SessionID)
	// A list bullet goes; a dash inside a line stays.
	require.Equal(t, "The lockout lives in pair.go, next to the timer - for now.", ids[reply.ID].Snippet)
	require.Equal(t, "…"+strings.Repeat("x", 60)+"LOCKOUT"+strings.Repeat("y", 60)+"…", ids[long.ID].Snippet)

	hits, err = w.SearchMessages(ctx, "lockout", 1)
	require.NoError(t, err)
	require.Len(t, hits, 1)

	// An archived session is left out.
	require.NoError(t, w.Archive(ctx, b.ID))
	hits, err = w.SearchMessages(ctx, "LOCKOUT", 30)
	require.NoError(t, err)
	require.Len(t, hits, 2)
	for _, h := range hits {
		require.Equal(t, a.ID, h.SessionID)
	}
}
