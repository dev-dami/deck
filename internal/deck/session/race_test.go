package session

import (
	"os"
	"strings"
	"testing"

	"github.com/dev-dami/deck/internal/deck/agent"
	"github.com/dev-dami/deck/internal/deck/agent/fake"
	"github.com/dev-dami/deck/internal/deck/store"
)

func TestRaceAddressing(t *testing.T) {
	kinds := []string{"claude", "codex"}
	ad := ParseAddressing("$claude×2 $codex*1 fix the flaky test", nil, kinds)
	if strings.Join(ad.Race, ",") != "claude,claude,codex" || ad.Body != "fix the flaky test" {
		t.Fatalf("race = %q, body %q", ad.Race, ad.Body)
	}
	for _, text := range []string{"$claude* hi", "$claude*3x hi", "$nobody×2 hi", "tell $claude×2 hi", "$claude×0 hi"} {
		if ad := ParseAddressing(text, nil, kinds); len(ad.Race) > 0 {
			t.Errorf("%q started a race: %q", text, ad.Race)
		}
	}

	got, err := parseEntries("claude*2, codex $codex×1", kinds)
	if err != nil || strings.Join(got, ",") != "claude,claude,codex,codex" {
		t.Fatalf("parseEntries = %q, %v", got, err)
	}
	if _, err := parseEntries("claude, gemini", kinds); err == nil {
		t.Fatal("an unknown kind")
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
	if err := r.m.SetOrchestrate(tid, true); err != nil {
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
	if err := r.m.Post(tid, "$claude×2 $codex×1 improve a.txt"); err != nil {
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
			turn(fake.Step{Exec: &fake.Exec{Op: "task.race", Args: []byte(`{"brief": "speed up the parser", "agents": "claude*2", "judge": "claude"}`)}}, say("racing"), finish()),
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
	if _, err := r.m.Race(tid, "", RaceSpec{Brief: "x", Kinds: []string{"claude", "codex"}}); err == nil || !strings.Contains(err.Error(), "git repository") {
		t.Fatalf("outside git: %v", err)
	}
	if _, err := r.m.Race(tid, "", RaceSpec{Brief: "x", Kinds: []string{"claude"}}); err == nil {
		t.Fatal("a race of one")
	}
	if len(r.m.Tasks(tid)) != 0 {
		t.Fatalf("a refused race left tasks: %+v", r.m.Tasks(tid))
	}
	r.add(t, tid, "claude")
	if _, err := r.m.Race(tid, "claude", RaceSpec{Brief: "x", Kinds: []string{"claude", "codex"}}); err == nil || !strings.Contains(err.Error(), "orchestration is off") {
		t.Fatalf("an agent without orchestration: %v", err)
	}
}
