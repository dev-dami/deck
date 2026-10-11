// Package guard holds repo-wide tests that fail when a Go file in the module
// references credentials, a model API endpoint, or an API-key environment
// variable. Deck runs the user's own CLIs under their own login, so none of
// these may appear in the code.
package guard

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// forbiddenText must not appear anywhere in a .go file of this module.
var forbiddenText = []string{
	".credentials.json",
	"auth.json",
	"api.anthropic.com",
	"api.openai.com",
	"chatgpt.com/backend-api",
}

// forbiddenEnvRead matches reading ANTHROPIC_API_KEY or OPENAI_API_KEY from the environment.
var forbiddenEnvRead = regexp.MustCompile(`(?:Getenv|LookupEnv)\(\s*"(?:ANTHROPIC_API_KEY|OPENAI_API_KEY)"\s*\)`)

func TestNoCredentialOrAPIReferences(t *testing.T) {
	root := repoRoot(t)

	files := goFiles(t, root)
	if len(files) < 3 {
		t.Fatalf("found only %d .go files under %s; the walker is not seeing the repo", len(files), root)
	}

	for _, path := range files {
		rel, _ := filepath.Rel(root, path)
		// This file names the forbidden patterns on purpose. Match by repo-relative
		// path, not runtime.Caller, which is wrong under -trimpath or cross builds.
		if filepath.ToSlash(rel) == "internal/deck/guard/guard_test.go" {
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Errorf("read %s: %v", path, err)
			continue
		}
		text := string(data)
		for _, bad := range forbiddenText {
			if strings.Contains(text, bad) {
				t.Errorf("%s references %q", rel, bad)
			}
		}
		if loc := forbiddenEnvRead.FindIndex(data); loc != nil {
			t.Errorf("%s reads an API-key environment variable: %s", rel, strings.TrimSpace(string(data[loc[0]:loc[1]])))
		}
	}
}

// TestGuardPatternsMatch checks that the patterns above actually match what
// they are meant to match, so the scan above cannot pass by accident.
func TestGuardPatternsMatch(t *testing.T) {
	for _, sample := range []string{
		`os.Getenv("ANTHROPIC_API_KEY")`,
		`os.LookupEnv("OPENAI_API_KEY")`,
	} {
		if !forbiddenEnvRead.MatchString(sample) {
			t.Errorf("env pattern does not match %q", sample)
		}
	}
	if forbiddenEnvRead.MatchString(`os.Getenv("DECK_REDUCED_MOTION")`) {
		t.Error("env pattern matches an unrelated variable")
	}
	for _, bad := range forbiddenText {
		if !strings.Contains("https://"+bad+"/x", bad) {
			t.Errorf("forbidden text %q does not match itself", bad)
		}
	}
}

// repoRoot walks up from the test's working directory to the directory that
// holds go.mod.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found above the test directory")
		}
		dir = parent
	}
}

// goFiles lists every .go file under root, skipping hidden directories and vendor/.
func goFiles(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			name := d.Name()
			if path != root && (strings.HasPrefix(name, ".") || name == "vendor") {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(path, ".go") {
			out = append(out, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	return out
}
