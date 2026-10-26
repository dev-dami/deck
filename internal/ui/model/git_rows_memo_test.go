package model

import (
	"errors"
	"fmt"
	"math/rand"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dev-dami/deck/internal/deck/gh"
	"github.com/dev-dami/deck/internal/ui/common"
	"github.com/dev-dami/deck/internal/ui/styles"
)

// newGitBenchUI is a UI whose git state fills the pane the way a busy
// repository does: a page of changed files, open pull requests, the
// recent branches and a commit graph with merges.
func newGitBenchUI(tb testing.TB) *UI {
	tb.Helper()
	sty := styles.DeckDark()
	u := &UI{com: &common.Common{Workspace: &testWorkspace{}, Styles: &sty}}
	u.gitClient()
	now := time.Now()
	g := &u.git
	g.statusOK = true
	g.status = gh.Status{Branch: "feature", Upstream: "origin/feature", Ahead: 2, Dirty: 10}
	for i := range 10 {
		g.files = append(g.files, gh.ChangedFile{Code: "M", Path: fmt.Sprintf("internal/pkg%d/file%d.go", i, i)})
	}
	g.ghChecked, g.ghAvail = true, true
	pr := &gh.PR{Number: 12, Title: "Add the form", State: "OPEN", HeadRefName: "feature", BaseRefName: "main",
		Checks: []gh.Check{{Name: "build", Bucket: gh.BucketPass}, {Name: "test", Bucket: gh.BucketPending}}}
	g.pr, g.prBranch, g.checks = pr, "feature", gh.Summarize(pr.Checks)
	prs := []gh.PR{*pr}
	for i := range 5 {
		prs = append(prs, gh.PR{Number: 20 + i, Title: fmt.Sprintf("Another pull request with a longer title %d", i),
			HeadRefName: "b" + strconv.Itoa(i), Checks: pr.Checks, IsDraft: i%2 == 0})
	}
	g.setHeads(prs, now)
	g.recent = append(g.recent, gh.Branch{Name: "feature", Upstream: "origin/feature", Committed: now.Add(-4 * time.Minute)})
	for i := range gitRecentBranches - 1 {
		g.recent = append(g.recent, gh.Branch{Name: "b" + strconv.Itoa(i), Upstream: "origin/b" + strconv.Itoa(i),
			Committed: now.Add(-time.Duration(i+1) * 37 * time.Minute)})
	}
	for i := range gitRecentCommits {
		c := gh.Commit{Hash: fmt.Sprintf("%07x", 0xa94d819+i), ID: "c" + strconv.Itoa(i),
			When: now.Add(-time.Duration(i) * 23 * time.Minute), Subject: fmt.Sprintf("Commit subject number %d that goes on", i)}
		if i+1 < gitRecentCommits {
			c.Parents = append(c.Parents, "c"+strconv.Itoa(i+1))
		}
		if i%5 == 0 && i+3 < gitRecentCommits {
			c.Parents = append(c.Parents, "c"+strconv.Itoa(i+3))
		}
		if i == 0 {
			c.Refs = []gh.Ref{{Name: "feature", Kind: gh.RefHead}}
		}
		g.commits = append(g.commits, c)
	}
	return u
}

func TestGitAgoUntil(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	now := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	require.True(t, gitAgoUntil(time.Time{}, now).IsZero())
	for range 5000 {
		// Up to a week back, and a little ahead (a skewed clock).
		at := now.Add(-time.Duration(r.Int63n(int64(7*24*time.Hour))) + time.Duration(r.Int63n(int64(2*time.Minute))))
		until := gitAgoUntil(at, now)
		require.True(t, until.After(now), "at %v", at)
		text := gitAgo(at, now)
		mid := now.Add(time.Duration(r.Int63n(int64(until.Sub(now)))))
		require.Equal(t, text, gitAgo(at, mid), "at %v, %v into it", at, mid.Sub(now))
		require.Equal(t, text, gitAgo(at, until.Add(-time.Nanosecond)), "at %v, just before %v", at, until)
		require.NotEqual(t, text, gitAgo(at, until), "at %v, at %v", at, until)
	}
}

// requireGitRowsFresh checks that the kept rows are what building them
// afresh gives, at now, and that the whole pane draws the same as with
// nothing kept.
func requireGitRowsFresh(t *testing.T, u *UI, width int, now time.Time, step string) {
	t.Helper()
	got := u.gitRootRowsAt(width, now)
	want, _ := u.buildGitRootRows(width, now)
	require.Equal(t, want, got, step)
	require.Equal(t, want, u.gitRootRowsAt(width, now), step+": again")

	// The pane draws at the wall clock; put the rows kept at now back
	// after, so the next step starts from them.
	kept := u.git.rows
	drawn := gp.render(u, width, 60, true)
	u.git.rows = gitRowsMemo{}
	require.Equal(t, gp.render(u, width, 60, true), drawn, step+": the pane")
	u.git.rows = kept
}

