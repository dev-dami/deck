// Package settings is the app's own preferences, kept as JSON in the
// user's config folder (…/Deck/settings.json).
package settings

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
)

// Settings is everything the Settings page changes, plus what the app
// remembers between runs.
type Settings struct {
	// Theme is "dark" or "light".
	Theme string `json:"theme"`
	// TextSize is the body size in points (13, 14 or 16).
	TextSize int `json:"text_size"`
	// DefaultMode is the mode new threads start in: "" leaves Deck's own
	// default, else ask, plan or yolo.
	DefaultMode string `json:"default_mode,omitempty"`
	// DeckPath overrides which deck binary runs the engines.
	DeckPath string `json:"deck_path,omitempty"`
	// ReopenProjects reopens the projects that were open at quit.
	ReopenProjects bool `json:"reopen_projects"`
	// PanelOpen keeps the side panel open.
	PanelOpen bool `json:"panel_open"`
	// Open is the projects open at the last quit.
	Open []string `json:"open,omitempty"`
	// Recents is the most recently opened projects, newest first.
	Recents []string `json:"recents,omitempty"`
}

// Defaults is a first run.
func Defaults() Settings {
	return Settings{Theme: "dark", TextSize: 14, ReopenProjects: true, PanelOpen: true}
}

func path() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "Deck", "settings.json")
}

// Load reads the settings, or the defaults when there are none.
func Load() Settings {
	s := Defaults()
	if p := path(); p != "" {
		if b, err := os.ReadFile(p); err == nil {
			_ = json.Unmarshal(b, &s)
		}
	}
	if s.Theme != "light" {
		s.Theme = "dark"
	}
	if !slices.Contains([]int{13, 14, 16}, s.TextSize) {
		s.TextSize = 14
	}
	return s
}

// Save writes the settings.
func (s Settings) Save() error {
	p := path()
	if p == "" {
		return os.ErrNotExist
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

// Touch moves dir to the front of Recents.
func (s *Settings) Touch(dir string) {
	s.Recents = append([]string{dir}, slices.DeleteFunc(s.Recents, func(d string) bool { return d == dir })...)
	if len(s.Recents) > 12 {
		s.Recents = s.Recents[:12]
	}
}
