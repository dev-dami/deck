package session

import (
	"strings"
	"testing"
	"time"

	"github.com/dev-dami/deck/internal/deck/agent/fake"
	"github.com/dev-dami/deck/internal/deck/config"
	"github.com/dev-dami/deck/internal/deck/store"
)

// levelRig is a thread at level with the user's claude in it; every codex
// helper blocks on an approval, so it stays live.
func levelRig(t *testing.T, level string) (*rig, string) {
	t.Helper()
	cfg := testCfg()
	cfg.Orchestration = level
	r := newRigWith(t, t.TempDir(), false, cfg, nil, []fake.Script{turn(blockStep("ap"), say("done"), finish())})
	tid := r.thread(t)
	r.add(t, tid, "claude")
	return r, tid
}

// userAsked makes member id's turn one the user spoke to it in.
func userAsked(r *rig, tid, id string) {
	th := r.m.thread(tid)
	th.mu.Lock()
	th.member(id).promptUser = 12
	th.mu.Unlock()
}

func TestNewThreadsStartAtTheConfiguredLevel(t *testing.T) {
	cfg := testCfg()
	cfg.Orchestration = ""
	r := newRigWith(t, t.TempDir(), false, cfg, nil, nil)
	if info, _ := r.m.Thread(r.thread(t)); info.Orchestration != config.LevelOff {
		t.Fatalf("default level = %q", info.Orchestration)
	}
	cfg.Orchestration = config.LevelFull
	r.m.SetConfig(cfg)
	tid := r.thread(t)
	if info, _ := r.m.Thread(tid); info.Orchestration != config.LevelFull {
		t.Fatalf("configured level = %q", info.Orchestration)
	}
	if err := r.m.SetLevel(tid, "max"); err == nil {
		t.Fatal("an unknown level")
	}
}

// A thread stored before levels keeps its choice: on is auto, and one that
// never turned it on takes the default.
func TestStoredLevel(t *testing.T) {
	cfg := config.Config{Orchestration: config.LevelAssist}
	for _, tc := range []struct {
		th   store.Thread
		want string
	}{
		{store.Thread{Orchestration: "full"}, "full"},
		{store.Thread{Orchestration: "off", Orchestrate: true}, "off"},
		{store.Thread{Orchestrate: true}, "auto"},
		{store.Thread{}, "assist"},
	} {
		if got := storedLevel(tc.th, cfg); got != tc.want {
			t.Errorf("storedLevel(%+v) = %q, want %q", tc.th, got, tc.want)
		}
	}
}

// Each level caps how many helpers a member runs, how deep helpers go, and
// whether agents may race.
func TestLevelsLimitSpawning(t *testing.T) {
	for _, tc := range []struct {
		level    string
		children int  // helpers the user's agent may have live
		depth    int  // levels of helpers below it
		race     bool // an agent may race
	}{
		{config.LevelOff, 4, 1, false},
		{config.LevelAssist, 2, 1, false},
		{config.LevelAuto, 4, 2, false},
		{config.LevelFull, 6, 3, true},
	} {
		t.Run(tc.level, func(t *testing.T) {
			r, tid := levelRig(t, tc.level)
			if tc.level == config.LevelOff {
				_, err := r.m.Spawn(tid, "claude", SpawnSpec{Kind: "codex", Brief: "unasked"})
				if err == nil || !strings.Contains(err.Error(), "only when the user asks") {
					t.Fatalf("an unasked spawn with orchestration off: %v", err)
				}
				userAsked(r, tid, "claude")
			}
			var first string
			for i := range tc.children {
				res, err := r.m.Spawn(tid, "claude", SpawnSpec{Kind: "codex", Brief: "part"})
				if err != nil {
					t.Fatalf("helper %d: %v", i+1, err)
				}
				if first == "" {
					first = res.AgentID
				}
			}
			if _, err := r.m.Spawn(tid, "claude", SpawnSpec{Kind: "codex", Brief: "one more"}); err == nil || !strings.Contains(err.Error(), "already running") {
				t.Fatalf("past %d helpers: %v", tc.children, err)
			}
			// Down the tree from the first helper, as far as the level goes.
			parent := first
			for d := 2; d <= tc.depth+1; d++ {
				res, err := r.m.Spawn(tid, parent, SpawnSpec{Kind: "codex", Brief: "deeper"})
				if ok := d <= tc.depth; ok != (err == nil) {
					t.Fatalf("a helper at depth %d: %v", d, err)
				}
				parent = res.AgentID
			}
			_, err := r.m.Race(tid, "claude", RaceSpec{Brief: "x", Entrants: []Entrant{{Agent: "claude"}, {Agent: "codex"}}})
			if gated := err != nil && strings.Contains(err.Error(), "full orchestration"); gated == tc.race {
				t.Fatalf("an agent's race: %v", err)
			}
		})
	}
}

