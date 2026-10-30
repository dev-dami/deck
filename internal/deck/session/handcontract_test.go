package session

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/dev-dami/deck/internal/deck/agent"
	"github.com/dev-dami/deck/internal/deck/agent/fake"
	"github.com/dev-dami/deck/internal/deck/config"
	"github.com/dev-dami/deck/internal/deck/store"
)

// worksFor makes member worker report to member boss, as a subagent does, so
// plain thread.send messages between the two start turns (handcontract.go).
// The tests of relay mechanics (hops, pauses, queues, repeats) use it: a
// plain message between peers is information and wakes nobody.
func worksFor(t *testing.T, m *Manager, worker, boss string) {
	t.Helper()
	a := m.get(worker)
	if a == nil || m.get(boss) == nil {
		t.Fatalf("no member %q or %q", worker, boss)
	}
	a.mu.Lock()
	a.parent = boss
	a.mu.Unlock()
}

// contractTo is a thread.send that hands work over under a structured
// contract.
func contractTo(to, objective, deliverable string) fake.Step {
	return fake.Step{Message: &agent.Message{To: to, Objective: objective, Deliverable: deliverable, DoneWhen: "it is reviewed"}}
}

// A plain message to a peer is information: logged for the peer's next
// briefing, it starts no turn and spends no hop.
func TestPlainMessageToAPeerIsInformation(t *testing.T) {
	r := newRig(t, []fake.Script{
		turn(sendTo("codex", "FYI the cache moved to internal/cache"), say("told codex"), finish()),
	}, []fake.Script{
		turn(say("looked at the parser"), finish()),
	})
	tid := r.thread(t)
	r.add(t, tid, "claude")
	r.add(t, tid, "codex")
	if err := r.m.postAt(tid, "$claude move the cache"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "claude", func() bool { return r.c.turnDones("claude") == 1 })
	time.Sleep(50 * time.Millisecond) // time for codex to wake, were it going to
	if n := len(r.codex.prompts("codex")); n != 0 {
		t.Fatalf("codex was woken by information: %d prompts", n)
	}
	res := r.c.toolResults("claude", "message")
	if len(res) != 1 || !strings.HasPrefix(res[0], "noted for codex: a message without a contract starts no turn") {
		t.Fatalf("thread.send result = %q", res)
	}
	if ti, _ := r.m.Thread(tid); ti.Hops != 0 {
		t.Fatalf("hops = %d", ti.Hops)
	}
	// codex reads it when the user next talks to it.
	if err := r.m.postAt(tid, "$codex parser?"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "codex", func() bool { return r.c.turnDones("codex") == 1 })
	has(t, r.codex.prompts("codex")[0], "[claude → codex] FYI the cache moved to internal/cache")
}

// A structured hand-off wakes the recipient with its terms; its reply is the
// result, and an answer back from the recipient wakes the sender.
func TestStructuredHandoffWakesTheRecipientAndItsAnswerComesBack(t *testing.T) {
	msg := &agent.Message{To: "codex", Text: "the parser change is in parser.go", Objective: "Review the parser change",
		Deliverable: "a list of bugs", DoneWhen: "every branch is read", Boundaries: "do not edit files", Check: "go test ./parser"}
	r := newRig(t, []fake.Script{
		turn(fake.Step{Message: msg}, say("handed it over"), finish()),
		turn(say("thanks"), finish()),
	}, []fake.Script{
		turn(sendTo("claude", "one question answered: the lexer is fine"), say("no bugs"), finish()),
	})
	tid := r.thread(t)
	r.add(t, tid, "claude")
	r.add(t, tid, "codex")
	if err := r.m.postAt(tid, "$claude get the parser reviewed"); err != nil {
		t.Fatal(err)
	}
	// codex never runs the check: its replies do not finish the task, which
	// is blocked for the user after the review rounds.
	waitFor(t, "t1 blocked", func() bool { return statusOf(r, tid, "t1") == store.TaskBlocked })
	waitFor(t, "claude's second turn", func() bool { return r.c.turnDones("claude") >= 2 })
	tk := taskOf(t, r, tid, "t1")
	if tk.Title != "Review the parser change" || tk.Owner != "codex" || tk.Creator != "claude" {
		t.Fatalf("task = %+v", tk)
	}
	c := tk.Contract
	if c == nil || c.Objective != "Review the parser change" || c.Expect != "a list of bugs" || c.Boundaries != "do not edit files" || c.Check != "go test ./parser" {
		t.Fatalf("contract = %+v", c)
	}
	has(t, r.codex.prompts("codex")[0], "[claude → codex] the parser change is in parser.go", "Expected back: a list of bugs",
		"Done when: every branch is read", "Boundaries: do not edit files", "Check: run `go test ./parser`")
	// codex's message answers claude's hand-off, so it is delivered.
	if res := r.c.toolResults("codex", "message"); len(res) != 1 || res[0] != "delivered to claude" && res[0] != "queued for claude (busy)" {
		t.Fatalf("codex's thread.send = %q", res)
	}
	has(t, r.claude.prompts("claude")[1], "[codex → claude] one question answered: the lexer is fine")
}

