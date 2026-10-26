package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

	"github.com/dev-dami/deck/internal/deck/profile"
)

// profileRig is a global config folder and a project folder (not a git
// repository, so the lookup stays in it).
func profileRig(t *testing.T) (globalFile, project string) {
	t.Helper()
	g := t.TempDir()
	t.Setenv("DECK_GLOBAL_CONFIG", g)
	return filepath.Join(g, "deck.json"), t.TempDir()
}

func writeJSON(t *testing.T, path, data string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(data), 0o600))
}

func TestLoadProfilesMergesProjectOverGlobal(t *testing.T) {
	global, project := profileRig(t)
	writeJSON(t, global, `{"options": {"debug": true}, "profiles": [
		{"name": "charles", "kind": "claude", "role": "reviewer", "unknown": 1},
		{"name": "ada", "kind": "codex", "model": "gpt-x", "effort": "high"},
		{"name": "all", "kind": "claude"},
		{"name": "grace", "kind": "gemini"}
	]}`)
	writeJSON(t, filepath.Join(project, "deck.json"), `{"profiles": [
		{"name": "Charles", "kind": "codex", "role": "lead"},
		{"name": "linus", "kind": "opencode"}
	]}`)
	writeJSON(t, filepath.Join(project, ".deck", "deck.json"), `{"profiles": [
		{"name": "linus", "kind": "antigravity", "paths": ["kernel/**"]}
	]}`)

	ps, warnings := LoadProfiles(project, nil)
	require.Equal(t, []string{"Charles", "ada", "linus"}, profile.Names(ps))
	require.Equal(t, profile.Profile{Name: "Charles", Kind: "codex", Role: "lead", Scope: profile.ScopeProject}, ps[0])
	require.Equal(t, profile.Profile{Name: "ada", Kind: "codex", Model: "gpt-x", Effort: "high", Scope: profile.ScopeGlobal}, ps[1])
	require.Equal(t, profile.Profile{Name: "linus", Kind: "antigravity", Paths: []string{"kernel/**"}, Scope: profile.ScopeProject}, ps[2])
	require.Len(t, warnings, 2)
	require.Contains(t, warnings[0], `name "all" is reserved`)
	require.Contains(t, warnings[1], `no agent kind "gemini"`)

	// A teammate's name is skipped too.
	ps, warnings = LoadProfiles(project, []string{"ADA"})
	require.Equal(t, []string{"Charles", "linus"}, profile.Names(ps))
	require.Len(t, warnings, 3)
}

func TestLoadProfilesSkipsBrokenFiles(t *testing.T) {
	global, project := profileRig(t)
	writeJSON(t, global, `{"profiles": [{"name": "ada", "kind": "codex"}]}`)
	writeJSON(t, filepath.Join(project, "deck.json"), `{"profiles": [`)
	writeJSON(t, filepath.Join(project, ".deck.json"), `{"profiles": {"name": "x"}}`)

	ps, warnings := LoadProfiles(project, nil)
	require.Equal(t, []string{"ada"}, profile.Names(ps))
	require.Len(t, warnings, 2)
	joined := strings.Join(warnings, "\n")
	require.Contains(t, joined, "invalid JSON")
	require.Contains(t, joined, "not a list")

	// Nothing anywhere is no profiles and no warnings.
	_, project = profileRig(t)
	ps, warnings = LoadProfiles(project, nil)
	require.Empty(t, ps)
	require.Empty(t, warnings)
}

func TestSaveAndDeleteProfiles(t *testing.T) {
	global, project := profileRig(t)
	writeJSON(t, global, `{"options": {"debug": true}}`)
	writeJSON(t, filepath.Join(project, "deck.json"), `{"profiles": [{"name": "charles", "kind": "claude"}]}`)

	// A new project profile goes to the workspace's .deck/deck.json.
	grace := profile.Profile{Name: "grace", Kind: "codex", Role: "tests", Scope: profile.ScopeProject}
	require.NoError(t, SaveProfile(project, grace, nil))
	data, err := os.ReadFile(filepath.Join(project, ".deck", "deck.json"))
	require.NoError(t, err)
	require.Equal(t, "grace", gjson.GetBytes(data, "profiles.0.name").String())
	require.False(t, gjson.GetBytes(data, "profiles.0.scope").Exists(), "the scope is the file, not a field")

	// One the project already has is changed where it is.
	require.NoError(t, SaveProfile(project, profile.Profile{Name: "Charles", Kind: "claude", Role: "reviewer", Scope: profile.ScopeProject}, nil))
	data, err = os.ReadFile(filepath.Join(project, "deck.json"))
	require.NoError(t, err)
	require.Equal(t, int64(1), gjson.GetBytes(data, "profiles.#").Int())
	require.Equal(t, "reviewer", gjson.GetBytes(data, "profiles.0.role").String())

	// A global one keeps the file's other settings.
	require.NoError(t, SaveProfile(project, profile.Profile{Name: "ada", Kind: "codex", Scope: profile.ScopeGlobal}, nil))
	data, err = os.ReadFile(global)
	require.NoError(t, err)
	require.True(t, gjson.GetBytes(data, "options.debug").Bool())
	require.Equal(t, "ada", gjson.GetBytes(data, "profiles.0.name").String())

	ps, warnings := LoadProfiles(project, nil)
	require.Empty(t, warnings)
	require.Equal(t, []string{"ada", "Charles", "grace"}, profile.Names(ps))

	// Invalid profiles and a missing scope are refused, and nothing is written.
	require.Error(t, SaveProfile(project, profile.Profile{Name: "deck", Kind: "claude", Scope: profile.ScopeGlobal}, nil))
	require.Error(t, SaveProfile(project, profile.Profile{Name: "bob", Kind: "claude"}, nil))
	require.ErrorContains(t, SaveProfile(project, profile.Profile{Name: "alice", Kind: "claude", Scope: profile.ScopeGlobal}, []string{"alice"}), "teammate")
	ps, _ = LoadProfiles(project, nil)
	require.Len(t, ps, 3)

	// Deleting takes it out of its scope only.
	require.NoError(t, DeleteProfile(project, "GRACE", profile.ScopeProject))
	require.True(t, errors.Is(DeleteProfile(project, "grace", profile.ScopeProject), profile.ErrNotFound))
	require.True(t, errors.Is(DeleteProfile(project, "charles", profile.ScopeGlobal), profile.ErrNotFound))
	require.Error(t, DeleteProfile(project, "ada", "team"))
	require.NoError(t, DeleteProfile(project, "ada", profile.ScopeGlobal))
	ps, _ = LoadProfiles(project, nil)
	require.Equal(t, []string{"Charles"}, profile.Names(ps))
	data, err = os.ReadFile(global)
	require.NoError(t, err)
	require.True(t, gjson.GetBytes(data, "options.debug").Bool())
	require.Equal(t, int64(0), gjson.GetBytes(data, "profiles.#").Int())
}
