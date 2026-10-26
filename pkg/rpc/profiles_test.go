package rpc

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	appconfig "github.com/dev-dami/deck/internal/config"
	"github.com/dev-dami/deck/internal/workspace"
	"github.com/dev-dami/deck/pkg/remote/wire"
	"github.com/dev-dami/deck/pkg/rpc/deckv1"
)

// fakeProfiles is a workspace with rooms that edits the profiles of the
// project in dir through the real deck.json code, with a temporary global
// config folder.
type fakeProfiles struct {
	*fakeRooms
	dir       string
	teammates []string
}

func (f *fakeProfiles) ListProfiles() []workspace.Profile {
	ps, _ := appconfig.LoadProfiles(f.dir, f.teammates)
	return ps
}

func (f *fakeProfiles) SaveProfile(p workspace.Profile) error {
	return appconfig.SaveProfile(f.dir, p, f.teammates)
}

func (f *fakeProfiles) DeleteProfile(name, scope string) error {
	return appconfig.DeleteProfile(f.dir, name, scope)
}

func profilesRig(t *testing.T) (f *fakeProfiles, c deckv1.DeckClient, globalFile string) {
	t.Helper()
	global := t.TempDir()
	t.Setenv("DECK_GLOBAL_CONFIG", global)
	f = &fakeProfiles{fakeRooms: newFakeRooms(), dir: t.TempDir(), teammates: []string{"alice"}}
	_, c = workspaceRig(t, f, "")
	return f, c, filepath.Join(global, "deck.json")
}

