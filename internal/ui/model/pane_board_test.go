package model

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"

	"github.com/dev-dami/deck/internal/config"
	"github.com/dev-dami/deck/internal/session"
	"github.com/dev-dami/deck/internal/ui/common"
	"github.com/dev-dami/deck/internal/ui/dialog"
	"github.com/dev-dami/deck/internal/workspace"
)

// boardWorkspace is an activity workspace with a task board that records what
// the pane asks of it.
type boardWorkspace struct {
	activityWorkspace
	board     workspace.BoardReport
	cancelled []string
	retried   []string
	orch      []bool
}

func (w *boardWorkspace) Board(string) workspace.BoardReport { return w.board }
func (w *boardWorkspace) CancelTask(_, id string) error {
	w.cancelled = append(w.cancelled, id)
	return nil
}
func (w *boardWorkspace) RetryTask(_, id string) error { w.retried = append(w.retried, id); return nil }
func (w *boardWorkspace) SteerTask(_, _, _ string) error {
	return nil
}
func (w *boardWorkspace) SetOrchestrate(_ string, on bool) error {
	w.orch = append(w.orch, on)
	return nil
}
func (w *boardWorkspace) Config() *config.Config { return nil }

func newBoardUI(w *boardWorkspace) *UI {
	com := common.DefaultCommon(w)
	return &UI{
		com:     com,
		status:  NewStatus(com, nil),
		chat:    NewChat(com, config.ScrollbarDefault),
		state:   uiChat,
		width:   140,
		height:  45,
		session: &session.Session{ID: "s1"},
		keyMap:  DefaultKeyMap(),
		dialog:  dialog.NewOverlay(),
	}
}

