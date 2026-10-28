package rpc

import (
	"context"
	"errors"

	"github.com/dev-dami/deck/internal/deck/highlights"
	pb "github.com/dev-dami/deck/pkg/rpc/deckv1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func (s *Server) Highlights(ctx context.Context, r *pb.SessionRef) (*pb.HighlightsResponse, error) {
	if s.crew == nil || s.crew.highlights == nil {
		return nil, errNoCrew
	}
	if err := needSession(r.GetSessionId()); err != nil {
		return nil, err
	}
	out, err := s.crew.highlights.Highlights(ctx, r.SessionId)
	return out, highlightFail(err)
}
func (s *Server) HighlightAction(ctx context.Context, r *pb.HighlightActionRequest) (*pb.HighlightItem, error) {
	if s.crew == nil || s.crew.highlights == nil {
		return nil, errNoCrew
	}
	if err := needSession(r.GetSessionId()); err != nil {
		return nil, err
	}
	out, err := s.crew.highlights.HighlightAction(ctx, r)
	return out, highlightFail(err)
}
func highlightFail(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, highlights.ErrInvalid):
		return invalid(err.Error())
	case errors.Is(err, highlights.ErrForbidden):
		return status.Error(codes.PermissionDenied, err.Error())
	case errors.Is(err, highlights.ErrNotFound):
		return status.Error(codes.NotFound, err.Error())
	default:
		return roomFail(err)
	}
}
