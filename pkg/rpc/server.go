// Package rpc serves Deck to a local desktop app over gRPC (proto/deck/v1):
// a thin layer over pkg/remote.Backend, the same engine adapter the phone's
// WebSocket uses, with a typed event feed that resumes by seq. It listens
// on a Unix socket and accepts only callers holding the per-launch token.
package rpc

import (
	"context"
	"crypto/subtle"
	"errors"
	"path"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/dev-dami/deck/internal/deck/gh"
	"github.com/dev-dami/deck/internal/usage"
	"github.com/dev-dami/deck/pkg/remote"
	"github.com/dev-dami/deck/pkg/remote/wire"
	"github.com/dev-dami/deck/pkg/rpc/deckv1"
)

const (
	// MaxImageBytes is the largest image Send accepts.
	MaxImageBytes = 16 << 20
	// MaxMessageBytes bounds one request: a few full-size images.
	MaxMessageBytes = 64 << 20
	defaultPage     = 50
	maxTitleRunes   = 200
)

// Server implements deckv1.DeckServer over a remote.Backend.
type Server struct {
	deckv1.UnimplementedDeckServer
	be    remote.Backend
	feed  *feed
	token string

	// The project (WithProject): where git runs and terminals start, and
	// Deck's usage record. Without it those calls fail or say unavailable.
	dir   string
	git   *gh.Client
	usage usage.Service
	terms *terminals
}

// New returns a server for be that admits callers presenting token.
func New(be remote.Backend, token string) *Server {
	return &Server{be: be, feed: newFeed(), token: token}
}

// Run pumps the backend's events into the feed until ctx ends.
func (s *Server) Run(ctx context.Context) {
	for ev := range s.be.Events(ctx) {
		if e, ok := toEvent(ev); ok {
			s.feed.publish(e)
		}
	}
}

// GRPC returns a grpc.Server with this service registered behind the token
// check.
func (s *Server) GRPC(opts ...grpc.ServerOption) *grpc.Server {
	opts = append([]grpc.ServerOption{
		grpc.MaxRecvMsgSize(MaxMessageBytes),
		grpc.MaxSendMsgSize(MaxMessageBytes),
		grpc.UnaryInterceptor(func(ctx context.Context, req any, _ *grpc.UnaryServerInfo, h grpc.UnaryHandler) (any, error) {
			if err := s.authorize(ctx); err != nil {
				return nil, err
			}
			return h(ctx, req)
		}),
		grpc.StreamInterceptor(func(srv any, ss grpc.ServerStream, _ *grpc.StreamServerInfo, h grpc.StreamHandler) error {
			if err := s.authorize(ss.Context()); err != nil {
				return err
			}
			return h(srv, ss)
		}),
	}, opts...)
	g := grpc.NewServer(opts...)
	deckv1.RegisterDeckServer(g, s)
	return g
}

func (s *Server) authorize(ctx context.Context) error {
	md, _ := metadata.FromIncomingContext(ctx)
	for _, v := range md.Get(TokenHeader) {
		got, ok := strings.CutPrefix(v, "Bearer ")
		if ok && subtle.ConstantTimeCompare([]byte(got), []byte(s.token)) == 1 {
			return nil
		}
	}
	return status.Error(codes.Unauthenticated, "missing or wrong token")
}

// fail maps a backend error to a status. The caller is the user's own
// desktop app, so the message is kept.
func fail(err error) error {
	if err == nil {
		return nil
	}
	var werr *wire.Error
	switch {
	case errors.As(err, &werr):
		return status.Error(wireCode(werr.Code), werr.Msg)
	case errors.Is(err, remote.ErrNotFound):
		return status.Error(codes.NotFound, "not found")
	case errors.Is(err, remote.ErrInvalid):
		return status.Error(codes.InvalidArgument, "the request was refused")
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return status.FromContextError(err).Err()
	}
	return status.Error(codes.Internal, err.Error())
}

