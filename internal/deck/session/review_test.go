package session

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/dev-dami/deck/internal/deck/agent"
	"github.com/dev-dami/deck/internal/deck/agent/fake"
	"github.com/dev-dami/deck/internal/deck/config"
	"github.com/dev-dami/deck/internal/deck/store"
)

// runsOf is a fake adapter whose nth run plays the nth list of turns, and the
// last list once they run out: each reviewer can answer differently.
type runsOf struct {
	mu   sync.Mutex
	hub  *fake.Hub
	runs [][]fake.Script
	n    int
}

func (r *runsOf) Name() string { return "fake" }

func (r *runsOf) Start(ctx context.Context, sp agent.Spec) (agent.Run, error) {
	r.mu.Lock()
	turns := r.runs[min(r.n, len(r.runs)-1)]
	r.n++
	r.mu.Unlock()
	return fake.NewTurns(0, r.hub, turns...).Start(ctx, sp)
}

// reviewRig is a thread with orchestration on and the user's claude in it.
// Each list of claude's and codex's is one run; codex nil leaves it out.
func reviewRig(t *testing.T, cfg config.Config, claude, codex [][]fake.Script) (*rig, string) {
	t.Helper()
	return reviewRigWith(t, cfg, claude, codex, func(_ string, a agent.Adapter) agent.Adapter { return a })
}

// reviewRigWith is reviewRig with each kind's adapter passed through wrap
// before the spy.
func reviewRigWith(t *testing.T, cfg config.Config, claude, codex [][]fake.Script, wrap func(kind string, a agent.Adapter) agent.Adapter) (*rig, string) {
	t.Helper()
	st, err := store.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	hub := fake.NewHub()
	r := &rig{st: st, hub: hub, claude: newSpy(wrap("claude", &runsOf{hub: hub, runs: claude}))}
	ads := map[string]agent.Adapter{"claude": r.claude}
	if codex != nil {
		r.codex = newSpy(wrap("codex", &runsOf{hub: hub, runs: codex}))
		ads["codex"] = r.codex
	}
	r.m = NewManager(ads, st, cfg, Options{Hub: hub, MCPFor: fake.MCPFor})
	t.Cleanup(r.m.Close)
	r.c = collect(t, r.m)
	tid, err := r.m.NewThread(gitRepo(t))
	if err != nil {
		t.Fatal(err)
	}
	r.add(t, tid, "claude")
	if err := r.m.SetLevel(tid, "auto"); err != nil {
		t.Fatal(err)
	}
	return r, tid
}

func callOp(op, args string) fake.Step {
	return fake.Step{Exec: &fake.Exec{Op: op, Args: []byte(args)}}
}

var (
	lead        = []fake.Script{turn(say("noted"), finish())}
	approves    = []fake.Script{turn(callOp("review.approve", `{"id": "t1", "note": "tests pass"}`), say("approved"), finish())}
	asksChanges = []fake.Script{turn(callOp("review.changes", `{"id": "t1", "notes": "add a test"}`), say("sent back"), finish())}
)

// told lists the messages about task t1 that reached the lead.
func told(r *rig) []string {
	var out []string
	for _, p := range r.claude.prompts("claude") {
		if strings.Contains(p, `task t1 "`) {
			out = append(out, p)
		}
	}
	return out
}

func spawnReviewed(t *testing.T, r *rig, tid string) {
	t.Helper()
	if _, err := r.m.Spawn(tid, "claude", SpawnSpec{Kind: "codex", Brief: "fix the parser", Review: "other"}); err != nil {
		t.Fatal(err)
	}
}

