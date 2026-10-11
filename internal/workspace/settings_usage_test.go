package workspace

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	deckconfig "github.com/dev-dami/deck/internal/deck/config"
)

// stubSaveDeckConfig records saves instead of writing ~/.deck. Tests using
// it must not run in parallel: saveDeckConfig is package state.
func stubSaveDeckConfig(t *testing.T) *[]deckconfig.Config {
	t.Helper()
	var saved []deckconfig.Config
	orig := saveDeckConfig
	saveDeckConfig = func(c deckconfig.Config) error {
		saved = append(saved, c)
		return nil
	}
	t.Cleanup(func() { saveDeckConfig = orig })
	return &saved
}

func TestSetDeckSettingsAppliesAndSaves(t *testing.T) {
	saved := stubSaveDeckConfig(t)
	w := newPickerWorkspace(t, pickerCfg())

	require.NoError(t, w.SetDeckSettings(func(c *deckconfig.Config) {
		c.Bell = true
		c.DefaultAgent = "codex"
	}))
	got := w.DeckSettings()
	require.True(t, got.Bell)
	require.Equal(t, "codex", got.DefaultAgent)
	require.Equal(t, "gpt-5", got.Models.For("codex"), "what the edit leaves alone is kept")
	require.Len(t, *saved, 1)
	require.Equal(t, got, (*saved)[0])
}

func TestSetDeckSettingsRefusesInvalid(t *testing.T) {
	saved := stubSaveDeckConfig(t)
	w := newPickerWorkspace(t, pickerCfg())

	err := w.SetDeckSettings(func(c *deckconfig.Config) { c.Motion = "sideways" })
	require.Error(t, err)
	require.Equal(t, "off", w.DeckSettings().Motion, "an invalid change is not applied")
	require.Empty(t, *saved, "nor saved")
}

func TestAgentKindsAreTheInstalledCLIs(t *testing.T) {
	w := newPickerWorkspace(t, pickerCfg())
	require.Equal(t, []string{"claude", "codex"}, w.AgentKinds())
}

func TestUsageWithoutAppIsEmpty(t *testing.T) {
	w := newPickerWorkspace(t, pickerCfg())
	rep, err := w.Usage(context.Background(), 7)
	require.NoError(t, err)
	require.True(t, rep.Empty())
}
