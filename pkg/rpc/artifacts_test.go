package rpc

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dev-dami/deck/internal/deck/artifacts"
	"github.com/dev-dami/deck/internal/pubsub"
	"github.com/dev-dami/deck/internal/workspace"
	"github.com/dev-dami/deck/pkg/remote/remotetest"
	pb "github.com/dev-dami/deck/pkg/rpc/deckv1"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type artifactWorkspace struct {
	store   *artifacts.Store
	changes *pubsub.Broker[workspace.DeckChange]
	ready   chan struct{}
	once    sync.Once
}

func (w *artifactWorkspace) SubscribeDeck(ctx context.Context) <-chan pubsub.Event[workspace.DeckChange] {
	out := w.changes.Subscribe(ctx)
	w.once.Do(func() { close(w.ready) })
	return out
}
func (w *artifactWorkspace) Artifacts(_ context.Context, r *pb.ArtifactsRequest) (*pb.ArtifactsResponse, error) {
	if r.SessionId != "s1" {
		return nil, workspace.ErrNotFound
	}
	return w.store.List(r)
}
func (w *artifactWorkspace) ArtifactRevisions(_ context.Context, r *pb.ArtifactRevisionsRequest) (*pb.ArtifactRevisionsResponse, error) {
	if r.SessionId != "s1" {
		return nil, workspace.ErrNotFound
	}
	return w.store.Revisions(r)
}
func (w *artifactWorkspace) ArtifactRead(_ context.Context, r *pb.ArtifactReadRequest) (*pb.ArtifactReadResponse, error) {
	if r.SessionId != "s1" {
		return nil, workspace.ErrNotFound
	}
	return w.store.Read(r)
}
func (w *artifactWorkspace) ArtifactAction(_ context.Context, r *pb.ArtifactActionRequest) (*pb.ArtifactActionResponse, error) {
	if r.SessionId != "s1" {
		return nil, workspace.ErrNotFound
	}
	return w.store.Action(artifacts.Actor{User: true}, r)
}
func (w *artifactWorkspace) ReportArtifactBuild(_ context.Context, r *pb.ReportArtifactBuildRequest) (*pb.ArtifactBuildReport, error) {
	if r.SessionId != "s1" {
		return nil, workspace.ErrNotFound
	}
	return w.store.Report(r)
}

