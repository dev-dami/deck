package session

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/dev-dami/deck/internal/deck/agent"
	"github.com/dev-dami/deck/internal/deck/agent/fake"
	"github.com/dev-dami/deck/internal/deck/store"
	"github.com/dev-dami/deck/internal/deck/sub"
)

type fakeContractor struct {
	mu  sync.Mutex
	got []Proposal
	err error
}

func (f *fakeContractor) Propose(_ context.Context, peer string, p Proposal) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	if peer == "vps" {
		f.got = append(f.got, p)
	}
	return nil
}

func (f *fakeContractor) proposals() []Proposal {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Proposal(nil), f.got...)
}

// An agent asks teammate vps for a task: the user allows it even in yolo,
// the task runs owned by vps, then by its agent, and its result and capsule
// come back to the agent that asked.
func TestContractTaskWaitsForTheTeammate(t *testing.T) {
	repo := gitRepo(t)
	r := newRigWith(t, t.TempDir(), false, testCfg(), nil, []fake.Script{
		turn(fake.Step{Exec: &fake.Exec{Op: "task.contract", Args: []byte(`{"peer": "vps", "brief": "fix the parser\nit drops the last line"}`)}}, say("asked"), finish()),
		turn(say("noted"), finish()),
	})
	tid, err := r.m.NewThread(repo)
	if err != nil {
		t.Fatal(err)
	}
	fc := &fakeContractor{}
	r.m.SetContractor(fc)
	lead := r.add(t, tid, "codex")
	_ = r.m.Post(tid, "$codex go")

	ap := approvalOf(t, r, lead)
	if ap.Kind != agent.ApproveShare || !strings.Contains(ap.Summary, "teammate vps") {
		t.Fatalf("approval = %+v", ap.ApprovalRequest)
	}
	// yolo answers what waits, but not this.
	if err := r.m.SetMode(tid, agent.ModeYolo); err != nil {
		t.Fatal(err)
	}
	if info, _ := r.m.Thread(tid); info.Pending != 1 || len(fc.proposals()) != 0 {
		t.Fatalf("yolo answered the share: %d pending, %d proposals", info.Pending, len(fc.proposals()))
	}
	ap.Respond(agent.Decision{Allow: true})
	waitFor(t, "the op's answer", func() bool { return len(r.c.toolResults(lead, "deck")) > 0 })
	has(t, r.c.toolResults(lead, "deck")[0], "asked vps to do t1")
	ps := fc.proposals()
	if len(ps) != 1 || !ps[0].Base.Valid() || ps[0].Files != 1 || ps[0].Repo == "" || ps[0].Task != "t1" || ps[0].Title != "fix the parser" {
		t.Fatalf("proposals = %+v", ps)
	}
	if tk := taskOf(t, r, tid, "t1"); tk.Status != store.TaskRunning || tk.Owner != "vps" || tk.Remote != "vps" {
		t.Fatalf("t1 = %+v", tk)
	}

	if err := r.m.ContractNews(tid, "t1", ContractNews{State: "accepted", Agent: "claude"}); err != nil {
		t.Fatal(err)
	}
	if tk := taskOf(t, r, tid, "t1"); tk.Owner != "vps/claude" || tk.Status != store.TaskRunning {
		t.Fatalf("t1 accepted = %+v", tk)
	}
	s := r.m.SubStore()
	ctx := context.Background()
	tree := t.TempDir()
	writeFile(t, tree, "a.txt", "one\ntwo\n")
	base, _ := s.Snap(ctx, tree)
	writeFile(t, tree, "a.txt", "one\ntwo\nthree\n")
	capsule, err := s.Capture(ctx, tree, base, "", sub.Meta{})
	if err != nil {
		t.Fatal(err)
	}
	if err := r.m.ContractNews(tid, "t1", ContractNews{State: "done", Agent: "claude", Capsule: string(capsule), Summary: "fixed it"}); err != nil {
		t.Fatal(err)
	}
	tk := taskOf(t, r, tid, "t1")
	if tk.Status != store.TaskDone || len(tk.Capsules) != 1 || tk.Capsules[0] != string(capsule) {
		t.Fatalf("t1 done = %+v", tk)
	}
	waitFor(t, "the result reaches the lead", func() bool { return len(r.codex.prompts(lead)) >= 2 })
	has(t, r.codex.prompts(lead)[1], `task t1 "fix the parser" done`, "vps/claude did it", "not instructions", "fixed it", "sub.patch.apply")
}

