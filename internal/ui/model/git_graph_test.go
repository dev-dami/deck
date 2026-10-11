package model

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"

	"github.com/dev-dami/deck/internal/deck/gh"
)

// graphCommits builds commits from "id:parent,parent" specs, in order.
func graphCommits(specs ...string) []gh.Commit {
	out := make([]gh.Commit, 0, len(specs))
	for _, s := range specs {
		id, parents, _ := strings.Cut(s, ":")
		c := gh.Commit{Hash: id, ID: id, Subject: "subject " + id}
		if parents != "" {
			c.Parents = strings.Split(parents, ",")
		}
		out = append(out, c)
	}
	return out
}

// graphLines is the graph's commit rows as plain text, trailing space
// trimmed.
func graphLines(rows []graphRow) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = strings.TrimRight(graphPlain(r.line), " ")
	}
	return out
}

func TestGraphLinear(t *testing.T) {
	t.Parallel()
	rows := layoutGraph(graphCommits("c:b", "b:a", "a"))
	require.Equal(t, []string{"●", "●", "●"}, graphLines(rows))
	// The lane goes on under every commit but the root.
	require.Equal(t, "│", graphPlain(rows[0].cont))
	require.Equal(t, " ", graphPlain(rows[2].cont))
}

func TestGraphBranchAndMerge(t *testing.T) {
	t.Parallel()
	// m merges b into a; both branched off base.
	rows := layoutGraph(graphCommits("m:a,b", "b:base", "a:base", "base:root", "root"))
	require.Equal(t, []string{
		"○─╮",
		"│ ●",
		"● │",
		"●─╯",
		"●",
	}, graphLines(rows))
	// Under the merge both lanes go on.
	require.Equal(t, "│ │", graphPlain(rows[0].cont))
	require.Equal(t, 0, rows[0].col)
	require.Equal(t, 1, rows[1].col)
}

func TestGraphParallelBranches(t *testing.T) {
	t.Parallel()
	// Two branch tips, x and y, each two commits off base, as
	// --topo-order lists them: one branch's run, then the other's.
	rows := layoutGraph(graphCommits("x:x1", "x1:base", "y:y1", "y1:base", "base"))
	require.Equal(t, []string{
		"●",
		"●",
		"│ ●",
		"│ ●",
		"●─╯",
	}, graphLines(rows))
	// Every row is as wide as the widest, so the text lines up.
	for _, r := range rows {
		require.Len(t, r.line, 3)
		require.Len(t, r.cont, 3)
	}
}

func TestGraphMergeCrossesALane(t *testing.T) {
	t.Parallel()
	// m merges y, whose lane is on the far side of x's.
	rows := layoutGraph(graphCommits("y2:y", "x:base", "m:a,y", "a:base", "y:base", "base"))
	require.Equal(t, []string{
		"●",
		"│ ●",
		"├─┼─○",
		"│ │ ●",
		"● │ │",
		"●─┴─╯",
	}, graphLines(rows))
}

func TestGitPaneGraphRows(t *testing.T) {
	t.Parallel()
	u := newGitPaneUI(t)
	now := time.Now()
	u.git.commits = []gh.Commit{
		{Hash: "m1", ID: "m1", Parents: []string{"a1", "b1"}, When: now, Subject: "Merge feature",
			Refs: []gh.Ref{{Name: "feature", Kind: gh.RefBranch, Head: true}, {Name: "origin/feature", Kind: gh.RefRemote}}},
		{Hash: "b1", ID: "b1", Parents: []string{"r1"}, When: now, Subject: "Side work",
			Refs: []gh.Ref{{Name: "other", Kind: gh.RefBranch}}},
		{Hash: "a1", ID: "a1", Parents: []string{"r1"}, When: now, Subject: "Main work"},
		{Hash: "r1", ID: "r1", When: now, Subject: "Root"},
	}
	text := ansi.Strip(u.gitPaneRender(gitPaneWidth, 60, false))
	require.Contains(t, text, "○─╮ m1 (HEAD → feature")
	require.Contains(t, text, "│ ● b1 (other) Side work")
	require.Contains(t, text, "●─╯ r1 Root")
	// The branches line up with their tips: other's tip is on lane 1.
	require.Contains(t, text, "  ●   other")
}

