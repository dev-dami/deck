package session

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/dev-dami/deck/internal/deck/agent"
	"github.com/dev-dami/deck/internal/deck/agent/fake"
	"github.com/dev-dami/deck/internal/deck/profile"
	"github.com/dev-dami/deck/internal/deck/store"
)

// The user asks their agent to split the work: with orchestration off it
// still spawns, through the agent.spawn op, and hears back from its helper.
func TestAUsersAgentSpawnsWhenAskedAndHearsBack(t *testing.T) {
	spawn, _ := json.Marshal(map[string]any{"brief": "write the limiter tests", "kind": "codex", "title": "limiter tests"})
	r := newRig(t,
		[]fake.Script{
			turn(fake.Step{Exec: &fake.Exec{Op: "agent.spawn", Args: spawn}}, say("codex is on the tests"), finish()),
			turn(say("merged the tests"), finish()),
		},
		[]fake.Script{turn(say("tests written"), finish())})
	tid := r.thread(t)
	r.add(t, tid, "claude")
	if err := r.m.Post(tid, "split this up: spin up a codex agent for the tests"); err != nil {
		t.Fatal(err)
	}

	waitFor(t, "the helper's task done", func() bool { tk, ok := r.m.Task(tid, "t1"); return ok && tk.Status == store.TaskDone })
	tk := taskOf(t, r, tid, "t1")
	if tk.Result != "tests written" || tk.Creator != "claude" || tk.Owner != "codex" || tk.Title != "limiter tests" {
		t.Fatalf("task = %+v", tk)
	}
	a, _ := memberStatus(r, "codex")
	if !a.Ephemeral || a.Parent != "claude" || a.Kind != "codex" {
		t.Fatalf("helper = %+v", a)
	}
	// The brief goes from the spawner to the helper as a handoff the thread shows.
	var brief *RelayMsg
	for _, m := range r.c.relays() {
		if m.Via == "spawn" {
			brief = &m
		}
	}
	if brief == nil || brief.From != "claude" || len(brief.To) != 1 || brief.To[0] != "codex" || !strings.Contains(brief.Text, "write the limiter tests") {
		t.Fatalf("spawn relay = %+v", brief)
	}
	has(t, r.codex.prompts("codex")[0], "write the limiter tests", "You cannot start subagents")
	waitFor(t, "claude told", func() bool { return len(r.claude.prompts("claude")) == 2 })
	has(t, r.claude.prompts("claude")[1], `task t1 "limiter tests" done: tests written`)
}

// Without orchestration a helper starts no helpers, and a member keeps to
// MaxChildren live helpers.
func TestSpawnWithoutOrchestrationIsOneLevel(t *testing.T) {
	cfg := testCfg()
	cfg.MaxChildren = 2
	r := newRigWith(t, t.TempDir(), false, cfg, nil, []fake.Script{turn(blockStep("ap"), say("done"), finish())})
	tid := r.thread(t)
	r.add(t, tid, "claude")
	a, err := r.m.Spawn(tid, "claude", SpawnSpec{Kind: "codex", Brief: "one"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.m.Spawn(tid, a.AgentID, SpawnSpec{Kind: "codex", Brief: "nested"}); err == nil || !strings.Contains(err.Error(), "orchestration is off") {
		t.Fatalf("a helper spawning with orchestration off: %v", err)
	}
	if _, err := r.m.Spawn(tid, "claude", SpawnSpec{Kind: "codex", Brief: "two"}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.m.Spawn(tid, "claude", SpawnSpec{Kind: "codex", Brief: "three"}); err == nil || !strings.Contains(err.Error(), "2 of yours are already running") {
		t.Fatalf("past max_children: %v", err)
	}
	// Orchestration on lets the helper go a level deeper.
	if err := r.m.SetOrchestrate(tid, true); err != nil {
		t.Fatal(err)
	}
	if _, err := r.m.Spawn(tid, a.AgentID, SpawnSpec{Kind: "codex", Brief: "nested"}); err != nil {
		t.Fatalf("a helper spawning with orchestration on: %v", err)
	}
}

// A helper runs in the thread's permission mode, and a named agent's name
// starts it from its profile.
func TestSpawnInheritsTheModeAndTakesNamedAgents(t *testing.T) {
	r := newRig(t, nil, []fake.Script{turn(say("done"), finish())})
	r.m.SetProfiles(func(string) []profile.Profile {
		return []profile.Profile{{Name: "ada", Kind: "codex", Model: "gpt-ada", Role: "writes tests"}}
	})
	tid := r.thread(t)
	r.add(t, tid, "claude")
	if err := r.m.SetMode(tid, agent.ModeYolo); err != nil {
		t.Fatal(err)
	}
	res, err := r.m.Spawn(tid, "claude", SpawnSpec{Kind: "ada", Brief: "cover the limiter"})
	if err != nil {
		t.Fatal(err)
	}
	a, _ := memberStatus(r, res.AgentID)
	if a.Name != "ada" || a.Kind != "codex" || a.Model != "gpt-ada" || a.Profile != "ada" || a.Parent != "claude" {
		t.Fatalf("helper = %+v", a)
	}
	sp, ok := r.codex.spec(res.AgentID)
	if !ok || sp.Mode != agent.ModeYolo {
		t.Fatalf("helper's mode = %q", sp.Mode)
	}
	if _, err := r.m.Spawn(tid, "claude", SpawnSpec{Kind: "nobody", Brief: "x"}); err == nil || !strings.Contains(err.Error(), "unknown agent kind") {
		t.Fatalf("an unknown name: %v", err)
	}
}
