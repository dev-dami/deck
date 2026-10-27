package session

import (
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/dev-dami/deck/internal/deck/agent"
	"github.com/dev-dami/deck/internal/deck/agent/fake"
	"github.com/dev-dami/deck/internal/deck/profile"
	"github.com/dev-dami/deck/internal/deck/store"
)

func TestParseEntries(t *testing.T) {
	got := parseEntries("claude, claude codex,,@ada")
	want := []Entrant{{Agent: "claude"}, {Agent: "claude"}, {Agent: "codex"}, {Agent: "@ada"}}
	if !slices.Equal(got, want) {
		t.Fatalf("parseEntries = %+v", got)
	}
}

// raceRig is a rig on a git repository, with orchestration on.
func raceRig(t *testing.T, claude, codex []fake.Script) (*rig, string, string) {
	t.Helper()
	repo := gitRepo(t)
	r := newRigWith(t, t.TempDir(), false, testCfg(), claude, codex)
	tid, err := r.m.NewThread(repo)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.m.SetLevel(tid, "full"); err != nil {
		t.Fatal(err)
	}
	return r, tid, repo
}

// entries are the tasks of race id.
func entries(r *rig, tid, id string) []store.Task {
	var out []store.Task
	for _, tk := range r.m.Tasks(tid) {
		if tk.Parent == id {
			out = append(out, tk)
		}
	}
	return out
}

func TestUserRaceKeepsOneEntry(t *testing.T) {
	work := []fake.Script{turn(blockStep("ap"), say("changed a.txt; go test passes"), finish())}
	r, tid, repo := raceRig(t, work, work)
	if _, err := r.m.Race(tid, "", RaceSpec{Brief: "improve a.txt", Entrants: []Entrant{{Agent: "claude"}, {Agent: "claude"}, {Agent: "codex"}}}); err != nil {
		t.Fatal(err)
	}
	var race store.Task
	for _, tk := range r.m.Tasks(tid) {
		if tk.Race {
			race = tk
		}
	}
	es := entries(r, tid, race.ID)
	if race.ID == "" || len(es) != 3 {
		t.Fatalf("board = %+v", r.m.Tasks(tid))
	}

	// Each entry writes its own version on its own worktree.
	for i, e := range es {
		if e.Run == nil || e.Run.Path == "" {
			t.Fatalf("entry %s has no worktree: %+v", e.ID, e.Run)
		}
		ap := approvalOf(t, r, e.Owner)
		has(t, strings.Join(append(r.claude.prompts(e.Owner), r.codex.prompts(e.Owner)...), ""), "improve a.txt", "This is a race: 3 agents")
		writeFile(t, e.Run.Path, "a.txt", strings.Repeat("v\n", i+1))
		ap.Respond(agent.Decision{Allow: true})
	}
	waitFor(t, "the race done", func() bool { return taskOf(t, r, tid, race.ID).Status == store.TaskDone })
	has(t, taskOf(t, r, tid, race.ID).Result, es[0].ID+" ", "done: 1 files +1 -2", "changed a.txt; go test passes", "The user keeps one entry")
	if d := taskOf(t, r, tid, es[2].ID).Diff; d == nil || d.Added != 3 || d.Files[0] != "a.txt" {
		t.Fatalf("diff of the third = %+v", d)
	}

	files, err := r.m.KeepEntry(tid, es[1].ID)
	if err != nil || len(files) != 1 {
		t.Fatalf("keep: %v %v", files, err)
	}
	if got := readFile(t, repo, "a.txt"); got != "v\nv\n" {
		t.Fatalf("a.txt = %q", got)
	}
	for _, e := range []store.Task{es[0], es[2]} {
		if _, err := os.Stat(e.Run.Path); !os.IsNotExist(err) {
			t.Errorf("the worktree of %s is still there: %v", e.ID, err)
		}
	}
	has(t, taskOf(t, r, tid, race.ID).Result, "The user keeps one entry", "the user kept "+es[1].ID)
	if _, err := r.m.KeepEntry(tid, race.ID); err == nil {
		t.Fatal("kept the race itself")
	}
}

func TestAgentRaceReportsOnceAndIsJudged(t *testing.T) {
	r, tid, _ := raceRig(t,
		[]fake.Script{turn(say("entry done"), finish())},
		[]fake.Script{
			turn(fake.Step{Exec: &fake.Exec{Op: "task.race", Args: []byte(`{"brief": "speed up the parser", "agents": "claude, claude", "judge": "claude"}`)}}, say("racing"), finish()),
			turn(say("noted"), finish()),
		})
	lead := r.add(t, tid, "codex")
	_ = r.m.Post(tid, "$codex go")
	waitFor(t, "the summary", func() bool { return len(r.codex.prompts(lead)) >= 2 })
	has(t, r.c.toolResults(lead, "deck")[0], "started race t1 with entries t2, t3")
	p := r.codex.prompts(lead)[1]
	has(t, p, "race: speed up the parser", "t2 ", "t3 ", "entry done", "A claude judge ranks them next")

	waitFor(t, "the judge's ranking", func() bool { tk, ok := r.m.Task(tid, "t4"); return ok && tk.Status == store.TaskDone })
	waitFor(t, "the ranking reaches the lead", func() bool {
		return strings.Contains(strings.Join(r.codex.prompts(lead), ""), `task t4 "rank the entries of t1" done`)
	})
	if judge := r.claude.prompts(taskOf(t, r, tid, "t4").Owner)[0]; !strings.Contains(judge, "Rank 2 attempts") || !strings.Contains(judge, "git -C ") {
		t.Fatalf("judge brief:\n%s", judge)
	}
	// One message for the race and one for the ranking: none for each entry.
	for _, q := range r.codex.prompts(lead)[1:] {
		lacks(t, q, "→ codex] task t2", "→ codex] task t3")
	}
}

