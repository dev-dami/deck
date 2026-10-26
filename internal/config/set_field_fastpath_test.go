package config_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dev-dami/deck/internal/config"
	"github.com/stretchr/testify/require"
)

// TestSetConfigField_ScalarOptionAppliedInMemory covers the no-reload path:
// the value lands both on disk and in the live config.
func TestSetConfigField_ScalarOptionAppliedInMemory(t *testing.T) {
	workDir, dataDir := isolateReloadEnv(t)
	store, err := config.Load(workDir, dataDir, false)
	require.NoError(t, err)

	require.NoError(t, store.SetConfigField(config.ScopeGlobal, "options.tui.active_theme", "deck"))
	require.NoError(t, store.SetConfigField(config.ScopeGlobal, "options.tui.active_theme", "gruvbox-dark"))
	require.NoError(t, store.SetConfigField(config.ScopeGlobal, "options.tui.mouse", false))
	require.Equal(t, "gruvbox-dark", store.Config().Options.TUI.ActiveTheme)
	require.NotNil(t, store.Config().Options.TUI.Mouse)
	require.False(t, *store.Config().Options.TUI.Mouse)
	require.True(t, store.HasConfigField(config.ScopeGlobal, "options.tui.active_theme"))
	require.False(t, store.ConfigStaleness().Dirty, "our own write must not read as an external edit")
}

// TestSetConfigField_ProjectConfigStillWins checks that a project config
// setting the same key keeps outranking the global write, as a reload would.
func TestSetConfigField_ProjectConfigStillWins(t *testing.T) {
	workDir, dataDir := isolateReloadEnv(t)
	require.NoError(t, os.WriteFile(filepath.Join(workDir, "deck.json"),
		[]byte(`{"options":{"tui":{"active_theme":"project-theme"}}}`), 0o644))
	store, err := config.Load(workDir, dataDir, false)
	require.NoError(t, err)

	require.NoError(t, store.SetConfigField(config.ScopeGlobal, "options.tui.active_theme", "deck"))
	require.NoError(t, store.SetConfigField(config.ScopeGlobal, "options.tui.active_theme", "gruvbox-dark"))
	require.Equal(t, "project-theme", store.Config().Options.TUI.ActiveTheme)
}

// TestSetConfigField_ExternalEditStillReloads checks that a pending edit to
// another config file is still picked up by the write, as before.
func TestSetConfigField_ExternalEditStillReloads(t *testing.T) {
	workDir, dataDir := isolateReloadEnv(t)
	projectCfg := filepath.Join(workDir, "deck.json")
	require.NoError(t, os.WriteFile(projectCfg, []byte(`{"options":{"notifications":"bell"}}`), 0o644))
	store, err := config.Load(workDir, dataDir, false)
	require.NoError(t, err)
	require.NoError(t, store.SetConfigField(config.ScopeGlobal, "options.tui.active_theme", "deck"))

	require.NoError(t, os.WriteFile(projectCfg, []byte(`{"options":{"notifications":"osc"}}`), 0o644))
	// Make sure the edit is visible to the mtime/size staleness check.
	future := time.Now().Add(2 * time.Second)
	require.NoError(t, os.Chtimes(projectCfg, future, future))

	require.NoError(t, store.SetConfigField(config.ScopeGlobal, "options.tui.active_theme", "gruvbox-dark"))
	require.Equal(t, "osc", store.Config().Options.Notifications)
	require.Equal(t, "gruvbox-dark", store.Config().Options.TUI.ActiveTheme)
}

// TestLoad_RecordsFileStateWithoutProviders checks that Load records config
// file state even though no provider is configured (always, for Deck), so
// an edit made right after startup reads as stale.
func TestLoad_RecordsFileStateWithoutProviders(t *testing.T) {
	workDir, dataDir := isolateReloadEnv(t)
	projectCfg := filepath.Join(workDir, "deck.json")
	require.NoError(t, os.WriteFile(projectCfg, []byte(`{"options":{"notifications":"bell"}}`), 0o644))
	store, err := config.Load(workDir, dataDir, false)
	require.NoError(t, err)
	require.False(t, store.Config().IsConfigured(), "test assumes the no-provider path")
	require.False(t, store.ConfigStaleness().Dirty, "nothing changed since Load")

	require.NoError(t, os.WriteFile(projectCfg, []byte(`{"options":{"notifications":"osc"}}`), 0o644))
	future := time.Now().Add(2 * time.Second)
	require.NoError(t, os.Chtimes(projectCfg, future, future))

	got := store.ConfigStaleness()
	require.True(t, got.Dirty, "an edit after Load must read as stale")
	abs, err := filepath.Abs(projectCfg)
	require.NoError(t, err)
	require.Contains(t, got.Changed, abs)
}
