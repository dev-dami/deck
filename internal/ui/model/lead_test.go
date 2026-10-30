package model

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"

	deckconfig "github.com/dev-dami/deck/internal/deck/config"
	"github.com/dev-dami/deck/internal/session"
	"github.com/dev-dami/deck/internal/ui/attachments"
	"github.com/dev-dami/deck/internal/ui/dialog"
	"github.com/dev-dami/deck/internal/ui/styles"
	"github.com/dev-dami/deck/internal/ui/util"
	"github.com/dev-dami/deck/internal/workspace"
)

// leadWorkspace has rooms with leads, the agent picker and the settings
// the default agent comes from; it records what SetAgentModel was asked.
type leadWorkspace struct {
	*testWorkspace
	leads   map[string]workspace.Lead
	cfg     deckconfig.Config
	kinds   []string
	set     []string // "session kind model effort"
	created int
}

func (w *leadWorkspace) Room(id string) workspace.Room                  { return workspace.Room{Lead: w.leads[id]} }
func (w *leadWorkspace) DeckSettings() deckconfig.Config                { return w.cfg }
func (w *leadWorkspace) SetDeckSettings(func(*deckconfig.Config)) error { return nil }
func (w *leadWorkspace) AgentKinds() []string                           { return w.kinds }

func (w *leadWorkspace) AgentModelChoices(context.Context, string) ([]workspace.ModelChoice, error) {
	return nil, nil
}
func (w *leadWorkspace) CachedAgentModelChoices(string) []workspace.ModelChoice { return nil }

func (w *leadWorkspace) SetAgentModel(sessionID, kind, model, effort string) error {
	w.set = append(w.set, strings.Join([]string{sessionID, kind, model, effort}, " "))
	return nil
}

func (w *leadWorkspace) CreateSession(context.Context, string) (session.Session, error) {
	w.created++
	return session.Session{ID: "new"}, nil
}

func newLeadUI(t *testing.T, sessionID string) (*UI, *leadWorkspace) {
	t.Helper()
	u, tw := newPlanUI(t, sessionID)
	tw.agentReady = true
	ws := &leadWorkspace{
		testWorkspace: tw,
		leads:         map[string]workspace.Lead{},
		cfg:           deckconfig.Config{DefaultAgent: "codex"},
		kinds:         []string{"claude", "codex"},
	}
	u.com.Workspace = ws
	u.keyMap = DefaultKeyMap()
	u.attachments = attachments.New(nil, attachments.Keymap{})
	u.status = NewStatus(u.com, nil)
	return u, ws
}

func plainLeadLine(l workspace.Lead, width int) string {
	sty := styles.DeckDark()
	return ansi.Strip(renderLeadLine(&sty, l, "ctrl+l", "agent", width))
}

func TestLeadLine(t *testing.T) {
	t.Parallel()
	claude := workspace.Lead{Agent: "claude", Kind: "claude", Model: "opus", Effort: "high"}
	mark := strings.Fields(plainLeadLine(claude, 80))[0]

	require.Equal(t, " "+mark+" claude · opus · high  ctrl+l agent", plainLeadLine(claude, 80))
	require.Equal(t, " "+mark+" charles · opus  ctrl+l agent",
		plainLeadLine(workspace.Lead{Kind: "claude", Profile: "charles", Model: "opus"}, 80),
		"a named agent shows its name; no effort, none shown")
	require.Equal(t, " "+mark+" claude  ctrl+l agent", plainLeadLine(workspace.Lead{Kind: "claude"}, 80),
		"the CLI's default model shows no model")
	require.Equal(t, " ctrl+l agent", plainLeadLine(workspace.Lead{}, 80), "an unknown lead: the hint alone")
	require.Equal(t, " "+mark+" claude · claude-opus-5-5 · high  ctrl+l agent",
		plainLeadLine(workspace.Lead{Kind: "claude", Model: "opus", Slug: "claude-opus-5-5", Effort: "high"}, 80),
		"the slug the CLI runs, not the alias")
	require.Equal(t, " "+mark+" claude · claude-opus-5-5  ctrl+l agent",
		plainLeadLine(workspace.Lead{Kind: "claude", Slug: "claude-opus-5-5"}, 80),
		"the CLI's default model, once a run named it")
}

// A narrow editor drops the hint first, then cuts the label; the line never
// runs past its width.
func TestLeadLineTruncates(t *testing.T) {
	t.Parallel()
	l := workspace.Lead{Kind: "claude", Model: "claude-opus-4-1", Effort: "high"}
	full := plainLeadLine(l, 80)
	require.Contains(t, full, "ctrl+l agent")

	narrow := plainLeadLine(l, ansi.StringWidth(full)-1)
	require.NotContains(t, narrow, "ctrl+l")
	require.True(t, strings.HasSuffix(narrow, "claude-opus-4-1 · high"), narrow)

	cut := plainLeadLine(l, 16)
	require.True(t, strings.HasSuffix(cut, "…"), cut)
	require.Equal(t, 16, ansi.StringWidth(cut))

	for w := range 50 {
		require.LessOrEqual(t, ansi.StringWidth(plainLeadLine(l, w)), max(0, w), "width %d", w)
		require.LessOrEqual(t, ansi.StringWidth(plainLeadLine(workspace.Lead{}, w)), max(0, w), "width %d", w)
	}
	require.Empty(t, plainLeadLine(l, 1))
}

