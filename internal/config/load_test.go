package config

import (
	"context"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/dev-dami/deck/internal/csync"
	"github.com/dev-dami/deck/internal/oauth"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMain(m *testing.M) {
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
	os.Exit(runIsolated(m))
}

// runIsolated runs the tests with the global config and data paths in a
// temp dir, so no test can rewrite the developer's real config.
func runIsolated(m *testing.M) int {
	home, err := os.MkdirTemp("", "deck-config-test")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(home)
	os.Setenv("DECK_GLOBAL_CONFIG", filepath.Join(home, "config"))
	os.Setenv("DECK_GLOBAL_DATA", filepath.Join(home, "data"))
	return m.Run()
}

func TestConfig_LoadFromBytes(t *testing.T) {
	data1 := []byte(`{"providers": {"openai": {"api_key": "key1", "base_url": "https://api.openai.com/v1"}}}`)
	data2 := []byte(`{"providers": {"openai": {"api_key": "key2", "base_url": "https://api.openai.com/v2"}}}`)
	data3 := []byte(`{"providers": {"openai": {}}}`)

	loadedConfig, err := loadFromBytes([][]byte{data1, data2, data3})

	require.NoError(t, err)
	require.NotNil(t, loadedConfig)
	require.Equal(t, 1, loadedConfig.Providers.Len())
	pc, _ := loadedConfig.Providers.Get("openai")
	require.Equal(t, "key2", pc.APIKey)
	require.Equal(t, "https://api.openai.com/v2", pc.BaseURL)
}

func TestLookupConfigs_BoundedByProject(t *testing.T) {
	// Force GlobalConfig and GlobalConfigData to point at locations we
	// control so they can be present in the result without polluting
	// the developer's real config.
	globalDir := t.TempDir()
	dataDir := t.TempDir()
	t.Setenv("DECK_GLOBAL_CONFIG", globalDir)
	t.Setenv("DECK_GLOBAL_DATA", dataDir)

	t.Run("does not pick up deck.json above non-git project", func(t *testing.T) {
		parent := t.TempDir()

		// deck.json above the project must not be adopted.
		require.NoError(t, os.WriteFile(
			filepath.Join(parent, "deck.json"),
			[]byte(`{}`),
			0o644,
		))

		project := filepath.Join(parent, "project")
		require.NoError(t, os.Mkdir(project, 0o755))

		got := lookupConfigs(project)
		for _, p := range got {
			require.NotEqual(t, filepath.Join(parent, "deck.json"), p)
		}
	})

	t.Run("does not climb out of git worktree to find deck.json", func(t *testing.T) {
		if _, err := exec.LookPath("git"); err != nil {
			t.Skip("git not available")
		}

		parent := t.TempDir()

		require.NoError(t, os.WriteFile(
			filepath.Join(parent, "deck.json"),
			[]byte(`{}`),
			0o644,
		))

		worktree := filepath.Join(parent, "worktree")
		require.NoError(t, os.Mkdir(worktree, 0o755))
		gitInit := exec.CommandContext(t.Context(), "git", "init", "-q")
		gitInit.Dir = worktree
		require.NoError(t, gitInit.Run())

		got := lookupConfigs(worktree)
		strayEval, err := filepath.EvalSymlinks(filepath.Join(parent, "deck.json"))
		require.NoError(t, err)
		for _, p := range got {
			pEval, err := filepath.EvalSymlinks(p)
			if err != nil {
				continue
			}
			require.NotEqual(t, strayEval, pEval, "must not adopt parent deck.json")
		}
	})

	t.Run("picks up deck.json inside the project", func(t *testing.T) {
		project := t.TempDir()
		local := filepath.Join(project, "deck.json")
		require.NoError(t, os.WriteFile(local, []byte(`{}`), 0o644))

		got := lookupConfigs(project)

		localEval, err := filepath.EvalSymlinks(local)
		require.NoError(t, err)
		var foundLocal bool
		for _, p := range got {
			pEval, err := filepath.EvalSymlinks(p)
			if err != nil {
				continue
			}
			if pEval == localEval {
				foundLocal = true
				break
			}
		}
		require.True(t, foundLocal, "expected project deck.json to be in lookup result: %v", got)
	})

	t.Run("global config is always included regardless of boundary", func(t *testing.T) {
		project := t.TempDir()

		got := lookupConfigs(project)
		// Global config and global data path are always prepended,
		// even when no project file exists.
		require.Contains(t, got, GlobalConfig())
		require.Contains(t, got, GlobalConfigData())
	})

	t.Run("global shell config (deckrc) is included", func(t *testing.T) {
		project := t.TempDir()

		got := lookupConfigs(project)
		// A global deckrc is discovered only beside the user config. The data
		// directory is machine-owned state and must never execute a deckrc.
		require.Contains(t, got, shellConfigSibling(GlobalConfig()))
		require.NotContains(t, got, shellConfigSibling(GlobalConfigData()))
	})

	t.Run("project deckrc and .deckrc are discovered", func(t *testing.T) {
		project := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(project, "deckrc"), []byte(""), 0o644))
		require.NoError(t, os.WriteFile(filepath.Join(project, ".deckrc"), []byte(""), 0o644))

		got := lookupConfigs(project)
		require.Contains(t, got, filepath.Join(project, "deckrc"))
		require.Contains(t, got, filepath.Join(project, ".deckrc"))
	})

	t.Run("system config is loaded first", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("system config not supported on Windows")
		}

		got := lookupConfigs(t.TempDir())
		require.NotEmpty(t, got)
		// The system-wide config must be first so it has the lowest
		// priority when configs are merged.
		require.Equal(t, "/etc/deck/deck.json", got[0])
	})
}