func TestProfileCalls(t *testing.T) {
	f, c, globalFile := profilesRig(t)
	ctx := ctxT(t)

	info, err := c.Info(ctx, &deckv1.Empty{})
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range []string{"ListProfiles", "SaveProfile", "DeleteProfile"} {
		if !slices.Contains(info.GetCapabilities(), m) {
			t.Fatalf("capabilities %v lack %s", info.GetCapabilities(), m)
		}
	}

	if r, err := c.ListProfiles(ctx, &deckv1.ListProfilesRequest{}); err != nil || len(r.GetProfiles()) != 0 {
		t.Fatalf("no profiles yet: %v %v", r, err)
	}

	global := &deckv1.Profile{Name: " charles ", Kind: "claude", Role: "reviewer", Voice: "dry, terse",
		Paths: []string{"internal/auth/**", " "}, Scope: "global"}
	saved, err := c.SaveProfile(ctx, &deckv1.SaveProfileRequest{Profile: global})
	if err != nil {
		t.Fatal(err)
	}
	if p := saved.GetProfile(); p.GetName() != "charles" || p.GetScope() != "global" || !slices.Equal(p.GetPaths(), []string{"internal/auth/**"}) {
		t.Fatalf("saved %+v", p)
	}
	if data, err := os.ReadFile(globalFile); err != nil || !strings.Contains(string(data), `"name": "charles"`) {
		t.Fatalf("the global deck.json: %s %v", data, err)
	}
	if _, err := c.SaveProfile(ctx, &deckv1.SaveProfileRequest{Profile: &deckv1.Profile{
		Name: "ada", Kind: "codex", Effort: "high", Role: "implements the plan", Scope: "global"}}); err != nil {
		t.Fatal(err)
	}
	// The project's charles is laid over the global one.
	if _, err := c.SaveProfile(ctx, &deckv1.SaveProfileRequest{Profile: &deckv1.Profile{
		Name: "Charles", Kind: "codex", Role: "lead", Scope: "project"}}); err != nil {
		t.Fatal(err)
	}
	r, err := c.ListProfiles(ctx, &deckv1.ListProfilesRequest{})
	if err != nil {
		t.Fatal(err)
	}
	got := r.GetProfiles()
	if len(got) != 2 || got[0].GetName() != "Charles" || got[0].GetKind() != "codex" || got[0].GetScope() != "project" ||
		got[1].GetName() != "ada" || got[1].GetEffort() != "high" || got[1].GetScope() != "global" {
		t.Fatalf("merged list %+v", got)
	}

	// Broken rules are InvalidArgument, in Deck's words.
	for name, p := range map[string]*deckv1.Profile{
		"reserved":      {Name: "all", Kind: "claude", Scope: "global"},
		"a kind":        {Name: "codex", Kind: "claude", Scope: "global"},
		"a teammate":    {Name: "Alice", Kind: "claude", Scope: "project"},
		"no scope":      {Name: "grace", Kind: "claude"},
		"a bad scope":   {Name: "grace", Kind: "claude", Scope: "team"},
		"unknown kind":  {Name: "grace", Kind: "gemini", Scope: "global"},
		"a # in a name": {Name: "gr#ce", Kind: "claude", Scope: "global"},
		"two-line role": {Name: "grace", Kind: "claude", Role: "a\nb", Scope: "global"},
	} {
		if _, err := c.SaveProfile(ctx, &deckv1.SaveProfileRequest{Profile: p}); status.Code(err) != codes.InvalidArgument {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := c.SaveProfile(ctx, &deckv1.SaveProfileRequest{}); status.Code(err) != codes.InvalidArgument {
		t.Errorf("no profile: %v", err)
	}

	// Deleting the project's charles uncovers the global one.
	if _, err := c.DeleteProfile(ctx, &deckv1.DeleteProfileRequest{Name: "charles", Scope: "project"}); err != nil {
		t.Fatal(err)
	}
	ps := f.ListProfiles()
	if len(ps) != 2 || ps[0].Name != "charles" || ps[0].Kind != "claude" || ps[0].Scope != "global" {
		t.Fatalf("after delete %+v", ps)
	}
	if _, err := c.DeleteProfile(ctx, &deckv1.DeleteProfileRequest{Name: "charles", Scope: "project"}); status.Code(err) != codes.NotFound {
		t.Fatalf("delete twice: %v", err)
	}
	if _, err := c.DeleteProfile(ctx, &deckv1.DeleteProfileRequest{Name: "charles", Scope: "team"}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("bad scope: %v", err)
	}
	if _, err := c.DeleteProfile(ctx, &deckv1.DeleteProfileRequest{Scope: "global"}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("no name: %v", err)
	}
}

func TestProfileCallsWithoutAWorkspace(t *testing.T) {
	_, c := roomRig(t, nil, "")
	ctx := ctxT(t)
	if _, err := c.ListProfiles(ctx, &deckv1.ListProfilesRequest{}); status.Code(err) != codes.Unimplemented {
		t.Fatalf("list: %v", err)
	}
	if _, err := c.SaveProfile(ctx, &deckv1.SaveProfileRequest{Profile: &deckv1.Profile{Name: "a", Kind: "claude", Scope: "global"}}); status.Code(err) != codes.Unimplemented {
		t.Fatalf("save: %v", err)
	}
	if _, err := c.DeleteProfile(ctx, &deckv1.DeleteProfileRequest{Name: "a", Scope: "global"}); status.Code(err) != codes.Unimplemented {
		t.Fatalf("delete: %v", err)
	}
	// A workspace without profiles does not advertise them.
	_, c = roomRig(t, newFakeRooms(), "")
	info, err := c.Info(ctx, &deckv1.Empty{})
	if err != nil || slices.Contains(info.GetCapabilities(), "ListProfiles") {
		t.Fatalf("info %v %v", info.GetCapabilities(), err)
	}
}

func TestRosterCarriesProfileAndRole(t *testing.T) {
	f := newFakeRooms()
	f.agents = []workspace.AgentLine{
		{ID: "claude", Kind: "claude"},
		{ID: "charles-2", Kind: "claude", Profile: "charles", Role: "reviewer"},
	}
	fake, c := roomRig(t, f, "")
	s := fake.AddSession("s1", "t")
	s.Roster = []wire.AgentLine{{ID: "charles", Name: "charles", Kind: "claude", Profile: "charles", Role: "reviewer"}}
	ctx := ctxT(t)

	r, err := c.Roster(ctx, &deckv1.SessionRef{SessionId: "s1"})
	if err != nil {
		t.Fatal(err)
	}
	if a := r.GetAgents(); len(a) != 1 || a[0].GetName() != "charles" || a[0].GetProfile() != "charles" || a[0].GetRole() != "reviewer" {
		t.Fatalf("roster %+v", a)
	}
	tree, err := c.Agents(ctx, &deckv1.SessionRef{SessionId: "s1"})
	if err != nil {
		t.Fatal(err)
	}
	if a := tree.GetAgents(); len(a) != 2 || a[0].GetProfile() != "" || a[1].GetName() != "charles-2" ||
		a[1].GetProfile() != "charles" || a[1].GetRole() != "reviewer" {
		t.Fatalf("agents %+v", a)
	}
}
