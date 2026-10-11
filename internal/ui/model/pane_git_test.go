package model

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/dev-dami/deck/internal/deck/gh"
	"github.com/dev-dami/deck/internal/ui/common"
	"github.com/dev-dami/deck/internal/ui/styles"
)

// newGitPaneUI is a UI whose git state is a dirty feature branch with a
// pull request, two open pull requests, recent branches and commits.
func newGitPaneUI(t *testing.T) *UI {
	t.Helper()
	sty := styles.DeckDark()
	u := &UI{com: &common.Common{Workspace: &testWorkspace{}, Styles: &sty}}
	u.gitClient()
	now := time.Now()
	g := &u.git
	g.statusOK = true
	g.status = gh.Status{Branch: "feature", Upstream: "origin/feature", Ahead: 2, Dirty: 2}
	g.files = []gh.ChangedFile{{Code: "M", Path: "a.go"}, {Code: "?", Path: "notes.txt"}}
	g.ghChecked, g.ghAvail = true, true
	pr := &gh.PR{Number: 12, Title: "Add the form", State: "OPEN", HeadRefName: "feature", BaseRefName: "main",
		Checks: []gh.Check{{Name: "build", Bucket: gh.BucketPass}, {Name: "test", Bucket: gh.BucketPass}}}
	g.pr, g.prBranch, g.checks = pr, "feature", gh.Summarize(pr.Checks)
	g.setHeads([]gh.PR{{Number: 9, Title: "Other", HeadRefName: "other"}, *pr}, now)
	g.recent = []gh.Branch{
		{Name: "feature", Upstream: "origin/feature", Committed: now.Add(-4 * time.Minute)},
		{Name: "other", Upstream: "origin/other", Committed: now.Add(-time.Hour)},
		{Name: "local", Committed: now.Add(-3 * time.Hour)},
	}
	g.commits = []gh.Commit{{Hash: "a94d819", When: now, Subject: "Sidebar panes"}, {Hash: "40da179", When: now, Subject: "Drop csync"}}
	return u
}

// gp is the git pane, as the strip would hold it.
var gp gitPane

func gitTestKey(k string) tea.KeyPressMsg {
	switch k {
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEscape}
	case "tab":
		return tea.KeyPressMsg{Code: tea.KeyTab}
	case "up":
		return tea.KeyPressMsg{Code: tea.KeyUp}
	case "down":
		return tea.KeyPressMsg{Code: tea.KeyDown}
	case "left":
		return tea.KeyPressMsg{Code: tea.KeyLeft}
	case "right":
		return tea.KeyPressMsg{Code: tea.KeyRight}
	case "backspace":
		return tea.KeyPressMsg{Code: tea.KeyBackspace}
	}
	r := []rune(k)[0]
	return tea.KeyPressMsg{Code: r, Text: k}
}

func gitPress(t *testing.T, u *UI, keys ...string) bool {
	t.Helper()
	handled := false
	for _, k := range keys {
		handled, _ = gp.key(u, gitTestKey(k))
	}
	return handled
}

func gitSelectedRow(u *UI) gitRow {
	rows := u.gitRootRows(gitPaneWidth)
	i := gitSelected(rows, &u.git.pane.root)
	if i < 0 {
		return gitRow{}
	}
	return rows[i]
}