func TestLoadFromConfigPaths_InvalidJSON(t *testing.T) {
	t.Parallel()

	t.Run("identifies the offending file", func(t *testing.T) {
		t.Parallel()
		tmpDir := t.TempDir()
		good := filepath.Join(tmpDir, "good.json")
		bad := filepath.Join(tmpDir, "bad.json")
		require.NoError(t, os.WriteFile(good, []byte(`{"providers":{}}`), 0o644))
		require.NoError(t, os.WriteFile(bad, []byte(`{not valid json}`), 0o644))

		_, _, err := loadFromConfigPaths(context.Background(), []string{good, bad})
		require.Error(t, err)
		require.Contains(t, err.Error(), "invalid JSON in config file")
		require.Contains(t, err.Error(), "bad.json")
	})

	t.Run("skips missing and empty files", func(t *testing.T) {
		t.Parallel()
		tmpDir := t.TempDir()
		empty := filepath.Join(tmpDir, "empty.json")
		require.NoError(t, os.WriteFile(empty, []byte(""), 0o644))

		cfg, _, err := loadFromConfigPaths(context.Background(), []string{
			filepath.Join(tmpDir, "nonexistent.json"),
			empty,
		})
		require.NoError(t, err)
		require.NotNil(t, cfg)
	})
}

// TestLoadFromConfigPaths_ConflictWarningNamesKeys verifies that when a JSON
// config and a deckrc coexist in the same directory, the merge warning names
// the overlapping top-level keys so incremental migrations can spot stale
// duplicates.
func TestLoadFromConfigPaths_ConflictWarningNamesKeys(t *testing.T) {
	capture := func(t *testing.T) *strings.Builder {
		t.Helper()
		var buf strings.Builder
		prev := slog.Default()
		slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})))
		t.Cleanup(func() { slog.SetDefault(prev) })
		return &buf
	}

	t.Run("names overlapping keys", func(t *testing.T) {
		buf := capture(t)
		tmpDir := t.TempDir()
		jsonPath := filepath.Join(tmpDir, "deck.json")
		rcPath := filepath.Join(tmpDir, "deckrc")
		require.NoError(t, os.WriteFile(jsonPath, []byte(`{"options":{"debug":true},"providers":{}}`), 0o644))
		require.NoError(t, os.WriteFile(rcPath, []byte("option debug true\n"), 0o644))

		_, _, err := loadFromConfigPaths(context.Background(), []string{jsonPath, rcPath})
		require.NoError(t, err)
		require.Contains(t, buf.String(), "deckrc taking precedence")
		require.Contains(t, buf.String(), `"conflicting_keys":"options"`)
	})

	t.Run("no warning when nothing overlaps", func(t *testing.T) {
		buf := capture(t)
		tmpDir := t.TempDir()
		jsonPath := filepath.Join(tmpDir, "deck.json")
		rcPath := filepath.Join(tmpDir, "deckrc")
		require.NoError(t, os.WriteFile(jsonPath, []byte(`{"providers":{}}`), 0o644))
		require.NoError(t, os.WriteFile(rcPath, []byte("option debug true\n"), 0o644))

		_, _, err := loadFromConfigPaths(context.Background(), []string{jsonPath, rcPath})
		require.NoError(t, err)
		require.NotContains(t, buf.String(), "deckrc taking precedence",
			"disjoint coexistence should not warn")
	})
}

