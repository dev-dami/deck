package model

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

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

func TestGitMaybePollPR(t *testing.T) {
	now := time.Now()
	u := newGitPaneUI(t)
	g := &u.git
	g.prBusy = false
	g.headsAt, g.prAt, g.prBranch = time.Time{}, time.Time{}, ""
	if u.gitMaybePollPR(now) == nil || !g.prBusy {
		t.Fatal("a branch whose pull request was never read is asked about")
	}
	g.prBusy = false
	g.headsAt, g.prAt, g.prBranch = now, now, "feature"
	if u.gitMaybePollPR(now) != nil {
		t.Fatal("a fresh read is not repeated")
	}
	g.headsAt = now.Add(-gitPRListInterval)
	if u.gitMaybePollPR(now) == nil {
		t.Fatal("the open pull requests are listed every two minutes")
	}
	g.prBusy = false
	g.ghAvail = false
	g.headsAt = time.Time{}
	if u.gitMaybePollPR(now) != nil {
		t.Fatal("GitHub is not asked when gh is missing")
	}
}

func TestGitAgo(t *testing.T) {
	now := time.Date(2026, 1, 2, 12, 0, 0, 0, time.UTC)
	cases := map[time.Duration]string{
		10 * time.Second: "just now",
		4 * time.Minute:  "4m ago",
		3 * time.Hour:    "3h ago",
		72 * time.Hour:   "3d ago",
	}
	for d, want := range cases {
		if got := gitAgo(now.Add(-d), now); got != want {
			t.Errorf("%v: got %q, want %q", d, got, want)
		}
	}
	if gitAgo(time.Time{}, now) != "" {
		t.Error("zero time should say nothing")
	}
}

func TestGitChecksBadge(t *testing.T) {
	cases := []struct {
		s    gh.CheckSummary
		want string
	}{
		{gh.CheckSummary{}, ""},
		{gh.CheckSummary{Total: 3, Passed: 3}, "✓"},
		{gh.CheckSummary{Total: 3, Passed: 2, Failed: 1}, "✗"},
		{gh.CheckSummary{Total: 5, Passed: 3, Pending: 2}, "● 3/5"},
	}
	for _, c := range cases {
		if got := ansi.Strip(gitChecksBadge(c.s)); got != c.want {
			t.Errorf("%+v: got %q, want %q", c.s, got, c.want)
		}
	}
}

func TestGitAheadBehind(t *testing.T) {
	if got := gitAheadBehind(gh.Status{Ahead: 2, Behind: 1}); got != "↑2 ↓1" {
		t.Fatalf("got %q", got)
	}
	if got := gitAheadBehind(gh.Status{}); got != "" {
		t.Fatalf("got %q", got)
	}
}