func TestReviewApproves(t *testing.T) {
	r, tid := reviewRig(t, testCfg(), [][]fake.Script{lead, approves}, [][]fake.Script{{turn(say("fixed the parser"), finish())}})
	spawnReviewed(t, r, tid)
	waitFor(t, "t1 done", func() bool { return taskOf(t, r, tid, "t1").Status == store.TaskDone })
	tk := taskOf(t, r, tid, "t1")
	has(t, tk.Result, "fixed the parser", "approved: tests pass")
	if tk.Reviews != 1 || tk.Reviewer != "" || tk.Attempts != 1 {
		t.Fatalf("task = %+v", tk)
	}
	reviewer := ""
	for _, a := range r.m.Agents() {
		if a.Kind == "claude" && a.Ephemeral {
			reviewer = a.ID
		}
	}
	has(t, r.claude.prompts(reviewer)[0], `Review task t1 "fix the parser"`, "fixed the parser", "review.approve", "shared work tree")
	waitFor(t, "the reviewer's answer", func() bool { return len(r.c.toolResults(reviewer, "deck")) == 1 })
	has(t, r.c.toolResults(reviewer, "deck")[0], "approved t1")
	waitFor(t, "the lead told", func() bool { return len(told(r)) == 1 })
	has(t, told(r)[0], "done: fixed the parser", "approved: tests pass")
	waitFor(t, "both subagents stopped", func() bool {
		for _, a := range r.m.Agents() {
			if a.Ephemeral && a.Status != StatusExited {
				return false
			}
		}
		return true
	})
	if n := len(told(r)); n != 1 {
		t.Fatalf("the lead was told %d times", n)
	}
}

func TestReviewChangesGoBackToTheWorker(t *testing.T) {
	r, tid := reviewRig(t, testCfg(), [][]fake.Script{lead, asksChanges, approves},
		[][]fake.Script{{turn(say("v1"), finish()), turn(say("v2 with a test"), finish())}})
	spawnReviewed(t, r, tid)
	waitFor(t, "t1 done", func() bool { return taskOf(t, r, tid, "t1").Status == store.TaskDone })
	tk := taskOf(t, r, tid, "t1")
	has(t, tk.Result, "v2 with a test", "approved")
	lacks(t, tk.Result, "v1")
	if tk.Reviews != 2 || tk.Attempts != 1 {
		t.Fatalf("task = %+v", tk)
	}
	ps := r.codex.prompts(tk.Owner)
	if len(ps) != 2 {
		t.Fatalf("the worker got %d prompts", len(ps))
	}
	has(t, ps[1], "asks for changes (round 1 of 2)", "add a test", "This hands you task t1")
	waitFor(t, "the lead told", func() bool { return len(told(r)) == 1 })
	has(t, told(r)[0], "v2 with a test")
}

func TestReviewThatDoesNotConvergeBlocks(t *testing.T) {
	work := []fake.Script{turn(say("v1"), finish()), turn(say("v2"), finish()), turn(say("v3"), finish())}
	r, tid := reviewRig(t, testCfg(), [][]fake.Script{lead, asksChanges}, [][]fake.Script{work})
	spawnReviewed(t, r, tid)
	waitFor(t, "t1 blocked", func() bool { return taskOf(t, r, tid, "t1").Status == store.TaskBlocked })
	tk := taskOf(t, r, tid, "t1")
	has(t, tk.Error, "review did not converge", "after 2 rounds", "add a test")
	if tk.Reviews != 3 || len(r.codex.prompts(tk.Owner)) != 3 {
		t.Fatalf("task = %+v", tk)
	}
	waitFor(t, "the lead told", func() bool { return len(told(r)) == 1 })
	has(t, told(r)[0], "blocked: review did not converge")
}

