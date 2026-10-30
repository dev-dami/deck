package rpc

import (
	"context"
	"errors"

	"github.com/dev-dami/deck/internal/deck/artifacts"
	"github.com/dev-dami/deck/internal/workspace"
	pb "github.com/dev-dami/deck/pkg/rpc/deckv1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var artifactMethods = []string{"Artifacts", "ArtifactRevisions", "ArtifactRead", "ArtifactAction", "ReportArtifactBuild"}

func artifactFail(err error) error {
	if err == nil {
		return nil
	}
	var conflict *artifacts.Conflict
	if errors.As(err, &conflict) {
		s := status.New(codes.Aborted, "artifact revision conflict")
		detail, e := s.WithDetails(conflict.Detail)
		if e != nil {
			return s.Err()
		}
		return detail.Err()
	}
	switch {
	case errors.Is(err, artifacts.ErrInvalid):
		return status.Error(codes.InvalidArgument, err.Error())
	case errors.Is(err, artifacts.ErrLimit):
		return status.Error(codes.ResourceExhausted, err.Error())
	case errors.Is(err, artifacts.ErrPlan):
		return status.Error(codes.FailedPrecondition, err.Error())
	case errors.Is(err, artifacts.ErrNotFound), errors.Is(err, workspace.ErrNotFound):
		return status.Error(codes.NotFound, "not found")
	default:
		return status.Error(codes.Internal, "could not read or persist artifacts")
	}
}
func (s *Server) Artifacts(ctx context.Context, r *pb.ArtifactsRequest) (*pb.ArtifactsResponse, error) {
	if s.crew == nil || s.crew.artifacts == nil {
		return nil, errNoCrew
	}
	if err := needSession(r.GetSessionId()); err != nil {
		return nil, err
	}
	out, err := s.crew.artifacts.Artifacts(ctx, r)
	return out, artifactFail(err)
}
func (s *Server) ArtifactRevisions(ctx context.Context, r *pb.ArtifactRevisionsRequest) (*pb.ArtifactRevisionsResponse, error) {
	if s.crew == nil || s.crew.artifacts == nil {
		return nil, errNoCrew
	}
	if err := needSession(r.GetSessionId()); err != nil {
		return nil, err
	}
	out, err := s.crew.artifacts.ArtifactRevisions(ctx, r)
	return out, artifactFail(err)
}
func (s *Server) ArtifactRead(ctx context.Context, r *pb.ArtifactReadRequest) (*pb.ArtifactReadResponse, error) {
	if s.crew == nil || s.crew.artifacts == nil {
		return nil, errNoCrew
	}
	if err := needSession(r.GetSessionId()); err != nil {
		return nil, err
	}
	out, err := s.crew.artifacts.ArtifactRead(ctx, r)
	return out, artifactFail(err)
}
func (s *Server) ArtifactAction(ctx context.Context, r *pb.ArtifactActionRequest) (*pb.ArtifactActionResponse, error) {
	if s.crew == nil || s.crew.artifacts == nil {
		return nil, errNoCrew
	}
	if err := needSession(r.GetSessionId()); err != nil {
		return nil, err
	}
	out, err := s.crew.artifacts.ArtifactAction(ctx, r)
	return out, artifactFail(err)
}
func (s *Server) ReportArtifactBuild(ctx context.Context, r *pb.ReportArtifactBuildRequest) (*pb.ArtifactBuildReport, error) {
	if s.crew == nil || s.crew.artifacts == nil {
		return nil, errNoCrew
	}
	if err := needSession(r.GetSessionId()); err != nil {
		return nil, err
	}
	out, err := s.crew.artifacts.ReportArtifactBuild(ctx, r)
	return out, artifactFail(err)
}
