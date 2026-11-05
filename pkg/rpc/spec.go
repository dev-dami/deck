package rpc

import (
	"context"
	"strings"

	"github.com/dev-dami/deck/internal/workspace"
	"github.com/dev-dami/deck/pkg/rpc/deckv1"
)

// specMethods are served when the workspace sets agents' specs; Info's
// capabilities list them. Each has its own scope: SetLead and SetMemberSpec
// change one session, SetDefaults the defaults (Settings only).
var specMethods = []string{"SetLead", "SetMemberSpec", "SetDefaults"}

func (s *Server) picker() (workspace.AgentPicker, error) {
	if s.crew == nil || s.crew.picker == nil {
		return nil, errNoCrew
	}
	return s.crew.picker, nil
}

// knownSession refuses a session the backend does not know, as every
// session call through it does.
func (s *Server) knownSession(sid string) error {
	if err := needSession(sid); err != nil {
		return err
	}
	_, err := s.be.Mode(sid)
	return fail(err)
}

// choice is a SpecChoice as the workspace reads it: nil for absent, "" for
// the default.
func choice(c *deckv1.SpecChoice) *string {
	if c == nil || c.GetPick() == nil {
		return nil
	}
	v := strings.TrimSpace(c.GetValue()) // "" when use_default
	return &v
}

func (s *Server) SetLead(ctx context.Context, r *deckv1.SetLeadRequest) (*deckv1.SetLeadResponse, error) {
	p, err := s.picker()
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(r.GetAgent()) == "" {
		return nil, invalid("agent is required")
	}
	if err := s.knownSession(r.GetSessionId()); err != nil {
		return nil, err
	}
	res, err := p.SetLead(r.GetSessionId(), r.GetAgent())
	if err != nil {
		return nil, roomFail(err)
	}
	s.recheck(ctx, r.GetSessionId())
	return &deckv1.SetLeadResponse{Member: res.Agent, Kind: res.Kind, Changed: res.Changed}, nil
}

func (s *Server) SetMemberSpec(ctx context.Context, r *deckv1.SetMemberSpecRequest) (*deckv1.SpecResult, error) {
	p, err := s.picker()
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(r.GetMember()) == "" {
		return nil, invalid("member is required")
	}
	if err := s.knownSession(r.GetSessionId()); err != nil {
		return nil, err
	}
	res, err := p.SetMemberSpec(r.GetSessionId(), r.GetMember(), choice(r.GetModel()), choice(r.GetEffort()))
	if err != nil {
		return nil, roomFail(err)
	}
	s.recheck(ctx, r.GetSessionId())
	return &deckv1.SpecResult{
		Member: res.Name, Kind: res.Kind, Model: res.Model, Effort: res.Effort,
		ModelSource: string(res.ModelFrom), EffortSource: string(res.EffortFrom), Warnings: res.Warnings,
	}, nil
}

func (s *Server) SetDefaults(_ context.Context, r *deckv1.SetDefaultsRequest) (*deckv1.Empty, error) {
	p, err := s.picker()
	if err != nil {
		return nil, err
	}
	if r.GetKind() == "" {
		return nil, invalid("kind is required")
	}
	if err := p.SetDefaults(r.GetKind(), choice(r.GetModel()), choice(r.GetEffort())); err != nil {
		return nil, refused(err)
	}
	return empty, nil
}

func (s *Server) GetCatalog(_ context.Context, r *deckv1.GetCatalogRequest) (*deckv1.Catalog, error) {
	if s.crew == nil || s.crew.catalog == nil {
		return nil, errNoCrew
	}
	out := &deckv1.Catalog{}
	for _, kc := range s.crew.catalog.ModelCatalog(r.GetKinds(), r.GetRefresh()) {
		k := &deckv1.KindCatalog{Kind: kc.Kind, FetchedAt: ms(kc.Fetched), State: kc.State, Error: kc.Err}
		for _, c := range kc.Models {
			mc := &deckv1.ModelChoice{
				Id: c.ID, Label: c.Name, Description: c.Description, Video: c.Video,
				IsDefault: c.Default, DefaultEffort: c.DefaultEffort,
			}
			for _, e := range c.Efforts {
				mc.Efforts = append(mc.Efforts, e.Level)
			}
			k.Models = append(k.Models, mc)
		}
		out.Kinds = append(out.Kinds, k)
	}
	return out, nil
}
