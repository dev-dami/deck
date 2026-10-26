package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// stubIdentity replaces the git and OS user lookups for one test.
func stubIdentity(t *testing.T, git, osName string) {
	t.Helper()
	oldGit, oldOS := gitUserName, osUserName
	gitUserName = func() string { return git }
	osUserName = func() string { return osName }
	t.Cleanup(func() { gitUserName, osUserName = oldGit, oldOS })
}

// isolateHome points the home directory at a temp dir on both Windows and POSIX.
func isolateHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv(EnvReducedMotion, "")
	return home
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestDefaultsWhenFileMissing(t *testing.T) {
	isolateHome(t)
	stubIdentity(t, "Ada Lovelace", "hp")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load with no file: %v", err)
	}
	want := Config{
		UserName:          "Ada Lovelace",
		DefaultAgent:      "claude",
		Motion:            "on",
		Bell:              true,
		TellAgentsName:    false,
		ClaudeBinary:      "claude",
		CodexBinary:       "codex",
		OpencodeBinary:    "opencode",
		AntigravityBinary: "agy",
		DefaultMode:       "ask",
		TrackChanges:      true,
		DeckAttribution:   true,
		DeckAuthor:        true,
		AutoTitle:         true,
	}
	if !reflect.DeepEqual(cfg, want) {
		t.Errorf("Load() = %+v\nwant %+v", cfg, want)
	}
}

func TestUserNameFallsBackToOSUser(t *testing.T) {
	isolateHome(t)
	stubIdentity(t, "", "hp")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.UserName != "hp" {
		t.Errorf("UserName = %q, want OS fallback hp", cfg.UserName)
	}
}

func TestPartialFileKeepsDefaults(t *testing.T) {
	home := isolateHome(t)
	stubIdentity(t, "Grace", "hp")
	writeFile(t, filepath.Join(home, ".deck", "config.json"), `{"bell": false, "models": {"codex": "gpt-x"}}`)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Bell {
		t.Error("bell should be false from file")
	}
	if cfg.DefaultAgent != "claude" || cfg.Motion != "on" || cfg.UserName != "Grace" {
		t.Errorf("defaults lost: %+v", cfg)
	}
	if cfg.Models.Codex != "gpt-x" || cfg.Models.Claude != "" {
		t.Errorf("models = %+v", cfg.Models)
	}
	if cfg.CodexBinary != "codex" {
		t.Errorf("CodexBinary = %q, want default", cfg.CodexBinary)
	}
}

func TestReducedMotionEnvForcesOff(t *testing.T) {
	isolateHome(t)
	stubIdentity(t, "x", "x")
	t.Setenv(EnvReducedMotion, "1")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Motion != "off" {
		t.Errorf("Motion = %q with %s=1, want off", cfg.Motion, EnvReducedMotion)
	}

	t.Setenv(EnvReducedMotion, "0")
	cfg, _ = Load()
	if cfg.Motion != "on" {
		t.Errorf("Motion = %q with %s=0, want on", cfg.Motion, EnvReducedMotion)
	}
}

func TestReducedMotionOverridesFileMotionOn(t *testing.T) {
	home := isolateHome(t)
	stubIdentity(t, "x", "x")
	writeFile(t, filepath.Join(home, ".deck", "config.json"), `{"motion":"on"}`)
	t.Setenv(EnvReducedMotion, "1")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Motion != "off" {
		t.Errorf("Motion = %q, env must win over file", cfg.Motion)
	}
}

