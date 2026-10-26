package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/dev-dami/deck/internal/deck/agent"
)

// legacyEvent is agent.Event as logs wrote it before its fields were
// omitempty: every field, empty or not.
type legacyEvent struct {
	AgentID   string
	Time      time.Time
	Kind      agent.Kind
	Text      string
	Delta     bool
	Tool      *agent.Tool
	Diff      *agent.FileDiff
	Usage     *agent.Usage
	Limits    *agent.Limits
	SessionID string
	Raw       json.RawMessage
}

type legacyEntry struct {
	legacyEvent
	Type    string   `json:"type,omitempty"`
	Speaker string   `json:"speaker,omitempty"`
	To      []string `json:"to,omitempty"`
	ID      string   `json:"id,omitempty"`
}

func TestLogsWithExplicitNullsLoadLikeSlimOnes(t *testing.T) {
	at := time.Date(2026, 1, 2, 3, 4, 5, 6, time.UTC)
	entries := []Entry{
		{Type: EntryUser, Speaker: "dami", To: []string{"claude"}, ID: "u1", Event: agent.Event{Kind: agent.KindUserText, Text: "fix it", Time: at}},
		{ID: "a1", Event: agent.Event{AgentID: "claude", Time: at, Kind: agent.KindSessionStarted, SessionID: "s-1", Text: "opus"}},
		{ID: "a2", Event: agent.Event{AgentID: "claude", Time: at, Kind: agent.KindToolCall,
			Tool: &agent.Tool{ID: "t1", Name: "Bash", Title: "go test", Input: json.RawMessage(`{"command":"go test"}`), Status: agent.ToolRunning}}},
		{ID: "a3", Event: agent.Event{AgentID: "claude", Time: at, Kind: agent.KindToolResult,
			Tool: &agent.Tool{ID: "t1", Name: "Bash", Output: "ok", Status: agent.ToolOK}}},
		{ID: "a4", Event: agent.Event{AgentID: "claude", Time: at, Kind: agent.KindFileChange,
			Diff: &agent.FileDiff{Path: "a.go", Added: 1, Hunks: []agent.Hunk{{NewStart: 1, NewLines: 1, Lines: []agent.DiffLine{{Op: agent.OpAdd, Text: "x"}}}}}}},
		{ID: "a5", Event: agent.Event{AgentID: "claude", Time: at, Kind: agent.KindError, Text: "boom"}},
		{ID: "a6", Event: agent.Event{AgentID: "claude", Time: at, Kind: agent.KindUsage, Usage: &agent.Usage{InputTokens: 10, OutputTokens: 2}}},
		{ID: "a7", Event: agent.Event{AgentID: "claude", Time: at, Kind: agent.KindAssistantText, Text: "done"}},
		{ID: "a8", Event: agent.Event{AgentID: "claude", Time: at, Kind: agent.KindTurnDone}},
	}

	// The same entries as an old log wrote them.
	oldDir := t.TempDir()
	meta := `{"id":"old1","title":"x","dir":"/w","created":"2026-01-01T00:00:00Z","updated":"2026-01-01T00:00:00Z","members":[{"id":"claude","kind":"claude"}]}`
	if err := os.WriteFile(filepath.Join(oldDir, "old1.meta.json"), []byte(meta), 0o600); err != nil {
		t.Fatal(err)
	}
	var old strings.Builder
	for _, e := range entries {
		ev := e.Event
		line, err := json.Marshal(legacyEntry{
			legacyEvent: legacyEvent{ev.AgentID, ev.Time, ev.Kind, ev.Text, ev.Delta, ev.Tool, ev.Diff, ev.Usage, ev.Limits, ev.SessionID, ev.Raw},
			Type:        e.Type, Speaker: e.Speaker, To: e.To, ID: e.ID,
		})
		if err != nil {
			t.Fatal(err)
		}
		old.Write(line)
		old.WriteByte('\n')
	}
	if !strings.Contains(old.String(), `"Raw":null`) || !strings.Contains(old.String(), `"Tool":null`) {
		t.Fatalf("legacy log lacks explicit nulls:\n%s", old.String())
	}
	if err := os.WriteFile(filepath.Join(oldDir, "old1.jsonl"), []byte(old.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	olds, err := New(oldDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = olds.Close() })
	fromOld, _, err := olds.LoadEntries("old1")
	if err != nil {
		t.Fatal(err)
	}

	// And as the store writes them now.
	s, dir := newStore(t)
	th := mustCreate(t, s, Thread{})
	if err := s.AppendEntries(th.ID, entries); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, th.ID+".jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal([]byte(line), &fields); err != nil {
			t.Fatal(err)
		}
		for k, v := range fields {
			switch string(v) {
			case "null", `""`, "false", `"0001-01-01T00:00:00Z"`:
				t.Errorf("new log writes the empty field %s: %s", k, line)
			}
		}
	}
	if len(data) >= old.Len() {
		t.Errorf("new log is %d bytes, the old one %d", len(data), old.Len())
	}
	fromNew, _, err := s.LoadEntries(th.ID)
	if err != nil {
		t.Fatal(err)
	}

	if len(fromOld) != len(entries) || !reflect.DeepEqual(fromOld, fromNew) {
		t.Fatalf("old log loads as\n%+v\nnew log as\n%+v", fromOld, fromNew)
	}
	if e := fromOld[5]; e.Raw != nil || e.Text != "boom" {
		t.Errorf("old error entry = %+v, want no Raw", e)
	}
	if e := fromOld[2]; e.Tool == nil || string(e.Tool.Input) != `{"command":"go test"}` || e.Diff != nil || e.Usage != nil {
		t.Errorf("old tool call = %+v", e)
	}
}
