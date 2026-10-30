package session

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/dev-dami/deck/internal/deck/agent"
	"github.com/dev-dami/deck/internal/deck/agent/fake"
	"github.com/dev-dami/deck/internal/deck/store"
)

// editOf is a change to p that replaces line old with line new.
func editOf(p, old, new string) fake.Step {
	return fake.Step{Event: agent.Event{Kind: agent.KindFileChange, Diff: &agent.FileDiff{Path: p, Added: 1, Removed: 1, Hunks: []agent.Hunk{{
		OldStart: 1, OldLines: 1, NewStart: 1, NewLines: 1,
		Lines: []agent.DiffLine{{Op: agent.OpDel, Text: old}, {Op: agent.OpAdd, Text: new}},
	}}}}}
}

// hold keeps a member in its turn: a command waiting for the person.
func hold(cmd string) fake.Step {
	return fake.Step{Approval: &agent.ApprovalRequest{Kind: agent.ApproveCommand, ToolName: "Bash", Summary: cmd}}
}

// gateCall runs b's gate for p on its own goroutine.
func gateCall(r *rig, b, p string, max time.Duration) <-chan gateResult {
	ch := make(chan gateResult, 1)
	go func() { ch <- r.m.gate(r.m.get(b), p, max) }()
	return ch
}

func (c *collector) asks(f func(FileAsk) bool) []FileAsk {
	var out []FileAsk
	for _, m := range c.all() {
		if a, ok := m.(FileAskMsg); ok && f(a.Ask) {
			out = append(out, a.Ask)
		}
	}
	return out
}

func waitAsk(t *testing.T, r *rig, what string, f func(FileAsk) bool) FileAsk {
	t.Helper()
	waitFor(t, what, func() bool { return len(r.c.asks(f)) > 0 })
	return r.c.asks(f)[0]
}

func got(t *testing.T, ch <-chan gateResult) gateResult {
	t.Helper()
	select {
	case res := <-ch:
		return res
	case <-time.After(5 * time.Second):
		t.Fatal("the gate did not open")
		return gateResult{}
	}
}

func still(t *testing.T, ch <-chan gateResult) {
	t.Helper()
	select {
	case res := <-ch:
		t.Fatalf("the gate opened early: %+v", res)
	case <-time.After(50 * time.Millisecond):
	}
}

// busyOwner starts a thread where claude changed a.go and is still at it
// (waiting on a command), with codex beside it.
func busyOwner(t *testing.T, codex ...fake.Script) (*rig, string) {
	r := newRig(t, []fake.Script{turn(editOf("a.go", "one", "one by claude"), hold("make"), say("done"), finish())}, codex)
	tid := r.thread(t)
	r.add(t, tid, "claude")
	r.add(t, tid, "codex")
	_ = r.m.postAt(tid, "$claude go")
	waitFor(t, "claude at work", func() bool { return len(r.c.approvals()) == 1 })
	return r, tid
}

