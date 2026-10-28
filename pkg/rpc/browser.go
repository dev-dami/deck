package rpc

import (
	"context"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/dev-dami/deck/internal/deck/browser"
	"github.com/dev-dami/deck/pkg/rpc/deckv1"
)

// The shared browser's reverse channel. Agents call browser.* ops on the
// engine, the browser lives in the desktop app, and the desktop is the gRPC
// client: it opens BrowserHost, receives the ops as a stream of requests and
// answers each with BrowserReply. The channel exists on this local socket
// only; a paired or remote Deck has no way to reach this machine's browser.

// browserMethods are served when the workspace has a browser channel; Info's
// capabilities list them.
var browserMethods = []string{"BrowserHost", "BrowserReply"}

// browserDesk is the workspace's channel to the desktop's browser.
type browserDesk interface {
	Browser() *browser.Broker
}

// maxBrowserImage bounds a screenshot a window sends back.
const maxBrowserImage = 8 << 20

func (s *Server) browserBroker() (*browser.Broker, error) {
	if s.crew == nil || s.crew.browser == nil {
		return nil, errNoCrew
	}
	b := s.crew.browser.Browser()
	if b == nil {
		return nil, errNoCrew
	}
	return b, nil
}

func (s *Server) BrowserHost(r *deckv1.BrowserAttach, stream deckv1.Deck_BrowserHostServer) error {
	b, err := s.browserBroker()
	if err != nil {
		return err
	}
	if r.GetHostId() == "" || len(r.GetHostId()) > 64 {
		return invalid("host_id is required")
	}
	ch, detach := b.Attach(r.GetHostId(), r.GetHost())
	defer detach()
	// The headers tell the window it is attached before any op comes.
	if err := stream.SendHeader(nil); err != nil {
		return err
	}
	ctx := stream.Context()
	for {
		select {
		case <-ctx.Done():
			return nil
		case req := <-ch:
			if err := stream.Send(toBrowserRequest(req)); err != nil {
				return err
			}
		}
	}
}

func (s *Server) BrowserReply(_ context.Context, r *deckv1.BrowserResult) (*deckv1.Empty, error) {
	b, err := s.browserBroker()
	if err != nil {
		return nil, err
	}
	if len(r.GetImage()) > maxBrowserImage {
		return nil, status.Error(codes.ResourceExhausted, "the screenshot is too large")
	}
	if !b.Complete(r.GetHostId(), browser.Result{
		ID: r.GetId(), Text: r.GetText(), IsError: r.GetIsError(), Image: r.GetImage(), Mime: r.GetMime(),
	}) {
		// Late (the op timed out) or not this window's: not an error for the app.
		return nil, status.Error(codes.NotFound, "no such request")
	}
	return empty, nil
}

func toBrowserRequest(r browser.Request) *deckv1.BrowserRequest {
	return &deckv1.BrowserRequest{
		Id: r.ID, Agent: r.Agent, Name: r.Name, Kind: r.Kind, Op: r.Op, Args: string(r.Args),
		Free: r.Free, Allowed: r.Allowed, TimeoutMs: int32(r.Timeout / time.Millisecond),
	}
}
