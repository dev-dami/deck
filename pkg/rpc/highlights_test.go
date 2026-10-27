package rpc

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/dev-dami/deck/internal/deck/highlights"
	"github.com/dev-dami/deck/internal/pubsub"
	"github.com/dev-dami/deck/internal/workspace"
	"github.com/dev-dami/deck/pkg/remote/remotetest"
	pb "github.com/dev-dami/deck/pkg/rpc/deckv1"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type highlightWorkspace struct {
	store   *highlights.Store
	changes *pubsub.Broker[workspace.DeckChange]
	ready   chan struct{}
	once    sync.Once
}

func (w *highlightWorkspace) SubscribeDeck(ctx context.Context) <-chan pubsub.Event[workspace.DeckChange] {
	sub := w.changes.Subscribe(ctx)
	w.once.Do(func() { close(w.ready) })
	return sub
}
func (w *highlightWorkspace) Highlights(_ context.Context, sid string) (*pb.HighlightsResponse, error) {
	if sid != "s1" {
		return nil, workspace.ErrNotFound
	}
	return w.store.Snapshot(sid)
}
func (w *highlightWorkspace) HighlightAction(_ context.Context, r *pb.HighlightActionRequest) (*pb.HighlightItem, error) {
	if r.SessionId != "s1" {
		return nil, workspace.ErrNotFound
	}
	return w.store.Action(highlights.Actor{User: true}, r, "")
}

func TestHighlightsRPCLiveEventsSnapshotsAndPermissions(t *testing.T) {
	w := &highlightWorkspace{changes: pubsub.NewBroker[workspace.DeckChange](), ready: make(chan struct{})}
	dir := t.TempDir()
	w.store = highlights.New(dir, func(c highlights.Change) {
		kind := workspace.ChangeHighlight
		if c.Summary != nil {
			kind = workspace.ChangeSummary
		}
		w.changes.PublishMustDeliver(context.Background(), pubsub.UpdatedEvent, workspace.DeckChange{Kind: kind, SessionID: c.SessionID, Highlight: c.Item, Summary: c.Summary})
	})
	srv := New(remotetest.New(), "secret").WithWorkspace(w)
	t.Cleanup(srv.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	go srv.Run(ctx)
	<-w.ready
	listener, sock, closeSock, err := Listen(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(closeSock)
	grpcServer := srv.GRPC()
	go func() { _ = grpcServer.Serve(listener) }()
	t.Cleanup(grpcServer.Stop)
	cc, client, err := Dial(sock, "secret")
	require.NoError(t, err)
	t.Cleanup(func() { _ = cc.Close() })
	info, err := client.Info(ctx, &pb.Empty{})
	require.NoError(t, err)
	require.Contains(t, info.Capabilities, "Highlights")
	require.Contains(t, info.Capabilities, "HighlightAction")
	stream, err := client.Events(ctx, &pb.Subscribe{})
	require.NoError(t, err)
	ready, err := stream.Recv()
	require.NoError(t, err)
	require.NotNil(t, ready.GetReady())
	actor := highlights.Actor{Agent: "codex", Turn: "actual-turn"}
	created, err := w.store.Action(actor, &pb.HighlightActionRequest{SessionId: "s1", Action: "add", Key: "deploy", Kind: "needs_you", Title: "Choose a deployment target"}, "")
	require.NoError(t, err)
	event, err := stream.Recv()
	require.NoError(t, err)
	require.Equal(t, created.Id, event.GetHighlightChanged().Id)
	seq := event.Seq
	seen, err := client.HighlightAction(ctx, &pb.HighlightActionRequest{SessionId: "s1", Action: "seen", Ids: []string{created.Id}})
	require.NoError(t, err)
	require.False(t, seen.Unread)
	require.Equal(t, "open", seen.State)
	event, err = stream.Recv()
	require.NoError(t, err)
	require.False(t, event.GetHighlightChanged().Unread)
	_, err = w.store.Summary("s1", actor, "Waiting for the deployment choice", nil)
	require.NoError(t, err)
	event, err = stream.Recv()
	require.NoError(t, err)
	require.Equal(t, "codex", event.GetSummaryChanged().Agent)
	resolved, err := client.HighlightAction(ctx, &pb.HighlightActionRequest{SessionId: "s1", Action: "resolve", Id: created.Id})
	require.NoError(t, err)
	require.Equal(t, "resolved", resolved.State)
	event, err = stream.Recv()
	require.NoError(t, err)
	require.Equal(t, "resolved", event.GetHighlightChanged().State)
	snapshot, err := client.Highlights(ctx, &pb.SessionRef{SessionId: "s1"})
	require.NoError(t, err)
	require.Len(t, snapshot.Items, 1)
	require.Len(t, snapshot.Summaries, 1)
	reloaded, err := highlights.New(dir, nil).Snapshot("s1")
	require.NoError(t, err)
	require.Equal(t, "resolved", reloaded.Items[0].State)
	require.False(t, reloaded.Items[0].Unread)
	_, err = client.HighlightAction(ctx, &pb.HighlightActionRequest{SessionId: "other", Action: "resolve", Id: created.Id})
	require.Equal(t, codes.NotFound, status.Code(err))
	_, err = client.HighlightAction(ctx, &pb.HighlightActionRequest{SessionId: "s1", Action: "seen", Ids: []string{created.Id, "unknown"}})
	require.Equal(t, codes.NotFound, status.Code(err))
	// Replay contains data deltas, unlike live-only browser commands.
	replay, err := client.Events(ctx, &pb.Subscribe{Epoch: ready.GetReady().Epoch, Seq: seq})
	require.NoError(t, err)
	_, err = replay.Recv()
	require.NoError(t, err)
	event, err = replay.Recv()
	require.NoError(t, err)
	require.NotNil(t, event.GetHighlightChanged())
	require.False(t, event.GetHighlightChanged().Unread)
}