func TestGateHoldsTheEditUntilTheOwnerAnswers(t *testing.T) {
	r, tid := busyOwner(t)
	ch := gateCall(r, "codex", "a.go", time.Minute)
	ask := waitAsk(t, r, "the ask", func(a FileAsk) bool { return a.State == AskPending })
	if ask.From != "codex" || ask.To != "claude" || ask.Path != "a.go" {
		t.Fatalf("ask = %+v", ask)
	}
	if got := AskLine(ask); got != "@codex asked @claude about `a.go`" {
		t.Fatalf("line = %q", got)
	}
	// claude is told, in its own conversation; nobody else is shown the relay.
	th := r.m.thread(tid)
	th.mu.Lock()
	var e store.Entry
	for _, x := range th.log {
		if x.Type == store.EntryRelay && x.Via == viaAsk {
			e = x
		}
	}
	th.mu.Unlock()
	if e.Speaker != "codex" || len(e.To) != 1 || e.To[0] != "claude" {
		t.Fatalf("ask relay = %+v", e)
	}
	has(t, e.Text, "I'm about to edit a.go, which you changed in this turn", "file.answer")
	still(t, ch)

	// claude answers (file.answer); codex gets claude's change and answer,
	// and the file is codex's.
	out, err := taskBoard{m: r.m, a: r.m.get("claude")}.TaskOp("file.answer", map[string]any{"path": "a.go", "text": "go ahead, I'm done"})
	if err != nil {
		t.Fatal(err)
	}
	has(t, out, "answered codex about a.go")
	res := got(t, ch)
	has(t, res.deny, "Deck held this edit of a.go: claude was changing it.", "-one\n+one by claude", `claude answered: "go ahead, I'm done"`, "a.go is yours now")
	done := waitAsk(t, r, "the answer", func(a FileAsk) bool { return a.State == AskGranted })
	if got := AskLine(done); got != "@codex asked @claude about `a.go` → claude: go ahead, I'm done" {
		t.Fatalf("line = %q", got)
	}
	// The ask no longer starts claude's turn; codex's edit now goes through,
	// and so do the next ones.
	if r.m.gate(r.m.get("codex"), "a.go", time.Second).deny != "" {
		t.Fatal("codex was stopped again")
	}
	th.mu.Lock()
	trigger := th.hasTrigger(r.m.get("claude"))
	th.mu.Unlock()
	if trigger {
		t.Fatal("the answered ask would start claude's turn")
	}
	// Nobody waits: a second answer finds no one.
	if _, err := r.m.answerAsks(r.m.get("claude"), "", "again", false); err == nil {
		t.Fatal("answered nobody")
	}
}

func TestGateAnsweredByAMessage(t *testing.T) {
	r, _ := busyOwner(t)
	ch := gateCall(r, "codex", "a.go", time.Minute)
	waitAsk(t, r, "the ask", func(a FileAsk) bool { return a.State == AskPending })
	r.m.handleMessage(agent.Message{From: "claude", To: "codex", Text: "wait for my tests, then take it"})
	res := got(t, ch)
	has(t, res.deny, `claude answered: "wait for my tests, then take it"`)
}

func TestGateOwnerAsksToWait(t *testing.T) {
	r, _ := busyOwner(t)
	ch := gateCall(r, "codex", "a.go", time.Minute)
	waitAsk(t, r, "the ask", func(a FileAsk) bool { return a.State == AskPending })
	if _, err := r.m.answerAsks(r.m.get("claude"), "a.go", "still on it", true); err != nil {
		t.Fatal(err)
	}
	res := got(t, ch)
	has(t, res.deny, `claude asks you to wait: "still on it"`, "file.claim")
	held := waitAsk(t, r, "the answer", func(a FileAsk) bool { return a.State == AskHeld })
	if got := AskLine(held); got != "@codex asked @claude about `a.go` → claude (wait): still on it" {
		t.Fatalf("line = %q", got)
	}
	// The file stays claude's: the next edit asks again.
	ch = gateCall(r, "codex", "a.go", time.Minute)
	waitFor(t, "the second ask", func() bool { return len(r.c.asks(func(a FileAsk) bool { return a.State == AskPending })) == 2 })
	still(t, ch)
}

func TestGateTimesOut(t *testing.T) {
	cfg := testCfg()
	cfg.EditGate.TimeoutSec = 1
	r := newRigWith(t, t.TempDir(), false, cfg, []fake.Script{turn(editOf("a.go", "one", "two"), hold("make"), finish())}, nil)
	tid := r.thread(t)
	r.add(t, tid, "claude")
	r.add(t, tid, "codex")
	_ = r.m.postAt(tid, "$claude go")
	waitFor(t, "claude at work", func() bool { return len(r.c.approvals()) == 1 })
	start := time.Now()
	res := got(t, gateCall(r, "codex", "a.go", time.Minute))
	if d := time.Since(start); d < 900*time.Millisecond {
		t.Fatalf("released after %s", d)
	}
	has(t, res.deny, "(no answer in 1s)", "-one\n+two", "a.go is yours now")
	gone := waitAsk(t, r, "the release", func(a FileAsk) bool { return a.State == AskReleased })
	if got := AskLine(gone); got != "@codex asked @claude about `a.go` → deck: no answer in 1s" {
		t.Fatalf("line = %q", got)
	}
}

