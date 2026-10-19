package session

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/dev-dami/deck/internal/deck/agent"
	"github.com/dev-dami/deck/internal/deck/agent/fake"
	"github.com/dev-dami/deck/internal/deck/store"
)

func usage(in, out int) fake.Step {
	return fake.Step{Event: agent.Event{Kind: agent.KindUsage, Usage: &agent.Usage{InputTokens: in, OutputTokens: out}}}
}

func TestWorkLimitPausesSpawnsUntilTheUserWrites(t *testing.T) {
	cfg := testCfg()
	cfg.MaxWork, cfg.MaxChildren = 2, 10
	r, tid := treeRig(t, cfg, nil, []fake.Script{turn(blockStep("ap"), say("done"), finish())})
	for i := range 2 {
		if _, err := r.m.Spawn(tid, "claude", SpawnSpec{Kind: "codex", Brief: fmt.Sprint("job ", i)}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := r.m.Spawn(tid, "claude", SpawnSpec{Kind: "codex", Brief: "one more"}); err == nil || !strings.Contains(err.Error(), "paused") {
		t.Fatalf("past max_work: %v", err)
	}
	if th, _ := r.m.Thread(tid); !th.Paused {
		t.Fatal("the thread is not paused")
	}
	waitFor(t, "a pause notice with its reason", func() bool {
		return r.c.count(func(m tea.Msg) bool { p, ok := m.(PausedMsg); return ok && strings.Contains(p.Reason, "2 subagents") }) == 1
	})
	// The user's next message lifts it.
	if err := r.m.Continue(tid); err != nil {
		t.Fatal(err)
	}
	if _, err := r.m.Spawn(tid, "claude", SpawnSpec{Kind: "codex", Brief: "after continue"}); err != nil {
		t.Fatalf("after Continue: %v", err)
	}
}

func TestSchedulerStopsAtTheWorkLimit(t *testing.T) {
	cfg := testCfg()
	cfg.MaxWork = 1
	r, tid := treeRig(t, cfg, nil, []fake.Script{turn(blockStep("ap"), say("done"), finish())})
	a, _ := r.m.CreateTask(tid, TaskSpec{Title: "first", Kind: "codex"})
	b, _ := r.m.CreateTask(tid, TaskSpec{Title: "second", Kind: "codex"})
	waitFor(t, "the first dispatched", func() bool { return taskOf(t, r, tid, a.ID).Status == store.TaskRunning })
	waitFor(t, "the pause", func() bool { th, _ := r.m.Thread(tid); return th.Paused })
	if st := taskOf(t, r, tid, b.ID).Status; st != store.TaskReady {
		t.Fatalf("the second ran past max_work: %s", st)
	}
	if err := r.m.Continue(tid); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the second dispatched", func() bool { return taskOf(t, r, tid, b.ID).Status == store.TaskRunning })
}

func TestBudgetCountsSubagentsAndBlocksTheTask(t *testing.T) {
	r, tid := treeRig(t, testCfg(),
		[]fake.Script{turn(usage(150, 50), blockStep("ap-b"), say("never"), finish())},
		[]fake.Script{turn(blockStep("ap-a"), say("never"), finish())})
	tk, err := r.m.CreateTask(tid, TaskSpec{Title: "lead", Creator: "claude", Budget: &store.Budget{Tokens: 150}})
	if err != nil {
		t.Fatal(err)
	}
	a, err := r.m.Spawn(tid, "claude", SpawnSpec{Kind: "codex", Brief: "lead it", TaskID: tk.ID})
	if err != nil {
		t.Fatal(err)
	}
	approvalOf(t, r, a.AgentID)
	// a's helper spends 200 tokens: that is a's task's money too.
	b, err := r.m.Spawn(tid, a.AgentID, SpawnSpec{Kind: "claude", Brief: "help"})
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the lead task blocked", func() bool { return taskOf(t, r, tid, tk.ID).Status == store.TaskBlocked })
	has(t, taskOf(t, r, tid, tk.ID).Error, "over its budget: 200 of 150 tokens")
	if st := taskOf(t, r, tid, b.TaskID).Status; st != store.TaskRunning {
		t.Fatalf("the helper's own task has no budget, so it stays %s", st)
	}
	// The creator hears about it.
	waitFor(t, "claude told", func() bool { return len(r.claude.prompts("claude")) == 1 })
	has(t, r.claude.prompts("claude")[0], "blocked: over its budget")
}

func TestOnlyTheCreatorChangesABudget(t *testing.T) {
	r := newRig(t, nil, nil) // no orchestration: nothing runs the task
	tid := r.thread(t)
	r.add(t, tid, "claude")
	r.add(t, tid, "codex")
	tk, err := r.m.CreateTask(tid, TaskSpec{Title: "x", Creator: "claude", Owner: "codex"})
	if err != nil {
		t.Fatal(err)
	}
	raise := &store.Budget{Tokens: 1_000_000}
	if _, err := r.m.UpdateTask(tid, tk.ID, "codex", TaskPatch{Budget: raise}); err == nil {
		t.Fatal("the owner raised its own budget")
	}
	if _, err := r.m.UpdateTask(tid, tk.ID, "", TaskPatch{Budget: raise}); err != nil {
		t.Fatalf("the user: %v", err)
	}
}

func TestOverBudget(t *testing.T) {
	now := time.Now()
	cases := []struct {
		b      store.Budget
		spent  store.Spend
		extra  store.Spend
		start  time.Time
		expect string
	}{
		{store.Budget{}, store.Spend{Tokens: 1e9}, store.Spend{}, now.Add(-time.Hour), ""},
		{store.Budget{Tokens: 100}, store.Spend{Tokens: 60}, store.Spend{Tokens: 50}, now, "110 of 100 tokens"},
		{store.Budget{Tokens: 200_000}, store.Spend{Tokens: 250_000}, store.Spend{}, now, "250k of 200k tokens"},
		{store.Budget{CostUSD: 1}, store.Spend{CostUSD: 0.6}, store.Spend{CostUSD: 0.5}, now, "$1.10 of $1.00"},
		{store.Budget{Minutes: 5}, store.Spend{}, store.Spend{}, now.Add(-6 * time.Minute), "ran 6 of 5 minutes"},
		{store.Budget{Minutes: 5}, store.Spend{}, store.Spend{}, now.Add(-4 * time.Minute), ""},
	}
	for _, c := range cases {
		got := overBudget(c.b, c.spent, c.extra, c.start, now)
		if c.expect == "" && got != "" || !strings.Contains(got, c.expect) {
			t.Errorf("overBudget(%+v, %+v, %+v) = %q, want %q", c.b, c.spent, c.extra, got, c.expect)
		}
	}
}

func TestSendGuards(t *testing.T) {
	r := newRig(t, nil, nil)
	tid := r.thread(t)
	r.add(t, tid, "claude")
	r.add(t, tid, "codex")
	th := r.m.thread(tid)
	th.mu.Lock()
	defer th.mu.Unlock()
	claude, codex := th.member("claude"), th.member("codex")
	now := time.Now()

	// claude has maxWaiting messages from agents it has not read.
	for i := range maxWaiting {
		th.log = append(th.log, store.Entry{Type: store.EntryRelay, Speaker: "codex", To: []string{"claude"}, Event: eventText(fmt.Sprint(i))})
	}
	if _, err := th.guardSend(codex, []*agentState{claude}, now); err == nil || !strings.Contains(err.Error(), "has not read") {
		t.Fatalf("to a full inbox: %v", err)
	}
	claude.cursor = len(th.log) // it read them

	for i := range maxSendsPerMinute {
		if _, err := th.guardSend(codex, []*agentState{claude}, now.Add(time.Duration(i)*time.Second)); err != nil {
			t.Fatalf("send %d: %v", i, err)
		}
	}
	if _, err := th.guardSend(codex, []*agentState{claude}, now.Add(30*time.Second)); err == nil || !strings.Contains(err.Error(), "slow down") {
		t.Fatalf("a burst: %v", err)
	}
	if _, err := th.guardSend(codex, []*agentState{claude}, now.Add(2*time.Minute)); err != nil {
		t.Fatalf("a minute later: %v", err)
	}
}

func TestRouteByPlanLimits(t *testing.T) {
	r := newRig(t, nil, nil)
	window := func(pct float64, resets time.Time) agent.Limits {
		return agent.Limits{Windows: []agent.LimitWindow{{ID: "five_hour", Label: "5h", UsedPct: pct, ResetsAt: resets}}}
	}
	later := time.Now().Add(time.Hour)

	if k, _ := r.m.routeKind("claude"); k != "claude" {
		t.Fatalf("no limits known: %s", k)
	}
	r.m.setLimits("claude", window(95, later))
	if k, note := r.m.routeKind("claude"); k != "codex" || !strings.Contains(note, "5h window is 95% used") {
		t.Fatalf("claude full, codex unknown: %s %q", k, note)
	}
	r.m.setLimits("codex", window(97, later))
	if k, _ := r.m.routeKind("claude"); k != "claude" {
		t.Fatalf("both full: %s", k)
	}
	r.m.setLimits("codex", window(40, later))
	if k, _ := r.m.routeKind("claude"); k != "codex" {
		t.Fatalf("codex has room: %s", k)
	}
	r.m.setLimits("claude", window(95, time.Now().Add(-time.Minute)))
	if k, _ := r.m.routeKind("claude"); k != "claude" {
		t.Fatalf("claude's window has reset: %s", k)
	}

	// A spawn that names no kind follows it, and says so.
	r.m.setLimits("claude", window(99, later))
	tid := r.thread(t)
	res, err := r.m.Spawn(tid, "", SpawnSpec{Brief: "anything"})
	if err != nil {
		t.Fatal(err)
	}
	if s, _ := r.m.Agent(res.AgentID); s.Kind != "codex" {
		t.Fatalf("spawned %s", s.Kind)
	}
	waitFor(t, "a routing notice", func() bool {
		return r.c.count(func(m tea.Msg) bool {
			n, ok := m.(NoticeMsg)
			return ok && strings.Contains(n.Text, "so codex took this task")
		}) == 1
	})
}
