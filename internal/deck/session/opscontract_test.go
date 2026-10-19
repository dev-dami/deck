package session

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dev-dami/deck/internal/deck/agent"
	"github.com/dev-dami/deck/internal/deck/agent/fake"
	"github.com/dev-dami/deck/internal/deck/store"
)

// fakeBox is a sandbox that knows one command.
type fakeBox struct {
	mu   sync.Mutex
	cmds []string
}

func (b *fakeBox) Run(_ context.Context, script string) (string, string, int, bool, error) {
	b.mu.Lock()
	b.cmds = append(b.cmds, script)
	b.mu.Unlock()
	if script == "podman ps" {
		return "web running\n", "", 0, false, nil
	}
	return "", "not permitted by contract", 126, false, nil
}

// hangBox is a sandbox whose commands run until release closes.
type hangBox chan struct{}

func (b hangBox) Run(ctx context.Context, _ string) (string, string, int, bool, error) {
	select {
	case <-b:
	case <-ctx.Done():
	}
	return "", "", 1, false, nil
}

// checkName passes a result whose first row has a name.
func checkName(raw json.RawMessage) (json.RawMessage, error) {
	var v struct{ Rows []map[string]string }
	if json.Unmarshal(raw, &v) != nil || len(v.Rows) == 0 || v.Rows[0]["name"] == "" {
		return nil, errors.New("rows need a name")
	}
	return json.RawMessage(`{"rows":[{"name":"web"}]}`), nil
}

// A teammate's ops contract runs with an agent that has no tools of its
// own and only sh.run and contract.result: every other op, thread.send,
// thread.read and approvals are refused. A result that does not fit goes
// back with the reason; one that fits ends the task.
func TestRunOpsDeliversACheckedResult(t *testing.T) {
	exec := func(op, args string) fake.Step {
		return fake.Step{Exec: &fake.Exec{Op: op, Args: json.RawMessage(args)}}
	}
	r := newRigWith(t, t.TempDir(), false, testCfg(), []fake.Script{turn(
		exec("sh.run", `{"cmd": "podman ps"}`),
		exec("sh.run", `{"cmd": "rm -rf /"}`),
		exec("git.status", `{}`),
		exec("search", `{"query": ""}`),
		fake.Step{Message: &agent.Message{To: "all", Text: "hello"}},
		fake.Step{ReadThread: true},
		exec("contract.result", `{"result": {"rows": [{"state": "up"}]}}`),
		exec("contract.result", `{"result": {"rows": [{"name": "web"}]}}`),
		say("done"), finish())}, nil)
	ended := make(chan store.Task, 4)
	r.m.SetTaskEnd(func(_ string, tk store.Task) { ended <- tk })
	run := OpsRun{Peer: "vps", Title: "report podman", Brief: "report the containers", Scope: []string{"podman-read"}, Targets: []string{"web"}, Check: checkName}
	if _, _, err := r.m.RunOps(run); err == nil || !strings.Contains(err.Error(), "no sandbox") {
		t.Fatalf("without a sandbox: %v", err)
	}

	box := &fakeBox{}
	var got []string
	r.m.SetSandbox(func(scope, targets []string, dir string) (Sandbox, error) {
		got = append(append(got, scope...), targets...)
		return box, nil
	})
	var mu sync.Mutex
	var audit []string
	run.Audit = func(event, detail string) {
		mu.Lock()
		audit = append(audit, event+": "+detail)
		mu.Unlock()
	}
	tid, task, err := r.m.RunOps(run)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, ",") != "podman-read,web" {
		t.Fatalf("sandbox for %v", got)
	}
	if info, _ := r.m.Thread(tid); info.Title != "vps: report podman" || info.Mode != agent.ModeAsk {
		t.Fatalf("thread %+v", info)
	}
	if err := r.m.SetMode(tid, agent.ModeYolo); err == nil {
		t.Fatal("an ops contract's thread went yolo")
	}
	if err := r.m.StartContract(tid, task); err != nil {
		t.Fatal(err)
	}
	tk := <-ended
	if tk.ID != task || tk.Status != store.TaskDone {
		t.Fatalf("ended = %+v", tk)
	}
	if res, ok := r.m.OpsResult(tid); !ok || string(res) != `{"rows":[{"name":"web"}]}` {
		t.Fatalf("result = %s %v", res, ok)
	}
	owner := tk.Owner
	waitFor(t, "the last op's answer", func() bool { return len(r.c.toolResults(owner, "deck")) == 6 })
	out := r.c.toolResults(owner, "deck")
	has(t, out[0], "web running", "[exit 0]")
	has(t, out[1], "not permitted by contract", "[exit 126]")
	has(t, out[2], `Unknown op "git.status"`)
	has(t, out[3], "sh.run {cmd*}", "contract.result {result*:json}")
	if strings.Contains(out[3], "git.status") || strings.Contains(out[3], "Skills") {
		t.Fatalf("search shows more than the two ops:\n%s", out[3])
	}
	has(t, out[4], "does not fit the contract: rows need a name", "try 1 of 3")
	has(t, out[5], "delivered")
	has(t, strings.Join(r.c.toolResults(owner, "message"), ""), "only sh.run and contract.result")
	has(t, strings.Join(r.c.toolResults(owner, "read"), ""), "only sh.run and contract.result")
	if box.cmds[0] != "podman ps" || box.cmds[1] != "rm -rf /" || len(box.cmds) != 2 {
		t.Fatalf("commands %v", box.cmds)
	}
	mu.Lock()
	has(t, strings.Join(audit, "\n"), "sh.run: exit 0: podman ps", "sh.run: exit 126: rm -rf /", "result refused: rows need a name")
	mu.Unlock()

	var spec agent.Spec
	r.claude.mu.Lock()
	for _, s := range r.claude.specs {
		if s.ID == owner {
			spec = s
		}
	}
	r.claude.mu.Unlock()
	if !spec.NoTools || spec.Instructions != opsInstructions {
		t.Fatalf("spec %+v", spec)
	}

	// An approval the agent raises anyway is denied.
	a := r.m.get(owner)
	denied := make(chan agent.Decision, 1)
	r.m.onApproval(a, agent.ApprovalRequest{ID: "x", Kind: agent.ApproveCommand, Respond: func(d agent.Decision) { denied <- d }})
	if d := <-denied; d.Allow {
		t.Fatal("an ops agent's approval was allowed")
	}
}