func TestGateReleasedWhenTheOwnersTurnEnds(t *testing.T) {
	r, _ := busyOwner(t)
	ch := gateCall(r, "codex", "a.go", time.Minute)
	waitAsk(t, r, "the ask", func(a FileAsk) bool { return a.State == AskPending })
	// claude's command runs and its turn ends: it never saw the ask.
	r.c.approvals()[0].Respond(agent.Decision{Allow: true})
	res := got(t, ch)
	has(t, res.deny, "(claude finished its turn)", "+one by claude")
	waitFor(t, "claude's turn", func() bool { return r.c.turnDones("claude") == 1 })
	if n := r.c.turnDones("claude"); n != 1 {
		t.Fatalf("claude was woken for a settled ask: %d turns", n)
	}
}

func TestGateWakesAnIdleLeaseHolder(t *testing.T) {
	r := newRig(t, []fake.Script{
		turn(editOf("a.go", "one", "two"), say("edited"), finish()),
		turn(say("take it, I'm done with a.go"), finish()),
	}, nil)
	tid := r.thread(t)
	r.add(t, tid, "claude")
	r.add(t, tid, "codex")
	_ = r.m.postAt(tid, "$claude go")
	waitFor(t, "claude's turn", func() bool { return r.c.turnDones("claude") == 1 })
	if _, err := r.m.acquireLease(r.m.get("claude"), "a.go", 0, false, "more to come"); err != nil {
		t.Fatal(err)
	}
	// claude is idle but holds the file: the ask wakes it, and its reply is
	// the answer.
	res := got(t, gateCall(r, "codex", "a.go", time.Minute))
	has(t, res.deny, `claude answered: "take it, I'm done with a.go"`)
	// The answer is claude's reply, which can land just before its turn is
	// counted as done.
	waitFor(t, "claude's second turn", func() bool { return r.c.turnDones("claude") == 2 })
	has(t, r.claude.prompts("claude")[1], "[codex → claude] I'm about to edit a.go, which you hold a lease on")
}

func TestGateHandsOverFromAFinishedOwner(t *testing.T) {
	r := newRig(t, []fake.Script{turn(editOf("a.go", "one", "two"), say("edited"), finish())}, nil)
	tid := r.thread(t)
	r.add(t, tid, "claude")
	r.add(t, tid, "codex")
	_ = r.m.postAt(tid, "$claude go")
	waitFor(t, "claude's turn", func() bool { return r.c.turnDones("claude") == 1 })
	// claude is done: codex is not held, but is shown claude's change once.
	start := time.Now()
	res := r.m.gate(r.m.get("codex"), "a.go", time.Minute)
	if time.Since(start) > time.Second {
		t.Fatal("waited for a finished owner")
	}
	has(t, res.deny, "-one\n+two", "(claude had finished with it)")
	if r.m.gate(r.m.get("codex"), "a.go", time.Minute).deny != "" {
		t.Fatal("shown twice")
	}
	// claude's own file, other files and an isolated or exited owner are free.
	if r.m.gate(r.m.get("claude"), "b.go", time.Minute).deny != "" {
		t.Fatal("a file nobody touched")
	}
}

func TestGateTwoMembersAskingEachOtherDoNotDeadlock(t *testing.T) {
	r := newRig(t,
		[]fake.Script{turn(editOf("a.go", "one", "a by claude"), hold("make a"), finish())},
		[]fake.Script{turn(editOf("b.go", "one", "b by codex"), hold("make b"), finish())})
	tid := r.thread(t)
	r.add(t, tid, "claude")
	r.add(t, tid, "codex")
	_ = r.m.postAt(tid, "$claude $codex go")
	waitFor(t, "both at work", func() bool { return len(r.c.approvals()) == 2 })

	// claude waits on codex for b.go, then codex on claude for a.go: claude's
	// wait is let go (it could not answer while it waits), codex's stays.
	first := gateCall(r, "claude", "b.go", time.Minute)
	waitAsk(t, r, "claude's ask", func(a FileAsk) bool { return a.From == "claude" && a.State == AskPending })
	second := gateCall(r, "codex", "a.go", time.Minute)
	res := got(t, first)
	has(t, res.deny, "(codex is waiting on claude too)", "+b by codex")
	still(t, second)
	if _, err := r.m.answerAsks(r.m.get("claude"), "a.go", "go ahead", false); err != nil {
		t.Fatal(err)
	}
	has(t, got(t, second).deny, `claude answered: "go ahead"`, "+a by claude")
}

