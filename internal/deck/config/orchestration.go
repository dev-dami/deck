package config

import (
	"encoding/json"
	"fmt"
	"slices"
)

// Orchestration levels: how much a thread's agents organise work on their
// own. Each level is a row of Levels; config.json may change any field of a
// row under "orchestration_levels", e.g. {"full": {"max_depth": 4}}.
const (
	// LevelOff: agents do what they are told. One starts helpers only in a
	// turn where the user spoke to it, and helpers start none.
	LevelOff = "off"
	// LevelAssist: agents may start a couple of helpers when it clearly
	// helps; helpers start none, and the board is a plain list.
	LevelAssist = "assist"
	// LevelAuto: agents plan on the task board, which the scheduler runs, and
	// helpers may start helpers of their own.
	LevelAuto = "auto"
	// LevelFull: deeper trees, more helpers, agents may race, and work done
	// by helpers is reviewed by another kind of agent.
	LevelFull = "full"
)

// LevelNames are the levels, from least to most.
var LevelNames = []string{LevelOff, LevelAssist, LevelAuto, LevelFull}

// DefaultLevel is the level of a new thread when the config names none: off,
// so one agent works like its own CLI until the user asks for more.
const DefaultLevel = LevelOff

// Level is what one orchestration level lets a thread's agents do.
type Level struct {
	// MaxDepth is how many levels of subagents go below the user's agents.
	MaxDepth int `json:"max_depth"`
	// MaxChildren is how many live subagents one member may have.
	MaxChildren int `json:"max_children"`
	// MaxSubagents is how many subagents may be live in the thread.
	MaxSubagents int `json:"max_subagents"`
	// MaxWork is how many subagents agents may start, and tasks the scheduler
	// may hand out, per message of the user before the thread pauses.
	MaxWork int `json:"max_work"`
	// MaxTurns is how many turns may start without the user per message of
	// theirs: every turn a relay, a hand-off, a task's brief or report, a
	// review or a retry starts, in any member or subagent. At the limit the
	// thread pauses until the user writes or continues it.
	MaxTurns int `json:"max_turns"`
	// AskFirst lets a member start subagents only in a turn where the user
	// spoke to it.
	AskFirst bool `json:"ask_first"`
	// Schedule has the scheduler run the board's ready tasks.
	Schedule bool `json:"schedule"`
	// Race lets agents start races (task.race); the user always may.
	Race bool `json:"race"`
	// Review is who reviews a task run by subagents that names no review,
	// when Config.Review is "": "" none, "other" or a kind.
	Review string `json:"review"`
}

// Levels is the table of levels.
var Levels = map[string]Level{
	LevelOff:    {MaxDepth: 1, MaxChildren: 4, MaxSubagents: 4, MaxWork: 8, MaxTurns: 12, AskFirst: true},
	LevelAssist: {MaxDepth: 1, MaxChildren: 2, MaxSubagents: 6, MaxWork: 8, MaxTurns: 16},
	LevelAuto:   {MaxDepth: 2, MaxChildren: 4, MaxSubagents: 12, MaxWork: 24, MaxTurns: 48, Schedule: true},
	LevelFull:   {MaxDepth: 3, MaxChildren: 6, MaxSubagents: 20, MaxWork: 48, MaxTurns: 96, Schedule: true, Race: true, Review: "other"},
}

// ValidLevel reports whether s names a level.
func ValidLevel(s string) bool { return slices.Contains(LevelNames, s) }

// StartLevel is the level new threads start at.
func (c Config) StartLevel() string {
	if ValidLevel(c.Orchestration) {
		return c.Orchestration
	}
	return DefaultLevel
}

// Level is level's row of Levels with the config's changes to it: its
// orchestration_levels entry, then max_depth, max_children, max_subagents
// and max_work, which cap every level when set. An unknown level is off.
// A max_turns of 0 in config.json means the level's own.
func (c Config) Level(level string) Level {
	if !ValidLevel(level) {
		level = LevelOff
	}
	l := Levels[level]
	if raw, ok := c.OrchestrationLevels[level]; ok {
		_ = json.Unmarshal(raw, &l) // Validate refused a bad one
		if l.MaxTurns == 0 {
			l.MaxTurns = Levels[level].MaxTurns
		}
	}
	ceil := func(n *int, max int) {
		if max > 0 && *n > max {
			*n = max
		}
	}
	ceil(&l.MaxDepth, c.MaxDepth)
	ceil(&l.MaxChildren, c.MaxChildren)
	ceil(&l.MaxSubagents, c.MaxSubagents)
	ceil(&l.MaxWork, c.MaxWork)
	return l
}

// validateLevels checks the orchestration and orchestration_levels keys.
func (c Config) validateLevels() error {
	if c.Orchestration != "" && !ValidLevel(c.Orchestration) {
		return fmt.Errorf("orchestration must be \"off\", \"assist\", \"auto\" or \"full\", got %q", c.Orchestration)
	}
	for name, raw := range c.OrchestrationLevels {
		if !ValidLevel(name) {
			return fmt.Errorf("orchestration_levels: no level %q", name)
		}
		var l Level
		if err := json.Unmarshal(raw, &l); err != nil {
			return fmt.Errorf("orchestration_levels.%s: %w", name, err)
		}
		if l.MaxDepth < 0 || l.MaxChildren < 0 || l.MaxSubagents < 0 || l.MaxWork < 0 || l.MaxTurns < 0 {
			return fmt.Errorf("orchestration_levels.%s: limits must be 0 or more", name)
		}
		if l.Review != "" && l.Review != "none" && l.Review != "other" && !slices.Contains(Kinds, l.Review) {
			return fmt.Errorf("orchestration_levels.%s: review must be \"none\", \"other\" or an agent kind, got %q", name, l.Review)
		}
	}
	return nil
}
