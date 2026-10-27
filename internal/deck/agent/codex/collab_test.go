package codex

import (
	"strings"
	"testing"

	"github.com/dev-dami/deck/internal/deck/agent"
)

// TestCollabSubagents: a spawnAgent call starts a subagent, the spawned
// thread's items are its work, a later wait reports how it ended, and its
// turn does not end codex's.
func TestCollabSubagents(t *testing.T) {
	tr := newTranslator("codex1", "/w")
	var evs []agent.Event
	add := func(method, params string) { evs = append(evs, notif(t, tr, method, params)...) }
	add(noteItemStarted, `{"threadId":"thr","turnId":"u1","item":{"type":"collabAgentToolCall","id":"c1","tool":"spawnAgent","status":"inProgress","senderThreadId":"thr","receiverThreadIds":[],"agentsStates":{},"prompt":"Review PR #296\nin depth","model":"gpt-5.5-codex"}}`)
	add(noteItemCompleted, `{"threadId":"thr","turnId":"u1","item":{"type":"collabAgentToolCall","id":"c1","tool":"spawnAgent","status":"completed","senderThreadId":"thr","receiverThreadIds":["kid"],"agentsStates":{"kid":{"status":"running"}}}}`)
	add(noteItemStarted, `{"threadId":"kid","turnId":"k1","item":{"type":"commandExecution","id":"x1","command":"gh pr diff 296","cwd":"/w","status":"inProgress"}}`)
	add(noteAgentDelta, `{"threadId":"kid","itemId":"m1","delta":"Looks"}`)
	add(noteItemCompleted, `{"threadId":"kid","turnId":"k1","item":{"type":"agentMessage","id":"m1","text":"Looks fine."}}`)
	add(noteTurnCompleted, `{"threadId":"kid","turn":{"id":"k1","status":"completed"}}`)
	add(noteItemCompleted, `{"threadId":"thr","turnId":"u1","item":{"type":"collabAgentToolCall","id":"c2","tool":"wait","status":"completed","senderThreadId":"thr","receiverThreadIds":["kid"],"agentsStates":{"kid":{"status":"completed","message":"Looks fine."}}}}`)

	var got []string
	for _, e := range evs {
		switch {
		case e.Kind == agent.KindSubagent:
			got = append(got, "sub "+e.Subagent.ID+" "+string(e.Subagent.Status)+" "+e.Subagent.Description+" "+e.Subagent.Result)
		case e.Sub != "":
			got = append(got, e.Sub+" "+string(e.Kind))
		default:
			got = append(got, "own "+string(e.Kind))
		}
	}
	want := []string{
		"sub c1 running Review PR #296 ",
		"c1 tool_call",
		"c1 assistant_text",
		"sub c1 done Review PR #296 Looks fine.",
	}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("events\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}
