package model

import (
	"testing"

	"github.com/dev-dami/deck/internal/config"
	"github.com/dev-dami/deck/internal/session"
	"github.com/dev-dami/deck/internal/ui/common"
	"github.com/dev-dami/deck/internal/workspace"
	"github.com/stretchr/testify/require"
)

// countingRosterWorkspace counts roster reads.
type countingRosterWorkspace struct {
	workspace.Workspace
	lines []workspace.AgentLine
	reads int
}

func (w *countingRosterWorkspace) Config() *config.Config { return &config.Config{} }
func (w *countingRosterWorkspace) Roster(string) []workspace.AgentLine {
	w.reads++
	return w.lines
}

// TestFrameMemo_OneRosterReadPerFrame asserts the roster is read once per
// session while a frame is drawn, and live outside of one.
func TestFrameMemo_OneRosterReadPerFrame(t *testing.T) {
	t.Parallel()

	ws := &countingRosterWorkspace{lines: []workspace.AgentLine{
		{ID: "claude", Kind: "claude", Status: "working", ContextTokens: 10, ContextWindow: 100},
	}}
	u := &UI{com: common.DefaultCommon(ws), session: &session.Session{ID: "s1"}}

	u.beginFrameMemo()
	_ = u.busyAgents()
	_ = u.markLevels()
	_ = u.sessionRoster()
	_ = u.modelInfo(40)
	_, _ = contextPercent(u.com, u.rosterOrNil(), u.session)
	u.endFrameMemo()
	require.Equal(t, 1, ws.reads, "one roster read per frame")

	_ = u.busyAgents()
	_ = u.busyAgents()
	require.Equal(t, 3, ws.reads, "outside a frame every read is live")
}

// TestAgentsPaneMemo asserts the agents pane keeps its text while the
// roster, width and theme hold, and rebuilds it when any changes.
func TestAgentsPaneMemo(t *testing.T) {
	t.Parallel()

	ws := &countingRosterWorkspace{lines: []workspace.AgentLine{
		{ID: "claude", Kind: "claude", Status: "working"},
	}}
	u := &UI{com: common.DefaultCommon(ws), session: &session.Session{ID: "s1"}}
	p := &agentsPane{}

	first := p.text(u, 40)
	require.Contains(t, first, "working")
	require.Equal(t, first, p.text(u, 40))

	ws.lines = []workspace.AgentLine{{ID: "claude", Kind: "claude", Status: "ready"}}
	second := p.text(u, 40)
	require.NotEqual(t, first, second, "a roster change rebuilds the text")
	require.Contains(t, second, "ready")

	require.NotEqual(t, second, p.text(u, 30), "a width change rebuilds the text")

	before := p.memo.text
	u.stylesGen++
	_ = p.text(u, 30)
	require.Equal(t, before, p.memo.text, "same inputs render the same text")
	require.Equal(t, u.stylesGen, p.memo.styles, "a theme change rebuilds the text")
}
