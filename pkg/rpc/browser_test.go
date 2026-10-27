package rpc

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"testing"
	"time"

	"github.com/dev-dami/deck/pkg/remote/remotetest"
	pb "github.com/dev-dami/deck/pkg/rpc/deckv1"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type browserWorkspace struct {
	run func(context.Context, string, string, *pb.BrowserCommand) *pb.BrowserActionResponse
}

func (w *browserWorkspace) InstallBrowser(run func(context.Context, string, string, *pb.BrowserCommand) *pb.BrowserActionResponse) {
	w.run = run
}
func (w *browserWorkspace) CheckBrowserSession(_ context.Context, sid string) error {
	if sid != "s1" {
		return invalid("unknown room")
	}
	return nil
}
func (w *browserWorkspace) ApproveBrowserOrigin(context.Context, string, string, string) bool {
	return true
}

func TestBrowserRPCPrivateLiveEventsAndImageResult(t *testing.T) {
	w := &browserWorkspace{}
	srv := New(remotetest.New(), "ordinary-token").WithWorkspace(w)
	t.Cleanup(srv.Close)
	l, sock, closeSock, err := Listen(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(closeSock)
	g := srv.GRPC()
	go func() { _ = g.Serve(l) }()
	t.Cleanup(g.Stop)
	cc, c, err := Dial(sock, "ordinary-token")
	require.NoError(t, err)
	t.Cleanup(func() { _ = cc.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err = c.RegisterBrowserController(ctx, &pb.RegisterBrowserControllerRequest{DesktopCapability: "ordinary-token"})
	require.Equal(t, codes.PermissionDenied, status.Code(err))
	ctrl, err := c.RegisterBrowserController(ctx, &pb.RegisterBrowserControllerRequest{DesktopCapability: srv.BrowserCapability()})
	require.NoError(t, err)
	ref := &pb.BrowserControllerRef{Id: ctrl.Id, Generation: ctrl.Generation, DesktopCapability: srv.BrowserCapability()}
	// A normal event subscription cannot receive private commands, including
	// when it asks to replay from zero.
	normal, ready := srv.feed.subscribe("", 0)
	defer srv.feed.unsubscribe(normal)
	require.NotNil(t, ready)
	stream, err := c.Events(ctx, &pb.Subscribe{BrowserController: ref})
	require.NoError(t, err)
	first, err := stream.Recv()
	require.NoError(t, err)
	require.NotNil(t, first.GetReady())
	out := make(chan *pb.BrowserActionResponse, 1)
	go func() {
		out <- w.run(ctx, "s1", "codex", &pb.BrowserCommand{Action: "open", Url: "http://localhost:3000"})
	}()
	event, err := stream.Recv()
	require.NoError(t, err)
	request := event.GetBrowserRequest()
	require.NotNil(t, request)
	require.Equal(t, "codex", request.Owner)
	select {
	case e := <-normal.ch:
		t.Fatalf("private command leaked into replay feed: %v", e)
	default:
	}
	state := &pb.BrowserStateResponse{SessionId: "s1", Tabs: []*pb.BrowserTab{{Id: "tab", SessionId: "s1", Owner: "codex"}}}
	_, err = c.BrowserUpdate(ctx, &pb.BrowserUpdateRequest{Controller: ref, State: state})
	require.NoError(t, err)
	_, err = c.BrowserResult(ctx, &pb.BrowserResultRequest{Controller: ref, Id: request.Id, Response: &pb.BrowserActionResponse{Tab: state.Tabs[0], Result: &pb.OpResult{Text: "opened"}}})
	require.NoError(t, err)
	require.Equal(t, "opened", (<-out).Result.Text)
	_, err = c.BrowserResult(ctx, &pb.BrowserResultRequest{Controller: ref, Id: request.Id, Response: &pb.BrowserActionResponse{}})
	require.Equal(t, codes.FailedPrecondition, status.Code(err))
	got, err := c.BrowserState(ctx, &pb.SessionRef{SessionId: "s1"})
	require.NoError(t, err)
	require.True(t, got.Available)
	require.Len(t, got.Tabs, 1)
	require.Equal(t, "s1", (<-normal.ch).GetBrowserChanged().SessionId)
	go func() { out <- w.run(ctx, "s1", "codex", &pb.BrowserCommand{Action: "screenshot", TabId: "tab"}) }()
	shot, err := stream.Recv()
	require.NoError(t, err)
	var pngBytes bytes.Buffer
	require.NoError(t, png.Encode(&pngBytes, image.NewRGBA(image.Rect(0, 0, 2, 2))))
	_, err = c.BrowserResult(ctx, &pb.BrowserResultRequest{Controller: ref, Id: shot.GetBrowserRequest().Id, Response: &pb.BrowserActionResponse{Result: &pb.OpResult{Images: []*pb.OpImage{{MimeType: "image/png", Data: pngBytes.Bytes()}}}}})
	require.NoError(t, err)
	require.Equal(t, pngBytes.Bytes(), (<-out).Result.Images[0].Data)
	_, err = c.BrowserUpdate(ctx, &pb.BrowserUpdateRequest{Controller: ref, State: &pb.BrowserStateResponse{SessionId: "s1"}})
	require.NoError(t, err)
	require.Empty(t, (<-normal.ch).GetBrowserChanged().Tabs)
	_, err = c.UnregisterBrowserController(ctx, ref)
	require.NoError(t, err)
	got, err = c.BrowserState(ctx, &pb.SessionRef{SessionId: "s1"})
	require.NoError(t, err)
	require.False(t, got.Available)
}