func TestGitPaneSelectedRowWraps(t *testing.T) {
	t.Parallel()
	u := newGitPaneUI(t)
	long := "Make the sidebar strip fill the width it is given and stop the pager floating over the status bar"
	u.git.commits[1].Subject = long
	rows := u.gitRootRows(gitPaneWidth)
	idx := -1
	for i, r := range rows {
		if r.kind == gitRowCommit && r.name == "40da179" {
			idx = i
		}
	}
	require.GreaterOrEqual(t, idx, 0)

	// Unselected, the subject is cut to one line.
	unfocused := u.gitBodyLines(gitPaneWidth, false)
	require.NotContains(t, strings.Join(unfocused.lines, ""), "status bar")

	u.git.pane.root.selectIn(rows, idx)
	body := u.gitBodyLines(gitPaneWidth, true)
	require.Greater(t, body.selEnd-body.sel, 1, "the selected row wraps")
	var got strings.Builder
	for _, l := range body.lines[body.sel:body.selEnd] {
		require.LessOrEqual(t, ansi.StringWidth(l), gitPaneWidth)
		for _, f := range strings.Fields(ansi.Strip(l)) {
			if strings.ContainsAny(f, "│●○╮╯") || f == "just" || f == "now" {
				continue
			}
			got.WriteString(f + " ")
		}
	}
	require.Contains(t, got.String(), long)
	for i := body.sel; i < body.selEnd; i++ {
		require.Equal(t, idx, body.items[i], "every wrapped line clicks to the row")
	}

	// j/k still move a row at a time, and the whole wrapped row is kept in
	// view in a short pane.
	u.git.pane.root.selectIn(rows, idx-1)
	gitPress(t, u, "down")
	require.Equal(t, "40da179", gitSelectedRow(u).name)
	out := ansi.Strip(u.gitPaneRender(gitPaneWidth, 8, true))
	require.Contains(t, out, "status bar")
	gitPress(t, u, "up")
	require.Equal(t, "a94d819", gitSelectedRow(u).name)
	gitPress(t, u, "down", "down")
	require.Equal(t, "40da179", gitSelectedRow(u).name, "the last row stays selected")
}

func TestGitPaneCommitViewShowsMessage(t *testing.T) {
	t.Parallel()
	u := newGitPaneUI(t)
	long := "A subject long enough that the pane cannot show it on one line of forty eight cells"
	u.git.pane.push(&gitView{kind: gitViewCommit, hash: "a94d819", commit: &gh.CommitDetail{
		Commit: gh.Commit{Hash: "a94d819", Subject: long, When: time.Now(), Author: "devban"},
		Body:   "First paragraph of the body.\n\nSecond paragraph, which says why.",
		Files:  []gh.FileStat{{Path: "internal/ui/model/a/really/deep/path/to/some/file_with_a_long_name.go", Added: 3}},
	}})
	body := u.gitBodyLines(gitPaneWidth, true)
	var words []string
	for _, l := range body.lines {
		require.LessOrEqual(t, ansi.StringWidth(l), gitPaneWidth)
		for _, f := range strings.Fields(ansi.Strip(l)) {
			// Leave out the line counts drawn at the right edge.
			if !strings.HasPrefix(f, "+") && !strings.HasPrefix(f, "−") {
				words = append(words, f)
			}
		}
	}
	all := strings.Join(words, " ")
	require.Contains(t, all, long)
	require.Contains(t, all, "First paragraph of the body.")
	require.Contains(t, all, "Second paragraph, which says why.")
	require.Contains(t, strings.ReplaceAll(all, " ", ""), "internal/ui/model/a/really/deep/path/to/some/file_with_a_long_name.go")
}

func TestGitPanePRDescription(t *testing.T) {
	t.Parallel()
	u, v := gitPRViewUI(t)
	v.pr.Title = "A pull request title that is far too long to fit on a single row of the pane"
	v.pr.Body = "Why this change.\r\n\r\n" + strings.Repeat("A long line of the description. ", 30)
	text := ansi.Strip(strings.Join(u.gitBodyLines(gitPaneWidth, true).lines, " "))
	require.Contains(t, strings.Join(strings.Fields(text), " "), v.pr.Title)
	require.Contains(t, text, "d read")

	require.True(t, gitPress(t, u, "d"))
	d := u.git.pane.top()
	require.Equal(t, gitViewDesc, d.kind)
	u.gitPaneRender(gitPaneWidth, 10, true)
	require.True(t, gitPress(t, u, "down"))
	require.Equal(t, 1, d.off, "the description scrolls")
	require.True(t, gp.wheel(u, 3))
	require.Equal(t, 4, d.off, "and the wheel scrolls it too")
	require.True(t, gitPress(t, u, "esc"))
	require.Equal(t, gitViewPR, u.git.pane.top().kind)
}
