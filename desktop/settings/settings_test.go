package settings

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadSaveAndBOM(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("APPDATA", dir)         // Windows
	t.Setenv("XDG_CONFIG_HOME", dir) // Linux
	t.Setenv("HOME", dir)            // macOS

	if s := Load(); s.Theme != "dark" || s.TextSize != 14 || s.PanelRatio != 0.64 {
		t.Fatalf("defaults = %+v", s)
	}
	s := Defaults()
	s.Theme, s.TextSize, s.PanelTab = "light", 16, "files"
	s.Touch("/a")
	s.Touch("/b")
	s.Touch("/a")
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	got := Load()
	if got.Theme != "light" || got.TextSize != 16 || got.PanelTab != "files" || len(got.Recents) != 2 || got.Recents[0] != "/a" {
		t.Fatalf("reloaded = %+v", got)
	}

	// An editor's BOM must not reset everything.
	b, _ := os.ReadFile(path())
	if err := os.WriteFile(path(), append([]byte("\xef\xbb\xbf"), b...), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := Load(); got.Theme != "light" {
		t.Fatalf("with a BOM: %+v", got)
	}
	if _, err := os.Stat(filepath.Dir(path())); err != nil {
		t.Fatal(err)
	}
}
