package browser

import (
	"context"
	"testing"
	"time"

	pb "github.com/dev-dami/deck/pkg/rpc/deckv1"
	"github.com/stretchr/testify/require"
)

func controller(t *testing.T, b *Broker) (*pb.BrowserControllerRef, <-chan *pb.BrowserRequest) {
	t.Helper()
	c, err := b.Register("secret", "")
	require.NoError(t, err)
	ref := &pb.BrowserControllerRef{Id: c.Id, Generation: c.Generation, DesktopCapability: "secret"}
	requests, _, err := b.Attach(ref)
	require.NoError(t, err)
	t.Cleanup(b.Close)
	return ref, requests
}
func receive(t *testing.T, requests <-chan *pb.BrowserRequest) *pb.BrowserRequest {
	t.Helper()
	select {
	case r := <-requests:
		return r
	case <-time.After(time.Second):
		t.Fatal("no browser request")
		return nil
	}
}
func action(b *Broker, owner string, c *pb.BrowserCommand) <-chan *pb.BrowserActionResponse {
	out := make(chan *pb.BrowserActionResponse, 1)
	go func() { out <- b.Action(context.Background(), "room", owner, c) }()
	return out
}
func reply(t *testing.T, out <-chan *pb.BrowserActionResponse) *pb.BrowserActionResponse {
	t.Helper()
	select {
	case r := <-out:
		return r
	case <-time.After(time.Second):
		t.Fatal("browser action hung")
		return nil
	}
}
func tabState(t *testing.T, b *Broker, ref *pb.BrowserControllerRef) {
	t.Helper()
	require.NoError(t, b.Update(ref, &pb.BrowserStateResponse{SessionId: "room", Tabs: []*pb.BrowserTab{{Id: "tab", SessionId: "room", Owner: "codex", SnapshotVersion: "v1"}}}))
}

func TestControllerCapabilityLiveStreamAndReconnect(t *testing.T) {
	b := New("secret", nil, nil)
	require.Equal(t, "browser_unavailable", b.Action(context.Background(), "room", "codex", &pb.BrowserCommand{Action: "open", Url: "http://localhost:3000"}).ErrorCode)
	_, err := b.Register("ordinary bearer", "")
	require.ErrorIs(t, err, ErrController)
	c, err := b.Register("secret", "")
	require.NoError(t, err)
	require.False(t, b.State("room").Available)
	ref := &pb.BrowserControllerRef{Id: c.Id, Generation: c.Generation, DesktopCapability: "secret"}
	requests, _, err := b.Attach(ref)
	require.NoError(t, err)
	_, _, err = b.Attach(ref)
	require.ErrorIs(t, err, ErrController)
	out := action(b, "codex", &pb.BrowserCommand{Action: "open", Url: "http://localhost:3000"})
	r := receive(t, requests)
	require.NoError(t, b.Disconnect(ref))
	require.Equal(t, "browser_unavailable", reply(t, out).ErrorCode)
	fresh, stream := controller(t, b)
	require.NotEqual(t, ref.Generation, fresh.Generation)
	require.Error(t, b.Complete(ref, r.Id, result("", "old")))
	require.Error(t, b.Complete(fresh, r.Id, result("", "old")))
	select {
	case <-stream:
		t.Fatal("replayed a command after reconnect")
	default:
	}
}

func TestTabOwnershipReadSharingAndUserTakeover(t *testing.T) {
	b := New("secret", nil, nil)
	ref, requests := controller(t, b)
	tabState(t, b, ref)
	require.Equal(t, "not_owner", reply(t, action(b, "claude", &pb.BrowserCommand{Action: "close", TabId: "tab"})).ErrorCode)
	out := action(b, "claude", &pb.BrowserCommand{Action: "snapshot", TabId: "tab"})
	r := receive(t, requests)
	user := action(b, "", &pb.BrowserCommand{Action: "take_control", TabId: "tab"})
	require.True(t, receive(t, requests).Cancelled)
	takeover := receive(t, requests)
	require.Equal(t, "cancelled", reply(t, out).ErrorCode)
	require.Error(t, b.Complete(ref, r.Id, result("", "late read")))
	require.Equal(t, "user_in_control", reply(t, action(b, "codex", &pb.BrowserCommand{Action: "screenshot", TabId: "tab"})).ErrorCode)
	require.NoError(t, b.Complete(ref, takeover.Id, result("", "control")))
	require.Empty(t, reply(t, user).ErrorCode)
}