func TestRaceRefusals(t *testing.T) {
	r := newRig(t, nil, nil)
	tid := r.thread(t) // not a git repository
	two := []Entrant{{Agent: "claude"}, {Agent: "codex"}}
	if _, err := r.m.Race(tid, "", RaceSpec{Brief: "x", Entrants: two}); err == nil || !strings.Contains(err.Error(), "git repository") {
		t.Fatalf("outside git: %v", err)
	}
	if _, err := r.m.Race(tid, "", RaceSpec{Brief: "x", Entrants: two[:1]}); err == nil {
		t.Fatal("a race of one")
	}
	if _, err := r.m.Race(tid, "", RaceSpec{Brief: "x", Entrants: []Entrant{{Agent: "claude"}, {Agent: "gemini"}}}); err == nil || !strings.Contains(err.Error(), `"gemini"`) {
		t.Fatalf("an unknown agent: %v", err)
	}
	if len(r.m.Tasks(tid)) != 0 {
		t.Fatalf("a refused race left tasks: %+v", r.m.Tasks(tid))
	}
	r.add(t, tid, "claude")
	if _, err := r.m.Race(tid, "claude", RaceSpec{Brief: "x", Entrants: two}); err == nil || !strings.Contains(err.Error(), "full orchestration") {
		t.Fatalf("an agent without orchestration: %v", err)
	}
}

// A race mixes kinds and repeats one on the same model: each entry runs the
// agent, model and effort it was given.
func TestMixedRaceRunsEachEntrant(t *testing.T) {
	work := []fake.Script{turn(say("done"), finish())}
	r, tid, _ := raceRig(t, work, work)
	res, err := r.m.Race(tid, "", RaceSpec{Brief: "fix the flaky limiter test", Entrants: []Entrant{
		{Agent: "codex", Model: "gpt-x", Effort: "High"},
		{Agent: "Claude", Model: "opus"},
		{Agent: "$claude", Model: "opus"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	es := entries(r, tid, res.TaskID)
	if len(es) != 3 {
		t.Fatalf("entries = %+v", es)
	}
	type ran struct{ kind, model, effort string }
	want := []ran{{"codex", "gpt-x", "high"}, {"claude", "opus", ""}, {"claude", "opus", ""}}
	owners := map[string]bool{}
	for i, e := range es {
		if got := (ran{e.Kind, e.Model, e.Effort}); got != want[i] {
			t.Errorf("entry %d = %+v, want %+v", i, got, want[i])
		}
		waitFor(t, "entry "+e.ID+" has an owner", func() bool { return taskOf(t, r, tid, e.ID).Owner != "" })
		owner := taskOf(t, r, tid, e.ID).Owner
		owners[owner] = true
		sp, ok := r.claude.spec(owner)
		if want[i].kind == "codex" {
			sp, ok = r.codex.spec(owner)
		}
		if !ok || sp.Model != want[i].model {
			t.Errorf("entry %d runs %+v (ok %v), want model %q", i, sp, ok, want[i].model)
		}
	}
	if len(owners) != 3 {
		t.Fatalf("the entries share members: %v", owners)
	}
}

// A named agent races as itself, and may judge as its kind.
func TestRaceWithNamedAgent(t *testing.T) {
	work := []fake.Script{turn(say("done"), finish())}
	r, tid, _ := raceRig(t, work, work)
	r.m.SetProfiles(func(string) []profile.Profile {
		return []profile.Profile{{Name: "ada", Kind: "claude", Model: "sonnet"}}
	})
	res, err := r.m.Race(tid, "", RaceSpec{Brief: "x", Entrants: []Entrant{{Agent: "@Ada"}, {Agent: "codex"}}, Judge: "ada"})
	if err != nil {
		t.Fatal(err)
	}
	es := entries(r, tid, res.TaskID)
	if len(es) != 2 || es[0].Kind != "claude" || es[1].Kind != "codex" {
		t.Fatalf("entries = %+v", es)
	}
	if race := taskOf(t, r, tid, res.TaskID); race.Judge != "claude" {
		t.Fatalf("judge = %q", race.Judge)
	}
	waitFor(t, "ada's entry starts", func() bool { return taskOf(t, r, tid, es[0].ID).Owner != "" })
	ti, _ := r.m.Thread(tid)
	found := false
	for _, mb := range ti.Members {
		found = found || mb.Profile == "ada"
	}
	if !found {
		t.Fatalf("no member from ada: %+v", ti.Members)
	}
}
