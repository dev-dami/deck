package hub

import (
	"context"
	"strings"
	"testing"
)

type fakeTeam struct{ asked string }

func (f *fakeTeam) List() string { return "Teammates (1):\n  vps: connected" }

func (f *fakeTeam) Status(_ context.Context, name string) (string, error) {
	f.asked = name
	return name + " answered in 12ms.", nil
}

func TestPeersOps(t *testing.T) {
	env := testEnv(t)
	if _, isErr := run(t, env, OpPeersList, ""); !isErr {
		t.Fatal("peers.list without a peer link must be an error")
	}

	team := &fakeTeam{}
	env.Team = team
	if got, isErr := run(t, env, OpPeersList, ""); isErr || !strings.Contains(got, "vps: connected") {
		t.Fatalf("peers.list = %q (error %v)", got, isErr)
	}
	if _, isErr := run(t, env, OpPeersStatus, `{}`); !isErr {
		t.Fatal("peers.status needs a peer")
	}
	if got, isErr := run(t, env, OpPeersStatus, `{"peer":"vps"}`); isErr || team.asked != "vps" || !strings.Contains(got, "12ms") {
		t.Fatalf("peers.status = %q (error %v, asked %q)", got, isErr, team.asked)
	}
	// An agent looking for its teammates finds the ops by the word peer.
	if out := Search(env, []byte(`{"query":"peer"}`)); !strings.Contains(out, OpPeersList) {
		t.Fatalf("searching for peer misses peers.list:\n%s", out)
	}
}
