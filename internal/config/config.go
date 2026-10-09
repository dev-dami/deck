// Package config reads and writes Deck's settings file, ~/.deck/config.json.
//
// A missing file is not an error: Load returns Defaults. A file that exists
// may omit any key, and the missing keys keep their defaults.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
)

// EnvReducedMotion, when set to "1", forces Motion to "off".
const EnvReducedMotion = "DECK_REDUCED_MOTION"

// Models names the model each CLI should run. An empty value means the CLI's default.
type Models struct {
	Claude string `json:"claude"`
	Codex  string `json:"codex"`
}

// Config is Deck's user settings.
type Config struct {
	UserName       string `json:"user_name"`        // shown in the UI; defaults to git's user.name
	DefaultAgent   string `json:"default_agent"`    // "claude" or "codex"
	Motion         string `json:"motion"`           // "on" or "off"
	Bell           bool   `json:"bell"`             // ring the terminal bell when a turn needs attention
	TellAgentsName bool   `json:"tell_agents_name"` // tell agents the user's name (agent.Spec.TellName)
	ClaudeBinary   string `json:"claude_binary"`    // path or command name; empty means "claude"
	CodexBinary    string `json:"codex_binary"`     // path or command name; empty means "codex"
	DefaultMode    string `json:"default_mode"`     // "plan", "ask" or "yolo"; empty means "ask". "yolo" is ignored at startup (see StartMode)
	// TrackChanges snapshots a thread's git work tree with every message you
	// send, for the changes panel and its undo.
	TrackChanges bool   `json:"track_changes"`
	Models       Models `json:"models"`
}

// gitUserName and osUserName are indirections so tests can stub them.
var (
	gitUserName = func() string {
		out, err := exec.Command("git", "config", "--global", "user.name").Output()
		if err != nil {
			return ""
		}
		return strings.TrimSpace(string(out))
	}
	osUserName = func() string {
		u, err := user.Current()
		if err != nil {
			return ""
		}
		name := u.Username
		// Windows reports DOMAIN\name; keep only the name.
		if i := strings.LastIndexAny(name, `\/`); i >= 0 {
			name = name[i+1:]
		}
		return name
	}
)

// Defaults returns the settings used when no file exists. UserName comes from
// `git config --global user.name`, falling back to the OS user name.
func Defaults() Config {
	name := gitUserName()
	if name == "" {
		name = osUserName()
	}
	return Config{
		UserName:       name,
		DefaultAgent:   "claude",
		Motion:         "on",
		Bell:           true,
		TellAgentsName: false,
		ClaudeBinary:   "claude",
		CodexBinary:    "codex",
		DefaultMode:    "ask",
		TrackChanges:   true,
	}
}

// Dir returns ~/.deck.
func Dir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".deck"), nil
}

// Path returns ~/.deck/config.json.
func Path() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.json"), nil
}

// Load reads the config from Path.
func Load() (Config, error) {
	path, err := Path()
	if err != nil {
		return Config{}, err
	}
	return LoadFrom(path)
}

// LoadFrom reads the config at path. A missing file yields Defaults. Keys
// absent from the file keep their defaults. The result is validated, and
// DECK_REDUCED_MOTION=1 turns Motion off. The environment override is not
// written back by Save unless the caller saves the returned value.
func LoadFrom(path string) (Config, error) {
	cfg := Defaults()
	data, err := os.ReadFile(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		// keep defaults
	case err != nil:
		return Config{}, fmt.Errorf("config: read %s: %w", path, err)
	default:
		if err := json.Unmarshal(data, &cfg); err != nil {
			return Config{}, fmt.Errorf("config: parse %s: %w", path, err)
		}
	}
	if os.Getenv(EnvReducedMotion) == "1" {
		cfg.Motion = "off"
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, fmt.Errorf("config: %s: %w", path, err)
	}
	return cfg, nil
}

// Save validates c and writes it to Path.
func Save(c Config) error {
	path, err := Path()
	if err != nil {
		return err
	}
	return SaveTo(path, c)
}

// SaveTo validates c and writes it to path, creating the parent directory.
func SaveTo(path string, c Config) error {
	if err := c.Validate(); err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		_ = os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	return nil
}

// Validate reports the first invalid value.
func (c Config) Validate() error {
	switch c.DefaultAgent {
	case "claude", "codex":
	default:
		return fmt.Errorf("default_agent must be \"claude\" or \"codex\", got %q", c.DefaultAgent)
	}
	switch c.Motion {
	case "on", "off":
	default:
		return fmt.Errorf("motion must be \"on\" or \"off\", got %q", c.Motion)
	}
	switch c.DefaultMode {
	case "", "plan", "ask", "yolo":
	default:
		return fmt.Errorf("default_mode must be \"plan\", \"ask\" or \"yolo\", got %q", c.DefaultMode)
	}
	return nil
}

// StartMode is the permission mode new threads start in: DefaultMode, or "ask"
// when it is empty. "yolo" is never a starting mode - turning everything on
// without asking must be a deliberate act during the session, not a setting that
// applies silently - so a config that says yolo starts threads in "ask" and
// reports ignored == true (the UI shows a notice).
func (c Config) StartMode() (mode string, ignored bool) {
	switch c.DefaultMode {
	case "plan":
		return "plan", false
	case "yolo":
		return "ask", true
	}
	return "ask", false
}