func TestConfig_setDefaults(t *testing.T) {
	t.Run("sets default data directory", func(t *testing.T) {
		cfg := &Config{}
		workingDir := t.TempDir()

		cfg.setDefaults(workingDir, "")

		require.NotNil(t, cfg.Options)
		require.NotNil(t, cfg.Options.TUI)
		require.NotNil(t, cfg.Options.ContextPaths)
		require.NotNil(t, cfg.Providers)
		require.NotNil(t, cfg.Models)
		require.NotNil(t, cfg.LSP)
		require.NotNil(t, cfg.MCP)
		require.Equal(t, filepath.Join(workingDir, ".deck"), cfg.Options.DataDirectory)
		require.Equal(t, "AGENTS.md", cfg.Options.InitializeAs)
		// DiffMode is deliberately left empty: the permissions dialog treats
		// the zero value as "pick split or unified based on terminal width".
		require.Empty(t, cfg.Options.TUI.DiffMode)
		require.Equal(t, ScrollbarDefault, cfg.Options.TUI.Scrollbar)
		require.Equal(t, ExitBannerDefault, cfg.Options.TUI.ExitBanner)
		for _, path := range defaultContextPaths {
			require.Contains(t, cfg.Options.ContextPaths, path)
		}
	})

	t.Run("sets TUI defaults only when unset", func(t *testing.T) {
		cfg := &Config{}
		workingDir := t.TempDir()

		cfg.setDefaults(workingDir, "")

		require.Empty(t, cfg.Options.TUI.DiffMode)
		require.Equal(t, ScrollbarDefault, cfg.Options.TUI.Scrollbar)
		require.Equal(t, ExitBannerDefault, cfg.Options.TUI.ExitBanner)

		cfg.Options.TUI.DiffMode = DiffModeSplit
		cfg.Options.TUI.Scrollbar = ScrollbarNever
		cfg.Options.TUI.ExitBanner = ExitBannerCompact
		cfg.setDefaults(workingDir, "")

		require.Equal(t, DiffModeSplit, cfg.Options.TUI.DiffMode)
		require.Equal(t, ScrollbarNever, cfg.Options.TUI.Scrollbar)
		require.Equal(t, ExitBannerCompact, cfg.Options.TUI.ExitBanner)
	})

	t.Run("prunes orphaned OAuth token MCP entries but keeps real ones", func(t *testing.T) {
		cfg := &Config{
			MCP: map[string]MCPConfig{
				"orphan":     {OAuthToken: &oauth.Token{AccessToken: "stale"}},
				"real-http":  {Type: MCPHttp, URL: "https://example.com/mcp", OAuthToken: &oauth.Token{AccessToken: "live"}},
				"real-stdio": {Type: MCPStdio, Command: "npx"},
				"malformed":  {Command: "npx"}, // missing type but has a command: surface the error, don't prune
			},
		}

		cfg.setDefaults(t.TempDir(), "")

		require.NotContains(t, cfg.MCP, "orphan", "orphaned token entry should be pruned")
		require.Contains(t, cfg.MCP, "real-http")
		require.Contains(t, cfg.MCP, "real-stdio")
		require.Contains(t, cfg.MCP, "malformed", "malformed entry should survive so its error surfaces")
	})

	t.Run("resolves relative configured data directory from working directory", func(t *testing.T) {
		cfg := &Config{Options: &Options{DataDirectory: "."}}
		workingDir := filepath.Join(t.TempDir(), "worktree")

		cfg.setDefaults(workingDir, "")

		require.Equal(t, workingDir, cfg.Options.DataDirectory)
	})

	t.Run("resolves relative flag data directory from working directory", func(t *testing.T) {
		cfg := &Config{}
		workingDir := filepath.Join(t.TempDir(), "worktree")

		cfg.setDefaults(workingDir, "./state")

		require.Equal(t, filepath.Join(workingDir, "state"), cfg.Options.DataDirectory)
	})

	t.Run("preserves absolute configured data directory", func(t *testing.T) {
		// Use a platform-appropriate absolute path so the test runs
		// the same way on POSIX and Windows.
		absDir := filepath.Join(t.TempDir(), "data")
		cfg := &Config{Options: &Options{DataDirectory: absDir}}

		cfg.setDefaults(filepath.Join(t.TempDir(), "worktree"), "")

		require.Equal(t, absDir, cfg.Options.DataDirectory)
	})

	t.Run("workspace merge re-entry keeps an absolute data directory", func(t *testing.T) {
		// Simulate the load and reload paths: defaults are applied
		// twice with the data directory potentially carried through
		// from an earlier merge as a relative string.
		workingDir := filepath.Join(t.TempDir(), "worktree")
		cfg := &Config{}
		cfg.setDefaults(workingDir, "")

		// Workspace JSON sets data_directory to a relative value; the
		// merge replaces the struct, then setDefaults runs again.
		cfg.Options.DataDirectory = "./state"
		cfg.setDefaults(workingDir, "")

		require.True(t, filepath.IsAbs(cfg.Options.DataDirectory),
			"data directory must remain absolute after re-merge, got %q",
			cfg.Options.DataDirectory)
		require.Equal(t, filepath.Join(workingDir, "state"), cfg.Options.DataDirectory)
	})

	t.Run("does not adopt .deck from a parent project", func(t *testing.T) {
		parent := t.TempDir()

		// .deck in the parent: it should not be reused by the child
		// because there is no git context joining them.
		require.NoError(t, os.Mkdir(filepath.Join(parent, defaultDataDirectory), 0o755))

		child := filepath.Join(parent, "child")
		require.NoError(t, os.Mkdir(child, 0o755))

		cfg := &Config{}
		cfg.setDefaults(child, "")

		require.Equal(
			t,
			filepath.Clean(filepath.Join(child, defaultDataDirectory)),
			filepath.Clean(cfg.Options.DataDirectory),
		)
	})

	t.Run("does not climb out of git worktree to find .deck", func(t *testing.T) {
		if _, err := exec.LookPath("git"); err != nil {
			t.Skip("git not available")
		}

		parent := t.TempDir()

		// Stray .deck above the worktree root.
		require.NoError(t, os.Mkdir(filepath.Join(parent, defaultDataDirectory), 0o755))

		worktree := filepath.Join(parent, "worktree")
		require.NoError(t, os.Mkdir(worktree, 0o755))

		sub := filepath.Join(worktree, "pkg")
		require.NoError(t, os.Mkdir(sub, 0o755))

		// Make worktree a real git repo so the boundary detection
		// resolves to it, mirroring what happens with linked worktrees
		// in real usage.
		gitInit := exec.CommandContext(t.Context(), "git", "init", "-q")
		gitInit.Dir = worktree
		require.NoError(t, gitInit.Run())

		cfg := &Config{}
		cfg.setDefaults(sub, "")

		// Resolve symlinks because TempDir on macOS sits under /var
		// which is a symlink to /private/var. The data directory has
		// not been created yet, so resolve its parent and join.
		gotDir, gotName := filepath.Split(cfg.Options.DataDirectory)
		gotEvalDir, err := filepath.EvalSymlinks(filepath.Clean(gotDir))
		require.NoError(t, err)
		gotEval := filepath.Join(gotEvalDir, gotName)

		strayEval, err := filepath.EvalSymlinks(filepath.Join(parent, defaultDataDirectory))
		require.NoError(t, err)
		require.NotEqual(t, strayEval, gotEval, "must not adopt parent .deck")

		subEval, err := filepath.EvalSymlinks(sub)
		require.NoError(t, err)
		require.Equal(t, filepath.Join(subEval, defaultDataDirectory), gotEval)
	})
}

