package session

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/dev-dami/deck/internal/deck/agent"
	"github.com/dev-dami/deck/internal/deck/agent/fake"
)

// command is a step that ran a shell command: weak progress.
func command(title string) fake.Step {
	return fake.Step{Event: agent.Event{Kind: agent.KindToolResult, Tool: &agent.Tool{ID: title, Name: "Bash", Title: title, Status: agent.ToolOK}}}
}

// words is a message of n turns' worth of words no other call repeats.
func words(who string, i int) string {
	return fmt.Sprintf("%s note%d about subject%d and detail%d", who, i, i*7, i*13)
}

// chatter is n turns that each send to the other agent a message unlike the
// others, plus whatever steps each turn adds.
func chatter(who, to string, n int, extra func(i int) []fake.Step) []fake.Script {
	var out []fake.Script
	for i := 1; i <= n; i++ {
		var steps []fake.Step
		if extra != nil {
			steps = extra(i)
		}
		steps = append(steps, sendTo(to, words(who, i)), finish())
		out = append(out, turn(steps...))
	}
	return out
}

// stalled is the PausedMsg of a stall, when one came.
func stalled(r *rig) (PausedMsg, bool) {
	for _, m := range r.c.all() {
		if p, ok := m.(PausedMsg); ok && strings.HasPrefix(p.Reason, "the agents stalled") {
			return p, true
		}
	}
	return PausedMsg{}, false
}

func stallRig(t *testing.T, claude, codex []fake.Script) (*rig, string) {
	t.Helper()
	r := newRig(t, claude, codex)
	r.m.maxHops = 100 // the stall must come first
	tid := r.thread(t)
	r.add(t, tid, "claude")
	r.add(t, tid, "codex")
	return r, tid
}

// Three turns the agents start that change nothing and run nothing pause
// the thread, well before the hop cap; Continue goes on.
func TestThreeIdleTurnsPauseTheThread(t *testing.T) {
	r, tid := stallRig(t, chatter("claude", "codex", 8, nil), chatter("codex", "claude", 8, nil))
	if err := r.m.postAt(tid, "$claude talk it over with codex"); err != nil {
		t.Fatal(err)
	}
	var p PausedMsg
	waitFor(t, "the stall", func() bool { var ok bool; p, ok = stalled(r); return ok })
	has(t, p.Reason, "took 3 turns in a row without changing a file or a task or running a command")
	// Turns already under way when the stall is judged run out; then no
	// more start, far short of the 17 scripted.
	turns := func() int { return len(r.claude.prompts("claude")) + len(r.codex.prompts("codex")) }
	time.Sleep(60 * time.Millisecond)
	before := turns()
	time.Sleep(60 * time.Millisecond)
	if n := turns(); n != before || n > 9 {
		t.Fatalf("%d turns ran (%d a moment before); the stall should have stopped them", n, before)
	}
	if err := r.m.Continue(tid); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the held turn after Continue", func() bool {
		return len(r.codex.prompts("codex"))+len(r.claude.prompts("claude")) > before
	})
}

// Near-identical messages stall even while each turn runs a command.
func TestRepeatedMessagesStall(t *testing.T) {
	var claude, codex []fake.Script
	for i := 1; i <= 8; i++ {
		bang := strings.Repeat("!", i) // a different text to the exact-repeat check, the same words
		claude = append(claude, turn(command("go test ./..."), sendTo("codex", "the parser test still fails on line 40, can you look again"+bang), finish()))
		codex = append(codex, turn(command("go test ./parser"), sendTo("claude", "I looked and it passes for me, please run it once more"+bang), finish()))
	}
	r, tid := stallRig(t, claude, codex)
	if err := r.m.postAt(tid, "$claude get the parser test passing with codex"); err != nil {
		t.Fatal(err)
	}
	var p PausedMsg
	waitFor(t, "the stall", func() bool { var ok bool; p, ok = stalled(r); return ok })
	has(t, p.Reason, "repeated the same exchange 3 times", "claude", "codex")
}

// Two agents passing work back and forth stall even when each turn runs a
// command and says something new, as long as no file or task changes.
func TestPingPongStalls(t *testing.T) {
	cmd := func(i int) []fake.Step { return []fake.Step{command(fmt.Sprintf("make check%d", i))} }
	r, tid := stallRig(t, chatter("claude", "codex", 8, cmd), chatter("codex", "claude", 8, cmd))
	if err := r.m.postAt(tid, "$claude work it out with codex"); err != nil {
		t.Fatal(err)
	}
	var p PausedMsg
	waitFor(t, "the stall", func() bool { var ok bool; p, ok = stalled(r); return ok })
	has(t, p.Reason, "passed the work back and forth 3 times with no file or task changing")
}

