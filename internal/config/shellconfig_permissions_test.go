package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/dev-dami/deck/internal/config"
	"github.com/stretchr/testify/require"
)

// loadDeckSh writes a deck.sh into an isolated project and loads it through
// the real config pipeline (discovery -> shell execution -> merge -> typed
// Config). Asserting on the resulting *config.Config is a black-box test of
// what a shell config command actually produces, and it stays valid across
// internal changes to how config is assembled.
func loadDeckSh(t *testing.T, script string) *config.ConfigStore {
	t.Helper()
	store, err := loadDeckShErr(t, script)
	require.NoError(t, err)
	return store
}

// loadDeckShErr is loadDeckSh without asserting success, for cases that are
// expected to fail at load time.
func loadDeckShErr(t *testing.T, script string) (*config.ConfigStore, error) {
	t.Helper()
	// Isolate from the developer's real global config so only the script
	// under test contributes. No t.Parallel(): these tests set env vars.
	isolated := t.TempDir()
	t.Setenv("HOME", isolated)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(isolated, ".config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(isolated, ".local", "share"))
	t.Setenv("DECK_GLOBAL_CONFIG", filepath.Join(isolated, ".config", "deck"))
	t.Setenv("DECK_GLOBAL_DATA", filepath.Join(isolated, ".local", "share", "deck"))

	workDir := t.TempDir()
	dataDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(workDir, "deckrc"), []byte(script), 0o644))

	return config.Load(workDir, dataDir, false)
}

func TestShellConfigPermissionsAllow(t *testing.T) {
	store := loadDeckSh(t, `permissions allow bash view`)

	require.NotNil(t, store.Config().Permissions)
	require.ElementsMatch(t, []string{"bash", "view"}, store.Config().Permissions.AllowedTools)
}

func TestShellConfigPermissionsAccumulateAndDedup(t *testing.T) {
	store := loadDeckSh(t, `permissions allow bash
permissions allow view
permissions allow bash`)

	require.Equal(t, []string{"bash", "view"}, store.Config().Permissions.AllowedTools)
}

func TestShellConfigPermissionsLegacyFlagFails(t *testing.T) {
	_, err := loadDeckShErr(t, `permissions --allow bash`)
	require.Error(t, err)
	require.Contains(t, err.Error(), "unknown subcommand")
}

func TestShellConfigPermissionsAllowRequiresTool(t *testing.T) {
	_, err := loadDeckShErr(t, `permissions allow`)
	require.Error(t, err)
	require.Contains(t, err.Error(), "usage: permissions allow")
}

// deny hides tools from the agent by writing options.disabled_tools.
func TestShellConfigPermissionsDeny(t *testing.T) {
	store := loadDeckSh(t, `permissions deny bash sourcegraph
permissions deny bash`)

	require.Equal(t, []string{"bash", "sourcegraph"}, store.Config().Options.DisabledTools)
}

// When a tool is both allowed and denied, deny wins: the tool lands in
// disabled_tools which removes it from the agent entirely, regardless of
// its presence in the allow-list.
func TestShellConfigPermissionsDenyWinsOverAllow(t *testing.T) {
	store := loadDeckSh(t, `permissions allow bash view
permissions deny bash`)

	require.ElementsMatch(t, []string{"bash", "view"}, store.Config().Permissions.AllowedTools)
	require.Equal(t, []string{"bash"}, store.Config().Options.DisabledTools)

	// SetupAgents resolves the effective tool set; denied tools are excluded.
	cfg := store.Config()
	cfg.SetupAgents()
	require.NotContains(t, cfg.Agents[config.AgentCoder].AllowedTools, "bash")
	require.Contains(t, cfg.Agents[config.AgentCoder].AllowedTools, "view")
}