func TestConfig_EnabledProviders(t *testing.T) {
	t.Run("all providers enabled", func(t *testing.T) {
		cfg := &Config{
			Providers: csync.NewMapFrom(map[string]ProviderConfig{
				"openai": {
					ID:      "openai",
					APIKey:  "key1",
					Disable: false,
				},
				"anthropic": {
					ID:      "anthropic",
					APIKey:  "key2",
					Disable: false,
				},
			}),
		}

		enabled := cfg.EnabledProviders()
		require.Len(t, enabled, 2)
	})

	t.Run("some providers disabled", func(t *testing.T) {
		cfg := &Config{
			Providers: csync.NewMapFrom(map[string]ProviderConfig{
				"openai": {
					ID:      "openai",
					APIKey:  "key1",
					Disable: false,
				},
				"anthropic": {
					ID:      "anthropic",
					APIKey:  "key2",
					Disable: true,
				},
			}),
		}

		enabled := cfg.EnabledProviders()
		require.Len(t, enabled, 1)
		require.Equal(t, "openai", enabled[0].ID)
	})

	t.Run("empty providers map", func(t *testing.T) {
		cfg := &Config{
			Providers: csync.NewMap[string, ProviderConfig](),
		}

		enabled := cfg.EnabledProviders()
		require.Len(t, enabled, 0)
	})
}