func TestGitPaneRootNavigation(t *testing.T) {
	u := newGitPaneUI(t)
	if r := gitSelectedRow(u); r.kind != gitRowBranch {
		t.Fatalf("starts on %+v, want the branch row", r)
	}
	gitPress(t, u, "down")
	if r := gitSelectedRow(u); r.kind != gitRowFile || !strings.Contains(r.text, "a.go") {
		t.Fatalf("down: %+v", r)
	}
	// Headers and notes are skipped: tab goes to the next section.
	gitPress(t, u, "tab")
	if r := gitSelectedRow(u); r.kind != gitRowThisPR || r.number != 12 {
		t.Fatalf("tab: %+v", r)
	}
	gitPress(t, u, "tab")
	if r := gitSelectedRow(u); r.kind != gitRowPR || r.number != 12 {
		t.Fatalf("the current branch's pull request is listed first: %+v", r)
	}
	gitPress(t, u, "down")
	if r := gitSelectedRow(u); r.kind != gitRowPR || r.number != 9 {
		t.Fatalf("down: %+v", r)
	}

	// enter goes into the pull request, esc comes back.
	if !gitPress(t, u, "enter") {
		t.Fatal("enter not handled")
	}
	p := &u.git.pane
	if v := p.top(); v.kind != gitViewPR || v.number != 9 {
		t.Fatalf("enter opened %+v", v)
	}
	if !gitPress(t, u, "esc") || p.top().kind != gitViewRoot {
		t.Fatal("esc should go back to the root")
	}
	if r := gitSelectedRow(u); r.number != 9 {
		t.Fatalf("the cursor should stay on #9: %+v", r)
	}
	// At the root, esc is the strip's.
	if gitPress(t, u, "esc") {
		t.Fatal("esc at the root must be left to the strip")
	}

	// Into a commit and back with backspace.
	gitPress(t, u, "G")
	if r := gitSelectedRow(u); r.kind != gitRowCommit || r.name != "40da179" {
		t.Fatalf("end: %+v", r)
	}
	gitPress(t, u, "enter")
	if v := p.top(); v.kind != gitViewCommit || v.hash != "40da179" || !v.loading {
		t.Fatalf("enter on a commit: %+v", v)
	}
	if !gitPress(t, u, "backspace") || len(p.stack) != 0 {
		t.Fatal("backspace should go back")
	}
}

func TestGitPaneSelectionFollowsKey(t *testing.T) {
	u := newGitPaneUI(t)
	u.git.pane.root.selKey = gitRowKey(gitRowRecent, "other")
	if r := gitSelectedRow(u); r.name != "other" {
		t.Fatalf("got %+v", r)
	}
	// A file more above it does not move the cursor off the branch.
	u.git.files = append(u.git.files, gh.ChangedFile{Code: "A", Path: "b.go", Staged: true})
	if r := gitSelectedRow(u); r.name != "other" {
		t.Fatalf("got %+v", r)
	}
	u.git.pane.root.selKey = gitSectionKey(gitSectionCommits)
	if r := gitSelectedRow(u); r.kind != gitRowCommit || r.name != "a94d819" {
		t.Fatalf("a section key selects its first row: %+v", r)
	}
}

func TestGitPaneCommitFlow(t *testing.T) {
	u := newGitPaneUI(t)
	p := &u.git.pane
	gitPress(t, u, "c")
	if !p.committing {
		t.Fatal("c should open the commit input")
	}
	// The input takes every key, esc included.
	if !gitPress(t, u, "q") || !gitPress(t, u, "esc") {
		t.Fatal("keys in the commit input are the pane's")
	}
	if p.committing || p.busy != "" {
		t.Fatal("esc should cancel without committing")
	}

	gitPress(t, u, "c")
	if p.input.Value() != "" {
		t.Fatalf("a new commit starts empty: %q", p.input.Value())
	}
	gitPress(t, u, "enter")
	if !p.committing || p.busy != "" {
		t.Fatal("an empty message must not commit")
	}
	for _, r := range "Fix it" {
		gitPress(t, u, string(r))
	}
	if p.input.Value() != "Fix it" {
		t.Fatalf("typed %q", p.input.Value())
	}
	gitPress(t, u, "enter")
	if p.committing || p.busy != "Committing…" {
		t.Fatalf("enter should commit: committing=%v busy=%q", p.committing, p.busy)
	}

	// A clean tree has nothing to commit.
	u2 := newGitPaneUI(t)
	u2.git.status.Dirty, u2.git.files = 0, nil
	gitPress(t, u2, "c")
	if u2.git.pane.committing || u2.git.pane.info == "" {
		t.Fatal("a clean tree should say there is nothing to commit")
	}
}