// The scheduler runs ready tasks at auto and full only.
func TestLevelsThatSchedule(t *testing.T) {
	for _, level := range config.LevelNames {
		t.Run(level, func(t *testing.T) {
			cfg := testCfg()
			cfg.DefaultAgent, cfg.Orchestration = "codex", level
			r := newRigWith(t, t.TempDir(), false, cfg, nil, []fake.Script{turn(say("built"), finish())})
			tid := r.thread(t)
			mkTask(t, r, tid, TaskSpec{Title: "build"})
			if config.Levels[level].Schedule {
				// At full it then goes to review: started is enough.
				waitFor(t, "the task started", func() bool { return taskOf(t, r, tid, "t1").Attempts == 1 })
				return
			}
			time.Sleep(150 * time.Millisecond)
			if tk := taskOf(t, r, tid, "t1"); tk.Status != store.TaskReady {
				t.Fatalf("at %s the scheduler ran it: %+v", level, tk)
			}
		})
	}
}

// What a member's instructions say follows the level, within their budget.
func TestInstructionsFollowTheLevel(t *testing.T) {
	for _, tc := range []struct {
		level      string
		has, lacks []string
	}{
		{config.LevelOff, []string{"Orchestration is off", "only when the user asks"}, []string{"task.race {", "task board (deps)"}},
		{config.LevelAssist, []string{"Orchestration is assist", "up to 2 helpers"}, []string{"task.race {", "task board (deps)"}},
		{config.LevelAuto, []string{"Orchestration is auto", "task board (deps)", "4 each, 12 in the thread, 2 levels deep", "review each other"}, []string{"task.race {"}},
		{config.LevelFull, []string{"Orchestration is full", "6 each, 20 in the thread, 3 levels deep", "task.race {brief, agents}", "Helpers' work is reviewed"}, nil},
	} {
		t.Run(tc.level, func(t *testing.T) {
			r, tid := levelRig(t, tc.level)
			r.m.SetTeam(&postTeam{})
			r.add(t, tid, "codex")
			sp, _ := r.codex.spec("codex")
			has(t, sp.Instructions, tc.has...)
			lacks(t, sp.Instructions, tc.lacks...)
			has(t, sp.Instructions, "Teammates' Decks", "Write plan files in") // nothing was cut
			if len(sp.Instructions) > maxInstrChars {
				t.Fatalf("Instructions are %d bytes", len(sp.Instructions))
			}
		})
	}
}

// A level change reaches the members with their next prompt.
func TestMembersHearOfALevelChange(t *testing.T) {
	r := newRig(t, []fake.Script{turn(say("ok"), finish())}, nil)
	tid := r.thread(t)
	r.add(t, tid, "claude")
	if err := r.m.SetLevel(tid, config.LevelFull); err != nil {
		t.Fatal(err)
	}
	_ = r.m.Post(tid, "$claude go")
	waitFor(t, "claude's turn", func() bool { return len(r.claude.prompts("claude")) == 1 })
	has(t, r.claude.prompts("claude")[0], "The user set orchestration to full", "task.race")
}

// At full a helper's work is reviewed by another kind unless the config or
// the task says otherwise; below full it is not.
func TestFullReviewsHelpersWork(t *testing.T) {
	r := newRig(t, nil, nil)
	run := store.Task{Run: &store.RunRef{}}
	if got := r.m.reviewOf(run, config.Levels[config.LevelFull]); got != store.ReviewOther {
		t.Fatalf("full: %q", got)
	}
	if got := r.m.reviewOf(run, config.Levels[config.LevelAuto]); got != "" {
		t.Fatalf("auto: %q", got)
	}
	run.Review = store.ReviewNone
	if got := r.m.reviewOf(run, config.Levels[config.LevelFull]); got != "" {
		t.Fatalf("a task that asks for none: %q", got)
	}
	if got := r.m.reviewOf(store.Task{}, config.Levels[config.LevelFull]); got != "" {
		t.Fatalf("the user's own task: %q", got)
	}
}