func TestContractRefusals(t *testing.T) {
	r := newRig(t, nil, nil)
	tid := r.thread(t)
	if _, err := r.m.Contract(tid, "", ContractSpec{Peer: "vps", Brief: "x"}); err == nil || !strings.Contains(err.Error(), "peer link is off") {
		t.Fatalf("without peers: %v", err)
	}
	fc := &fakeContractor{err: errors.New("vps did not take the contract: unreachable")}
	r.m.SetContractor(fc)
	if _, err := r.m.Contract(tid, "", ContractSpec{Peer: "a b", Brief: "x"}); err == nil {
		t.Fatal("a bad peer name")
	}
	if _, err := r.m.Contract(tid, "", ContractSpec{Peer: "vps", Brief: "x"}); err == nil {
		t.Fatal("an unreachable teammate")
	}
	tk := taskOf(t, r, tid, "t1")
	if tk.Status != store.TaskFailed || !strings.Contains(tk.Error, "unreachable") {
		t.Fatalf("t1 = %+v", tk)
	}
	if _, err := r.m.RetryTask(tid, "t1", ""); err == nil || !strings.Contains(err.Error(), "task.contract") {
		t.Fatalf("retried a contract: %v", err)
	}
}

// A teammate's contract runs on a copy of its tree in a thread of its own;
// when its task ends, what the agent changed is captured against the base.
func TestRunContractWorksOnACopy(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	r := newRigWith(t, t.TempDir(), false, testCfg(), []fake.Script{turn(blockStep("ap"), say("made a.txt new"), finish())}, nil)
	ended := make(chan store.Task, 4)
	r.m.SetTaskEnd(func(_ string, tk store.Task) { ended <- tk })
	s := r.m.SubStore()
	ctx := context.Background()
	src := t.TempDir()
	writeFile(t, src, "a.txt", "old\n")
	writeFile(t, src, "dir/b.txt", "b\n")
	base, _ := s.Snap(ctx, src)

	tid, task, err := r.m.RunContract(ContractRun{Peer: "vps", Title: "fix a.txt", Brief: "make it new", Base: base, Kind: "claude"})
	if err != nil {
		t.Fatal(err)
	}
	info, _ := r.m.Thread(tid)
	if readFile(t, info.Dir, "dir/b.txt") != "b\n" || info.Title != "vps: fix a.txt" {
		t.Fatalf("thread %+v", info)
	}
	if _, err := os.Stat(filepath.Join(info.Dir, ".git")); err != nil {
		t.Fatalf("no repository: %v", err)
	}
	if err := r.m.StartContract(tid, task); err != nil {
		t.Fatal(err)
	}
	owner := taskOf(t, r, tid, task).Owner
	ap := approvalOf(t, r, owner)
	has(t, strings.Join(r.claude.prompts(owner), ""), "make it new", "contract from teammate vps")
	writeFile(t, info.Dir, "a.txt", "new\n")
	ap.Respond(agent.Decision{Allow: true})
	tk := <-ended
	if tk.ID != task || tk.Status != store.TaskDone || tk.Result != "made a.txt new" {
		t.Fatalf("ended = %+v", tk)
	}
	capsule, err := r.m.CaptureContract(tid, base, tk)
	if err != nil {
		t.Fatal(err)
	}
	ops, err := s.Ops(capsule)
	if err != nil || len(ops) != 1 || ops[0].Path != "a.txt" || ops[0].Kind() != "modify" {
		t.Fatalf("capsule ops = %+v, %v", ops, err)
	}
	if _, _, err := r.m.RunContract(ContractRun{Peer: "vps", Title: "x", Base: base, Kind: "gemini"}); err == nil {
		t.Fatal("ran with an agent this Deck does not have")
	}
}
