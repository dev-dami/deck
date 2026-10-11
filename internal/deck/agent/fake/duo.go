package fake

import (
	"time"

	"github.com/dev-dami/deck/internal/deck/agent"
)

// MCPFor is an Options.MCPFor for the fake agents: a placeholder server, so a
// session.Manager briefs them as if the deck messaging tools were available.
func MCPFor(agentID string) *agent.MCPServer { return &agent.MCPServer{Name: "deck"} }

// DemoDuo returns a claude and a codex adapter that share one Hub and play
// DemoDuoScripts. Pass the Hub to session.Options{Hub: hub, MCPFor: fake.MCPFor}.
func DemoDuo(speed float64) (map[string]agent.Adapter, *Hub) {
	hub := NewHub()
	c, x := DemoDuoScripts()
	return map[string]agent.Adapter{
		"claude": WithModels(NewTurns(speed, hub, c...), ClaudeModels()),
		"codex":  WithModels(NewTurns(speed, hub, x...), CodexModels()),
	}, hub
}

func say(text string) Step {
	return Step{Delay: 150 * time.Millisecond, Event: agent.Event{Kind: agent.KindAssistantText, Text: text}}
}

func usage(cost float64) []Step {
	return []Step{
		{Event: agent.Event{Kind: agent.KindUsage, Usage: &agent.Usage{InputTokens: 9000, OutputTokens: 700, CostUSD: cost}}},
		{Event: agent.Event{Kind: agent.KindTurnDone}},
	}
}

func tool(id, name, title string, delay time.Duration, out string) []Step {
	return []Step{
		{Delay: 200 * time.Millisecond, Event: agent.Event{Kind: agent.KindToolCall, Tool: &agent.Tool{
			ID: id, Name: name, Title: title, Status: agent.ToolRunning}}},
		{Delay: delay, Event: agent.Event{Kind: agent.KindToolResult, Tool: &agent.Tool{
			ID: id, Name: name, Title: title, Output: out, Status: agent.ToolOK}}},
	}
}

// DemoDuoScripts returns per-turn scripts for a claude and a codex that
// collaborate, for use with NewTurns:
//
//	claude turn 1 (the user asks for a plan): plans, then a thread.send to codex
//	    asking for a review of the plan.
//	codex  turn 1 (the relay arrives): reads the thread, checks the code, and
//	    answers with a line starting "$claude ..." which Deck relays.
//	claude turn 2 (the mention arrives): asks for permission to edit a test
//	    file, applies it and acknowledges, ending the exchange.
func DemoDuoScripts() (claude, codex []Script) {
	plan := "Plan for the store change:\n\n" +
		"- drop streaming deltas before they reach disk\n" +
		"- keep one lock per thread so writers do not wait on each other\n" +
		"- add a test that appends 1000 deltas and expects an empty log"

	c1 := Script{{Event: agent.Event{Kind: agent.KindSessionStarted, SessionID: "fake-duo-claude"}}}
	c1 = append(c1, tool("c_read", "Read", "internal/store/store.go", 300*time.Millisecond, "148 lines read")...)
	c1 = append(c1, say(plan))
	c1 = append(c1, Step{Delay: 300 * time.Millisecond, Message: &agent.Message{
		To:   "codex",
		Text: "Please review this plan for the store change and tell me what you would change:\n" + plan,
	}})
	c1 = append(c1, say("I asked codex to review the plan; I will continue once it answers.")) // final reply
	c1 = append(c1, usage(0.09)...)

	diff := fileDiff("internal/store/store_test.go", hunk(40, 40, `
 func TestAppend(t *testing.T) {
+	// 1000 deltas must leave the log empty
+	for i := 0; i < 1000; i++ {
+		_ = s.Append(id, agent.Event{Kind: agent.KindAssistantText, Delta: true})
+	}
 }
`))
	c2 := Script{
		say("Good catch on the lock ordering. Adding the delta test now."),
		{
			Delay: 300 * time.Millisecond,
			Event: agent.Event{Kind: agent.KindToolCall, Tool: &agent.Tool{
				ID: "c_edit", Name: "Edit", Title: "internal/store/store_test.go", Status: agent.ToolRunning}},
			Approval: &agent.ApprovalRequest{
				ID: "fake-duo-approval-edit", Kind: agent.ApproveEdit, ToolName: "Edit",
				Summary: "internal/store/store_test.go", Diff: diff,
			},
		},
		{Delay: 100 * time.Millisecond, Event: agent.Event{Kind: agent.KindFileChange, Diff: diff}},
		{Delay: 150 * time.Millisecond, Event: agent.Event{Kind: agent.KindToolResult, Tool: &agent.Tool{
			ID: "c_edit", Name: "Edit", Title: "internal/store/store_test.go", Output: "Applied 1 hunk", Status: agent.ToolOK}}},
		say("Added the test and acknowledged codex's review: lock ordering fixed, delta test in place."),
	}
	c2 = append(c2, usage(0.06)...)

	x1 := Script{
		{Event: agent.Event{Kind: agent.KindSessionStarted, SessionID: "fake-duo-codex"}},
		{ReadThread: true},
	}
	x1 = append(x1, tool("x_read", "Read", "internal/store/store.go", 400*time.Millisecond, "148 lines read")...)
	x1 = append(x1, say("The plan holds. One change: take the per-thread lock before the index lock, "+
		"never the other way round, or Append and List can deadlock.\n\n"+
		"$claude agree on the lock order and add the 1000-delta test; I will not touch store.go meanwhile."))
	x1 = append(x1, usage(0.04)...)

	return []Script{c1, c2}, []Script{x1}
}
