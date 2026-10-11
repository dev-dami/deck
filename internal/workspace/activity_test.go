package workspace

import (
	"encoding/json"
	"testing"
	"time"

	deckagent "github.com/dev-dami/deck/internal/deck/agent"
	"github.com/stretchr/testify/require"
)

func toolEv(kind deckagent.Kind, agentID string, tool *deckagent.Tool) deckagent.Event {
	return deckagent.Event{AgentID: agentID, Kind: kind, Tool: tool}
}

func TestActivityTracksCallsUntilResult(t *testing.T) {
	t.Parallel()
	var a activity
	who := agentRef{id: "a1", name: "claude", kind: "claude"}
	now := time.Unix(1000, 0)

	a.observe("s1", who, toolEv(deckagent.KindToolCall, "a1", &deckagent.Tool{
		ID: "t1", Name: "Task", Input: json.RawMessage(`{"description":"Find the bug","subagent_type":"Explore","prompt":"look around"}`),
	}), now)
	a.observe("s1", who, toolEv(deckagent.KindToolCall, "a1", &deckagent.Tool{
		ID: "t2", Name: "Bash", Input: json.RawMessage(`{"command":"go test ./...","description":"run tests"}`),
	}), now.Add(time.Second))
	a.observe("s1", who, toolEv(deckagent.KindToolCall, "a1", &deckagent.Tool{ID: "t3", Name: "Read"}), now)

	r := a.report("s1")
	require.Len(t, r.Tools, 3)
	require.Len(t, r.Subagents, 1)
	require.Equal(t, "Find the bug", r.Subagents[0].Summary)
	require.Contains(t, r.Subagents[0].Detail, "Explore")
	require.Equal(t, "claude", r.Subagents[0].AgentName)
	require.Len(t, r.Commands, 1)
	require.Equal(t, "go test ./...", r.Commands[0].Summary)
	require.Equal(t, "run tests", r.Commands[0].Detail)
	require.Empty(t, a.report("other").Tools)

	a.observe("s1", who, toolEv(deckagent.KindToolResult, "a1", &deckagent.Tool{ID: "t1", Name: "Task", Status: deckagent.ToolOK}), now)
	r = a.report("s1")
	require.Empty(t, r.Subagents)
	require.Len(t, r.Commands, 1)

	a.observe("s1", who, deckagent.Event{AgentID: "a1", Kind: deckagent.KindTurnDone}, now)
	require.Empty(t, a.report("s1").Tools)
}

func TestActivityTextForDeckJobs(t *testing.T) {
	t.Parallel()
	w := &DeckWorkspace{back: map[string]string{"t1": "s1"}}
	who := agentRef{id: "a1", name: "claude", kind: "claude"}
	w.act.observe("s1", who, toolEv(deckagent.KindToolCall, "a1", &deckagent.Tool{
		ID: "b1", Name: "Bash", Input: json.RawMessage(`{"command":"go test ./...\nmore"}`),
	}), time.Now())
	text := w.activityText("t1")
	require.Contains(t, text, "Running now:")
	require.Contains(t, text, "claude [Bash] go test ./... ...")
	require.Empty(t, w.activityText("unknown"))
}

func TestActivityRepeatedCallKeepsStart(t *testing.T) {
	t.Parallel()
	var a activity
	who := agentRef{id: "a1", name: "codex", kind: "codex"}
	start := time.Unix(1000, 0)
	a.observe("s", who, toolEv(deckagent.KindToolCall, "a1", &deckagent.Tool{ID: "x", Name: "Bash", Title: "ls"}), start)
	a.observe("s", who, toolEv(deckagent.KindToolCall, "a1", &deckagent.Tool{ID: "x", Name: "Bash", Title: "ls -la"}), start.Add(time.Minute))
	r := a.report("s")
	require.Len(t, r.Commands, 1)
	require.Equal(t, start, r.Commands[0].Started)
	require.Equal(t, "ls -la", r.Commands[0].Summary)
}

func TestActivityOnlyDropsThatAgent(t *testing.T) {
	t.Parallel()
	var a activity
	now := time.Unix(1000, 0)
	a.observe("s", agentRef{id: "a1"}, toolEv(deckagent.KindToolCall, "a1", &deckagent.Tool{ID: "1", Name: "Bash"}), now)
	a.observe("s", agentRef{id: "a2"}, toolEv(deckagent.KindToolCall, "a2", &deckagent.Tool{ID: "2", Name: "Bash"}), now)
	a.observe("s", agentRef{id: "a1"}, deckagent.Event{AgentID: "a1", Kind: deckagent.KindError}, now)
	r := a.report("s")
	require.Len(t, r.Commands, 1)
	require.Equal(t, "a2", r.Commands[0].AgentID)
}

