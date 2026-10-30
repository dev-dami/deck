package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The levels go from least to most: every limit grows or stays, and only
// the levels that schedule run the board.
func TestLevelsGrow(t *testing.T) {
	var prev Level
	for i, name := range LevelNames {
		l := Levels[name]
		if i > 0 && (l.MaxDepth < prev.MaxDepth || l.MaxSubagents < prev.MaxSubagents || l.MaxWork < prev.MaxWork || l.MaxTurns < prev.MaxTurns || l.MaxTurns == 0) {
			t.Errorf("%s shrinks a limit: %+v after %+v", name, l, prev)
		}
		prev = l
	}
	if !Levels[LevelOff].AskFirst || Levels[LevelAssist].AskFirst || Levels[LevelAssist].Schedule || !Levels[LevelAuto].Schedule || !Levels[LevelFull].Race || Levels[LevelAuto].Race {
		t.Fatalf("levels = %+v", Levels)
	}
	// One agent works like its own CLI until the user asks for more: a new
	// thread's board is a list, not a scheduler that starts subagents.
	if (Config{}).StartLevel() != LevelOff || (Config{Orchestration: "assist"}).StartLevel() != LevelAssist || Levels[DefaultLevel].Schedule {
		t.Fatal("start level")
	}
}

// config.json changes a level field by field; the old max_* keys cap every
// level; an unknown level reads as off.
func TestLevelOverrides(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	raw := `{"orchestration": "full", "max_children": 3, "orchestration_levels": {"full": {"max_depth": 5, "race": false}, "assist": {"schedule": true}}}`
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := LoadFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	full := c.Level(LevelFull)
	if full.MaxDepth != 5 || full.Race || full.MaxChildren != 3 || full.MaxSubagents != 20 || full.Review != "other" {
		t.Fatalf("full = %+v", full)
	}
	if a := c.Level(LevelAssist); !a.Schedule || a.MaxChildren != 2 {
		t.Fatalf("assist = %+v", a)
	}
	if c.Level("max") != c.Level(LevelOff) {
		t.Fatal("an unknown level is not off")
	}
	if Levels[LevelFull].MaxDepth != 3 {
		t.Fatal("an override changed the table")
	}
}

func TestLevelValidation(t *testing.T) {
	for _, tc := range []struct {
		c    Config
		want string
	}{
		{Config{Orchestration: "on"}, "orchestration must be"},
		{Config{OrchestrationLevels: map[string]json.RawMessage{"max": json.RawMessage(`{}`)}}, "no level"},
		{Config{OrchestrationLevels: map[string]json.RawMessage{"auto": json.RawMessage(`{"max_depth": -1}`)}}, "0 or more"},
		{Config{OrchestrationLevels: map[string]json.RawMessage{"auto": json.RawMessage(`{"review": "gemini"}`)}}, "review must be"},
		{Config{OrchestrationLevels: map[string]json.RawMessage{"auto": json.RawMessage(`{"race": "yes"}`)}}, "orchestration_levels.auto"},
	} {
		if err := tc.c.validateLevels(); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%+v: %v", tc.c, err)
		}
	}
}
