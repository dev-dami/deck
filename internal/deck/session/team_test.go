package session

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dev-dami/deck/internal/deck/agent/fake"
)

// postTeam is a hub.Team that records posts, and hands its inbox to
// News: one message per number, for an agent or ("") all of them.
type postTeam struct {
	mu    sync.Mutex
	posts []string
	inbox []teamLine
}

type teamLine struct {
	seq       uint64
	for_, txt string
}

func (p *postTeam) News(agent string, since uint64, _ time.Time) (string, uint64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	var lines []string
	last := since
	for _, l := range p.inbox {
		if l.seq <= since {
			continue
		}
		last = max(last, l.seq)
		if l.for_ == "" || l.for_ == agent {
			lines = append(lines, "[vps/claude] "+l.txt)
		}
	}
	if len(lines) == 0 {
		return "", last
	}
	return "[deck] Team chat:\n" + strings.Join(lines, "\n"), last
}

func (p *postTeam) List() string                                   { return "" }
func (p *postTeam) Names() []string                                { return []string{"vps", "pc2"} }
func (p *postTeam) Status(context.Context, string) (string, error) { return "", nil }
func (p *postTeam) Read(int) string                                { return "" }
func (p *postTeam) Post(_ context.Context, agent, to, text string) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if to != "" {
		agent += " → " + to
	}
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

// TestInstructionsNameTheTeam: an agent hears of subagents (level_test.go has
// what each orchestration level adds) and leases, and,
// while teammates are paired, of the team ops and who the teammates are.
func TestInstructionsNameTheTeam(t *testing.T) {
	r := newRig(t, nil, nil)
	tid := r.thread(t)
	r.add(t, tid, "claude")
	sp, _ := r.claude.spec("claude")
	// Alone with the user, it hears only where Deck's ops are (solo).
	has(t, sp.Instructions, "agent.spawn", "not your own Task/subagent tool")
	lacks(t, sp.Instructions, "task.contract", "Teammates", "lease.acquire")

	r.m.SetTeam(&postTeam{})
	r.add(t, tid, "codex")
	sp, _ = r.codex.spec("codex")
	has(t, sp.Instructions, "agent.spawn", "lease.acquire", "Teammates' Decks on other machines: vps, pc2", "task.contract {peer, brief}", "team.post", `"[vps] ..."`)
	if len(sp.Instructions) > maxInstrChars {
		t.Fatalf("Instructions are %d bytes", len(sp.Instructions))
	}
	has(t, sp.Instructions, "Write plan files in") // nothing was cut
}

// TestTeamNewsReachesTheAgent: what teammates write reaches this Deck's
// agents at the start of their next turn, once, and a message for one agent
// reaches that agent only.
func TestTeamNewsReachesTheAgent(t *testing.T) {
	r := newRig(t, nil, []fake.Script{turn(say("a"), finish()), turn(say("b"), finish())})
	team := &postTeam{inbox: []teamLine{{1, "codex", "hey now"}, {2, "claude", "not for codex"}, {3, "", "build is green"}}}
	r.m.SetTeam(team)
	tid := r.thread(t)
	r.add(t, tid, "codex")
	_ = r.m.Post(tid, "$codex go")
	waitFor(t, "turn 1", func() bool { return r.c.turnDones("codex") == 1 })
	first := r.codex.prompts("codex")[0]
	has(t, first, "hey now", "build is green", "go")
	lacks(t, first, "not for codex")

	_ = r.m.Post(tid, "$codex again")
	waitFor(t, "turn 2", func() bool { return r.c.turnDones("codex") == 2 })
	lacks(t, r.codex.prompts("codex")[1], "hey now", "build is green")
}

// TestTeamPostTo: team.post with to goes to that teammate (or agent) only.
func TestTeamPostTo(t *testing.T) {
	post := fake.Step{Exec: &fake.Exec{Op: "team.post", Args: []byte(`{"text": "hey", "to": "vps/codex"}`)}}
	r := newRig(t, nil, []fake.Script{turn(post, say("ok"), finish())})
	team := &postTeam{}
	r.m.SetTeam(team)
	tid := r.thread(t)
	r.add(t, tid, "codex")
	_ = r.m.Post(tid, "$codex go")
	waitFor(t, "codex", func() bool { return r.c.turnDones("codex") == 1 })
	team.mu.Lock()
	defer team.mu.Unlock()
	if len(team.posts) != 1 || team.posts[0] != "codex → vps/codex: hey" {
		t.Fatalf("posts = %q", team.posts)
	}
}