// Three results that do not fit fail the contract, and so does its time.
func TestRunOpsFails(t *testing.T) {
	bad := fake.Step{Exec: &fake.Exec{Op: "contract.result", Args: json.RawMessage(`{"result": {"rows": []}}`)}}
	r := newRigWith(t, t.TempDir(), false, testCfg(), []fake.Script{turn(bad, bad, bad, finish())}, nil)
	r.m.SetSandbox(func([]string, []string, string) (Sandbox, error) { return &fakeBox{}, nil })
	ended := make(chan store.Task, 4)
	r.m.SetTaskEnd(func(_ string, tk store.Task) { ended <- tk })
	tid, task, err := r.m.RunOps(OpsRun{Peer: "vps", Title: "x", Brief: "x", Scope: []string{"podman-read"}, Check: checkName})
	if err != nil {
		t.Fatal(err)
	}
	if err := r.m.StartContract(tid, task); err != nil {
		t.Fatal(err)
	}
	tk := <-ended
	if tk.Status != store.TaskFailed || !strings.Contains(tk.Error, "did not fit the contract 3 times") {
		t.Fatalf("ended = %+v", tk)
	}
	if _, ok := r.m.OpsResult(tid); ok {
		t.Fatal("a failed contract has a result")
	}

	// A command that hangs: the contract's time runs out.
	hang := fake.Step{Exec: &fake.Exec{Op: "sh.run", Args: json.RawMessage(`{"cmd": "podman logs -f web"}`)}}
	r2 := newRigWith(t, t.TempDir(), false, testCfg(), []fake.Script{turn(hang, finish())}, nil)
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	r2.m.SetSandbox(func([]string, []string, string) (Sandbox, error) { return hangBox(release), nil })
	r2.m.SetTaskEnd(func(_ string, tk store.Task) { ended <- tk })
	tid, task, err = r2.m.RunOps(OpsRun{Peer: "vps", Title: "y", Brief: "y", Scope: []string{"podman-read"}, Check: checkName, TTL: 300 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	if err := r2.m.StartContract(tid, task); err != nil {
		t.Fatal(err)
	}
	tk = <-ended
	if tk.ID != task || tk.Status != store.TaskFailed || !strings.Contains(tk.Error, "ran out of its time") {
		t.Fatalf("ended = %+v", tk)
	}

	m := NewManager(map[string]agent.Adapter{"agy": plainAdapter{fake.NewTurns(0, nil)}}, r.st, testCfg(), Options{})
	t.Cleanup(m.Close)
	m.SetSandbox(func([]string, []string, string) (Sandbox, error) { return &fakeBox{}, nil })
	if _, _, err := m.RunOps(OpsRun{Peer: "vps", Title: "z", Scope: []string{"podman-read"}, Check: checkName}); err == nil || !strings.Contains(err.Error(), "no agent on this Deck can run an ops contract") {
		t.Fatalf("an adapter that cannot turn its tools off: %v", err)
	}
}

// An agent that ends its turn without a result fails its contract at
// once, with or without a reply: no person waits to unblock it.
func TestRunOpsFailsATurnWithoutAResult(t *testing.T) {
	for _, script := range []fake.Script{turn(finish()), turn(say("all good"), finish())} {
		r := newRigWith(t, t.TempDir(), false, testCfg(), []fake.Script{script}, nil)
		r.m.SetSandbox(func([]string, []string, string) (Sandbox, error) { return &fakeBox{}, nil })
		ended := make(chan store.Task, 4)
		r.m.SetTaskEnd(func(_ string, tk store.Task) { ended <- tk })
		tid, task, err := r.m.RunOps(OpsRun{Peer: "vps", Title: "x", Brief: "x", Scope: []string{"podman-read"}, Check: checkName})
		if err != nil {
			t.Fatal(err)
		}
		if err := r.m.StartContract(tid, task); err != nil {
			t.Fatal(err)
		}
		select {
		case tk := <-ended:
			if tk.ID != task || tk.Status != store.TaskFailed || tk.Error != "its agent ended its turn without a result" {
				t.Fatalf("ended = %+v", tk)
			}
		case <-time.After(5 * time.Second):
			tk, _ := r.m.Task(tid, task)
			t.Fatalf("the task did not end: %+v", tk)
		}
	}
}

// An ops thread stays locked down after a restart: its running task fails,
// and its agent gets no tools back.
func TestOpsThreadStaysLockedAfterARestart(t *testing.T) {
	dir := t.TempDir()
	hang := fake.Step{Exec: &fake.Exec{Op: "sh.run", Args: json.RawMessage(`{"cmd": "podman logs -f web"}`)}}
	r := newRigWith(t, dir, false, testCfg(), []fake.Script{turn(hang, finish())}, nil)
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	r.m.SetSandbox(func([]string, []string, string) (Sandbox, error) { return hangBox(release), nil })
	tid, task, err := r.m.RunOps(OpsRun{Peer: "vps", Title: "x", Brief: "x", Scope: []string{"podman-read"}, Check: checkName})
	if err != nil {
		t.Fatal(err)
	}
	if err := r.m.StartContract(tid, task); err != nil {
		t.Fatal(err)
	}
	tk, _ := r.m.Task(tid, task)
	waitFor(t, "the command", func() bool {
		return len(r.c.events(func(e EventMsg) bool { return e.AgentID == tk.Owner && e.Kind == agent.KindToolCall })) > 0
	})
	r.m.Close()

	r2 := newRigWith(t, dir, false, testCfg(), nil, nil)
	ended := make(chan store.Task, 4)
	r2.m.SetTaskEnd(func(_ string, tk store.Task) { ended <- tk })
	info, _, err := r2.m.Resume(tid)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case tk := <-ended:
		if tk.ID != task || tk.Status != store.TaskFailed || tk.Error != OpsRestarted {
			t.Fatalf("ended = %+v", tk)
		}
	case <-time.After(5 * time.Second):
		got, _ := r2.m.Task(tid, task)
		t.Fatalf("the task did not end: %+v", got)
	}
	owner := info.Members[0].ID
	if owner != tk.Owner {
		t.Fatalf("resumed as %s, not %s", owner, tk.Owner)
	}
	if spec, ok := r2.claude.spec(owner); !ok || !spec.NoTools || spec.Instructions != opsInstructions {
		t.Fatalf("resumed spec %+v", spec)
	}
	if err := r2.m.SetMode(tid, agent.ModeYolo); err == nil {
		t.Fatal("a resumed ops thread went yolo")
	}
	has(t, execErr(t, r2, owner, "sh.run", `{"cmd": "podman ps"}`), "this contract is over")
	has(t, execErr(t, r2, owner, "contract.result", `{"result": {"rows": [{"name": "web"}]}}`), "this contract is over")
	has(t, execErr(t, r2, owner, "git.status", `{}`), `Unknown op "git.status"`)
	has(t, r2.m.readThread(owner), "only sh.run and contract.result")
	sent := make(chan error, 1)
	r2.m.handleMessage(agent.Message{From: owner, To: "all", Text: "hello", Respond: func(_ string, err error) { sent <- err }})
	if err := <-sent; err == nil || !strings.Contains(err.Error(), "only sh.run and contract.result") {
		t.Fatalf("thread.send: %v", err)
	}
	denied := make(chan agent.Decision, 1)
	r2.m.onApproval(r2.m.get(owner), agent.ApprovalRequest{ID: "x", Kind: agent.ApproveCommand, Respond: func(d agent.Decision) { denied <- d }})
	if d := <-denied; d.Allow {
		t.Fatal("a resumed ops agent's approval was allowed")
	}
}
