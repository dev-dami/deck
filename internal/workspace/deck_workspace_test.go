package workspace

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	deckagent "github.com/dev-dami/deck/internal/deck/agent"
	"github.com/dev-dami/deck/internal/deck/agent/fake"
	deckconfig "github.com/dev-dami/deck/internal/deck/config"
	"github.com/dev-dami/deck/internal/deck/engine"
	decks "github.com/dev-dami/deck/internal/deck/session"
	"github.com/dev-dami/deck/internal/deck/store"
	"github.com/dev-dami/deck/internal/message"
	tools "github.com/dev-dami/deck/internal/toolspec"
	"github.com/stretchr/testify/require"
)

func TestSetText(t *testing.T) {
	t.Parallel()

	t.Run("no text yet appends", func(t *testing.T) {
		t.Parallel()
		m := message.Message{}
		setText(&m, "hello", false)
		require.Len(t, m.Parts, 1)
		require.Equal(t, "hello", m.Content().Text)
	})

	t.Run("streamed text is replaced", func(t *testing.T) {
		t.Parallel()
		m := message.Message{Parts: []message.ContentPart{message.TextContent{Text: "hel"}}}
		setText(&m, "hello", true)
		require.Len(t, m.Parts, 1)
		require.Equal(t, "hello", m.Content().Text)
	})

	t.Run("earlier text is followed", func(t *testing.T) {
		t.Parallel()
		m := message.Message{Parts: []message.ContentPart{message.TextContent{Text: "first"}}}
		setText(&m, "second", false)
		require.Len(t, m.Parts, 1)
		require.Equal(t, "first"+"\n\n"+"second", m.Content().Text)
	})
}

func TestSides(t *testing.T) {
	t.Parallel()
	d := deckagent.FileDiff{Path: "a.go", Hunks: []deckagent.Hunk{
		{Lines: []deckagent.DiffLine{
			{Op: deckagent.OpContext, Text: "a"},
			{Op: deckagent.OpDel, Text: "b"},
			{Op: deckagent.OpAdd, Text: "B"},
		}},
		{Lines: []deckagent.DiffLine{
			{Op: deckagent.OpContext, Text: "x"},
			{Op: deckagent.OpAdd, Text: "y"},
		}},
	}}
	before, after := sides(d)
	require.Equal(t, lines("a", "b", "…", "x"), before)
	require.Equal(t, lines("a", "B", "…", "x", "y"), after)

	before, after = sides(deckagent.FileDiff{})
	require.Empty(t, before)
	require.Empty(t, after)
}

// lines joins ls with a newline after each.
func lines(ls ...string) string {
	var s string
	for _, l := range ls {
		s += l + "\n"
	}
	return s
}

