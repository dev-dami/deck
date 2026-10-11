package model

import (
	"strings"
	"testing"
	"time"

	"github.com/dev-dami/deck/internal/deck/gh"
)

func TestGitPRBody(t *testing.T) {
	if got := gitPRBody(nil, "Add the form"); got != "" {
		t.Fatalf("no commits: %q", got)
	}
	if got := gitPRBody([]string{"Add the form"}, "Add the form"); got != "" {
		t.Fatalf("one commit that is the title: %q", got)
	}
	// LogSince lists newest first; the body reads oldest first.
	got := gitPRBody([]string{"Add tests", "Add the form"}, "Add tests")
	if got != "- Add the form\n- Add tests" {
		t.Fatalf("got %q", got)
	}
}

func TestGitPRNotes(t *testing.T) {
	if got := gitPRNotes(gh.Status{Branch: "x"}); got != "pushes the branch first" {
		t.Fatalf("got %q", got)
	}
	got := gitPRNotes(gh.Status{Branch: "x", Upstream: "origin/x", Ahead: 2, Dirty: 1})
	if got != "pushes 2 commit(s) first · 1 uncommitted change(s) stay out" {
		t.Fatalf("got %q", got)
	}
	if got := gitPRNotes(gh.Status{Branch: "x", Upstream: "origin/x"}); got != "" {
		t.Fatalf("got %q", got)
	}
}

func TestGitHandoffText(t *testing.T) {
	got := gitHandoffText("codex", 12, "test (ubuntu-latest)", "--- FAIL: TestX\n")
	want := "$codex CI failed on #12 (test (ubuntu-latest)):\n```\n--- FAIL: TestX\n```"
	if got != want {
		t.Fatalf("got %q", got)
	}
	// A log with a fence of its own gets a longer one.
	got = gitHandoffText("claude", 3, "lint", "see ```code```")
	if !strings.Contains(got, "\n````\n") || !strings.HasSuffix(got, "\n````") {
		t.Fatalf("fence not lengthened: %q", got)
	}
}

func TestGitNudgeable(t *testing.T) {
	now := time.Now()
	g := gitState{defaultBranch: "trunk", dismissed: map[string]bool{"old-news": true}}
	cases := []struct {
		b    gh.Branch
		want bool
	}{
		{gh.Branch{Name: "feature", Upstream: "origin/feature", Committed: now.Add(-4 * time.Minute)}, true},
		{gh.Branch{Name: "local-only", Committed: now.Add(-time.Minute)}, false},
		{gh.Branch{Name: "stale", Upstream: "origin/stale", Committed: now.Add(-2 * time.Hour)}, false},
		{gh.Branch{Name: "trunk", Upstream: "origin/trunk", Committed: now}, false},
		{gh.Branch{Name: "main", Upstream: "origin/main", Committed: now}, false},
		{gh.Branch{Name: "old-news", Upstream: "origin/old-news", Committed: now}, false},
	}
	for _, c := range cases {
		if got := g.nudgeable(c.b, now); got != c.want {
			t.Errorf("%s: got %v, want %v", c.b.Name, got, c.want)
		}
	}
}

func TestGitWantPRList(t *testing.T) {
	now := time.Now()
	g := gitState{
		status: gh.Status{Branch: "feature"},
		recent: []gh.Branch{
			{Name: "feature", Upstream: "origin/feature", Committed: now},
			{Name: "older", Upstream: "origin/older", Committed: now.Add(-3 * time.Hour)},
		},
	}
	if g.wantPRList(now) {
		t.Fatal("only the current branch is recent; its pull request is read anyway")
	}
	g.recent = append(g.recent, gh.Branch{Name: "other", Upstream: "origin/other", Committed: now.Add(-10 * time.Minute)})
	if !g.wantPRList(now) {
		t.Fatal("another recently pushed branch needs the list")
	}
	g.headsAt = now
	if g.wantPRList(now) {
		t.Fatal("a fresh list is not read again")
	}
}