func wireCode(c string) codes.Code {
	switch c {
	case wire.CodeNotFound:
		return codes.NotFound
	case wire.CodeInvalid:
		return codes.InvalidArgument
	case wire.CodeForbidden:
		return codes.PermissionDenied
	case wire.CodeUnauthorized:
		return codes.Unauthenticated
	case wire.CodeBusy:
		return codes.FailedPrecondition
	case wire.CodeTooLarge:
		return codes.ResourceExhausted
	}
	return codes.Internal
}

func invalid(msg string) error { return status.Error(codes.InvalidArgument, msg) }

func needSession(id string) error {
	if id == "" {
		return invalid("session_id is required")
	}
	return nil
}

var empty = &deckv1.Empty{}

func done(err error) (*deckv1.Empty, error) {
	if err != nil {
		return nil, fail(err)
	}
	return empty, nil
}

func title(t string) (string, error) {
	t = strings.TrimSpace(t)
	if len([]rune(t)) > maxTitleRunes {
		return "", invalid("the title is too long")
	}
	return t, nil
}

func (s *Server) Info(context.Context, *deckv1.Empty) (*deckv1.InfoResponse, error) {
	return toInfo(s.be.Info()), nil
}

func (s *Server) ListSessions(ctx context.Context, _ *deckv1.Empty) (*deckv1.ListSessionsResponse, error) {
	v, err := s.be.Sessions(ctx)
	if err != nil {
		return nil, fail(err)
	}
	return &deckv1.ListSessionsResponse{Sessions: each(v, toSession)}, nil
}

func (s *Server) CreateSession(ctx context.Context, r *deckv1.CreateSessionRequest) (*deckv1.Session, error) {
	t, err := title(r.GetTitle())
	if err != nil {
		return nil, err
	}
	v, err := s.be.CreateSession(ctx, t)
	if err != nil {
		return nil, fail(err)
	}
	return toSession(v), nil
}

func (s *Server) RenameSession(ctx context.Context, r *deckv1.RenameSessionRequest) (*deckv1.Empty, error) {
	if err := needSession(r.GetSessionId()); err != nil {
		return nil, err
	}
	t, err := title(r.GetTitle())
	if err != nil {
		return nil, err
	}
	if t == "" {
		return nil, invalid("title is required")
	}
	return done(s.be.RenameSession(ctx, r.GetSessionId(), t))
}

func (s *Server) DeleteSession(ctx context.Context, r *deckv1.SessionRef) (*deckv1.Empty, error) {
	if err := needSession(r.GetSessionId()); err != nil {
		return nil, err
	}
	return done(s.be.DeleteSession(ctx, r.GetSessionId()))
}

func (s *Server) ListMessages(ctx context.Context, r *deckv1.ListMessagesRequest) (*deckv1.ListMessagesResponse, error) {
	p := wire.MessagesListParams{SessionID: r.GetSessionId(), Before: r.GetBefore(), Limit: int(r.GetLimit())}
	if err := needSession(p.SessionID); err != nil {
		return nil, err
	}
	if err := p.Validate(); err != nil {
		return nil, fail(err)
	}
	if p.Limit == 0 {
		p.Limit = defaultPage
	}
	msgs, more, next, err := s.be.Messages(ctx, p.SessionID, p.Before, p.Limit)
	if err != nil {
		return nil, fail(err)
	}
	res := &deckv1.ListMessagesResponse{Messages: each(msgs, toMessage), More: more}
	if more {
		res.Cursor = next
	}
	return res, nil
}