func TestReviewNeedsAnotherKind(t *testing.T) {
	r, tid := reviewRig(t, testCfg(), [][]fake.Script{lead, {turn(say("fixed it"), finish())}}, nil)
	if _, err := r.m.Spawn(tid, "claude", SpawnSpec{Kind: "claude", Brief: "fix the parser", Review: "codex"}); err == nil {
		t.Fatal("a review by a kind that is not installed")
	}
	if _, err := r.m.Spawn(tid, "claude", SpawnSpec{Kind: "claude", Brief: "fix the parser", Review: "other"}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "t1 done", func() bool { return taskOf(t, r, tid, "t1").Status == store.TaskDone })
	has(t, taskOf(t, r, tid, "t1").Result, "fixed it", "not reviewed: no agent kind other than claude")
	if n := r.c.count(func(m tea.Msg) bool { c, ok := m.(TaskChangedMsg); return ok && c.Task.Status == store.TaskReview }); n != 0 {
		t.Fatalf("the task went to review %d times", n)
	}
}

func TestOnlyTheReviewerAnswers(t *testing.T) {
	waits := []fake.Script{turn(blockStep("ap-review"), callOp("review.approve", `{"id": "t1"}`), finish())}
	worker := []fake.Script{turn(
		callOp("task.done", `{"id": "t1", "result": "fixed"}`),
		callOp("review.approve", `{"id": "t1", "note": "mine is fine"}`),
		say("reported"), finish())}
	r, tid := reviewRig(t, testCfg(), [][]fake.Script{lead, waits}, [][]fake.Script{worker})
	spawnReviewed(t, r, tid)
	owner := taskOf(t, r, tid, "t1").Owner
	waitFor(t, "the worker's answer", func() bool { return len(r.c.toolResults(owner, "deck")) == 2 })
	has(t, r.c.toolResults(owner, "deck")[0], "t1 [review]", "reviewed by")
	has(t, r.c.toolResults(owner, "deck")[1], "you are not reviewing t1")
	tk := taskOf(t, r, tid, "t1")
	if tk.Status != store.TaskReview || tk.Reviewer == "" {
		t.Fatalf("task = %+v", tk)
	}
	approvalOf(t, r, tk.Reviewer).Respond(agent.Decision{Allow: true})
	waitFor(t, "t1 done", func() bool { return taskOf(t, r, tid, "t1").Status == store.TaskDone })
	lacks(t, taskOf(t, r, tid, "t1").Result, "mine is fine")
}

// The move to review and the scheduler can both try to start the reviewer.
// The one that comes second waits for the first, so that it too returns with
// the reviewer bound: task.done names the reviewer whichever one won.
func TestASecondReviewStartWaitsForTheFirst(t *testing.T) {
	var g *gatedAdapter
	gated := func(kind string, a agent.Adapter) agent.Adapter {
		if kind != "claude" {
			return a
		}
		g = newGate(a, func(sp agent.Spec) bool { return sp.ID != "claude" }) // the lead starts at once
		return g
	}
	waits := []fake.Script{turn(blockStep("ap-review"), callOp("review.approve", `{"id": "t1"}`), finish())}
	r, tid := reviewRigWith(t, testCfg(), [][]fake.Script{lead, waits}, [][]fake.Script{{turn(say("fixed"), finish())}}, gated)
	t.Cleanup(g.release) // runs before Close: a start still held must not stall it
	spawnReviewed(t, r, tid)
	<-g.entered // a first caller is starting the reviewer
	time.AfterFunc(50*time.Millisecond, g.release)
	r.m.startReview(r.m.thread(tid), "t1")
	if tk := taskOf(t, r, tid, "t1"); tk.Status != store.TaskReview || tk.Reviewer == "" {
		t.Fatalf("startReview returned before the reviewer was bound: %+v", tk)
	}
}

func TestReviewerThatStopsBlocksTheTask(t *testing.T) {
	silent := []fake.Script{turn(say("looks fine to me"), finish())}
	r, tid := reviewRig(t, testCfg(), [][]fake.Script{lead, silent}, [][]fake.Script{{turn(say("fixed"), finish())}})
	spawnReviewed(t, r, tid)
	waitFor(t, "t1 blocked", func() bool { return taskOf(t, r, tid, "t1").Status == store.TaskBlocked })
	has(t, taskOf(t, r, tid, "t1").Error, "stopped before it answered the review")
	waitFor(t, "the lead told", func() bool { return len(told(r)) == 1 })
	has(t, told(r)[0], "blocked")
}

