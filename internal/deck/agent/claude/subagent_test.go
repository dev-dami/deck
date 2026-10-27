package claude

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/dev-dami/deck/internal/deck/agent"
)

// TestSubagents replays two parallel Task subagents, one of which fails and
// is started again: each comes as a KindSubagent when it starts and when it
// ends, its work carries its Task call's id as Sub, and none of it is the
// agent's own text or context.
func TestSubagents(t *testing.T) {
	evs := replay(t, filepath.Join("testdata", "subagents.jsonl"))

	type change struct {
		id     string
		status agent.SubStatus
	}
	var changes []change
	subs := map[string]agent.Subagent{}
	work := map[string][]string{}
	var own []string
	for _, ev := range evs {
		switch {
		case ev.Kind == agent.KindSubagent:
			if ev.Subagent == nil || ev.Sub != "" {
				t.Fatalf("subagent event %+v", ev)
			}
			changes = append(changes, change{ev.Subagent.ID, ev.Subagent.Status})
			subs[ev.Subagent.ID] = *ev.Subagent
		case ev.Sub != "":
			if _, ok := subs[ev.Sub]; !ok {
				t.Fatalf("work of %s before it started: %+v", ev.Sub, ev)
			}
			w := string(ev.Kind)
			if ev.Tool != nil {
				w += ":" + ev.Tool.Name
			}
			work[ev.Sub] = append(work[ev.Sub], w)
		case ev.Kind == agent.KindAssistantText:
			own = append(own, ev.Text)
		}
	}
	want := []change{
		{"toolu_01TaskA", agent.SubRunning},
		{"toolu_01TaskB", agent.SubRunning},
		{"toolu_01TaskA", agent.SubDone},
		{"toolu_01TaskB", agent.SubFailed},
		{"toolu_01TaskC", agent.SubRunning},
		{"toolu_01TaskC", agent.SubDone},
	}
	if len(changes) != len(want) {
		t.Fatalf("changes %v\nwant    %v", changes, want)
	}
	for i := range want {
		if changes[i] != want[i] {
			t.Fatalf("changes %v\nwant    %v", changes, want)
		}
	}

	a, b, c := subs["toolu_01TaskA"], subs["toolu_01TaskB"], subs["toolu_01TaskC"]
	if a.Description != "Review PR #295" || a.Type != "general-purpose" || !strings.HasPrefix(a.Result, "PR #295 looks good") {
		t.Errorf("A = %+v", a)
	}
	if b.Type != "code-reviewer" || b.Error != "Agent stopped: API Error: 529 overloaded" || b.Result != "" {
		t.Errorf("B = %+v", b)
	}
	if c.Description != "Review PR #296" || !strings.Contains(c.Result, "nil") {
		t.Errorf("C = %+v", c)
	}

	eq := func(got, want []string) bool { return strings.Join(got, ",") == strings.Join(want, ",") }
	if w := work["toolu_01TaskA"]; !eq(w, []string{"assistant_text", "tool_call:Bash", "tool_result:Bash", "tool_call:Read", "tool_result:Read", "assistant_text"}) {
		t.Errorf("A's work %v", w)
	}
	if w := work["toolu_01TaskB"]; !eq(w, []string{"tool_call:Bash", "tool_result:Bash"}) {
		t.Errorf("B's work %v", w)
	}
	if w := work["toolu_01TaskC"]; !eq(w, []string{"reasoning", "tool_call:Grep", "tool_result:Grep", "assistant_text"}) {
		t.Errorf("C's work %v", w)
	}
	// B's failed call is its own, not the agent's.
	for _, ev := range evs {
		if ev.Kind == agent.KindToolResult && ev.Tool.ID == "toolu_01SubB1" && (ev.Sub != "toolu_01TaskB" || ev.Tool.Status != agent.ToolError) {
			t.Errorf("B's failed call %+v", ev)
		}
	}

	// The agent's own words, and the Task calls and results as its tools.
	if !eq(own, []string{"I'll review both PRs in parallel.", "The #296 reviewer stopped. I've restarted the #296 review.", "Both reviews are done: #295 is good, #296 needs a nil check."}) {
		t.Errorf("own text %q", own)
	}
	tasks := 0
	for _, ev := range evs {
		if ev.Sub == "" && ev.Kind == agent.KindToolResult && ev.Tool.Name == "Task" {
			tasks++
		}
	}
	if tasks != 3 {
		t.Errorf("Task results = %d, want 3", tasks)
	}

	// The subagents' usage (a smaller model, its own context) does not move
	// the agent's meter: every context figure is the agent's.
	for _, ev := range evs {
		if ev.Kind == agent.KindUsage && ev.Usage.ContextTokens > 0 && ev.Usage.ContextTokens < 30000 {
			t.Errorf("subagent usage leaked: %+v", ev.Usage)
		}
	}
}

// TestSubagentsStopWithTheTurn: a turn interrupted while its subagents run
// ends them as stopped, before it ends.
func TestSubagentsStopWithTheTurn(t *testing.T) {
	tr := newTranslator("c1")
	var evs []agent.Event
	for _, l := range []string{
		`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"tk1","name":"Agent","input":{"description":"Map the code","subagent_type":"Explore"}},{"type":"tool_use","id":"tk2","name":"Task","input":{"description":"Find tests"}}]}}`,
		`{"type":"assistant","parent_tool_use_id":"tk1","message":{"content":[{"type":"tool_use","id":"g1","name":"Glob","input":{"pattern":"**/*.go"}}]}}`,
		`{"type":"result","subtype":"error_during_execution","is_error":true,"result":"interrupted"}`,
	} {
		evs = append(evs, tr.Line([]byte(l))...)
	}
	var got []string
	for _, ev := range evs {
		if ev.Kind == agent.KindSubagent {
			got = append(got, ev.Subagent.ID+"="+string(ev.Subagent.Status))
		}
	}
	if strings.Join(got, " ") != "tk1=running tk2=running tk1=stopped tk2=stopped" {
		t.Fatalf("subagents %v", got)
	}
	if last := evs[len(evs)-1]; last.Kind != agent.KindTurnDone {
		t.Fatalf("last %+v", last)
	}
	// A subagent whose start Deck did not see starts with its first line.
	tr = newTranslator("c1")
	evs = tr.Line([]byte(`{"type":"assistant","parent_tool_use_id":"tk9","message":{"content":[{"type":"text","text":"hi"}]}}`))
	if len(evs) != 2 || evs[0].Kind != agent.KindSubagent || evs[0].Subagent.ID != "tk9" || evs[1].Sub != "tk9" {
		t.Fatalf("unseen start %+v", evs)
	}
}
