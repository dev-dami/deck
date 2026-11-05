package rpc

import (
	"context"
	"fmt"
	"slices"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/dev-dami/deck/internal/workspace"
	"github.com/dev-dami/deck/pkg/rpc/deckv1"
)

// fakePicker records the spec calls it gets, nil values as "-".
type fakePicker struct {
	*fakeRooms
	calls []string
}

func show(p *string) string {
	if p == nil {
		return "-"
	}
	return fmt.Sprintf("%q", *p)
}

func (f *fakePicker) AgentModelChoices(context.Context, string) ([]workspace.ModelChoice, error) {
	return nil, nil
}
func (f *fakePicker) CachedAgentModelChoices(string) []workspace.ModelChoice { return nil }
func (f *fakePicker) SetAgentModel(sid, agent, model, effort string) error {
	f.calls = append(f.calls, "agentmodel "+sid+" "+agent+" "+model+" "+effort)
	return nil
}
func (f *fakePicker) SetLead(sid, agent string) (workspace.LeadResult, error) {
	f.calls = append(f.calls, "lead "+sid+" "+agent)
	return workspace.LeadResult{Agent: agent + "-2", Kind: agent, Changed: true}, nil
}
func (f *fakePicker) SetMemberSpec(sid, member string, model, effort *string) (workspace.SpecResult, error) {
	f.calls = append(f.calls, "spec "+sid+" "+member+" "+show(model)+" "+show(effort))
	return workspace.SpecResult{Name: member, Kind: "claude", Model: "big", ModelFrom: "thread", EffortFrom: "parent",
		Warnings: []string{"effort reset"}}, nil
}
func (f *fakePicker) SetDefaults(kind string, model, effort *string) error {
	f.calls = append(f.calls, "defaults "+kind+" "+show(model)+" "+show(effort))
	return nil
}
func (f *fakePicker) ModelCatalog(kinds []string, refresh bool) []workspace.KindCatalog {
	f.calls = append(f.calls, fmt.Sprintf("catalog %v %v", kinds, refresh))
	return []workspace.KindCatalog{
		{Kind: "claude", State: "fresh", Models: []workspace.ModelChoice{
			{Kind: "claude", ID: "", Name: "Default", Default: true, DefaultEffort: "medium",
				Efforts: []workspace.EffortChoice{{Level: "low"}, {Level: "high"}}},
		}},
		{Kind: "codex", State: "failed", Err: "not logged in"},
	}
}

func TestSpecCalls(t *testing.T) {
	f := &fakePicker{fakeRooms: newFakeRooms()}
	fake, c := workspaceRig(t, f, "")
	fake.AddSession("s1", "one")
	ctx := ctxT(t)

	info, err := c.Info(ctx, &deckv1.Empty{})
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range append(slices.Clone(specMethods), "GetCatalog") {
		if !slices.Contains(info.GetCapabilities(), m) {
			t.Fatalf("capabilities %v lack %s", info.GetCapabilities(), m)
		}
	}

	lead, err := c.SetLead(ctx, &deckv1.SetLeadRequest{SessionId: "s1", Agent: "codex"})
	if err != nil || lead.GetMember() != "codex-2" || !lead.GetChanged() {
		t.Fatalf("SetLead = %v, %v", lead, err)
	}
	res, err := c.SetMemberSpec(ctx, &deckv1.SetMemberSpecRequest{
		SessionId: "s1", Member: "claude-2",
		Model:  &deckv1.SpecChoice{Pick: &deckv1.SpecChoice_UseDefault{UseDefault: true}},
		Effort: nil, // left alone
	})
	if err != nil || res.GetModelSource() != "thread" || res.GetEffortSource() != "parent" || len(res.GetWarnings()) != 1 {
		t.Fatalf("SetMemberSpec = %v, %v", res, err)
	}
	if _, err := c.SetMemberSpec(ctx, &deckv1.SetMemberSpecRequest{
		SessionId: "s1", Member: "claude-2",
		Effort: &deckv1.SpecChoice{Pick: &deckv1.SpecChoice_Value{Value: "high"}},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.SetDefaults(ctx, &deckv1.SetDefaultsRequest{Kind: "claude",
		Model: &deckv1.SpecChoice{Pick: &deckv1.SpecChoice_Value{Value: "big"}}}); err != nil {
		t.Fatal(err)
	}
	cat, err := c.GetCatalog(ctx, &deckv1.GetCatalogRequest{Kinds: []string{"claude", "codex"}})
	if err != nil {
		t.Fatal(err)
	}
	if k := cat.GetKinds(); len(k) != 2 || len(k[0].GetModels()) != 1 || !k[0].GetModels()[0].GetIsDefault() ||
		k[0].GetModels()[0].GetDefaultEffort() != "medium" || k[1].GetState() != "failed" || k[1].GetError() == "" {
		t.Fatalf("catalog = %v", cat)
	}

	want := []string{
		"lead s1 codex",
		`spec s1 claude-2 "" -`,
		`spec s1 claude-2 - "high"`,
		`defaults claude "big" -`,
		"catalog [claude codex] false",
	}
	if !slices.Equal(f.calls, want) {
		t.Fatalf("calls =\n%q\nwant\n%q", f.calls, want)
	}

	// An unknown session is NotFound and reaches no workspace call.
	_, err = c.SetMemberSpec(ctx, &deckv1.SetMemberSpecRequest{SessionId: "ghost", Member: "claude"})
	if status.Code(err) != codes.NotFound {
		t.Fatalf("unknown session: %v", err)
	}
	if _, err := c.SetLead(ctx, &deckv1.SetLeadRequest{SessionId: "s1"}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("no agent: %v", err)
	}
	if len(f.calls) != len(want) {
		t.Fatalf("refused calls reached the workspace: %q", f.calls[len(want):])
	}
}