func TestDependantsWaitForTheReview(t *testing.T) {
	cfg := testCfg()
	cfg.Review = "other" // the config's review, for tasks run by subagents
	waits := []fake.Script{turn(blockStep("ap-review"), callOp("review.approve", `{"id": "t1"}`), finish())}
	r, tid := reviewRig(t, cfg, [][]fake.Script{lead, waits, {turn(say("t2 done"), finish())}}, [][]fake.Script{{turn(say("t1 done"), finish())}})
	if _, err := r.m.CreateTask(tid, TaskSpec{Title: "first", Kind: "codex"}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.m.CreateTask(tid, TaskSpec{Title: "second", Deps: []string{"t1"}, Review: store.ReviewNone}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "t1 in review", func() bool { return taskOf(t, r, tid, "t1").Reviewer != "" })
	reviewer := taskOf(t, r, tid, "t1").Reviewer
	ap := approvalOf(t, r, reviewer)
	if tk := taskOf(t, r, tid, "t2"); tk.Status != store.TaskPending {
		t.Fatalf("t2 is %s while t1 is in review", tk.Status)
	}
	ap.Respond(agent.Decision{Allow: true})
	waitFor(t, "t2 done", func() bool { return taskOf(t, r, tid, "t2").Status == store.TaskDone })
	if tk := taskOf(t, r, tid, "t2"); tk.Reviews != 0 || tk.Result != "t2 done" {
		t.Fatalf("t2 = %+v", tk)
	}
}

func TestChangesForAStoppedWorkerGoToANewOneOnItsWorktree(t *testing.T) {
	waits := []fake.Script{turn(blockStep("ap-review"), callOp("review.changes", `{"id": "t1", "notes": "add a test"}`), finish())}
	r, tid := reviewRig(t, testCfg(), [][]fake.Script{lead, waits, approves},
		[][]fake.Script{{turn(say("v1"), finish())}, {turn(say("v2 with a test"), finish())}})
	if _, err := r.m.Spawn(tid, "claude", SpawnSpec{Kind: "codex", Brief: "fix the parser", Review: "other", Isolate: true}); err != nil {
		t.Fatal(err)
	}
	// Spawn binds the reviewer before it sends the brief: wait for both.
	waitFor(t, "t1 in review", func() bool {
		rev := taskOf(t, r, tid, "t1").Reviewer
		return rev != "" && len(r.claude.prompts(rev)) > 0
	})
	first := taskOf(t, r, tid, "t1")
	has(t, r.claude.prompts(first.Reviewer)[0], "git -C "+first.Run.Path+" diff")
	r.m.retireMember(r.m.thread(tid), first.Owner)
	waitFor(t, "the worker stopped", func() bool { a, _ := memberStatus(r, first.Owner); return a.Status == StatusExited })

	approvalOf(t, r, first.Reviewer).Respond(agent.Decision{Allow: true})
	waitFor(t, "t1 done", func() bool { return taskOf(t, r, tid, "t1").Status == store.TaskDone })
	tk := taskOf(t, r, tid, "t1")
	if tk.Owner == first.Owner || tk.Run.Branch != first.Run.Branch || tk.Reviews != 2 {
		t.Fatalf("task = %+v", tk)
	}
	has(t, tk.Result, "v2 with a test", "approved")
	has(t, r.codex.prompts(tk.Owner)[0], "fix the parser", "you carry on with its work", "Its result was: v1", "add a test")
}

// notices counts the notices that contain sub.
func notices(r *rig, sub string) int {
	return r.c.count(func(m tea.Msg) bool { n, ok := m.(NoticeMsg); return ok && strings.Contains(n.Text, sub) })
}

const slotWait = "t2 waits for a free agent slot for its review"

// slotRig is a thread with orchestration off whose two subagent slots the
// user's subagents fill: a claude that holds one, and a codex whose task t2
// waits in review for a reviewer. It returns the holder's id.
func slotRig(t *testing.T) (*rig, string, string) {
	t.Helper()
	cfg := testCfg()
	cfg.MaxSubagents = 2
	holds := []fake.Script{turn(blockStep("ap-hold"), say("held"), finish())}
	approvesT2 := []fake.Script{turn(callOp("review.approve", `{"id": "t2"}`), say("approved"), finish())}
	r, tid := reviewRig(t, cfg, [][]fake.Script{lead, holds, approvesT2}, [][]fake.Script{{turn(say("fixed the parser"), finish())}})
	if err := r.m.SetLevel(tid, "off"); err != nil {
		t.Fatal(err)
	}
	hold, err := r.m.Spawn(tid, "", SpawnSpec{Kind: "claude", Brief: "hold a slot"})
	if err != nil {
		t.Fatal(err)
	}
	approvalOf(t, r, hold.AgentID)
	if _, err := r.m.Spawn(tid, "", SpawnSpec{Kind: "codex", Brief: "fix the parser", Review: "other"}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the wait notice", func() bool { return notices(r, slotWait) == 1 })
	r.m.startReviews(r.m.thread(tid)) // a retry without room says nothing new
	if tk := taskOf(t, r, tid, "t2"); tk.Status != store.TaskReview || tk.Reviewer != "" {
		t.Fatalf("t2 = %+v", tk)
	}
	return r, tid, hold.AgentID
}

// oneReview checks that t2 is done after one reviewer and one wait notice.
func oneReview(t *testing.T, r *rig, tid string) {
	t.Helper()
	waitFor(t, "t2 done", func() bool { return taskOf(t, r, tid, "t2").Status == store.TaskDone })
	has(t, taskOf(t, r, tid, "t2").Result, "fixed the parser", "approved")
	r.claude.mu.Lock()
	n := len(r.claude.specs)
	r.claude.mu.Unlock()
	if n != 3 {
		t.Fatalf("claude started %d times, want the lead, the holder and one reviewer", n)
	}
	if n := notices(r, slotWait); n != 1 {
		t.Fatalf("%d wait notices", n)
	}
}

func TestReviewWaitsForASlotWithoutOrchestration(t *testing.T) {
	r, tid, hold := slotRig(t)
	r.m.retireMember(r.m.thread(tid), hold) // its exit frees the slot
	oneReview(t, r, tid)
}

func TestRetriesStartOneReviewer(t *testing.T) {
	r, tid, hold := slotRig(t)
	th := r.m.thread(tid)
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r.m.kick(th)
			r.m.startReviews(th)
		}()
	}
	r.m.retireMember(th, hold)
	wg.Wait()
	oneReview(t, r, tid)
}

func TestReviewWaitsForTheUserAfterMaxWork(t *testing.T) {
	cfg := testCfg()
	cfg.MaxWork = 1
	r, tid := reviewRig(t, cfg, [][]fake.Script{lead, approves}, [][]fake.Script{{turn(say("fixed the parser"), finish())}})
	spawnReviewed(t, r, tid) // the lead's spawn takes the one unit of work
	if err := r.m.SetLevel(tid, "off"); err != nil {
		t.Fatal(err)
	}
	const waits = "t1 waits for its review until the user continues the thread"
	waitFor(t, "the wait notice", func() bool { return notices(r, waits) == 1 })
	if th, _ := r.m.Thread(tid); !th.Paused {
		t.Fatal("the thread is not paused")
	}
	if err := r.m.Post(tid, "go on"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "t1 done", func() bool { return taskOf(t, r, tid, "t1").Status == store.TaskDone })
	has(t, taskOf(t, r, tid, "t1").Result, "approved: tests pass")
	if n := notices(r, waits); n != 1 {
		t.Fatalf("%d wait notices", n)
	}
}
