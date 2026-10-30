package dialog

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/require"

	"github.com/dev-dami/deck/internal/ui/common"
	"github.com/dev-dami/deck/internal/ui/styles"
	"github.com/dev-dami/deck/internal/workspace"
)

// newTestAgentPicker opens the Agent picker on claude (the lead, on opus
// at high), codex, and charles, a named claude agent that runs haiku.
func newTestAgentPicker() *AgentModels {
	sty := styles.DeckDark()
	efforts := []workspace.EffortChoice{{Level: "low"}, {Level: "high"}}
	return NewAgentPicker(&common.Common{Styles: &sty}, []AgentChoice{
		{Name: "claude", Kind: "claude", Current: true, Model: "opus", Slug: "claude-opus-5-5", Effort: "high"},
		{Name: "codex", Kind: "codex"},
		{Name: "charles", Kind: "claude", Role: "reviewer", Named: true, Model: "haiku"},
		{Name: "pi", Kind: "pi"},
	}, []workspace.ModelChoice{
		{Kind: "claude", ID: "opus", Name: "Opus", Efforts: efforts, Current: true, CurrentEffort: "high"},
		{Kind: "claude", ID: "haiku", Name: "Haiku"},
		{Kind: "codex", ID: "gpt-5", Name: "gpt-5", Efforts: efforts, DefaultEffort: "low", Current: true},
	})
}

func selectedRow(t *testing.T, d *AgentModels) *agentModelItem {
	t.Helper()
	it, ok := d.list.SelectedItem().(*agentModelItem)
	require.True(t, ok)
	return it
}

func TestAgentPickerStartsOnTheAgents(t *testing.T) {
	t.Parallel()
	d := newTestAgentPicker()
	require.Equal(t, AgentModelsID, d.ID())
	require.Len(t, d.list.FilteredItems(), 4)
	it := selectedRow(t, d)
	require.Equal(t, "claude", it.title, "the current agent is selected")
	require.Equal(t, "current · claude-opus-5-5 · high", it.info, "the slug, not the alias")

	view := agentModelsView(d)
	require.Contains(t, view, "Agent")
	require.Contains(t, view, "charles")
	require.Contains(t, view, "reviewer · claude")
	require.NotContains(t, view, "Choose a model")
}

// Picking a kind lists only its models, the current one selected; then its
// efforts, as the model picker does.
func TestAgentPickerKindThenModelThenEffort(t *testing.T) {
	t.Parallel()
	d := newTestAgentPicker()
	agentModelsKey(d, tea.KeyDown)
	require.Nil(t, agentModelsKey(d, tea.KeyEnter))
	require.NotNil(t, d.agent)
	require.Equal(t, "codex", d.agent.Name)
	require.Len(t, d.list.FilteredItems(), 1, "codex's models only")
	require.Contains(t, agentModelsView(d), "codex · model")
	require.Contains(t, agentModelsView(d), "⌫ back to agents")

	require.Nil(t, agentModelsKey(d, tea.KeyEnter))
	require.Equal(t, "gpt-5", d.picked.ID)
	require.Equal(t, "low", selectedRow(t, d).key, "the model's default effort")
	agentModelsKey(d, tea.KeyDown)
	require.Equal(t, ActionSelectAgentModel{Kind: "codex", Model: "gpt-5", Effort: "high"}, agentModelsKey(d, tea.KeyEnter))
}

// The current agent's model step marks what it runs.
func TestAgentPickerCurrentAgentKeepsItsModel(t *testing.T) {
	t.Parallel()
	d := newTestAgentPicker()
	require.Nil(t, agentModelsKey(d, tea.KeyEnter))
	require.Len(t, d.list.FilteredItems(), 2, "claude's models only")
	require.Equal(t, "Opus", selectedRow(t, d).title)
	require.Equal(t, "current · high", selectedRow(t, d).info)
	require.Nil(t, agentModelsKey(d, tea.KeyEnter))
	require.Equal(t, ActionSelectAgentModel{Kind: "claude", Model: "opus", Effort: "high"}, agentModelsKey(d, tea.KeyEnter))
}

// A named agent runs its kind's models, its own marked, and the pick names
// it.
func TestAgentPickerNamedAgent(t *testing.T) {
	t.Parallel()
	d := newTestAgentPicker()
	agentModelsKey(d, tea.KeyDown)
	agentModelsKey(d, tea.KeyDown)
	require.Nil(t, agentModelsKey(d, tea.KeyEnter))
	require.Equal(t, "charles", d.agent.Name)
	require.Len(t, d.list.FilteredItems(), 2)
	it := selectedRow(t, d)
	require.Equal(t, "Haiku", it.title, "the profile's own model")
	require.Equal(t, "current", it.info)
	require.Equal(t, ActionSelectAgentModel{Kind: "claude", Model: "haiku", Agent: "charles"}, agentModelsKey(d, tea.KeyEnter))
}

// Typing filters the agents; the filter goes when the models show.
func TestAgentPickerFiltersAgents(t *testing.T) {
	t.Parallel()
	d := newTestAgentPicker()
	for _, r := range "revi" {
		d.HandleMsg(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	require.Len(t, d.list.FilteredItems(), 1)
	require.Equal(t, "charles", selectedRow(t, d).title)
	require.Nil(t, agentModelsKey(d, tea.KeyEnter))
	require.Empty(t, d.input.Value())
	require.Len(t, d.list.FilteredItems(), 2)
}

// An agent whose kind listed no models is picked outright.
func TestAgentPickerAgentWithoutModels(t *testing.T) {
	t.Parallel()
	d := newTestAgentPicker()
	agentModelsKey(d, tea.KeyUp)
	require.Equal(t, ActionSelectAgentModel{Kind: "pi"}, agentModelsKey(d, tea.KeyEnter))
}

// esc and backspace step back: effort to models, models to agents (the
// picked one selected), agents out.
func TestAgentPickerStepsBack(t *testing.T) {
	t.Parallel()
	d := newTestAgentPicker()
	agentModelsKey(d, tea.KeyDown)
	agentModelsKey(d, tea.KeyEnter)
	agentModelsKey(d, tea.KeyEnter)
	require.NotNil(t, d.picked)

	require.Nil(t, agentModelsKey(d, tea.KeyEscape))
	require.Nil(t, d.picked)
	require.NotNil(t, d.agent)
	require.Len(t, d.list.FilteredItems(), 1)

	require.Nil(t, agentModelsKey(d, tea.KeyBackspace))
	require.Nil(t, d.agent)
	require.Len(t, d.list.FilteredItems(), 4)
	require.Equal(t, "codex", selectedRow(t, d).title)

	agentModelsKey(d, tea.KeyEnter)
	require.Nil(t, agentModelsKey(d, tea.KeyEscape))
	require.Nil(t, d.agent)
	require.Equal(t, ActionClose{}, agentModelsKey(d, tea.KeyEscape))
}
