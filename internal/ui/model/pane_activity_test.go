package model

import (
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"

	"github.com/dev-dami/deck/internal/config"
	"github.com/dev-dami/deck/internal/session"
	"github.com/dev-dami/deck/internal/ui/common"
	"github.com/dev-dami/deck/internal/ui/dialog"
	"github.com/dev-dami/deck/internal/ui/notice"
	"github.com/dev-dami/deck/internal/ui/util"
	"github.com/dev-dami/deck/internal/workspace"
)

// activityWorkspace reports a fixed activity.
type activityWorkspace struct {
	workspace.Workspace
	report workspace.ActivityReport
}

func (w *activityWorkspace) Activity(string) workspace.ActivityReport { return w.report }
func (w *activityWorkspace) Config() *config.Config                   { return nil }

func newActivityUI(r workspace.ActivityReport) *UI {
	com := common.DefaultCommon(&activityWorkspace{report: r})
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

func TestActivityPanesRegistered(t *testing.T) {
	t.Parallel()
	for _, id := range []paneID{paneTasks, paneCommands, paneNotices} {
		f, ok := paneFactories[id]
		require.True(t, ok, id)
		require.Equal(t, id, f().id())
	}
}

func TestTasksPaneHeightAndRows(t *testing.T) {
	t.Parallel()
	m := newActivityUI(workspace.ActivityReport{})
	p := paneFactories[paneTasks]()
	require.Equal(t, 1, p.height(m, 30), "the empty line")
	require.Contains(t, ansi.Strip(p.render(m, 30, 1, false)), "No subagents running")
	require.Equal(t, "", p.badge(m))

	now := time.Now()
	m = newActivityUI(workspace.ActivityReport{Subagents: []workspace.RunningTool{
		{ToolID: "a", AgentKind: "claude", AgentName: "claude", Name: "Task", Summary: "Find the bug", Started: now.Add(-42 * time.Second)},
		{ToolID: "b", AgentKind: "opencode", AgentName: "opencode", Name: "task", Summary: "Write tests", Detail: "general\nwrite them", Started: now},
	}})
	require.Equal(t, 2, p.height(m, 30))
	require.Equal(t, "2", p.badge(m))
	out := ansi.Strip(p.render(m, 30, 2, true))
	require.Contains(t, out, "Find the bug")
	require.Contains(t, out, "42s")
	for _, l := range strings.Split(out, "\n") {
		require.LessOrEqual(t, ansi.StringWidth(l), 30)
	}

	// enter expands the selected row.
	handled, _ := p.key(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	require.True(t, handled)
	require.Greater(t, p.height(m, 30), 2)
}

func TestCommandsPaneSections(t *testing.T) {
	t.Parallel()
	m := newActivityUI(workspace.ActivityReport{})
	p := paneFactories[paneCommands]()
	require.Equal(t, 1, p.height(m, 30))
	require.Contains(t, ansi.Strip(p.render(m, 30, 1, false)), "Nothing running")

	now := time.Now()
	m = newActivityUI(workspace.ActivityReport{
		Commands:   []workspace.RunningTool{{ToolID: "c1", AgentKind: "codex", Name: "Bash", Summary: "go test ./...", Started: now}},
		Background: []workspace.BackgroundShell{{ShellID: "bash_1", AgentKind: "claude", Command: "npm run dev", Started: now.Add(-3 * time.Minute)}},
	})
	m.jobStarted("git-1", "Pushing…")
	// Running + 1, Background + 1, Deck + 1.
	require.Equal(t, 6, p.height(m, 40))
	require.Equal(t, "2", p.badge(m))
	out := ansi.Strip(p.render(m, 40, 6, false))
	for _, want := range []string{"Running", "go test ./...", "Background", "npm run dev", "bash_1", "since 3m", "Deck", "Pushing"} {
		require.Contains(t, out, want)
	}

	m.jobDone("git-1", errors.New("rejected"))
	require.Equal(t, 4, p.height(m, 40))
}

func TestPaneListSkipsHeaders(t *testing.T) {
	t.Parallel()
	rows := []paneRow{
		{lines: []string{"Running"}},
		{key: "a", selectable: true, lines: []string{"a"}},
		{lines: []string{"Background"}},
		{key: "b", selectable: true, lines: []string{"b", "b2"}},
	}
	var l paneList
	require.Equal(t, "a", l.selected(rows))
	l.move(rows, 1)
	require.Equal(t, "b", l.selected(rows))
	l.move(rows, 1)
	require.Equal(t, "b", l.selected(rows), "stays on the last")
	l.home(rows)
	require.Equal(t, "a", l.selected(rows))
	require.Equal(t, 5, rowsHeight(rows))

	// Scrolled to keep the cursor's row in view, and clicks map back.
	l.end(rows)
	out := l.view(rows, 10, 2, true, lipgloss.Color("#ffffff"))
	require.Equal(t, 2, len(strings.Split(out, "\n")))
	require.Contains(t, ansi.Strip(out), "b2")
	require.Equal(t, 3, l.rowAt(0))
}

func TestNoticesPaneAndStore(t *testing.T) {
	t.Parallel()
	m := newActivityUI(workspace.ActivityReport{})
	p := paneFactories[paneNotices]()
	require.Equal(t, 1, p.height(m, 30))
	require.Contains(t, ansi.Strip(p.render(m, 30, 1, false)), "No notifications")

	m.noticeFromInfo(util.InfoMsg{Type: util.InfoTypeError, Msg: "push rejected"})
	m.noticeFromInfo(util.InfoMsg{Type: util.InfoTypeInfo, Msg: "CI passed on #12"})
	m.noticeFromInfo(util.InfoMsg{Type: util.InfoTypeYolo, Msg: "yolo"})
	require.Equal(t, 2, m.notices.store.Len(), "mode banners are not notices")
	list := m.notices.store.List()
	require.Equal(t, notice.SourceCI, list[0].Source)
	require.Equal(t, notice.Success, list[0].Level)
	require.NotNil(t, list[0].Action)
	require.Equal(t, notice.ActionGitPR+"12", list[0].Action.ID)
	require.Equal(t, 2, m.notices.toasts.Len())
	require.Equal(t, "2 new", p.badge(m))

	// Looking at the pane reads them.
	p.render(m, 30, 10, true)
	require.Equal(t, "", p.badge(m))
	require.Greater(t, p.height(m, 30), 2)

	handled, _ := p.key(m, tea.KeyPressMsg{Code: 'c', Text: "c"})
	require.True(t, handled)
	require.Equal(t, 0, m.notices.store.Len())
	require.Equal(t, 0, m.notices.toasts.Len())
}

func TestPlainInfoIsQuiet(t *testing.T) {
	t.Parallel()
	m := newActivityUI(workspace.ActivityReport{})
	m.noticeFromInfo(util.InfoMsg{Type: util.InfoTypeInfo, Msg: "Copied"})
	require.Equal(t, 1, m.notices.store.Len())
	require.Equal(t, 0, m.notices.toasts.Len())
}

func TestToastTickScheduling(t *testing.T) {
	t.Parallel()
	m := newActivityUI(workspace.ActivityReport{})
	require.Nil(t, m.scheduleToastTick(time.Now()), "no toasts, no timer")
	m.Notify(notice.Notice{Level: notice.Warn, Body: "careful"})
	require.NotNil(t, m.scheduleToastTick(time.Now()))
	require.Nil(t, m.scheduleToastTick(time.Now()), "one timer at a time")
	gen := m.notices.gen

	// A stale tick is ignored; the live one expires the toast at its time.
	require.Nil(t, m.handleToastTick(toastTickMsg{at: time.Now(), gen: gen - 1}))
	require.Equal(t, 1, m.notices.toasts.Len())
	m.handleToastTick(toastTickMsg{at: time.Now().Add(notice.ToastTTL + time.Second), gen: gen})
	require.Equal(t, 0, m.notices.toasts.Len())
	require.False(t, m.notices.ticking)
}

func TestPRNumber(t *testing.T) {
	t.Parallel()
	require.Equal(t, 12, prNumber("CI failed on #12 — ctrl+x to see why"))
	require.Equal(t, 0, prNumber("no number"))
}
