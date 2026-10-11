package model

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/dev-dami/deck/internal/config"
	"github.com/dev-dami/deck/internal/session"
	"github.com/dev-dami/deck/internal/ui/common"
	"github.com/dev-dami/deck/internal/workspace"
	"github.com/stretchr/testify/require"
)

func TestFmtElapsed(t *testing.T) {
	t.Parallel()
	cases := []struct {
		d    time.Duration
		want string
	}{
		{0, "0s"},
		{5 * time.Second, "5s"},
		{59 * time.Second, "59s"},
		{time.Minute, "1m00s"},
		{3*time.Minute + 5*time.Second, "3m05s"},
		{time.Hour + 2*time.Minute + 30*time.Second, "1h02m"},
		{25 * time.Hour, "25h00m"},
	}
	for _, c := range cases {
		require.Equal(t, c.want, fmtElapsed(c.d), "fmtElapsed(%v)", c.d)
	}
}

// rosterWorkspace is a workspace stub that lists a fixed roster.
type rosterWorkspace struct {
	workspace.Workspace
	lines []workspace.AgentLine
}

func (w *rosterWorkspace) Config() *config.Config              { return &config.Config{} }
func (w *rosterWorkspace) Roster(string) []workspace.AgentLine { return w.lines }

func newWorkingUI(lines ...workspace.AgentLine) *UI {
	com := common.DefaultCommon(&rosterWorkspace{lines: lines})
	return &UI{com: com, session: &session.Session{ID: "s1"}}
}

func TestWorkingLine(t *testing.T) {
	t.Parallel()

	t.Run("empty when nobody works", func(t *testing.T) {
		t.Parallel()
		u := newWorkingUI(
			workspace.AgentLine{ID: "claude", Kind: "claude", Status: "idle"},
			workspace.AgentLine{ID: "codex", Kind: "codex", Status: "exited"},
		)
		require.Empty(t, u.busyAgents())
		require.Empty(t, u.workingLine(80))
	})

	t.Run("names the busy agents", func(t *testing.T) {
		t.Parallel()
		u := newWorkingUI(
			workspace.AgentLine{ID: "claude", Kind: "claude", Status: "working", Since: time.Now().Add(-65 * time.Second)},
			workspace.AgentLine{ID: "codex", Kind: "codex", Status: "approval"},
			workspace.AgentLine{ID: "opencode", Kind: "opencode", Status: "idle"},
		)
		require.Len(t, u.busyAgents(), 2)
		line := ansi.Strip(u.workingLine(200))
		require.Contains(t, line, "claude working 1m0")
		require.Contains(t, line, "codex needs you")
		require.NotContains(t, line, "opencode")
		require.True(t, strings.HasSuffix(line, "esc esc stops"), line)
	})

	t.Run("fits the width", func(t *testing.T) {
		t.Parallel()
		u := newWorkingUI(workspace.AgentLine{ID: "claude", Kind: "claude", Status: "tool"})
		require.LessOrEqual(t, ansi.StringWidth(u.workingLine(20)), 20)
	})

	t.Run("no session", func(t *testing.T) {
		t.Parallel()
		u := newWorkingUI(workspace.AgentLine{ID: "claude", Kind: "claude", Status: "working"})
		u.session = nil
		require.Empty(t, u.workingLine(80))
	})
}
