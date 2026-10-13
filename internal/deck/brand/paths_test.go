package brand

import (
	"errors"
	"path/filepath"
	"testing"
)

func fakeHome(t *testing.T, home string) {
	t.Helper()
	old := homeDir
	homeDir = func() (string, error) { return home, nil }
	t.Cleanup(func() { homeDir = old })
}

func TestShowPath(t *testing.T) {
	for _, tc := range []struct {
		name, home, in, want string
	}{
		{"claude memory, windows", `C:\Users\hp`, `C:\Users\hp\.claude\projects\C--Users-hp-Downloads-Prepdrill\memory\MEMORY.md`, "memory · MEMORY.md"},
		{"claude memory, tilde backslash", `C:\Users\hp`, `~\.claude\projects\C--Users-hp-Downloads-Prepdrill\memory\MEMORY.md`, "memory · MEMORY.md"},
		{"claude memory, posix", "/home/me", "/home/me/.claude/projects/-home-me-app/memory/notes/x.md", "memory · notes/x.md"},
		{"claude memory, mixed separators", `C:\Users\hp`, `C:/Users/hp\.claude/projects\slug/memory/X.md`, "memory · X.md"},
		{"drive letter case", `C:\Users\hp`, `c:\users\HP\.claude\plans\plan.md`, "plans · plan.md"},
		{"claude plans, tilde", "/home/me", "~/.claude/plans/happy-otter.md", "plans · happy-otter.md"},
		{"$HOME prefix", "/home/me", "$HOME/.claude/plans/p.md", "plans · p.md"},
		{"%USERPROFILE% prefix", `C:\Users\hp`, `%USERPROFILE%\.claude\plans\p.md`, "plans · p.md"},
		{"folder itself", "/home/me", "/home/me/.claude/plans", "plans"},
		{"trailing separator on home", "/home/me/", "/home/me/.claude/plans/p.md", "plans · p.md"},
		{"deck memory", "/home/me", "/home/me/.deck/memory/-home-me-app/MEMORY.md", "memory · MEMORY.md"},
		{"deck plans", `C:\Users\hp`, `C:\Users\hp\.deck\plans\p.md`, "plans · p.md"},

		// Left alone.
		{"transcript is not memory", "/home/me", "/home/me/.claude/projects/slug/abc.jsonl", "/home/me/.claude/projects/slug/abc.jsonl"},
		{"skills", "/home/me", "/home/me/.claude/skills/pdf/SKILL.md", "/home/me/.claude/skills/pdf/SKILL.md"},
		{"project .claude", "/home/me", "/home/me/app/.claude/plans/p.md", "/home/me/app/.claude/plans/p.md"},
		{"another home", "/home/me", "/home/meg/.claude/plans/p.md", "/home/meg/.claude/plans/p.md"},
		{"relative", "/home/me", ".claude/plans/p.md", ".claude/plans/p.md"},
		{"tilde-like name", "/home/me", "~x/.claude/plans/p.md", "~x/.claude/plans/p.md"},
		{"home itself", "/home/me", "/home/me", "/home/me"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fakeHome(t, tc.home)
			got, ok := ShowPath(tc.in)
			if got != tc.want || ok != (tc.want != tc.in) {
				t.Errorf("ShowPath(%q) = %q, %v; want %q", tc.in, got, ok, tc.want)
			}
		})
	}
}

func TestShowPathNoHome(t *testing.T) {
	old := homeDir
	homeDir = func() (string, error) { return "", errors.New("no home") }
	t.Cleanup(func() { homeDir = old })
	if got, ok := ShowPath("~/.claude/plans/p.md"); !ok || got != "plans · p.md" {
		t.Errorf("tilde without a home dir = %q, %v", got, ok)
	}
	if _, ok := ShowPath("/home/me/.claude/plans/p.md"); ok {
		t.Error("an absolute path matched without a home dir")
	}
}

func TestMemoryDir(t *testing.T) {
	home := t.TempDir()
	fakeHome(t, home)
	for _, tc := range []struct{ project, slug string }{
		{`C:\Users\hp\Downloads\Prepdrill`, "C--Users-hp-Downloads-Prepdrill"},
		{"/home/me/my_app.v2", "-home-me-my-app-v2"},
		{"/tmp/café", "-tmp-caf-"},
	} {
		if got, want := MemoryDir(tc.project), filepath.Join(home, ".deck", "memory", tc.slug); got != want {
			t.Errorf("MemoryDir(%q) = %q, want %q", tc.project, got, want)
		}
	}
	if got, want := PlansDir(), filepath.Join(home, ".deck", "plans"); got != want {
		t.Errorf("PlansDir() = %q, want %q", got, want)
	}
}
