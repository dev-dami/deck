package session

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/dev-dami/deck/internal/deck/agent/fake"
)

// postTeam is a hub.Team that records posts.
type postTeam struct {
	mu    sync.Mutex
	posts []string
}

func (p *postTeam) List() string                                   { return "" }
func (p *postTeam) Status(context.Context, string) (string, error) { return "", nil }
func (p *postTeam) Read(int) string                                { return "" }
func (p *postTeam) Post(_ context.Context, agent, text string) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.posts = append(p.posts, agent+": "+text)
	return "posted to vps", nil
}

func TestTeamPostIsNamedRedactedAndLimited(t *testing.T) {
	post := fake.Step{Exec: &fake.Exec{Op: "team.post", Args: []byte(`{"text": "deploying; token ghp_abcdefghijklmnopqrstuvwxyz0123456789"}`)}}
	var steps []fake.Step
	for range maxTeamPosts + 1 {
		steps = append(steps, post)
	}
	r := newRig(t, nil, []fake.Script{turn(append(steps, say("ok"), finish())...)})
	team := &postTeam{}
	r.m.SetTeam(team)
	tid := r.thread(t)
	r.add(t, tid, "codex")
	_ = r.m.Post(tid, "$codex go")
	waitFor(t, "codex", func() bool { return r.c.turnDones("codex") == 1 })

	res := r.c.toolResults("codex", "deck")
	if len(res) != maxTeamPosts+1 || !strings.Contains(res[0], "posted to vps") || !strings.Contains(res[maxTeamPosts], "slow down") {
		t.Fatalf("results = %q", res)
	}
	team.mu.Lock()
	defer team.mu.Unlock()
	if len(team.posts) != maxTeamPosts || !strings.HasPrefix(team.posts[0], "codex: deploying") || strings.Contains(team.posts[0], "ghp_") {
		t.Fatalf("posts = %q", team.posts)
	}
}