func TestDeckTool(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		tool     deckagent.Tool
		wantName string
		want     map[string]any
	}{
		{"bash", deckagent.Tool{Name: "Bash", Input: raw(`{"command":"go vet ./...","description":"vet"}`)},
			tools.BashToolName, map[string]any{"command": "go vet ./...", "description": "vet"}},
		{"bash cmd key", deckagent.Tool{Name: "Bash", Input: raw(`{"cmd":"ls"}`)},
			tools.BashToolName, map[string]any{"command": "ls"}},
		{"bash from title", deckagent.Tool{Name: "Bash", Title: "make"},
			tools.BashToolName, map[string]any{"command": "make"}},
		{"read", deckagent.Tool{Name: "Read", Input: raw(`{"file_path":"a.go"}`)},
			tools.ViewToolName, map[string]any{"file_path": "a.go"}},
		{"read opencode keys", deckagent.Tool{Name: "Read", Input: raw(`{"filePath":"b.go"}`)},
			tools.ViewToolName, map[string]any{"file_path": "b.go"}},
		{"read path key", deckagent.Tool{Name: "Read", Input: raw(`{"path":"c.go"}`)},
			tools.ViewToolName, map[string]any{"file_path": "c.go"}},
		{"read from title", deckagent.Tool{Name: "Read", Title: "d.go"},
			tools.ViewToolName, map[string]any{"file_path": "d.go"}},
		{"edit", deckagent.Tool{Name: "Edit", Input: raw(`{"file_path":"a.go","old_string":"x","new_string":"y"}`)},
			tools.EditToolName, map[string]any{"file_path": "a.go", "old_string": "x", "new_string": "y"}},
		{"edit opencode keys", deckagent.Tool{Name: "Edit", Input: raw(`{"filePath":"a.go","oldString":"x","newString":"y"}`)},
			tools.EditToolName, map[string]any{"file_path": "a.go", "old_string": "x", "new_string": "y"}},
		{"multiedit", deckagent.Tool{Name: "MultiEdit", Input: raw(`{"file_path":"a.go"}`)},
			tools.EditToolName, map[string]any{"file_path": "a.go"}},
		{"write", deckagent.Tool{Name: "Write", Input: raw(`{"file_path":"n.go","content":"package n"}`)},
			tools.WriteToolName, map[string]any{"file_path": "n.go", "content": "package n"}},
		{"grep", deckagent.Tool{Name: "Grep", Input: raw(`{"pattern":"TODO","path":"internal","glob":"*.go"}`)},
			tools.GrepToolName, map[string]any{"pattern": "TODO", "path": "internal", "include": "*.go"}},
		{"grep include key", deckagent.Tool{Name: "Grep", Input: raw(`{"pattern":"x","include":"*.md"}`)},
			tools.GrepToolName, map[string]any{"pattern": "x", "include": "*.md"}},
		{"glob", deckagent.Tool{Name: "Glob", Input: raw(`{"pattern":"**/*.go","path":"cmd"}`)},
			tools.GlobToolName, map[string]any{"pattern": "**/*.go", "path": "cmd"}},
		{"ls", deckagent.Tool{Name: "LS", Input: raw(`{"path":"internal"}`)},
			tools.LSToolName, map[string]any{"path": "internal"}},
		{"webfetch", deckagent.Tool{Name: "WebFetch", Input: raw(`{"url":"https://example.com"}`)},
			tools.FetchToolName, map[string]any{"url": "https://example.com", "format": "markdown"}},
		{"unknown keeps its input", deckagent.Tool{Name: "Task", Input: raw(`{"prompt":"look"}`)},
			"Task", map[string]any{"prompt": "look"}},
		{"unknown without input uses the title", deckagent.Tool{Name: "Task", Title: "looking"},
			"Task", map[string]any{"input": "looking"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			name, input := deckTool(&c.tool)
			require.Equal(t, c.wantName, name)
			var got map[string]any
			require.NoError(t, json.Unmarshal([]byte(input), &got))
			for k, v := range c.want {
				require.Equal(t, v, got[k], "key %q in %s", k, input)
			}
		})
	}
}

func raw(s string) json.RawMessage { return json.RawMessage(s) }