func TestActivityBackgroundShellLifecycle(t *testing.T) {
	t.Parallel()
	who := agentRef{id: "a1", name: "claude", kind: "claude"}
	now := time.Unix(1000, 0)
	start := func(a *activity) {
		a.observe("s", who, toolEv(deckagent.KindToolCall, "a1", &deckagent.Tool{
			ID: "t1", Name: "Bash", Input: json.RawMessage(`{"command":"npm run dev","run_in_background":true}`),
		}), now)
		a.observe("s", who, toolEv(deckagent.KindToolResult, "a1", &deckagent.Tool{
			ID: "t1", Name: "Bash", Status: deckagent.ToolOK, Output: "Command running in background with ID: bash_1",
		}), now.Add(time.Second))
	}

	t.Run("recorded with its shell id", func(t *testing.T) {
		t.Parallel()
		var a activity
		start(&a)
		r := a.report("s")
		require.Empty(t, r.Commands)
		require.Len(t, r.Background, 1)
		require.Equal(t, "bash_1", r.Background[0].ShellID)
		require.Equal(t, "npm run dev", r.Background[0].Command)
		require.Equal(t, now, r.Background[0].Started)
	})

	t.Run("survives the turn, output is kept", func(t *testing.T) {
		t.Parallel()
		var a activity
		start(&a)
		a.observe("s", who, deckagent.Event{AgentID: "a1", Kind: deckagent.KindTurnDone}, now)
		a.observe("s", who, toolEv(deckagent.KindToolResult, "a1", &deckagent.Tool{
			ID: "t2", Name: "BashOutput", Input: json.RawMessage(`{"bash_id":"bash_1"}`),
			Output: "<status>running</status>\n<stdout>ready on :3000</stdout>",
		}), now)
		r := a.report("s")
		require.Len(t, r.Background, 1)
		require.Contains(t, r.Background[0].Output, "ready on :3000")
	})

	t.Run("BashOutput says it finished", func(t *testing.T) {
		t.Parallel()
		var a activity
		start(&a)
		a.observe("s", who, toolEv(deckagent.KindToolResult, "a1", &deckagent.Tool{
			ID: "t2", Name: "BashOutput", Input: json.RawMessage(`{"bash_id":"bash_1"}`),
			Output: "<status>completed</status>\n<exit_code>0</exit_code>",
		}), now)
		require.Empty(t, a.report("s").Background)
	})

	t.Run("KillShell", func(t *testing.T) {
		t.Parallel()
		var a activity
		start(&a)
		a.observe("s", who, toolEv(deckagent.KindToolCall, "a1", &deckagent.Tool{
			ID: "t3", Name: "KillShell", Input: json.RawMessage(`{"shell_id":"bash_1"}`),
		}), now)
		require.Empty(t, a.report("s").Background)
	})

	t.Run("the agent exits", func(t *testing.T) {
		t.Parallel()
		var a activity
		start(&a)
		a.observe("s", who, deckagent.Event{AgentID: "a1", Kind: deckagent.KindExited}, now)
		require.Empty(t, a.report("s").Background)
	})

	t.Run("no id in the output falls back to the call id", func(t *testing.T) {
		t.Parallel()
		var a activity
		a.observe("s", who, toolEv(deckagent.KindToolCall, "a1", &deckagent.Tool{
			ID: "t9", Name: "Bash", Input: json.RawMessage(`{"command":"sleep 100","run_in_background":true}`),
		}), now)
		a.observe("s", who, toolEv(deckagent.KindToolResult, "a1", &deckagent.Tool{ID: "t9", Name: "Bash", Status: deckagent.ToolOK}), now)
		r := a.report("s")
		require.Len(t, r.Background, 1)
		require.Equal(t, "t9", r.Background[0].ShellID)
	})

	t.Run("a failed start is not recorded", func(t *testing.T) {
		t.Parallel()
		var a activity
		a.observe("s", who, toolEv(deckagent.KindToolCall, "a1", &deckagent.Tool{
			ID: "t9", Name: "Bash", Input: json.RawMessage(`{"command":"nope","run_in_background":true}`),
		}), now)
		a.observe("s", who, toolEv(deckagent.KindToolResult, "a1", &deckagent.Tool{ID: "t9", Name: "Bash", Status: deckagent.ToolError}), now)
		require.Empty(t, a.report("s").Background)
	})
}

func TestShellHelpers(t *testing.T) {
	t.Parallel()
	require.Equal(t, "bash_3", backgroundID("Command running in background with ID: bash_3"))
	require.Equal(t, "abc", backgroundID(`{"shell_id": "abc"}`))
	require.Equal(t, "", backgroundID("all done"))
	require.True(t, shellEnded("<status>killed</status>"))
	require.True(t, shellEnded(`"status": "failed"`))
	require.False(t, shellEnded("<status>running</status>"))
	require.False(t, shellEnded("no status here"))
	require.Equal(t, "go test", commandOf(&deckagent.Tool{}, json.RawMessage(`{"command":["bash","-lc","go test"]}`)))
	require.Equal(t, "b\nc", tail("a\nb\nc", 4))
	require.LessOrEqual(t, len(tail(string(make([]byte, 10000)), maxShellOutput)), maxShellOutput)
}