func (s *Server) Send(ctx context.Context, r *deckv1.SendRequest) (*deckv1.SendResponse, error) {
	if err := needSession(r.GetSessionId()); err != nil {
		return nil, err
	}
	if strings.TrimSpace(r.GetText()) == "" && len(r.GetImages()) == 0 {
		return nil, invalid("nothing to send")
	}
	images := make([]remote.Image, 0, len(r.GetImages()))
	for _, im := range r.GetImages() {
		if !strings.HasPrefix(im.GetMime(), "image/") {
			return nil, invalid("attachments must be images")
		}
		if len(im.GetData()) == 0 {
			return nil, invalid("an image is empty")
		}
		if len(im.GetData()) > MaxImageBytes {
			return nil, status.Error(codes.ResourceExhausted, "an image may be at most 16 MB")
		}
		name := path.Base(strings.ReplaceAll(im.GetName(), "\\", "/"))
		if name == "." || name == "/" || name == "" {
			name = "image"
		}
		images = append(images, remote.Image{Name: name, Mime: im.GetMime(), Data: im.GetData()})
	}
	id, err := s.be.Send(ctx, r.GetSessionId(), r.GetText(), images)
	if err != nil {
		return nil, fail(err)
	}
	return &deckv1.SendResponse{MessageId: id}, nil
}

func (s *Server) EditMessage(ctx context.Context, r *deckv1.EditMessageRequest) (*deckv1.Empty, error) {
	if r.GetSessionId() == "" || r.GetMessageId() == "" || strings.TrimSpace(r.GetText()) == "" {
		return nil, invalid("session_id, message_id and text are required")
	}
	return done(s.be.EditMessage(ctx, r.GetSessionId(), r.GetMessageId(), r.GetText()))
}

func (s *Server) Cancel(_ context.Context, r *deckv1.SessionRef) (*deckv1.Empty, error) {
	if err := needSession(r.GetSessionId()); err != nil {
		return nil, err
	}
	return done(s.be.Cancel(r.GetSessionId()))
}

func (s *Server) Interrupt(_ context.Context, r *deckv1.InterruptRequest) (*deckv1.Empty, error) {
	if r.GetSessionId() == "" || r.GetAgent() == "" {
		return nil, invalid("session_id and agent are required")
	}
	return done(s.be.Interrupt(r.GetSessionId(), r.GetAgent()))
}

func (s *Server) Continue(_ context.Context, r *deckv1.SessionRef) (*deckv1.Empty, error) {
	if err := needSession(r.GetSessionId()); err != nil {
		return nil, err
	}
	return done(s.be.Continue(r.GetSessionId()))
}

func (s *Server) Queue(_ context.Context, r *deckv1.SessionRef) (*deckv1.QueueResponse, error) {
	if err := needSession(r.GetSessionId()); err != nil {
		return nil, err
	}
	v, err := s.be.Queue(r.GetSessionId())
	if err != nil {
		return nil, fail(err)
	}
	return &deckv1.QueueResponse{Items: each(v, toQueued)}, nil
}

func (s *Server) Steer(_ context.Context, r *deckv1.QueueItemRef) (*deckv1.Empty, error) {
	if r.GetSessionId() == "" || r.GetItemId() == "" {
		return nil, invalid("session_id and item_id are required")
	}
	return done(s.be.Steer(r.GetSessionId(), r.GetItemId()))
}

func (s *Server) CancelQueued(_ context.Context, r *deckv1.QueueItemRef) (*deckv1.Empty, error) {
	if r.GetSessionId() == "" || r.GetItemId() == "" {
		return nil, invalid("session_id and item_id are required")
	}
	return done(s.be.CancelQueued(r.GetSessionId(), r.GetItemId()))
}

func (s *Server) Roster(_ context.Context, r *deckv1.SessionRef) (*deckv1.RosterResponse, error) {
	if err := needSession(r.GetSessionId()); err != nil {
		return nil, err
	}
	v, err := s.be.Roster(r.GetSessionId())
	if err != nil {
		return nil, fail(err)
	}
	return &deckv1.RosterResponse{Agents: each(v, toAgent)}, nil
}

