package rpc

import (
	"context"
	"github.com/dev-dami/deck/internal/deck/browser"
	"github.com/dev-dami/deck/internal/workspace"
	pb "github.com/dev-dami/deck/pkg/rpc/deckv1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

func (s *Server) withBrowser(ws any) {
	w, ok := ws.(workspace.DesktopBrowser)
	if !ok {
		return
	}
	s.browserWorkspace = w
	s.browserCapability = NewToken()
	s.browser = browser.New(s.browserCapability, w.ApproveBrowserOrigin, func(state *pb.BrowserStateResponse) {
		s.feed.publish(&pb.Event{SessionId: state.SessionId, Kind: &pb.Event_BrowserChanged{BrowserChanged: s.forUser(state)}})
	})
	w.InstallBrowser(s.browser.Action)
}

// browserNamer is a workspace that knows the name each agent is listed under.
type browserNamer interface {
	BrowserAgentName(sid, id string) string
}

// forUser is the state as the person's window shows it: the broker and the
// desktop controller know a tab's owner by agent id, the roster by name.
func (s *Server) forUser(state *pb.BrowserStateResponse) *pb.BrowserStateResponse {
	namer, ok := s.browserWorkspace.(browserNamer)
	if !ok || state == nil {
		return state
	}
	out := proto.Clone(state).(*pb.BrowserStateResponse)
	for _, t := range out.Tabs {
		t.Owner = namer.BrowserAgentName(out.SessionId, t.Owner)
	}
	return out
}

// BrowserCapability is emitted only on the private parent stdout handshake.
// It must never be added to the engine or agent environment or Info response.
func (s *Server) BrowserCapability() string { return s.browserCapability }
func (s *Server) BrowserState(ctx context.Context, r *pb.SessionRef) (*pb.BrowserStateResponse, error) {
	if s.browser == nil {
		return nil, errNoCrew
	}
	if err := s.browserWorkspace.CheckBrowserSession(ctx, r.GetSessionId()); err != nil {
		return nil, roomFail(err)
	}
	return s.forUser(s.browser.State(r.SessionId)), nil
}
func (s *Server) BrowserAction(ctx context.Context, r *pb.BrowserActionRequest) (*pb.BrowserActionResponse, error) {
	if s.browser == nil {
		return nil, errNoCrew
	}
	if err := s.browserWorkspace.CheckBrowserSession(ctx, r.GetSessionId()); err != nil {
		return nil, roomFail(err)
	}
	out := s.browser.Action(ctx, r.SessionId, "", r.Command)
	if out.Tab != nil {
		shown := s.forUser(&pb.BrowserStateResponse{SessionId: r.SessionId, Tabs: []*pb.BrowserTab{out.Tab}})
		out = proto.Clone(out).(*pb.BrowserActionResponse)
		out.Tab = shown.Tabs[0]
	}
	return out, nil
}
func (s *Server) RegisterBrowserController(_ context.Context, r *pb.RegisterBrowserControllerRequest) (*pb.BrowserController, error) {
	if s.browser == nil {
		return nil, errNoCrew
	}
	c, err := s.browser.Register(r.GetDesktopCapability(), r.GetSessionId())
	if err != nil {
		return nil, status.Error(codes.PermissionDenied, err.Error())
	}
	return c, nil
}
func (s *Server) UnregisterBrowserController(_ context.Context, r *pb.BrowserControllerRef) (*pb.Empty, error) {
	if s.browser == nil {
		return nil, errNoCrew
	}
	if err := s.browser.Disconnect(r); err != nil {
		return nil, status.Error(codes.PermissionDenied, err.Error())
	}
	return empty, nil
}
func (s *Server) BrowserResult(_ context.Context, r *pb.BrowserResultRequest) (*pb.Empty, error) {
	if s.browser == nil {
		return nil, errNoCrew
	}
	if err := s.browser.Complete(r.GetController(), r.GetId(), r.GetResponse()); err != nil {
		return nil, status.Error(codes.FailedPrecondition, err.Error())
	}
	return empty, nil
}
func (s *Server) BrowserUpdate(ctx context.Context, r *pb.BrowserUpdateRequest) (*pb.Empty, error) {
	if s.browser == nil {
		return nil, errNoCrew
	}
	if err := s.browserWorkspace.CheckBrowserSession(ctx, r.GetState().GetSessionId()); err != nil {
		return nil, roomFail(err)
	}
	if err := s.browser.Update(r.GetController(), r.GetState()); err != nil {
		return nil, status.Error(codes.PermissionDenied, err.Error())
	}
	return empty, nil
}
func (s *Server) BrowserOrigin(ctx context.Context, r *pb.BrowserOriginRequest) (*pb.BrowserOriginResponse, error) {
	if s.browser == nil {
		return nil, errNoCrew
	}
	allow, err := s.browser.ApproveOrigin(ctx, r)
	if err != nil {
		return nil, status.Error(codes.PermissionDenied, err.Error())
	}
	return &pb.BrowserOriginResponse{Allow: allow}, nil
}
func (s *Server) browserEvents(r *pb.BrowserControllerRef, stream pb.Deck_EventsServer) error {
	if s.browser == nil {
		return errNoCrew
	}
	requests, done, err := s.browser.Attach(r)
	if err != nil {
		return status.Error(codes.PermissionDenied, err.Error())
	}
	defer s.browser.Disconnect(r)
	if err := stream.Send(&pb.Event{Kind: &pb.Event_Ready{Ready: &pb.Ready{}}}); err != nil {
		return err
	}
	for {
		select {
		case <-stream.Context().Done():
			return nil
		case <-done:
			return nil
		case request := <-requests:
			if err := stream.Send(&pb.Event{SessionId: request.SessionId, Kind: &pb.Event_BrowserRequest{BrowserRequest: request}}); err != nil {
				return err
			}
		}
	}
}