func TestGateThroughAnEditApproval(t *testing.T) {
	edit := fake.Step{
		Event:    agent.Event{Kind: agent.KindToolCall, Tool: &agent.Tool{ID: "e1", Name: "Edit", Title: "a.go", Status: agent.ToolRunning}},
		Approval: &agent.ApprovalRequest{Kind: agent.ApproveEdit, ToolName: "Edit", Summary: "a.go"},
	}
	r, tid := busyOwner(t, turn(edit, finish()), turn(edit, finish()))
	_ = r.m.postAt(tid, "$codex edit a.go")
	waitAsk(t, r, "the ask", func(a FileAsk) bool { return a.State == AskPending })
	if n := len(r.c.approvals()); n != 1 {
		t.Fatalf("codex's edit reached the person before claude answered: %d approvals", n)
	}
	if _, err := r.m.answerAsks(r.m.get("claude"), "a.go", "go ahead", false); err != nil {
		t.Fatal(err)
	}
	// Refused once, with claude's change; the retry reaches the person.
	waitFor(t, "codex refused", func() bool { return r.c.turnDones("codex") == 1 })
	_ = r.m.postAt(tid, "$codex again")
	waitFor(t, "the retry", func() bool { return len(r.c.approvals()) == 2 })
	if a := r.c.approvals()[1]; a.AgentID != "codex" || a.Kind != agent.ApproveEdit {
		t.Fatalf("approval = %+v", a.ApprovalRequest)
	}
}

func TestGateOff(t *testing.T) {
	r, tid := busyOwner(t)
	if err := r.m.SetEditGate(tid, false); err != nil {
		t.Fatal(err)
	}
	if res := r.m.gate(r.m.get("codex"), "a.go", time.Minute); res.deny != "" || res.waiting {
		t.Fatalf("gated while off: %+v", res)
	}
	out, err := r.m.claimFile(r.m.get("codex"), "a.go")
	if err != nil || !strings.Contains(out, "off") {
		t.Fatalf("claim = %q, %v", out, err)
	}
}

func TestFileClaimWaitsAndComesBack(t *testing.T) {
	r, _ := busyOwner(t)
	codex := r.m.get("codex")
	// A short wait comes back still waiting; the ask stays, and the next
	// claim joins it.
	res := r.m.gate(codex, "a.go", 20*time.Millisecond)
	if !res.waiting || res.owner != "claude" {
		t.Fatalf("res = %+v", res)
	}
	done := make(chan string, 1)
	go func() {
		out, _ := r.m.claimFile(codex, "a.go, b.go")
		done <- out
	}()
	waitFor(t, "the claim to wait", func() bool { return len(r.c.asks(func(a FileAsk) bool { return true })) == 1 })
	time.Sleep(20 * time.Millisecond)
	if _, err := r.m.answerAsks(r.m.get("claude"), "", "all yours", false); err != nil {
		t.Fatal(err)
	}
	var out string
	select {
	case out = <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("claim never returned")
	}
	has(t, out, "Before you edit a.go: claude was changing it.", `claude answered: "all yours"`, "b.go: free, go ahead")
	if n := len(r.c.asks(func(a FileAsk) bool { return a.State == AskPending })); n != 1 {
		t.Fatalf("%d asks made", n)
	}
}

func TestAskMessagesReachTheUI(t *testing.T) {
	r, _ := busyOwner(t)
	ch := gateCall(r, "codex", "a.go", time.Minute)
	waitAsk(t, r, "the ask", func(a FileAsk) bool { return a.State == AskPending })
	// The relay that tells claude is marked, so the conversation can leave it out.
	if n := r.c.count(func(m tea.Msg) bool { rm, ok := m.(RelayMsg); return ok && rm.Via == viaAsk }); n != 1 {
		t.Fatalf("ask relays = %d", n)
	}
	r.m.answerAsks(r.m.get("claude"), "a.go", "ok", false)
	got(t, ch)
	if asks := r.m.Asks(r.m.get("claude").thread.id); len(asks) != 1 || asks[0].State != AskGranted {
		t.Fatalf("Asks = %+v", asks)
	}
}
