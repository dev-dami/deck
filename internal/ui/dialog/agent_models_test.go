package dialog

import (
	"image"
	"testing"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/dev-dami/deck/internal/ui/common"
	"github.com/dev-dami/deck/internal/ui/styles"
	"github.com/dev-dami/deck/internal/workspace"
	"github.com/stretchr/testify/require"
)

func newTestAgentModels() *AgentModels {
	sty := styles.DeckDark()
	efforts := []workspace.EffortChoice{{Level: "low"}, {Level: "high"}}
	return NewAgentModels(&common.Common{Styles: &sty}, []workspace.ModelChoice{
		{Kind: "claude", ID: "opus", Name: "Opus", Efforts: efforts, Current: true, CurrentEffort: "high"},
		{Kind: "claude", ID: "haiku", Name: "Haiku"},
		{Kind: "codex", ID: "gpt-5", Name: "gpt-5", Efforts: efforts, DefaultEffort: "low"},
	})
}

func agentModelsKey(d *AgentModels, code rune) Action {
	return d.HandleMsg(tea.KeyPressMsg{Code: code})
}

// agentModelsView draws the dialog and returns its plain text.
func agentModelsView(d *AgentModels) string {
	scr := uv.NewScreenBuffer(100, 30)
	d.Draw(scr, image.Rect(0, 0, 100, 30))
	return ansi.Strip(scr.Render())
}

func TestAgentModelsStartsOnTheCurrentModel(t *testing.T) {
	t.Parallel()
	d := newTestAgentModels()
	require.Equal(t, AgentModelsID, d.ID())
	require.Len(t, d.list.FilteredItems(), 3)
	it, ok := d.list.SelectedItem().(*agentModelItem)
	require.True(t, ok)
	require.Equal(t, "Opus", it.title)
	require.Contains(t, agentModelsView(d), "Choose a model")
}

func TestAgentModelsEffortStage(t *testing.T) {
	t.Parallel()
	d := newTestAgentModels()

	// enter on a model with efforts moves to the effort step
	require.Nil(t, agentModelsKey(d, tea.KeyEnter))
	require.NotNil(t, d.picked)
	require.Equal(t, "opus", d.picked.ID)
	require.Len(t, d.list.FilteredItems(), 3, "default plus two efforts")
	require.Contains(t, agentModelsView(d), "Opus · effort")

	// the current effort is selected
	it, ok := d.list.SelectedItem().(*agentModelItem)
	require.True(t, ok)
	require.Equal(t, "high", it.key)

	require.Equal(t, ActionSelectAgentModel{Kind: "claude", Model: "opus", Effort: "high"}, agentModelsKey(d, tea.KeyEnter))
}

func TestAgentModelsPickAnotherEffort(t *testing.T) {
	t.Parallel()
	d := newTestAgentModels()
	agentModelsKey(d, tea.KeyEnter)
	agentModelsKey(d, tea.KeyUp)
	require.Equal(t, ActionSelectAgentModel{Kind: "claude", Model: "opus", Effort: "low"}, agentModelsKey(d, tea.KeyEnter))

	d = newTestAgentModels()
	agentModelsKey(d, tea.KeyEnter)
	agentModelsKey(d, tea.KeyUp)
	agentModelsKey(d, tea.KeyUp)
	require.Equal(t, ActionSelectAgentModel{Kind: "claude", Model: "opus", Effort: ""}, agentModelsKey(d, tea.KeyEnter),
		"the default row leaves the effort to the model")
}

func TestAgentModelsDefaultEffortIsSelected(t *testing.T) {
	t.Parallel()
	d := newTestAgentModels()
	agentModelsKey(d, tea.KeyDown)
	agentModelsKey(d, tea.KeyDown)
	require.Nil(t, agentModelsKey(d, tea.KeyEnter))
	require.Equal(t, "gpt-5", d.picked.ID)
	it, ok := d.list.SelectedItem().(*agentModelItem)
	require.True(t, ok)
	require.Equal(t, "low", it.key)
}

func TestAgentModelsModelWithoutEfforts(t *testing.T) {
	t.Parallel()
	d := newTestAgentModels()
	agentModelsKey(d, tea.KeyDown)
	require.Equal(t, ActionSelectAgentModel{Kind: "claude", Model: "haiku"}, agentModelsKey(d, tea.KeyEnter))
	require.Nil(t, d.picked)
}

func TestAgentModelsEscGoesBack(t *testing.T) {
	t.Parallel()
	d := newTestAgentModels()
	agentModelsKey(d, tea.KeyEnter)
	require.NotNil(t, d.picked)

	require.Nil(t, agentModelsKey(d, tea.KeyEscape))
	require.Nil(t, d.picked)
	require.Len(t, d.list.FilteredItems(), 3)
	require.Contains(t, agentModelsView(d), "Choose a model")

	require.Equal(t, ActionClose{}, agentModelsKey(d, tea.KeyEscape))
}

func TestAgentModelsBackspaceGoesBack(t *testing.T) {
	t.Parallel()
	d := newTestAgentModels()
	agentModelsKey(d, tea.KeyEnter)
	require.NotNil(t, d.picked)
	require.Nil(t, agentModelsKey(d, tea.KeyBackspace))
	require.Nil(t, d.picked)
}