func TestPickerAgents(t *testing.T) {
	t.Parallel()
	choices := []workspace.ModelChoice{{Kind: "claude", ID: "opus"}, {Kind: "claude", ID: "haiku"}, {Kind: "codex", ID: "gpt-5"}}
	profiles := []workspace.Profile{{Name: "charles", Kind: "claude", Role: "reviewer", Model: "haiku"}}

	got := pickerAgents(choices, profiles, workspace.Lead{Kind: "codex", Model: "gpt-5", Effort: "low"})
	require.Equal(t, []dialog.AgentChoice{
		{Name: "claude", Kind: "claude"},
		{Name: "codex", Kind: "codex", Current: true, Model: "gpt-5", Effort: "low"},
		{Name: "charles", Kind: "claude", Role: "reviewer", Named: true, Model: "haiku"},
	}, got)

	got = pickerAgents(choices, profiles, workspace.Lead{Kind: "claude", Profile: "Charles", Slug: "claude-haiku-4-5", Effort: "high"})
	require.False(t, got[0].Current, "the lead is charles, not plain claude")
	require.Equal(t, dialog.AgentChoice{
		Name: "charles", Kind: "claude", Role: "reviewer", Named: true, Current: true,
		Model: "haiku", Slug: "claude-haiku-4-5", Effort: "high",
	}, got[2])

	got = pickerAgents(choices, nil, workspace.Lead{Kind: "claude", Profile: "gone", Model: "opus"})
	require.Equal(t, dialog.AgentChoice{Name: "gone", Kind: "claude", Named: true, Current: true, Model: "opus"}, got[len(got)-1],
		"a lead whose profile was removed is still listed")
}

// The room's lead wins; before one exists, the default agent with its
// configured model; a default that is not installed falls to the first kind.
func TestThreadLead(t *testing.T) {
	t.Parallel()
	_, ws := newLeadUI(t, "s1")
	ws.cfg.Models.Set("codex", "gpt-5")
	ws.leads["s1"] = workspace.Lead{Agent: "claude", Kind: "claude", Model: "opus"}
	require.Equal(t, ws.leads["s1"], threadLead(ws, "s1"))
	require.Equal(t, workspace.Lead{Kind: "codex", Model: "gpt-5"}, threadLead(ws, "s2"))
	require.Equal(t, workspace.Lead{Kind: "codex", Model: "gpt-5"}, threadLead(ws, ""))
	ws.cfg.DefaultAgent = "pi"
	require.Equal(t, "claude", threadLead(ws, "").Kind)
	require.Equal(t, workspace.Lead{}, threadLead(struct{}{}, "s1"))
}

// The editor's last row shows the session's lead once it is read, and the
// hint alone before.
func TestEditorShowsTheLead(t *testing.T) {
	t.Parallel()
	u, ws := newLeadUI(t, "s1")
	ws.leads["s1"] = workspace.Lead{Agent: "claude", Kind: "claude", Model: "opus", Effort: "high"}
	u.textarea.SetWidth(60)

	last := func() string {
		lines := strings.Split(ansi.Strip(u.renderEditorView(60)), "\n")
		return lines[len(lines)-1]
	}
	require.Equal(t, " ctrl+l agent", last())

	require.True(t, u.leadStale())
	cmd := u.dispatchLeadRefresh()
	require.NotNil(t, cmd)
	require.Nil(t, u.dispatchLeadRefresh(), "one read at a time")
	require.Nil(t, u.applyLead(cmd().(leadMsg)))
	require.False(t, u.leadStale())
	require.Contains(t, last(), "claude · opus · high  ctrl+l agent")
}

// A pick in a session sets the lead through SetAgentModel, named agents by
// name, and shows at once; a read that raced the pick is dropped.
func TestPickSetsTheLead(t *testing.T) {
	t.Parallel()
	u, ws := newLeadUI(t, "s1")
	stale := u.dispatchLeadRefresh()

	cmd := u.selectAgentModel(dialog.ActionSelectAgentModel{Kind: "claude", Model: "haiku", Agent: "charles"})
	require.Equal(t, workspace.Lead{Kind: "claude", Profile: "charles", Model: "haiku"}, u.shownLead())
	require.Nil(t, u.applyLead(stale().(leadMsg)), "no read while the pick is applied")
	require.Equal(t, "charles", u.shownLead().Profile)

	picked, ok := cmd().(leadPickedMsg)
	require.True(t, ok)
	require.Equal(t, "Talking to charles · haiku", picked.info.Msg)
	require.Equal(t, []string{"s1 charles haiku "}, ws.set)

	u.Update(picked)
	require.True(t, u.leadStale(), "the lead is read again once the pick is in")
}

// With no session yet the pick waits: the editor shows it, and the first
// message starts the session with it before the message goes.
func TestPickBeforeTheFirstMessage(t *testing.T) {
	t.Parallel()
	u, ws := newLeadUI(t, "")
	cmd := u.selectAgentModel(dialog.ActionSelectAgentModel{Kind: "codex", Model: "gpt-5", Effort: "high"})
	require.Equal(t, "codex takes the first message", cmd().(util.InfoMsg).Msg)
	require.Empty(t, ws.set)
	require.Equal(t, workspace.Lead{Kind: "codex", Model: "gpt-5", Effort: "high"}, u.shownLead())

	batch, ok := u.sendMessageInternal("hello", false)().(tea.BatchMsg)
	require.True(t, ok)
	require.Equal(t, 1, ws.created)
	require.Nil(t, u.pendingLead)
	require.Equal(t, "codex", u.shownLead().Kind, "the new session shows the pick")
	// The last command runs the message; the first loads the session.
	batch[len(batch)-1]()
	require.Equal(t, []string{"new codex gpt-5 high"}, ws.set)
	require.Equal(t, []string{"hello"}, ws.runPrompts)
}
