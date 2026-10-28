package rpc

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/dev-dami/deck/internal/deck/browser"
	"github.com/dev-dami/deck/pkg/remote/remotetest"
	"github.com/dev-dami/deck/pkg/rpc/deckv1"
)

type browserWS struct{ b *browser.Broker }

func (w browserWS) Browser() *browser.Broker { return w.b }

// browserRig serves a fake backend with ws as its workspace (nil: none) and
// dials it.
func browserRig(t *testing.T, ws any) (deckv1.DeckClient, *browser.Broker) {
	t.Helper()
	srv := New(remotetest.New(), "secret")
	var b *browser.Broker
	if ws != nil {
		srv.WithWorkspace(ws)
		b = ws.(browserWS).b
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
	t.Cleanup(func() { g.Stop(); closeSock() })
	cc, c, err := Dial(sock, "secret")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cc.Close() })
	return c, b
}

// attach opens a BrowserHost stream and waits for the headers, which say the
// window is attached.
func attach(t *testing.T, c deckv1.DeckClient, id string, host bool) (deckv1.Deck_BrowserHostClient, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	st, err := c.BrowserHost(ctx, &deckv1.BrowserAttach{HostId: id, Host: host})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.Header(); err != nil {
		t.Fatal(err)
	}
	return st, cancel
}

func TestBrowserChannel(t *testing.T) {
	c, b := browserRig(t, browserWS{browser.NewBroker()})

	info, err := c.Info(ctxT(t), &deckv1.Empty{})
	if err != nil || !slices.Contains(info.GetCapabilities(), "BrowserHost") {
		t.Fatalf("capabilities = %v, %v", info.GetCapabilities(), err)
	}

	// No window yet: the engine says so.
	if _, err := b.Do(ctxT(t), browser.Request{Agent: "claude2", Op: "tabs"}); !errors.Is(err, browser.ErrNoBrowser) {
		t.Fatalf("err = %v, want ErrNoBrowser", err)
	}

	st, _ := attach(t, c, "w1", true)
	type out struct {
		res browser.Result
		err error
	}
	done := make(chan out, 1)
	go func() {
		r, err := b.Do(ctxT(t), browser.Request{Agent: "claude2", Name: "claude", Kind: "claude", Op: "click", Args: []byte(`{"ref":"e12"}`), Allowed: []string{"https://a.com"}, Timeout: 3 * time.Second})
		done <- out{r, err}
	}()
	req, err := st.Recv()
	if err != nil {
		t.Fatal(err)
	}
	if req.GetOp() != "click" || req.GetAgent() != "claude2" || req.GetName() != "claude" || req.GetArgs() != `{"ref":"e12"}` ||
		!slices.Equal(req.GetAllowed(), []string{"https://a.com"}) || req.GetTimeoutMs() != 3000 || req.GetId() == "" {
		t.Fatalf("request = %v", req)
	}
	if _, err := c.BrowserReply(ctxT(t), &deckv1.BrowserResult{HostId: "w1", Id: req.GetId(), Text: "clicked", Image: []byte("img"), Mime: "image/png"}); err != nil {
		t.Fatal(err)
	}
	got := <-done
	if got.err != nil || got.res.Text != "clicked" || string(got.res.Image) != "img" || got.res.Mime != "image/png" {
		t.Fatalf("result = %+v", got)
	}

	// A late or repeated answer is refused; so is one from another window.
	_, err = c.BrowserReply(ctxT(t), &deckv1.BrowserResult{HostId: "w1", Id: req.GetId(), Text: "again"})
	if status.Code(err) != codes.NotFound {
		t.Fatalf("repeated reply: %v", err)
	}
}

