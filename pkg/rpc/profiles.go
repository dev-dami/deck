package rpc

import (
	"context"
	"errors"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	deckconfig "github.com/dev-dami/deck/internal/deck/config"
	"github.com/dev-dami/deck/internal/deck/profile"
	"github.com/dev-dami/deck/internal/workspace"
	"github.com/dev-dami/deck/pkg/rpc/deckv1"
)

// profileMethods are served when the workspace edits profiles; Info's
// capabilities list them.
var profileMethods = []string{"ListProfiles", "SaveProfile", "DeleteProfile"}

func (s *Server) profiles() (workspace.Profiles, error) {
	if s.crew == nil || s.crew.profiles == nil {
		return nil, errNoCrew
	}
	return s.crew.profiles, nil
}

func (s *Server) ListProfiles(context.Context, *deckv1.ListProfilesRequest) (*deckv1.ListProfilesResponse, error) {
	ps, err := s.profiles()
	if err != nil {
		return nil, err
	}
	return &deckv1.ListProfilesResponse{Profiles: each(ps.ListProfiles(), toProfile)}, nil
}

func (s *Server) SaveProfile(_ context.Context, r *deckv1.SaveProfileRequest) (*deckv1.SaveProfileResponse, error) {
	ps, err := s.profiles()
	if err != nil {
		return nil, err
	}
	in := r.GetProfile()
	if in == nil {
		return nil, status.Error(codes.InvalidArgument, "no profile")
	}
	p := fromProfile(in)
	// The rules are checked here too, so that a broken one is
	// InvalidArgument and a file that cannot be written is not. The
	// teammates are checked when the workspace saves it.
	if p.Scope != profile.ScopeGlobal && p.Scope != profile.ScopeProject {
		return nil, status.Errorf(codes.InvalidArgument, "profile %q needs a scope: global or project", p.Name)
	}
	if err := profile.Validate(p, deckconfig.Kinds, nil); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	if err := ps.SaveProfile(p); err != nil {
		if errors.Is(err, profile.ErrInvalid) {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
		return nil, refused(err)
	}
	return &deckv1.SaveProfileResponse{Profile: toProfile(p)}, nil
}

func (s *Server) DeleteProfile(_ context.Context, r *deckv1.DeleteProfileRequest) (*deckv1.DeleteProfileResponse, error) {
	ps, err := s.profiles()
	if err != nil {
		return nil, err
	}
	name, scope := strings.TrimSpace(r.GetName()), r.GetScope()
	if name == "" {
		return nil, status.Error(codes.InvalidArgument, "no profile name")
	}
	if scope != profile.ScopeGlobal && scope != profile.ScopeProject {
		return nil, status.Error(codes.InvalidArgument, "scope is global or project")
	}
	if err := ps.DeleteProfile(name, scope); err != nil {
		if errors.Is(err, profile.ErrNotFound) {
			return nil, status.Error(codes.NotFound, err.Error())
		}
		return nil, refused(err)
	}
	return &deckv1.DeleteProfileResponse{}, nil
}

func toProfile(p profile.Profile) *deckv1.Profile {
	return &deckv1.Profile{
		Name: p.Name, Kind: p.Kind, Model: p.Model, Effort: p.Effort, Role: p.Role,
		Instructions: p.Instructions, Voice: p.Voice, Color: p.Color, Paths: p.Paths, Scope: p.Scope,
	}
}

func fromProfile(p *deckv1.Profile) profile.Profile {
	var paths []string
	for _, g := range p.GetPaths() {
		if g = strings.TrimSpace(g); g != "" {
			paths = append(paths, g)
		}
	}
	return profile.Profile{
		Name: strings.TrimSpace(p.GetName()), Kind: strings.TrimSpace(p.GetKind()),
		Model: strings.TrimSpace(p.GetModel()), Effort: strings.TrimSpace(p.GetEffort()),
		Role: strings.TrimSpace(p.GetRole()), Instructions: strings.TrimSpace(p.GetInstructions()),
		Voice: strings.TrimSpace(p.GetVoice()), Color: strings.TrimSpace(p.GetColor()),
		Paths: paths, Scope: strings.TrimSpace(p.GetScope()),
	}
}