func TestGitPaneDirtyCheckoutAsksFirst(t *testing.T) {
	u := newGitPaneUI(t)
	p := &u.git.pane
	p.root.selKey = gitRowKey(gitRowRecent, "other")
	gitPress(t, u, "enter")
	if p.confirm != "other" || p.busy != "" {
		t.Fatalf("a dirty tree should ask first: confirm=%q busy=%q", p.confirm, p.busy)
	}
	gitPress(t, u, "n")
	if p.confirm != "" || p.busy != "" {
		t.Fatal("n keeps the branch")
	}
	gitPress(t, u, "enter", "y")
	if p.busy != "Checking out other…" {
		t.Fatalf("y should check out: %q", p.busy)
	}

	// P on another branch checks it out, then opens a pull request.
	u2 := newGitPaneUI(t)
	u2.git.status.Dirty = 0
	u2.git.pane.root.selKey = gitRowKey(gitRowRecent, "local")
	gitPress(t, u2, "P")
	if u2.git.pane.busy != "Checking out local…" {
		t.Fatalf("P on another branch: %q", u2.git.pane.busy)
	}
}

func gitPRViewUI(t *testing.T) (*UI, *gitView) {
	t.Helper()
	u := newGitPaneUI(t)
	gitPress(t, u, "tab", "enter")
	v := u.git.pane.top()
	if v.kind != gitViewPR {
		t.Fatalf("no pull request view: %+v", v)
	}
	v.loading = false
	v.setPR(&gh.PR{Number: 12, Title: "Add the form", State: "OPEN", HeadRefName: "feature", BaseRefName: "main"}, []gh.Check{
		{Name: "build", Bucket: gh.BucketPass},
		{Name: "test", Bucket: gh.BucketFail, Link: "https://github.com/o/r/actions/runs/7/job/8"},
		{Name: "lint", Bucket: gh.BucketPending},
	})
	return u, v
}

func TestGitPaneMergeNeedsChoice(t *testing.T) {
	u, v := gitPRViewUI(t)
	p := &u.git.pane
	if v.sel != 1 {
		t.Fatalf("the view should start on the failed check, at %d", v.sel)
	}
	gitPress(t, u, "m")
	if !v.merging || p.busy != "" {
		t.Fatal("m should only ask")
	}
	// Nothing but enter merges.
	gitPress(t, u, "y", "m", "c", "o", "right")
	if !v.merging || p.busy != "" {
		t.Fatalf("a stray key merged or left the question: merging=%v busy=%q", v.merging, p.busy)
	}
	if gh.MergeMethods[v.method] != "merge" {
		t.Fatalf("right should choose the next method, got %s", gh.MergeMethods[v.method])
	}
	gitPress(t, u, "esc")
	if v.merging || p.busy != "" || p.top() != v {
		t.Fatal("esc cancels the merge and stays on the pull request")
	}
	gitPress(t, u, "m", "left", "enter")
	if v.merging || p.busy != "Merging #12…" {
		t.Fatalf("enter should merge: busy=%q", p.busy)
	}

	// A merged pull request is not offered for merging.
	u2, v2 := gitPRViewUI(t)
	v2.pr.State = "MERGED"
	gitPress(t, u2, "m")
	if v2.merging {
		t.Fatal("a merged pull request cannot merge again")
	}
}

func TestGitPaneLogView(t *testing.T) {
	u, v := gitPRViewUI(t)
	p := &u.git.pane
	gitPress(t, u, "l")
	if p.busy == "" {
		t.Fatal("l should fetch the failed log")
	}
	u.handleGitMsg(gitLogMsg{number: 12, check: v.checks[1], log: "line 1\nline 2\n--- FAIL"})
	lv := p.top()
	if lv.kind != gitViewLog || p.busy != "" {
		t.Fatalf("the log should show: %+v busy=%q", lv, p.busy)
	}
	gitPress(t, u, "esc")
	if p.top() != v {
		t.Fatal("esc from the log goes back to the pull request")
	}
}