func TestTurnResult(t *testing.T) {
	t.Parallel()
	diff := deckagent.FileDiff{Path: "internal/a.go", Added: 1, Removed: 1, Hunks: []deckagent.Hunk{{Lines: []deckagent.DiffLine{
		{Op: deckagent.OpDel, Text: "old"},
		{Op: deckagent.OpAdd, Text: "new"},
	}}}}
	tr := &turn{diffs: map[string]deckagent.FileDiff{"internal/a.go": diff}}

	t.Run("bash", func(t *testing.T) {
		t.Parallel()
		r := tr.result(&deckagent.Tool{ID: "1", Name: "Bash", Input: raw(`{"command":"ls"}`), Output: "a.go", Status: deckagent.ToolOK})
		require.Equal(t, "1", r.ToolCallID)
		require.Equal(t, tools.BashToolName, r.Name)
		require.Equal(t, "a.go", r.Content)
		require.False(t, r.IsError)
		var meta tools.BashResponseMetadata
		require.NoError(t, json.Unmarshal([]byte(r.Metadata), &meta))
		require.Empty(t, meta.Output, "the output is kept once, in Content")
	})

	t.Run("bash whose output reads no output", func(t *testing.T) {
		t.Parallel()
		r := tr.result(&deckagent.Tool{ID: "1b", Name: "Bash", Input: raw(`{"command":"echo"}`), Output: tools.BashNoOutput, Status: deckagent.ToolOK})
		var meta tools.BashResponseMetadata
		require.NoError(t, json.Unmarshal([]byte(r.Metadata), &meta))
		require.Equal(t, tools.BashNoOutput, meta.Output, "Content alone would read as no output at all")
	})

	t.Run("error status", func(t *testing.T) {
		t.Parallel()
		r := tr.result(&deckagent.Tool{ID: "2", Name: "Bash", Input: raw(`{"command":"false"}`), Status: deckagent.ToolError})
		require.True(t, r.IsError)
	})

	editMeta := func(t *testing.T, path string) tools.EditResponseMetadata {
		t.Helper()
		r := tr.result(&deckagent.Tool{ID: "3", Name: "Edit", Input: raw(`{"file_path":"` + path + `"}`), Status: deckagent.ToolOK})
		require.Equal(t, tools.EditToolName, r.Name)
		require.NotEmpty(t, r.Metadata)
		var meta tools.EditResponseMetadata
		require.NoError(t, json.Unmarshal([]byte(r.Metadata), &meta))
		return meta
	}

	t.Run("edit by exact path", func(t *testing.T) {
		t.Parallel()
		meta := editMeta(t, "internal/a.go")
		require.Equal(t, 1, meta.Additions)
		require.Equal(t, 1, meta.Removals)
		require.Equal(t, lines("old"), meta.OldContent)
		require.Equal(t, lines("new"), meta.NewContent)
	})

	t.Run("edit by path suffix", func(t *testing.T) {
		t.Parallel()
		meta := editMeta(t, "/home/me/repo/internal/a.go")
		require.Equal(t, 1, meta.Additions)
		require.Equal(t, lines("new"), meta.NewContent)
	})

	t.Run("failed edit has no earlier edit's diff", func(t *testing.T) {
		t.Parallel()
		r := tr.result(&deckagent.Tool{ID: "3b", Name: "Edit", Input: raw(`{"file_path":"internal/a.go"}`), Output: "not found", Status: deckagent.ToolError})
		require.True(t, r.IsError)
		require.Empty(t, r.Metadata)
	})

	t.Run("write without a diff has no metadata", func(t *testing.T) {
		t.Parallel()
		r := tr.result(&deckagent.Tool{ID: "4", Name: "Write", Input: raw(`{"file_path":"other.go"}`), Status: deckagent.ToolOK})
		require.Equal(t, tools.WriteToolName, r.Name)
		require.Empty(t, r.Metadata)
	})

	t.Run("view", func(t *testing.T) {
		t.Parallel()
		r := tr.result(&deckagent.Tool{ID: "5", Name: "Read", Input: raw(`{"file_path":"x.go"}`), Output: "package x", Status: deckagent.ToolOK})
		require.Equal(t, tools.ViewToolName, r.Name)
		var meta tools.ViewResponseMetadata
		require.NoError(t, json.Unmarshal([]byte(r.Metadata), &meta))
		require.Equal(t, "x.go", meta.FilePath)
		require.Empty(t, meta.Content, "the output is kept once, in Content")
		require.Equal(t, "package x", r.Content)
	})

	t.Run("other tools have no metadata", func(t *testing.T) {
		t.Parallel()
		r := tr.result(&deckagent.Tool{ID: "6", Name: "Task", Input: raw(`{"prompt":"go"}`), Output: "done"})
		require.Equal(t, "Task", r.Name)
		require.Equal(t, "done", r.Content)
		require.Empty(t, r.Metadata)
	})
}

func TestFirstLine(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		n    int
		want string
	}{
		{"short", 10, "short"},
		{"  padded  ", 10, "padded"},
		{"first" + "\n" + "second", 20, "first"},
		{"abcdef", 4, "abc…"},
		{"héllo wörld", 5, "héll…"},
		{"", 5, ""},
	}
	for _, c := range cases {
		require.Equal(t, c.want, firstLine(c.in, c.n), "firstLine(%q, %d)", c.in, c.n)
	}
}

// newPickerWorkspace is a DeckWorkspace with only the engine side: fake claude
// and codex adapters and the given Deck config.
func newPickerWorkspace(t *testing.T, cfg deckconfig.Config) *DeckWorkspace {
	t.Helper()
	st, err := store.New(t.TempDir())
	require.NoError(t, err)
	adapters := map[string]deckagent.Adapter{
		"claude": fake.New(nil, 0),
		"codex":  fake.WithModels(fake.New(nil, 0), fake.CodexModels()),
	}
	m := decks.NewManager(adapters, st, cfg)
	t.Cleanup(m.Close)
	return &DeckWorkspace{
		eng:     &engine.Engine{Manager: m, Config: cfg},
		threads: map[string]string{},
		back:    map[string]string{},
		models:  map[string][]deckagent.ModelInfo{},
		since:   map[string]time.Time{},
	}
}

func pickerCfg() deckconfig.Config {
	cfg := deckconfig.Config{UserName: "tester", DefaultAgent: "claude", Motion: "off"}
	cfg.Models.Set("codex", "gpt-5")
	cfg.Efforts.Set("claude", "high")
	cfg.Efforts.Set("codex", "low")
	return cfg
}

func TestCachedAgentModelChoicesBeforeAsking(t *testing.T) {
	w := newPickerWorkspace(t, pickerCfg())
	got := w.CachedAgentModelChoices("s1")
	require.Equal(t, []ModelChoice{
		{Kind: "claude", ID: "", Name: "default model", Current: true, CurrentEffort: "high"},
		{Kind: "codex", ID: "gpt-5", Name: "gpt-5", Current: true, CurrentEffort: "low"},
	}, got)
}