// TestGitRowsMemoMatchesUncached walks the git state through its writes,
// the theme, the width and the clock, and checks the kept rows against
// rows built afresh at every step.
func TestGitRowsMemoMatchesUncached(t *testing.T) {
	u := newGitBenchUI(t)
	g := &u.git
	w := gitPaneWidth
	now := time.Now()
	requireGitRowsFresh(t, u, w, now, "start")

	first := u.gitRootRowsAt(w, now)
	require.Same(t, &first[0], &u.gitRootRowsAt(w, now)[0], "nothing changed: the rows are kept")

	files := append(slices.Clone(g.files), gh.ChangedFile{Code: "A", Path: "new.go"})
	u.applyGitMsg(gitStatusMsg{status: g.status, files: files, same: true})
	requireGitRowsFresh(t, u, w, now, "a file changed")

	st := g.status
	st.Ahead, st.Behind = 0, 3
	u.applyGitMsg(gitStatusMsg{status: st, files: files, recent: g.recent[:4], commits: g.commits[:6]})
	requireGitRowsFresh(t, u, w, now, "status, branches and commits read again")

	u.applyGitMsg(gitStatusMsg{err: errors.New("fatal: not a git repository")})
	requireGitRowsFresh(t, u, w, now, "status failed")

	u.applyGitMsg(gitStatusMsg{status: st, files: files, recent: g.recent, commits: g.commits})
	requireGitRowsFresh(t, u, w, now, "status back")

	failing := &gh.PR{Number: 12, Title: "Add the form, renamed", State: "OPEN", HeadRefName: "feature",
		Checks: []gh.Check{{Name: "build", Bucket: gh.BucketFail}}}
	u.applyGitMsg(gitPRMsg{branch: "feature", avail: true, pr: failing, listed: true, prs: []gh.PR{*failing}})
	requireGitRowsFresh(t, u, w, now, "pull request polled")

	u.applyGitMsg(gitPRMsg{branch: "feature", avail: false})
	requireGitRowsFresh(t, u, w, now, "gh went away")

	u.applyGitMsg(gitPRMsg{branch: "feature", avail: true, pr: failing})
	requireGitRowsFresh(t, u, w, now, "gh is back")

	g.pane.push(&gitView{kind: gitViewPR, number: 12, loading: true})
	passing := *failing
	passing.Checks = []gh.Check{{Name: "build", Bucket: gh.BucketPass}}
	u.applyGitMsg(gitPRDetailMsg{req: 12, pr: &passing, checks: passing.Checks})
	g.pane.toRoot()
	requireGitRowsFresh(t, u, w, now, "pull request view read it")

	u.gitRefreshAll()
	requireGitRowsFresh(t, u, w, now, "refresh asked for")

	g.setHeads([]gh.PR{passing, {Number: 30, Title: "Late", HeadRefName: "b2"}}, now)
	requireGitRowsFresh(t, u, w, now, "pull requests listed")

	u.gitPoll()
	u.gitMaybePollPR(now)
	requireGitRowsFresh(t, u, w, now, "polls started")

	requireGitRowsFresh(t, u, w-10, now, "narrower")

	light := styles.DeckLight()
	*u.com.Styles = light
	u.stylesGen++
	requireGitRowsFresh(t, u, w, now, "theme changed")
	other := styles.DeckDark()
	u.com.Styles = &other
	requireGitRowsFresh(t, u, w, now, "styles replaced")

	// Two hours in ten-second steps: every relative time on the rows
	// rolls over (minutes, hours), and a kept row must never lag.
	for i := range 720 {
		requireGitRowsFresh(t, u, w, now.Add(time.Duration(i)*10*time.Second), fmt.Sprintf("%ds later", i*10))
	}
}

// benchGitFrame draws the git pane the way the strip does each frame:
// its height, then its render, inside one frame memo.
func benchGitFrame(u *UI) {
	u.beginFrameMemo()
	h := gp.height(u, gitPaneWidth)
	_ = gp.render(u, gitPaneWidth, h, true)
	u.endFrameMemo()
}

// BenchmarkGitPaneFrame draws the pane frame after frame: with the git
// state as it was (kept) and with a write before every frame (changed).
func BenchmarkGitPaneFrame(b *testing.B) {
	for _, write := range []bool{false, true} {
		name := "kept"
		if write {
			name = "changed"
		}
		b.Run(name, func(b *testing.B) {
			u := newGitBenchUI(b)
			benchGitFrame(u)
			b.ReportAllocs()
			for b.Loop() {
				if write {
					u.git.touch()
				}
				benchGitFrame(u)
			}
		})
	}
}