func TestGitPaneBadge(t *testing.T) {
	u := newGitPaneUI(t)
	g := &u.git
	cases := []struct {
		checks gh.CheckSummary
		want   string
	}{
		{gh.CheckSummary{Total: 2, Passed: 2}, "#12 ✓"},
		{gh.CheckSummary{Total: 5, Passed: 3, Pending: 2}, "#12 ● 3/5"},
		{gh.CheckSummary{Total: 2, Passed: 1, Failed: 1}, "#12 ✗"},
		{gh.CheckSummary{}, "#12"},
	}
	for _, c := range cases {
		g.checks = c.checks
		if got := ansi.Strip(gp.badge(u)); got != c.want {
			t.Errorf("%+v: got %q, want %q", c.checks, got, c.want)
		}
	}
	g.pr = nil
	if got := ansi.Strip(gp.badge(u)); got != "↑2" {
		t.Errorf("ahead without a pull request: got %q", got)
	}
	g.status.Ahead = 0
	if got := gp.badge(u); got != "" {
		t.Errorf("level without a pull request: got %q", got)
	}
}

// gitCheckHeight checks that height is what the view needs and that render
// fills exactly the size it is given.
func gitCheckHeight(t *testing.T, u *UI, name string) {
	t.Helper()
	w := gitPaneWidth
	h := gp.height(u, w)
	body := u.gitBodyLines(w, false)
	lines, hints := u.gitFooter(w)
	if want := len(body.lines) + len(lines) + len(hints); h != want || h <= 0 {
		t.Fatalf("%s: height %d, want %d", name, h, want)
	}
	out := gp.render(u, w, h, true)
	if got := strings.Count(out, "\n") + 1; got != h {
		t.Fatalf("%s: rendered %d lines at height %d", name, got, h)
	}
	for i, l := range strings.Split(out, "\n") {
		if ansi.StringWidth(l) > w {
			t.Fatalf("%s: line %d is %d wide: %q", name, i, ansi.StringWidth(l), ansi.Strip(l))
		}
	}
	small := gp.render(u, w, 5, true)
	if got := strings.Count(small, "\n") + 1; got != 5 {
		t.Fatalf("%s: rendered %d lines at height 5", name, got)
	}
}

func TestGitPaneHeight(t *testing.T) {
	u := newGitPaneUI(t)
	gitCheckHeight(t, u, "root")
	rootH := gp.height(u, gitPaneWidth)

	gitPress(t, u, "c")
	if got := gp.height(u, gitPaneWidth); got <= rootH-2 {
		t.Fatalf("the commit input should add a line: %d vs %d", got, rootH)
	}
	gitCheckHeight(t, u, "root committing")
	gitPress(t, u, "esc")

	u2, _ := gitPRViewUI(t)
	gitCheckHeight(t, u2, "pull request")
	gitPress(t, u2, "m")
	gitCheckHeight(t, u2, "pull request merging")
	gitPress(t, u2, "esc")

	u2.handleGitMsg(gitLogMsg{number: 12, check: gh.Check{Name: "test"}, log: strings.Repeat("log line\n", 40)})
	gitCheckHeight(t, u2, "log")
	if h := gp.height(u2, gitPaneWidth); h < 41 {
		t.Fatalf("the log needs its lines: %d", h)
	}

	u3 := newGitPaneUI(t)
	u3.gitOpenCommit("a94d819")
	gitCheckHeight(t, u3, "commit loading")
	u3.handleGitMsg(gitShowMsg{hash: "a94d819", detail: &gh.CommitDetail{
		Commit: gh.Commit{Hash: "a94d819", Subject: "Sidebar panes", When: time.Now()},
		Files:  []gh.FileStat{{Path: "pane.go", Added: 64}, {Path: "logo.png", Binary: true}},
		Added:  64,
	}})
	gitCheckHeight(t, u3, "commit")

	u4 := newGitPaneUI(t)
	u4.gitStartCreatePR()
	gitCheckHeight(t, u4, "create preparing")
	u4.handleGitMsg(gitPrefillMsg{branch: "feature", title: "Add the form", base: "main", body: "- one\n- two", notes: "pushes 2 commit(s) first"})
	gitCheckHeight(t, u4, "create")

	u5 := newGitPaneUI(t)
	u5.git.statusOK, u5.git.statusErr = false, "not a git repository"
	gitCheckHeight(t, u5, "not a repository")
	if out := ansi.Strip(gp.render(u5, gitPaneWidth, 10, true)); !strings.Contains(out, "not a git repository") {
		t.Fatalf("got %q", out)
	}
}