func TestBrowserOnlyTheHostWindowGetsRequests(t *testing.T) {
	c, b := browserRig(t, browserWS{browser.NewBroker()})
	host, _ := attach(t, c, "host", true)
	other, _ := attach(t, c, "other", false)

	go func() {
		r, _ := b.Do(ctxT(t), browser.Request{Agent: "a", Op: "tabs"})
		_ = r
	}()
	req, err := host.Recv()
	if err != nil {
		t.Fatal(err)
	}
	// The other window cannot answer a request it never got.
	if _, err := c.BrowserReply(ctxT(t), &deckv1.BrowserResult{HostId: "other", Id: req.GetId(), Text: "forged"}); status.Code(err) != codes.NotFound {
		t.Fatalf("forged reply: %v", err)
	}
	if _, err := c.BrowserReply(ctxT(t), &deckv1.BrowserResult{HostId: "host", Id: req.GetId(), Text: "ok"}); err != nil {
		t.Fatal(err)
	}
	recv := make(chan struct{})
	go func() {
		if _, err := other.Recv(); err == nil {
			close(recv)
		}
	}()
	select {
	case <-recv:
		t.Fatal("a window that is not the host received a request")
	case <-time.After(100 * time.Millisecond):
	}
}

func TestBrowserTimeoutAndDetach(t *testing.T) {
	c, b := browserRig(t, browserWS{browser.NewBroker()})
	st, cancel := attach(t, c, "w1", true)

	// The window never answers: the call times out and frees its slot.
	_, err := b.Do(ctxT(t), browser.Request{Agent: "a", Op: "wait", Timeout: 100 * time.Millisecond})
	if err == nil || errors.Is(err, browser.ErrNoBrowser) {
		t.Fatalf("err = %v, want a timeout", err)
	}
	if _, err := st.Recv(); err != nil {
		t.Fatal(err) // it did get the request
	}

	// Closing the window fails what it had not answered and ends the host.
	done := make(chan browser.Result, 1)
	go func() {
		r, _ := b.Do(ctxT(t), browser.Request{Agent: "a", Op: "wait", Timeout: 5 * time.Second})
		done <- r
	}()
	if _, err := st.Recv(); err != nil {
		t.Fatal(err)
	}
	cancel()
	select {
	case r := <-done:
		if !r.IsError {
			t.Fatalf("result = %+v", r)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("a request outlived its window")
	}
	waitHosted := time.After(3 * time.Second)
	for {
		if hosted, _ := b.Hosted(); !hosted {
			break
		}
		select {
		case <-waitHosted:
			t.Fatal("the window is still attached after it closed")
		case <-time.After(10 * time.Millisecond):
		}
	}
	if _, err := b.Do(ctxT(t), browser.Request{Agent: "a", Op: "tabs"}); !errors.Is(err, browser.ErrNoBrowser) {
		t.Fatalf("after detach: %v", err)
	}
}

func TestBrowserNeedsAWorkspaceWithOne(t *testing.T) {
	c, _ := browserRig(t, nil)
	st, err := c.BrowserHost(ctxT(t), &deckv1.BrowserAttach{HostId: "w1", Host: true})
	if err == nil {
		_, err = st.Recv()
	}
	if status.Code(err) != codes.Unimplemented {
		t.Fatalf("err = %v, want Unimplemented", err)
	}
	if _, err := c.BrowserReply(ctxT(t), &deckv1.BrowserResult{HostId: "w1", Id: "b1"}); status.Code(err) != codes.Unimplemented {
		t.Fatalf("reply: %v", err)
	}
	info, _ := c.Info(ctxT(t), &deckv1.Empty{})
	if slices.Contains(info.GetCapabilities(), "BrowserHost") {
		t.Fatal("a Deck without a browser channel lists it")
	}
}

func TestBrowserRefusesAnonymousWindows(t *testing.T) {
	c, _ := browserRig(t, browserWS{browser.NewBroker()})
	st, err := c.BrowserHost(ctxT(t), &deckv1.BrowserAttach{})
	if err == nil {
		_, err = st.Recv()
	}
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("err = %v", err)
	}
}