// A hand-off with an objective names its deliverable.
func TestHandoffNeedsADeliverable(t *testing.T) {
	r := newRig(t, []fake.Script{
		turn(fake.Step{Message: &agent.Message{To: "codex", Objective: "Review it"}}, say("tried"), finish()),
	}, nil)
	tid := r.thread(t)
	r.add(t, tid, "claude")
	r.add(t, tid, "codex")
	if err := r.m.postAt(tid, "$claude go"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "claude", func() bool { return r.c.turnDones("claude") == 1 })
	res := r.c.toolResults("claude", "message")
	if len(res) != 1 || !strings.Contains(res[0], "names its deliverable") {
		t.Fatalf("result = %q", res)
	}
	if len(r.m.Tasks(tid)) != 0 {
		t.Fatal("a task was made from a refused hand-off")
	}
}

// A plain message to one's own subagent, and its report back, start turns.
func TestPlainMessagesAlongTheLineOfCommandStartTurns(t *testing.T) {
	r := newRig(t, []fake.Script{
		turn(sendTo("codex", "also check the lexer"), say("asked"), finish()),
		turn(say("thanks"), finish()),
	}, []fake.Script{
		turn(sendTo("claude", "lexer is fine"), say("checked"), finish()),
	})
	tid := r.thread(t)
	r.add(t, tid, "claude")
	r.add(t, tid, "codex")
	worksFor(t, r.m, "codex", "claude")
	if err := r.m.postAt(tid, "$claude go"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "claude's second turn", func() bool { return r.c.turnDones("claude") == 2 })
	has(t, r.claude.prompts("claude")[1], "[codex → claude] lexer is fine")
}

// A question to a peer goes to the user: the thread shows it, the peer is not
// woken, and the asker's turn ends blocked.
func TestQuestionToAPeerGoesToTheUser(t *testing.T) {
	r := newRig(t, []fake.Script{
		turn(sendTo("codex", "should the cache be per thread?"), say("Should the cache be per thread?"), finish()),
	}, nil)
	tid := r.thread(t)
	r.add(t, tid, "claude")
	r.add(t, tid, "codex")
	if err := r.m.postAt(tid, "$claude design the cache"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "claude", func() bool { return r.c.turnDones("claude") == 1 })
	res := r.c.toolResults("claude", "message")
	if len(res) != 1 || !strings.HasPrefix(res[0], "not sent to codex: agents do not ask each other questions") {
		t.Fatalf("result = %q", res)
	}
	if n := r.c.count(func(m tea.Msg) bool {
		n, ok := m.(NoticeMsg)
		return ok && strings.Contains(n.Text, "claude has a question") && strings.Contains(n.Text, "should the cache be per thread?")
	}); n != 1 {
		t.Fatalf("question notices = %d", n)
	}
	if got := endOf(t, r, "claude", 0); got != agent.TurnBlocked {
		t.Fatalf("turn end = %q", got)
	}
	if len(r.c.relays()) != 0 || len(r.codex.prompts("codex")) != 0 {
		t.Fatalf("the question reached codex: %+v", r.c.relays())
	}
}

// isQuestion tells a question from a URL's query.
func TestIsQuestion(t *testing.T) {
	for text, want := range map[string]bool{
		"is it per thread?":              true,
		"is it (per thread?)":            true,
		"see https://x.dev/a?b=1 please": false,
		"done.":                          false,
		"first? then":                    true,
	} {
		if got := isQuestion(text); got != want {
			t.Errorf("isQuestion(%q) = %v", text, got)
		}
	}
}

// At orchestration full only the lead routes: a worker cannot message
// another worker, but reports to the lead, which wakes it.
func TestFactoryModeWorkersReportToTheLead(t *testing.T) {
	r := newRig(t, nil, []fake.Script{
		turn(sendTo("claude2", "take the lexer"), sendTo("claude1", "parser done, lexer next"), say("reported"), finish()),
	})
	tid := r.thread(t)
	lead := r.add(t, tid, "claude")
	other := r.add(t, tid, "claude")
	r.add(t, tid, "codex")
	if err := r.m.SetLevel(tid, config.LevelFull); err != nil {
		t.Fatal(err)
	}
	if lead != "claude" || other != "claude2" { // ids; their names are claude1 and claude2
		t.Fatalf("members %s %s", lead, other)
	}
	if err := r.m.postAt(tid, "$codex go"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "codex", func() bool { return r.c.turnDones("codex") == 1 })
	res := r.c.toolResults("codex", "message")
	if len(res) != 2 {
		t.Fatalf("results = %q", res)
	}
	if !strings.Contains(res[0], "orchestration is full, so only the lead routes work") || !strings.Contains(res[0], "Report to claude1, the lead") {
		t.Fatalf("worker to worker = %q", res[0])
	}
	if res[1] != "delivered to claude1" {
		t.Fatalf("report to the lead = %q", res[1])
	}
}

// Below full, a worker may still message a peer (as information).
func TestBelowFullWorkersMayMessagePeers(t *testing.T) {
	r := newRig(t, nil, []fake.Script{
		turn(sendTo("claude2", "take the lexer"), say("told"), finish()),
	})
	tid := r.thread(t)
	r.add(t, tid, "claude")
	r.add(t, tid, "claude")
	r.add(t, tid, "codex")
	if err := r.m.SetLevel(tid, config.LevelAuto); err != nil {
		t.Fatal(err)
	}
	if err := r.m.postAt(tid, "$codex go"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "codex", func() bool { return r.c.turnDones("codex") == 1 })
	if res := r.c.toolResults("codex", "message"); len(res) != 1 || !strings.HasPrefix(res[0], "noted for claude2") {
		t.Fatalf("results = %q", res)
	}
}