func TestAgentModelChoicesListsModelsAndEfforts(t *testing.T) {
	w := newPickerWorkspace(t, pickerCfg())
	got, err := w.AgentModelChoices(context.Background(), "s1")
	require.NoError(t, err)
	require.Len(t, got, len(fake.ClaudeModels())+len(fake.CodexModels()))

	byID := map[string]ModelChoice{}
	for _, c := range got {
		byID[c.Kind+"/"+c.ID] = c
	}

	// claude has no model set: the CLI's default is current.
	def := byID["claude/default"]
	require.True(t, def.Default)
	require.True(t, def.Current)
	require.Equal(t, "high", def.CurrentEffort)
	opus := byID["claude/opus"]
	require.False(t, opus.Current)
	require.Len(t, opus.Efforts, 4)
	require.Equal(t, "low", opus.Efforts[0].Level)
	require.Equal(t, "max", opus.Efforts[3].Level)
	require.Empty(t, byID["claude/haiku"].Efforts)

	// codex runs gpt-5 by config, not its default model.
	require.False(t, byID["codex/gpt-5-codex"].Current)
	g5 := byID["codex/gpt-5"]
	require.True(t, g5.Current)
	require.Equal(t, "low", g5.CurrentEffort)
	require.Equal(t, "medium", g5.DefaultEffort)

	// the lists are kept: the cached answer is the same.
	require.Equal(t, got, w.CachedAgentModelChoices("s1"))
}

func TestRosterBeforeAnyoneJoins(t *testing.T) {
	w := newPickerWorkspace(t, pickerCfg())
	require.Equal(t, []AgentLine{
		{ID: "claude", Kind: "claude", Status: "ready", Model: "", Effort: "high"},
		{ID: "codex", Kind: "codex", Status: "ready", Model: "gpt-5", Effort: "low"},
	}, w.Roster("s1"))
}

func TestRosterListsThreadMembers(t *testing.T) {
	w := newPickerWorkspace(t, pickerCfg())
	tid, err := w.mgr().NewThread(t.TempDir())
	require.NoError(t, err)
	w.threads["s1"], w.back[tid] = tid, "s1"
	_, err = w.mgr().AddMember(tid, "codex", "")
	require.NoError(t, err)

	got := w.Roster("s1")
	require.Len(t, got, 1)
	require.Equal(t, "codex", got[0].Kind)
	require.NotEmpty(t, got[0].ID)
	require.NotEqual(t, "ready", got[0].Status)

	// a session with no thread still lists the installed CLIs.
	require.Len(t, w.Roster("other"), 2)
}

func TestRosterListsSubagentsUnderTheirParent(t *testing.T) {
	// Agents that stop at an approval nobody answers, so they stay running.
	block := fake.Script{{
		Event:    deckagent.Event{Kind: deckagent.KindToolCall, Tool: &deckagent.Tool{ID: "b", Name: "Bash", Title: "make", Status: deckagent.ToolRunning}},
		Approval: &deckagent.ApprovalRequest{ID: "b", Kind: deckagent.ApproveCommand, ToolName: "Bash", Summary: "make"},
	}}
	w := newPickerWorkspace(t, pickerCfg())
	st, err := store.New(t.TempDir())
	require.NoError(t, err)
	m := decks.NewManager(map[string]deckagent.Adapter{
		"claude": fake.NewTurns(0, nil, block), "codex": fake.NewTurns(0, nil, block),
	}, st, pickerCfg())
	t.Cleanup(m.Close)
	w.eng = &engine.Engine{Manager: m, Config: pickerCfg()}

	tid, err := m.NewThread(t.TempDir())
	require.NoError(t, err)
	w.threads["s1"], w.back[tid] = tid, "s1"
	lead, err := m.AddMember(tid, "codex", "")
	require.NoError(t, err)
	_, err = m.AddMember(tid, "claude", "")
	require.NoError(t, err)
	require.NoError(t, m.SetOrchestrate(tid, true))
	a, err := m.Spawn(tid, lead, decks.SpawnSpec{Kind: "claude", Brief: "one"})
	require.NoError(t, err)
	_, err = m.Spawn(tid, a.AgentID, decks.SpawnSpec{Kind: "codex", Brief: "two"})
	require.NoError(t, err)

	var got []string
	for _, l := range w.Roster("s1") {
		got = append(got, fmt.Sprintf("%d %s<%s", l.Depth, l.ID, l.Parent))
	}
	require.Equal(t, []string{"0 codex1<", "1 claude2<codex1", "2 codex2<claude2", "0 claude1<"}, got)
}