func TestArtifactsRPCSocketEventsConflictsAndBuildFeedback(t *testing.T) {
	w := &artifactWorkspace{changes: pubsub.NewBroker[workspace.DeckChange](), ready: make(chan struct{})}
	w.store = artifacts.New(t.TempDir(), func(sid string, i *pb.ArtifactSummary) {
		w.changes.PublishMustDeliver(context.Background(), pubsub.UpdatedEvent, workspace.DeckChange{Kind: workspace.ChangeArtifact, SessionID: sid, Artifact: i})
	})
	srv := New(remotetest.New(), "secret").WithWorkspace(w)
	t.Cleanup(srv.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	go srv.Run(ctx)
	<-w.ready
	listener, sock, closeSock, err := Listen(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(closeSock)
	server := srv.GRPC()
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)
	cc, client, err := Dial(sock, "secret")
	require.NoError(t, err)
	t.Cleanup(func() { _ = cc.Close() })
	info, err := client.Info(ctx, &pb.Empty{})
	require.NoError(t, err)
	for _, method := range artifactMethods {
		require.Contains(t, info.Capabilities, method)
	}
	stream, err := client.Events(ctx, &pb.Subscribe{})
	require.NoError(t, err)
	ready, err := stream.Recv()
	require.NoError(t, err)
	require.NotNil(t, ready.GetReady())
	r := &pb.ArtifactActionRequest{SessionId: "s1", Action: "create", Key: "app", Title: "App", Kind: "react", Entrypoint: "App.tsx", Files: []*pb.ArtifactFileInput{{Path: "App.tsx", Content: strings.Repeat("x", artifacts.MaxFileBytes)}}}
	created, err := client.ArtifactAction(ctx, r)
	require.NoError(t, err)
	id := created.Artifact.Id
	event, err := stream.Recv()
	require.NoError(t, err)
	require.Equal(t, id, event.GetArtifactChanged().Id)
	require.Equal(t, uint32(1), event.GetArtifactChanged().CurrentRevision)
	seq := event.Seq
	read, err := client.ArtifactRead(ctx, &pb.ArtifactReadRequest{SessionId: "s1", Id: id, Path: "App.tsx", Limit: artifacts.MaxRead})
	require.NoError(t, err)
	require.Len(t, read.Content, artifacts.MaxFileBytes)
	require.True(t, read.Eof)
	r.Action = "update"
	r.Id = id
	r.Key = ""
	r.Kind = ""
	r.BaseRevision = 1
	updated, err := client.ArtifactAction(ctx, r)
	require.NoError(t, err)
	require.Equal(t, uint32(2), updated.Artifact.CurrentRevision)
	_, err = client.ArtifactAction(ctx, r)
	require.Equal(t, codes.Aborted, status.Code(err))
	details := status.Convert(err).Details()
	require.Len(t, details, 1)
	conflict, ok := details[0].(*pb.ArtifactConflict)
	require.True(t, ok)
	require.Equal(t, uint32(2), conflict.CurrentRevision)
	event, err = stream.Recv()
	require.NoError(t, err)
	require.Equal(t, uint32(2), event.GetArtifactChanged().CurrentRevision)
	_, err = client.ReportArtifactBuild(ctx, &pb.ReportArtifactBuildRequest{SessionId: "s1", Id: id, Revision: 2, ReportId: "build-1", RuntimeVersion: "react-19.2.0", ClientInstance: "desktop", Attempt: 1, Phase: "build", Diagnostics: []*pb.ArtifactDiagnostic{{File: "App.tsx", Line: 1, Message: "Expected an export"}}})
	require.NoError(t, err)
	event, err = stream.Recv()
	require.NoError(t, err)
	require.Equal(t, uint32(2), event.GetArtifactChanged().CurrentRevision)
	require.Equal(t, uint32(3), event.GetArtifactChanged().StateVersion)
	require.Equal(t, "error", event.GetArtifactChanged().BuildStatus)
	meta, err := client.ArtifactRead(ctx, &pb.ArtifactReadRequest{SessionId: "s1", Id: id, Revision: 2})
	require.NoError(t, err)
	require.Len(t, meta.Builds, 1)
	require.Equal(t, "Expected an export", meta.Builds[0].Diagnostics[0].Message)
	_, err = client.ArtifactRead(ctx, &pb.ArtifactReadRequest{SessionId: "other", Id: id})
	require.Equal(t, codes.NotFound, status.Code(err))
	list, err := client.Artifacts(ctx, &pb.ArtifactsRequest{SessionId: "s1"})
	require.NoError(t, err)
	require.Len(t, list.Items, 1)
	history, err := client.ArtifactRevisions(ctx, &pb.ArtifactRevisionsRequest{SessionId: "s1", Id: id})
	require.NoError(t, err)
	require.Len(t, history.Items, 2)
	replay, err := client.Events(ctx, &pb.Subscribe{Epoch: ready.GetReady().Epoch, Seq: seq})
	require.NoError(t, err)
	_, err = replay.Recv()
	require.NoError(t, err)
	event, err = replay.Recv()
	require.NoError(t, err)
	require.Equal(t, uint32(2), event.GetArtifactChanged().CurrentRevision)
	badConn, badClient, err := Dial(sock, "wrong-token")
	require.NoError(t, err)
	defer badConn.Close()
	_, err = badClient.Artifacts(ctx, &pb.ArtifactsRequest{SessionId: "s1"})
	require.Equal(t, codes.Unauthenticated, status.Code(err))
}

func TestArtifactsCapabilityUnavailableWithoutWorkspace(t *testing.T) {
	s := New(remotetest.New(), "secret")
	defer s.Close()
	_, err := s.Artifacts(t.Context(), &pb.ArtifactsRequest{SessionId: "s1"})
	require.Equal(t, codes.Unimplemented, status.Code(err))
	require.NotContains(t, s.capabilities(), "Artifacts")
}