func TestApprovalDoesNotConsumeExecutionDeadlineAndDisconnectCancelsIt(t *testing.T) {
	entered := make(chan struct{}, 2)
	allow := make(chan bool, 1)
	b := New("secret", func(ctx context.Context, _, _, _ string) bool {
		entered <- struct{}{}
		select {
		case yes := <-allow:
			return yes
		case <-ctx.Done():
			return false
		}
	}, nil)
	b.execution = 20 * time.Millisecond
	ref, requests := controller(t, b)
	out := action(b, "codex", &pb.BrowserCommand{Action: "open", Url: "https://example.test"})
	<-entered
	select {
	case <-out:
		t.Fatal("approval used execution deadline")
	case <-time.After(50 * time.Millisecond):
	}
	allow <- true
	r := receive(t, requests)
	require.NoError(t, b.Complete(ref, r.Id, result("", "ok")))
	require.Empty(t, reply(t, out).ErrorCode)
	out = action(b, "codex", &pb.BrowserCommand{Action: "open", Url: "https://another.test"})
	<-entered
	require.NoError(t, b.Disconnect(ref))
	require.Equal(t, "browser_unavailable", reply(t, out).ErrorCode)
}

func TestDeadlineCancelsLiveCommandAndRejectsLateResult(t *testing.T) {
	b := New("secret", nil, nil)
	b.execution = 20 * time.Millisecond
	ref, requests := controller(t, b)
	out := action(b, "codex", &pb.BrowserCommand{Action: "open", Url: "http://127.0.0.1:3000"})
	r := receive(t, requests)
	require.Equal(t, "timeout", reply(t, out).ErrorCode)
	cancel := receive(t, requests)
	require.True(t, cancel.Cancelled)
	require.Equal(t, r.Id, cancel.Id)
	require.Error(t, b.Complete(ref, r.Id, result("", "late")))
}

func TestOriginApprovalPausesRunningCommand(t *testing.T) {
	entered := make(chan struct{})
	allow := make(chan bool)
	b := New("secret", func(ctx context.Context, _, _, _ string) bool {
		close(entered)
		select {
		case v := <-allow:
			return v
		case <-ctx.Done():
			return false
		}
	}, nil)
	b.execution = 40 * time.Millisecond
	ref, requests := controller(t, b)
	out := action(b, "codex", &pb.BrowserCommand{Action: "open", Url: "http://localhost:3000"})
	r := receive(t, requests)
	approved := make(chan bool, 1)
	go func() {
		ok, err := b.ApproveOrigin(context.Background(), &pb.BrowserOriginRequest{Controller: ref, RequestId: r.Id, SessionId: "room", Origin: "https://example.test"})
		approved <- ok && err == nil
	}()
	<-entered
	select {
	case <-out:
		t.Fatal("redirect approval used execution deadline")
	case <-time.After(70 * time.Millisecond):
	}
	allow <- true
	require.True(t, <-approved)
	require.NoError(t, b.Complete(ref, r.Id, result("", "ok")))
	require.Empty(t, reply(t, out).ErrorCode)
}

func TestOriginPolicy(t *testing.T) {
	for _, raw := range []string{"file:///secret", "chrome://settings", "devtools://x", "javascript:alert(1)", "https://user:pass@example.test", "data:text/html,x"} {
		_, _, err := Origin(raw)
		require.Error(t, err, raw)
	}
	for _, raw := range []string{"http://localhost:3000/path", "http://127.10.0.1:8000", "http://[::1]:3000"} {
		_, local, err := Origin(raw)
		require.NoError(t, err)
		require.True(t, local, raw)
	}
	origin, local, err := Origin("https://example.test/path")
	require.NoError(t, err)
	require.False(t, local)
	require.Equal(t, "https://example.test", origin)
}

func TestBrowserTabsHidesPageMetadataUnderUserControl(t *testing.T) {
	b := New("secret", nil, nil)
	ref, _ := controller(t, b)
	require.NoError(t, b.Update(ref, &pb.BrowserStateResponse{SessionId: "room", Tabs: []*pb.BrowserTab{{Id: "tab", SessionId: "room", Owner: "codex", UserInControl: true, Url: "https://private.test/account", Title: "private title"}}}))
	response := b.Action(context.Background(), "room", "claude", &pb.BrowserCommand{Action: "tabs"})
	require.Empty(t, response.ErrorCode)
	require.Contains(t, response.Result.Text, "tab")
	require.NotContains(t, response.Result.Text, "private")
}