func TestGitPaneNoGH(t *testing.T) {
	u := newGitPaneUI(t)
	u.git.ghAvail = false
	u.git.pr, u.git.prs = nil, nil
	out := ansi.Strip(gp.render(u, gitPaneWidth, 60, false))
	if !strings.Contains(out, "gh auth login") {
		t.Fatalf("the GitHub sections should say how to sign in:\n%s", out)
	}
	if !strings.Contains(out, "a.go") || !strings.Contains(out, "Sidebar panes") {
		t.Fatalf("the git sections should still show:\n%s", out)
	}
}

func TestGitPaneCreateForm(t *testing.T) {
	u := newGitPaneUI(t)
	p := &u.git.pane
	gitPress(t, u, "P")
	v := p.top()
	if v.kind != gitViewCreate || v.form == nil || !v.form.preparing {
		t.Fatalf("P should open the form: %+v", v)
	}
	// Keys wait for the prefill.
	gitPress(t, u, "x", "tab")
	if v.form.field != prFieldTitle || v.form.title.Value() != "" {
		t.Fatal("keys must wait for the prefill")
	}
	u.handleGitMsg(gitPrefillMsg{branch: "feature", title: "Add the form", base: "main"})
	gitPress(t, u, "tab", "tab", "tab")
	if v.form.field != prFieldDraft {
		t.Fatalf("tab should reach the draft box, at %d", v.form.field)
	}
	gitPress(t, u, " ")
	if !v.form.draft {
		t.Fatal("space should toggle draft")
	}
	gitPress(t, u, "tab")
	if v.form.field != prFieldTitle {
		t.Fatal("tab wraps to the title")
	}
	// ctrl+s submits.
	gp.key(u, tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	if p.busy != "Opening the pull request…" {
		t.Fatalf("ctrl+s should submit: %q", p.busy)
	}
	// It worked: the new pull request takes the form's place.
	u.handleGitMsg(gitOpMsg{info: "PR #13 opened", openPR: 13})
	if top := p.top(); top.kind != gitViewPR || top.number != 13 || len(p.stack) != 1 {
		t.Fatalf("got %+v (%d views)", top, len(p.stack))
	}
	if p.busy != "" || p.info != "PR #13 opened" {
		t.Fatalf("status line: busy=%q info=%q", p.busy, p.info)
	}
}

func TestGitPaneClick(t *testing.T) {
	u := newGitPaneUI(t)
	p := &u.git.pane
	gp.render(u, gitPaneWidth, 60, true)
	body := u.gitBodyLines(gitPaneWidth, true)
	rows := u.gitRootRows(gitPaneWidth)
	line := -1
	for i, item := range body.items {
		if item >= 0 && rows[item].kind == gitRowPR && rows[item].number == 9 {
			line = i
		}
	}
	if line < 0 {
		t.Fatal("no row for #9")
	}
	gp.click(u, 3, line-p.root.off)
	if r := gitSelectedRow(u); r.number != 9 || len(p.stack) != 0 {
		t.Fatalf("the first click selects: %+v", r)
	}
	gp.click(u, 3, line-p.root.off)
	if v := p.top(); v.kind != gitViewPR || v.number != 9 {
		t.Fatalf("a click on the selected row opens it: %+v", v)
	}
}