func TestConfig_IsConfigured(t *testing.T) {
	t.Run("returns true when at least one provider is enabled", func(t *testing.T) {
		cfg := &Config{
			Providers: csync.NewMapFrom(map[string]ProviderConfig{
				"openai": {
					ID:      "openai",
					APIKey:  "key1",
					Disable: false,
				},
			}),
		}

		require.True(t, cfg.IsConfigured())
	})

	t.Run("returns false when no providers are configured", func(t *testing.T) {
		cfg := &Config{
			Providers: csync.NewMap[string, ProviderConfig](),
		}

		require.False(t, cfg.IsConfigured())
	})

	t.Run("returns false when all providers are disabled", func(t *testing.T) {
		cfg := &Config{
			Providers: csync.NewMapFrom(map[string]ProviderConfig{
				"openai": {
					ID:      "openai",
					APIKey:  "key1",
					Disable: true,
				},
				"anthropic": {
					ID:      "anthropic",
					APIKey:  "key2",
					Disable: true,
				},
			}),
		}

		require.False(t, cfg.IsConfigured())
	})
}

func TestConfig_setupAgentsWithNoDisabledTools(t *testing.T) {
	cfg := &Config{
		Options: &Options{
			DisabledTools: []string{},
		},
	}

	cfg.SetupAgents()
	coderAgent, ok := cfg.Agents[AgentCoder]
	require.True(t, ok)
	assert.Equal(t, allToolNames(), coderAgent.AllowedTools)

	taskAgent, ok := cfg.Agents[AgentTask]
	require.True(t, ok)
	assert.Equal(t, []string{"lsp_symbols", "lsp_definition", "lsp_call_hierarchy", "glob", "grep", "ls", "sourcegraph", "view"}, taskAgent.AllowedTools)

	planAgent, ok := cfg.Agents[AgentPlan]
	require.True(t, ok)
	assert.Equal(t, []string{"agent", "lsp_symbols", "lsp_definition", "lsp_call_hierarchy", "glob", "grep", "ls", "question", "sourcegraph", "view"}, planAgent.AllowedTools)
}