// A file change is progress: agents that change files as they talk do not
// stall.
func TestFileChangesAreProgress(t *testing.T) {
	edit := func(i int) []fake.Step {
		return []fake.Step{{Event: agent.Event{Kind: agent.KindFileChange, Diff: &agent.FileDiff{Path: fmt.Sprintf("f%d.go", i), Added: 1}}}}
	}
	r, tid := stallRig(t, chatter("claude", "codex", 4, edit), chatter("codex", "claude", 4, edit))
	if err := r.m.postAt(tid, "$claude build it with codex"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the agents to run out of script", func() bool {
		return len(r.claude.prompts("claude")) >= 5 && len(r.codex.prompts("codex")) >= 4
	})
	if p, ok := stalled(r); ok {
		t.Fatalf("stalled while changing files: %q", p.Reason)
	}
}

// Turns the person starts never count, however alike and idle they are.
func TestTurnsThePersonStartsNeverStall(t *testing.T) {
	var scripts []fake.Script
	for range 6 {
		scripts = append(scripts, turn(say("I have nothing new to add on this one"), finish()))
	}
	r := newRig(t, scripts, nil)
	tid := r.thread(t)
	r.add(t, tid, "claude")
	for i := 1; i <= 6; i++ {
		if err := r.m.Post(tid, fmt.Sprintf("anything new? (%d)", i)); err != nil {
			t.Fatal(err)
		}
		waitFor(t, "the turn", func() bool { return r.c.turnDones("claude") == i })
	}
	if p, ok := stalled(r); ok {
		t.Fatalf("the person's own turns stalled: %q", p.Reason)
	}
	if th, _ := r.m.Thread(tid); th.Paused {
		t.Fatal("paused")
	}
}

// At full, the first stall asks the lead to re-plan instead of pausing; the
// second, before the person speaks, pauses.
func TestFullLevelReplansOnceThenPauses(t *testing.T) {
	r, tid := stallRig(t, chatter("claude", "codex", 20, nil), chatter("codex", "claude", 20, nil))
	if err := r.m.SetLevel(tid, "full"); err != nil {
		t.Fatal(err)
	}
	if err := r.m.postAt(tid, "$claude talk it over with codex"); err != nil {
		t.Fatal(err)
	}
	var p PausedMsg
	waitFor(t, "the second stall", func() bool { var ok bool; p, ok = stalled(r); return ok })
	has(t, p.Reason, "took 3 turns in a row")
	replans := r.c.count(func(m tea.Msg) bool {
		n, ok := m.(NoticeMsg)
		return ok && strings.HasPrefix(n.Text, "stalled: ") && strings.Contains(n.Text, "Deck asked claude to re-plan")
	})
	if replans != 1 {
		t.Fatalf("re-plan notices = %d, want 1", replans)
	}
	// The request is a message from Deck to the lead. (When the lead is in a
	// turn as it comes, the second stall can pause the thread before the
	// lead's next turn reads it; the person's next message then brings it.)
	var asks int
	for _, rl := range r.c.relays() {
		if rl.From == deckSpeaker && rl.Via == viaStall && len(rl.To) == 1 && rl.To[0] == "claude" && strings.Contains(rl.Text, "The work has stalled: ") {
			asks++
		}
	}
	if asks != 1 {
		t.Fatalf("re-plan requests to the lead = %d, want 1", asks)
	}
	// The person's message clears the ledger and the re-plan: a later
	// stall asks the lead again.
	before := replans
	if err := r.m.postAt(tid, "$claude keep going"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "a second re-plan after the person spoke", func() bool {
		return r.c.count(func(m tea.Msg) bool {
			n, ok := m.(NoticeMsg)
			return ok && strings.Contains(n.Text, "Deck asked claude to re-plan")
		}) == before+1
	})
}

func TestSimilarity(t *testing.T) {
	a := shinglesOf("The parser test still fails on line 40, can you look again?")
	b := shinglesOf("the parser test still fails on line 40 -- can you look again!!")
	c := shinglesOf("The parser test passes now; the lexer one fails on line 12.")
	if s := similarity(a, b); s < stallSimilar {
		t.Errorf("punctuation and case: %.2f", s)
	}
	if s := similarity(a, c); s >= stallSimilar {
		t.Errorf("different messages: %.2f", s)
	}
	if !alternates([]string{"a", "b", "a", "b", "a", "b"}, 3) || alternates([]string{"a", "b", "a", "c", "a", "b"}, 3) || alternates([]string{"a", "b", "a", "b"}, 3) {
		t.Error("alternates")
	}
}