func (s *Server) Models(ctx context.Context, r *deckv1.ModelsRequest) (*deckv1.ModelsResponse, error) {
	if r.GetSessionId() == "" || r.GetKind() == "" {
		return nil, invalid("session_id and kind are required")
	}
	v, err := s.be.Models(ctx, r.GetSessionId(), r.GetKind())
	if err != nil {
		return nil, fail(err)
	}
	return &deckv1.ModelsResponse{Models: each(v, toModel)}, nil
}

func (s *Server) SetModel(_ context.Context, r *deckv1.SetModelRequest) (*deckv1.Empty, error) {
	if r.GetSessionId() == "" || r.GetKind() == "" || r.GetModel() == "" {
		return nil, invalid("session_id, kind and model are required")
	}
	return done(s.be.SetModel(r.GetSessionId(), r.GetKind(), r.GetModel(), r.GetEffort()))
}

func (s *Server) GetMode(_ context.Context, r *deckv1.SessionRef) (*deckv1.ModeResponse, error) {
	if err := needSession(r.GetSessionId()); err != nil {
		return nil, err
	}
	m, err := s.be.Mode(r.GetSessionId())
	if err != nil {
		return nil, fail(err)
	}
	return &deckv1.ModeResponse{Mode: m}, nil
}

// SetMode allows yolo: unlike a phone, the desktop app is the user at this
// machine.
func (s *Server) SetMode(_ context.Context, r *deckv1.SetModeRequest) (*deckv1.Empty, error) {
	p := wire.ModeSetParams{SessionID: r.GetSessionId(), Mode: r.GetMode()}
	if err := needSession(p.SessionID); err != nil {
		return nil, err
	}
	if err := p.Validate(); err != nil {
		return nil, fail(err)
	}
	return done(s.be.SetMode(p.SessionID, p.Mode))
}

func (s *Server) Approvals(_ context.Context, r *deckv1.SessionRef) (*deckv1.ApprovalsResponse, error) {
	v, err := s.be.Approvals(r.GetSessionId())
	if err != nil {
		return nil, fail(err)
	}
	return &deckv1.ApprovalsResponse{Approvals: each(v, toApproval)}, nil
}

func (s *Server) Answer(_ context.Context, r *deckv1.AnswerRequest) (*deckv1.AnswerResponse, error) {
	p := wire.ApprovalsAnswerParams{SessionID: r.GetSessionId(), ID: r.GetId(), Decision: r.GetDecision()}
	if p.SessionID == "" || p.ID == "" {
		return nil, invalid("session_id and id are required")
	}
	if err := p.Validate(); err != nil {
		return nil, fail(err)
	}
	resolved, err := s.be.Answer(p.SessionID, p.ID, p.Decision)
	if err != nil {
		return nil, fail(err)
	}
	return &deckv1.AnswerResponse{Resolved: resolved}, nil
}

func (s *Server) Changes(ctx context.Context, r *deckv1.SessionRef) (*deckv1.ChangesResponse, error) {
	if err := needSession(r.GetSessionId()); err != nil {
		return nil, err
	}
	v, err := s.be.Changes(ctx, r.GetSessionId())
	if err != nil {
		return nil, fail(err)
	}
	return &deckv1.ChangesResponse{Files: each(v, toChanged)}, nil
}

// Events sends Ready, then the replay, then live events. A subscriber that
// falls QueueSize events behind is ended with ResourceExhausted and
// resumes from its last seq.
func (s *Server) Events(r *deckv1.Subscribe, stream deckv1.Deck_EventsServer) error {
	sb, ready := s.feed.subscribe(r.GetEpoch(), r.GetSeq())
	defer s.feed.unsubscribe(sb)
	if err := stream.Send(&deckv1.Event{Kind: &deckv1.Event_Ready{Ready: ready}}); err != nil {
		return err
	}
	ctx := stream.Context()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-sb.kicked:
			return status.Error(codes.ResourceExhausted, "fell behind; resume from the last seq")
		case ev := <-sb.ch:
			if err := stream.Send(ev); err != nil {
				return err
			}
		}
	}
}