func TestConfig_setupAgentsWithDisabledTools(t *testing.T) {
	cfg := &Config{
		Options: &Options{
			DisabledTools: []string{
				"edit",
				"download",
				"grep",
			},
		},
	}

	cfg.SetupAgents()
	coderAgent, ok := cfg.Agents[AgentCoder]
	require.True(t, ok)

	assert.Equal(t, []string{"agent", "bash", "deck_info", "deck_logs", "job_output", "job_kill", "multiedit", "lsp_diagnostics", "lsp_references", "lsp_restart", "lsp_symbols", "lsp_definition", "lsp_call_hierarchy", "lsp_rename", "lsp_replace_symbol", "fetch", "agentic_fetch", "glob", "ls", "question", "sourcegraph", "todos", "view", "write", "list_mcp_resources", "read_mcp_resource"}, coderAgent.AllowedTools)

	taskAgent, ok := cfg.Agents[AgentTask]
	require.True(t, ok)
	assert.Equal(t, []string{"lsp_symbols", "lsp_definition", "lsp_call_hierarchy", "glob", "ls", "sourcegraph", "view"}, taskAgent.AllowedTools)

	planAgent, ok := cfg.Agents[AgentPlan]
	require.True(t, ok)
	assert.Equal(t, []string{"agent", "lsp_symbols", "lsp_definition", "lsp_call_hierarchy", "glob", "ls", "question", "sourcegraph", "view"}, planAgent.AllowedTools)
}

func TestConfig_setupAgentsWithEveryReadOnlyToolDisabled(t *testing.T) {
	cfg := &Config{
		Options: &Options{
			DisabledTools: []string{
				"agent",
				"glob",
				"grep",
				"ls",
				"lsp_call_hierarchy",
				"lsp_definition",
				"lsp_symbols",
				"sourcegraph",
				"view",
			},
		},
	}

	cfg.SetupAgents()
	coderAgent, ok := cfg.Agents[AgentCoder]
	require.True(t, ok)
	assert.Equal(t, []string{"bash", "deck_info", "deck_logs", "job_output", "job_kill", "download", "edit", "multiedit", "lsp_diagnostics", "lsp_references", "lsp_restart", "lsp_rename", "lsp_replace_symbol", "fetch", "agentic_fetch", "question", "todos", "write", "list_mcp_resources", "read_mcp_resource"}, coderAgent.AllowedTools)

	taskAgent, ok := cfg.Agents[AgentTask]
	require.True(t, ok)
	assert.Len(t, taskAgent.AllowedTools, 0)

	planAgent, ok := cfg.Agents[AgentPlan]
	require.True(t, ok)
	assert.Equal(t, []string{"question"}, planAgent.AllowedTools)
}

func TestConfig_setDefaultsDisableDefaultProvidersEnvVar(t *testing.T) {
	t.Run("sets option from environment variable", func(t *testing.T) {
		t.Setenv("DECK_DISABLE_DEFAULT_PROVIDERS", "true")

		cfg := &Config{}
		cfg.setDefaults("/tmp", "")

		require.True(t, cfg.Options.DisableDefaultProviders)
	})

	t.Run("does not override when env var is not set", func(t *testing.T) {
		cfg := &Config{
			Options: &Options{
				DisableDefaultProviders: true,
			},
		}
		cfg.setDefaults("/tmp", "")

		require.True(t, cfg.Options.DisableDefaultProviders)
	})
}

func TestConfig_LoadFromBytes_Env(t *testing.T) {
	data := []byte(`{"env": {"AWS_PROFILE": "my-profile", "AWS_REGION": "us-west-2"}}`)

	loadedConfig, err := loadFromBytes([][]byte{data})

	require.NoError(t, err)
	require.NotNil(t, loadedConfig.Env)
	require.Equal(t, "my-profile", loadedConfig.Env["AWS_PROFILE"])
	require.Equal(t, "us-west-2", loadedConfig.Env["AWS_REGION"])
}

func TestConfig_LoadFromBytes_EnvMerge(t *testing.T) {
	data1 := []byte(`{"env": {"AWS_PROFILE": "first", "AWS_REGION": "us-east-1"}}`)
	data2 := []byte(`{"env": {"AWS_PROFILE": "second"}}`)

	loadedConfig, err := loadFromBytes([][]byte{data1, data2})

	require.NoError(t, err)
	require.NotNil(t, loadedConfig.Env)
	require.Equal(t, "second", loadedConfig.Env["AWS_PROFILE"])
	require.Equal(t, "us-east-1", loadedConfig.Env["AWS_REGION"])
}