func TestInvalidValuesRejected(t *testing.T) {
	home := isolateHome(t)
	stubIdentity(t, "x", "x")
	path := filepath.Join(home, ".deck", "config.json")

	cases := map[string]string{
		"bad agent":           `{"default_agent":"gemini"}`,
		"bad motion":          `{"motion":"loud"}`,
		"bad mode":            `{"default_mode":"reckless"}`,
		"bad json":            `{"bell": `,
		"bad briefing_pct":    `{"context":{"briefing_pct":40}}`,
		"bad tool_output_pct": `{"context":{"tool_output_pct":-1}}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			writeFile(t, path, body)
			if _, err := Load(); err == nil {
				t.Fatalf("Load(%s) succeeded, want error", body)
			}
		})
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	isolateHome(t)
	stubIdentity(t, "x", "x")

	want := Config{
		UserName:       "Ada",
		DefaultAgent:   "codex",
		Motion:         "off",
		Bell:           false,
		TellAgentsName: true,
		ClaudeBinary:   `C:\tools\claude.exe`,
		CodexBinary:    "codex",
		Models:         Models{Claude: "opus", Codex: "gpt-5"},
	}
	if err := Save(want); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("round trip:\n got %+v\nwant %+v", got, want)
	}

	path, _ := Path()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{`"user_name"`, `"default_agent"`, `"tell_agents_name"`, `"claude_binary"`, `"models"`} {
		if !strings.Contains(string(raw), key) {
			t.Errorf("saved file lacks key %s", key)
		}
	}
}

func TestSaveRejectsInvalidAndWritesNothing(t *testing.T) {
	home := isolateHome(t)
	stubIdentity(t, "x", "x")
	bad := Defaults()
	bad.Motion = "sometimes"
	if err := Save(bad); err == nil {
		t.Fatal("Save accepted invalid motion")
	}
	if _, err := os.Stat(filepath.Join(home, ".deck", "config.json")); !os.IsNotExist(err) {
		t.Errorf("config file written despite validation error: %v", err)
	}
}

func TestSaveToCreatesDirectory(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "deck", "config.json")
	stubIdentity(t, "x", "x")
	if err := SaveTo(path, Defaults()); err != nil {
		t.Fatalf("SaveTo: %v", err)
	}
	if _, err := LoadFrom(path); err != nil {
		t.Fatalf("LoadFrom after SaveTo: %v", err)
	}
}

func TestDefaultModeValidationAndStartMode(t *testing.T) {
	home := isolateHome(t)
	stubIdentity(t, "x", "x")
	path := filepath.Join(home, ".deck", "config.json")

	cases := []struct {
		body    string
		want    string
		ignored bool
	}{
		{`{}`, "ask", false}, // missing key keeps the default
		{`{"default_mode":"ask"}`, "ask", false},
		{`{"default_mode":"plan"}`, "plan", false},
		{`{"default_mode":""}`, "ask", false},
		{`{"default_mode":"yolo"}`, "ask", true}, // never silently yolo
	}
	for _, c := range cases {
		writeFile(t, path, c.body)
		cfg, err := Load()
		if err != nil {
			t.Fatalf("Load(%s): %v", c.body, err)
		}
		got, ignored := cfg.StartMode()
		if got != c.want || ignored != c.ignored {
			t.Errorf("StartMode for %s = %q, %v; want %q, %v", c.body, got, ignored, c.want, c.ignored)
		}
	}

	// yolo is a valid stored value (the user may mean it for a session) ...
	cfg := Defaults()
	cfg.DefaultMode = "yolo"
	if err := cfg.Validate(); err != nil {
		t.Errorf("yolo should validate: %v", err)
	}
	// ... but junk is not.
	cfg.DefaultMode = "turbo"
	if err := cfg.Validate(); err == nil {
		t.Error("default_mode turbo validated")
	}
	if err := SaveTo(path, cfg); err == nil {
		t.Error("SaveTo accepted an invalid default_mode")
	}
}

func TestValidateReview(t *testing.T) {
	cfg := Defaults()
	for _, v := range []string{"", "none", "other", "codex"} {
		if cfg.Review = v; cfg.Validate() != nil {
			t.Errorf("review %q should validate", v)
		}
	}
	if cfg.Review = "gpt"; cfg.Validate() == nil {
		t.Error("review gpt validated")
	}
}

func TestValidateContext(t *testing.T) {
	cfg := Defaults()
	cfg.Context = Context{BriefingPct: 6, ToolOutputPct: 2.5}
	if err := cfg.Validate(); err != nil {
		t.Errorf("valid context rejected: %v", err)
	}
	bad := -1
	for _, c := range []Context{
		{BriefingPct: MaxBriefingPct + 1}, {ToolOutputPct: -0.5},
		{InstructionFiles: "some"}, {MCP: "all"}, {CompactAtPct: 101},
		{PerAgent: map[string]ContextOverride{"gemini": {}}},
		{PerAgent: map[string]ContextOverride{"codex": {MCP: "everything"}}},
		{PerAgent: map[string]ContextOverride{"codex": {CompactAtPct: &bad}}},
	} {
		if cfg.Context = c; cfg.Validate() == nil {
			t.Errorf("context %+v validated", c)
		}
	}
	// The zero value is the defaults and is not written out.
	cfg.Context = Context{}
	b, _ := json.Marshal(cfg)
	if strings.Contains(string(b), `"context"`) {
		t.Errorf("an unset context section was written: %s", b)
	}
}

func TestContextFor(t *testing.T) {
	off, at := false, 60
	c := Context{IsolateGlobalConfig: true, InstructionFiles: "project-only", CompactAtPct: 80,
		PerAgent: map[string]ContextOverride{"codex": {IsolateGlobalConfig: &off, MCP: "inherit", CompactAtPct: &at}}}
	if got, want := c.For("claude"), (ContextPolicy{IsolateGlobalConfig: true, InstructionFiles: "project-only", CompactAtPct: 80}); got != want {
		t.Errorf("claude = %+v, want %+v", got, want)
	}
	if got, want := c.For("codex"), (ContextPolicy{InstructionFiles: "project-only", MCP: "inherit", CompactAtPct: 60}); got != want {
		t.Errorf("codex = %+v, want %+v", got, want)
	}
	cfg := Defaults()
	cfg.Context = c
	if err := cfg.Validate(); err != nil {
		t.Errorf("valid policy rejected: %v", err)
	}
}
