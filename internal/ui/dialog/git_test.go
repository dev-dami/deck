package dialog

import (
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/dev-dami/deck/internal/deck/gh"
	"github.com/dev-dami/deck/internal/ui/common"
	"github.com/dev-dami/deck/internal/ui/styles"
)

func TestGitAgo(t *testing.T) {
	now := time.Date(2026, 1, 2, 12, 0, 0, 0, time.UTC)
	cases := map[time.Duration]string{
		10 * time.Second: "just now",
		4 * time.Minute:  "4m ago",
		3 * time.Hour:    "3h ago",
		72 * time.Hour:   "3d ago",
	}
	for d, want := range cases {
		if got := GitAgo(now.Add(-d), now); got != want {
			t.Errorf("%v: got %q, want %q", d, got, want)
		}
	}
	if GitAgo(time.Time{}, now) != "" {
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
		if got := ansi.Strip(GitChecksBadge(c.s)); got != c.want {
			t.Errorf("%+v: got %q, want %q", c.s, got, c.want)
		}
	}
}

func TestGitAheadBehind(t *testing.T) {
	if got := GitAheadBehind(gh.Status{Ahead: 2, Behind: 1}); got != "↑2 ↓1" {
		t.Fatalf("got %q", got)
	}
	if got := GitAheadBehind(gh.Status{}); got != "" {
		t.Fatalf("got %q", got)
	}
}

func gitTestCommon() *common.Common {
	s := styles.DeckDark()
	return &common.Common{Styles: &s}
}

func gitKey(k string) tea.KeyPressMsg {
	switch k {
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEscape}
	case "tab":
		return tea.KeyPressMsg{Code: tea.KeyTab}
	}
	r := []rune(k)[0]
	return tea.KeyPressMsg{Code: r, Text: k}
}

func TestGitPanelDirtyCheckoutAsksFirst(t *testing.T) {
	data := GitPanelData{
		Loaded:      true,
		GHAvailable: true,
		Status:      gh.Status{Branch: "main", Upstream: "origin/main", Dirty: 2},
		Recent:      []gh.Branch{{Name: "main"}, {Name: "feature"}},
	}
	g := NewGit(gitTestCommon(), GitTabRecent, data, "")
	g.sel[GitTabRecent] = 1
	if a := g.HandleMsg(gitKey("enter")); a != nil {
		t.Fatalf("checked out a dirty tree without asking: %#v", a)
	}
	a, ok := g.HandleMsg(gitKey("y")).(ActionGitCheckout)
	if !ok || a.Branch != "feature" || a.ThenCreatePR {
		t.Fatalf("got %#v", a)
	}
}

func TestGitPanelCreatePRForOtherBranchChecksOutFirst(t *testing.T) {
	data := GitPanelData{
		Loaded:      true,
		GHAvailable: true,
		Status:      gh.Status{Branch: "main", Upstream: "origin/main"},
		Recent:      []gh.Branch{{Name: "main"}, {Name: "feature", Upstream: "origin/feature"}},
	}
	g := NewGit(gitTestCommon(), GitTabRecent, data, "")
	g.sel[GitTabRecent] = 1
	a, ok := g.HandleMsg(gitKey("p")).(ActionGitCheckout)
	if !ok || a.Branch != "feature" || !a.ThenCreatePR {
		t.Fatalf("got %#v", a)
	}
}

func TestGitPanelCommit(t *testing.T) {
	data := GitPanelData{Loaded: true, Status: gh.Status{Branch: "feature", Dirty: 1}}
	g := NewGit(gitTestCommon(), GitTabBranch, data, "")
	// The first action is "Commit all…": it opens the message input.
	if _, ok := g.HandleMsg(gitKey("enter")).(ActionCmd); !ok || !g.committing {
		t.Fatal("commit input not opened")
	}
	for _, r := range "Fix it" {
		g.HandleMsg(gitKey(string(r)))
	}
	a, ok := g.HandleMsg(gitKey("enter")).(ActionGitCommitAll)
	if !ok || a.Message != "Fix it" {
		t.Fatalf("got %#v", a)
	}
}

func TestGitMergeAsksMethod(t *testing.T) {
	d := NewGitMerge(gitTestCommon(), 12, "Add the form")
	d.HandleMsg(tea.KeyPressMsg{Code: tea.KeyRight})
	a, ok := d.HandleMsg(gitKey("enter")).(ActionGitPRMerge)
	if !ok || a.Number != 12 || a.Method != "merge" {
		t.Fatalf("got %#v", a)
	}
	if _, ok := NewGitMerge(gitTestCommon(), 12, "x").HandleMsg(gitKey("esc")).(ActionClose); !ok {
		t.Fatal("esc must not merge")
	}
}