func TestTasksPaneShowsTheBoardAndActsOnIt(t *testing.T) {
	t.Parallel()
	w := &boardWorkspace{board: workspace.BoardReport{Orchestrate: true, Tasks: []workspace.BoardTask{
		{ID: "t1", Title: "write the parser", Status: "done", Owner: "claude", Result: "parse.go"},
		{ID: "t2", Title: "test it", Status: "failed", Owner: "codex", Deps: []string{"t1"}, Error: "flaky", Attempts: 2, Tries: 2},
		{ID: "t3", Title: "ship it", Status: "blocked", Deps: []string{"t2"}},
	}}}
	m := newBoardUI(w)
	p := paneFactories[paneTasks]()

	require.Equal(t, "Tasks", p.title(m))
	require.Equal(t, "2", p.badge(m), "done tasks are not open")
	out := ansi.Strip(p.render(m, 40, 6, true))
	for _, want := range []string{"orchestrating", "✓ t1 write the parser", "✗ t2 test it", "! t3 ship it", "codex"} {
		require.Contains(t, out, want)
	}
	for _, l := range strings.Split(out, "\n") {
		require.LessOrEqual(t, ansi.StringWidth(l), 40)
	}

	// The cursor starts on t1 (the header is not selectable): one step down is
	// t2. Expand it, retry it, then step to t3 and cancel it.
	p.key(m, tea.KeyPressMsg{Code: 'j', Text: "j"})
	handled, _ := p.key(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	require.True(t, handled)
	open := ansi.Strip(p.render(m, 40, 12, true))
	require.Contains(t, open, "attempt 2 of 2")
	require.Contains(t, open, "✗ flaky")

	handled, cmd := p.key(m, tea.KeyPressMsg{Code: 'r', Text: "r"})
	require.True(t, handled)
	require.NotNil(t, cmd)
	cmd()
	require.Equal(t, []string{"t2"}, w.retried)

	p.key(m, tea.KeyPressMsg{Code: 'j', Text: "j"})
	_, cmd = p.key(m, tea.KeyPressMsg{Code: 'x', Text: "x"})
	require.NotNil(t, cmd)
	cmd()
	require.Equal(t, []string{"t3"}, w.cancelled)

	handled, cmd = p.key(m, tea.KeyPressMsg{Code: 'o', Text: "o"})
	require.True(t, handled)
	cmd()
	require.Equal(t, []bool{false}, w.orch, "o flips the current setting")
}

func TestTasksPaneWithoutABoardIsAsBefore(t *testing.T) {
	t.Parallel()
	m := newActivityUI(workspace.ActivityReport{})
	p := paneFactories[paneTasks]()
	require.Equal(t, "", p.badge(m))
	require.Contains(t, ansi.Strip(p.render(m, 30, 1, false)), "No subagents running")
	handled, cmd := p.key(m, tea.KeyPressMsg{Code: 'x', Text: "x"})
	require.False(t, handled)
	require.Nil(t, cmd)
}

// worktreeWorkspace is a board workspace whose tasks' worktrees can be
// applied and discarded.
type worktreeWorkspace struct {
	boardWorkspace
	applied, discarded []string
}

func (w *worktreeWorkspace) ApplyTask(_, id string) ([]string, error) {
	w.applied = append(w.applied, id)
	return []string{"a.go", "b.go"}, nil
}
func (w *worktreeWorkspace) DiscardTask(_, id string) error {
	w.discarded = append(w.discarded, id)
	return nil
}

func TestTasksPaneAppliesAndDiscardsWorktrees(t *testing.T) {
	t.Parallel()
	w := &worktreeWorkspace{boardWorkspace: boardWorkspace{board: workspace.BoardReport{Tasks: []workspace.BoardTask{
		{ID: "t1", Title: "isolated", Status: "done", Owner: "codex2", Branch: "deck/ab12cd-codex2"},
		{ID: "t2", Title: "shared tree", Status: "done", Owner: "claude"},
	}}}}
	m := newBoardUI(&w.boardWorkspace)
	m.com = common.DefaultCommon(w)
	p := paneFactories[paneTasks]()
	run := func(cmd tea.Cmd) tea.Msg {
		require.NotNil(t, cmd)
		return cmd()
	}

	// On t1: a applies.
	handled, cmd := p.key(m, tea.KeyPressMsg{Code: 'a', Text: "a"})
	require.True(t, handled)
	require.Contains(t, fmt.Sprint(run(cmd)), "Applied 2 files from deck/ab12cd-codex2")
	require.Equal(t, []string{"t1"}, w.applied)

	// d asks first; the second d discards.
	handled, cmd = p.key(m, tea.KeyPressMsg{Code: 'd', Text: "d"})
	require.True(t, handled)
	require.Contains(t, fmt.Sprint(run(cmd)), "Press d again")
	require.Empty(t, w.discarded)
	_, cmd = p.key(m, tea.KeyPressMsg{Code: 'd', Text: "d"})
	run(cmd)
	require.Equal(t, []string{"t1"}, w.discarded)

	// t2 has no worktree: a and d are not the pane's.
	p.key(m, tea.KeyPressMsg{Code: 'j', Text: "j"})
	handled, _ = p.key(m, tea.KeyPressMsg{Code: 'a', Text: "a"})
	require.False(t, handled)
}

func TestTasksPaneComparesRaceEntriesAndKeepsOne(t *testing.T) {
	t.Parallel()
	w := &worktreeWorkspace{boardWorkspace: boardWorkspace{board: workspace.BoardReport{Tasks: []workspace.BoardTask{
		{ID: "t1", Title: "race: fix the flaky test", Status: "done", Owner: "deck", Race: true},
		{ID: "t2", Title: "fix the flaky test", Status: "done", Owner: "claude2", Entry: true, Branch: "deck/ab-claude2",
			Stats: "2 files +10 -3 · 12k tokens · 4 min", Files: []string{"a.go", "a_test.go"}, Result: "go test passes"},
		{ID: "t3", Title: "fix the flaky test", Status: "failed", Owner: "codex2", Entry: true, Error: "gave up"},
	}}}}
	m := newBoardUI(&w.boardWorkspace)
	m.com = common.DefaultCommon(w)
	p := paneFactories[paneTasks]()

	out := ansi.Strip(p.render(m, 80, 10, true))
	require.Contains(t, out, "race: fix the flaky test")
	require.Contains(t, out, "  ✓ t2 claude2")
	require.Contains(t, out, "2 files +10 -3 · 12k tokens · 4 min")
	require.Contains(t, out, "  ✗ t3 codex2")

	// enter on t2 shows its files and what a does there.
	p.key(m, tea.KeyPressMsg{Code: 'j', Text: "j"})
	p.key(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	out = ansi.Strip(p.render(m, 80, 20, true))
	require.Contains(t, out, "files a.go, a_test.go")
	require.Contains(t, out, "a keep this one")

	handled, cmd := p.key(m, tea.KeyPressMsg{Code: 'a', Text: "a"})
	require.True(t, handled)
	require.Contains(t, fmt.Sprint(cmd()), "Kept t2: applied 2 files from deck/ab-claude2 and discarded the other entries")
	require.Equal(t, []string{"t2"}, w.applied)
}
