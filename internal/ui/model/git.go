package model

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	deckconfig "github.com/dev-dami/deck/internal/deck/config"
	"github.com/dev-dami/deck/internal/deck/gh"
	"github.com/dev-dami/deck/internal/ui/dialog"
	"github.com/dev-dami/deck/internal/ui/notification"
	"github.com/dev-dami/deck/internal/ui/util"
)

// Git polling. Status is a local read and cheap; GitHub is asked more often
// while checks run, and never when gh is missing or signed out (opening the
// git pane asks again).
const (
	gitStatusInterval = 10 * time.Second
	// gitListEvery is how many status polls go by between reads of the
	// recent branches and commits while HEAD stays put (other worktrees can
	// still add branches).
	gitListEvery = 6
	// gitPollTimeout bounds one status poll.
	gitPollTimeout       = 15 * time.Second
	gitStatusErrBackoff  = time.Minute
	gitPRPendingInterval = 20 * time.Second
	gitPRIdleInterval    = 2 * time.Minute
	gitPRListInterval    = 2 * time.Minute
	gitPRDetailInterval  = 20 * time.Second
	// gitNudgeWindow is how recent a pushed branch's last commit must be
	// for the "open a PR" nudge.
	gitNudgeWindow = time.Hour
	// gitRecentBranches is how many branches the pane lists.
	gitRecentBranches = 15
	// gitRecentCommits is how many commits the pane lists.
	gitRecentCommits = 20
)

// gitState is what the UI knows about the work tree's git and GitHub state,
// for the git pane, the header badge and the nudge above the composer. It
// is only touched in Update; reads happen in commands that report back with
// gitMsg messages.
type gitState struct {
	client *gh.Client

	status     gh.Status
	statusOK   bool
	statusAt   time.Time
	statusBusy bool
	polls      int // status polls so far, for gitListEvery
	// statusErr says why there is no status ("Not a git repository").
	statusErr string
	files     []gh.ChangedFile
	recent    []gh.Branch
	commits   []gh.Commit

	ghChecked bool
	ghAvail   bool

	// pr is prBranch's pull request (nil: it has none), checks its checks.
	pr       *gh.PR
	prBranch string
	checks   gh.CheckSummary
	prAt     time.Time
	prBusy   bool

	// prs is the open pull requests and heads maps the head branch of each
	// to its number, as of headsAt.
	prs     []gh.PR
	heads   map[string]int
	headsAt time.Time

	// defaultBranch is the repository's default branch once read; it never
	// gets the nudge.
	defaultBranch string

	// dismissed is the branches whose nudge the user has seen.
	dismissed map[string]bool
	// verdicts is the last check state seen per pull request, to notice a
	// pending → passed/failed change.
	verdicts map[int]string

	// detailTick is set while a pull request view refresh is scheduled.
	detailTick bool

	// pane is the git pane's views and inputs.
	pane gitPaneState
}

// gitMsg is a message from one of the git commands; Update hands them all
// to handleGitMsg.
type gitMsg interface{ gitMsg() }

type (
	// gitStatusMsg is a status poll's result.
	gitStatusMsg struct {
		status  gh.Status
		files   []gh.ChangedFile
		recent  []gh.Branch
		commits []gh.Commit
		same    bool // recent and commits were not read: keep the old ones
		err     error
	}
	// gitPRMsg is a pull request poll's result. prs is set when the open
	// pull requests were listed too.
	gitPRMsg struct {
		branch string
		avail  bool
		pr     *gh.PR
		err    error
		prs    []gh.PR
		listed bool
	}
	// gitOpMsg is a finished git or gh operation.
	gitOpMsg struct {
		info         string
		err          error
		refresh      bool
		thenCreatePR bool
		job          string // the Commands pane's job for it
		// openPR is a pull request to show when it worked (one just
		// opened); it takes the create view's place.
		openPR int
	}
	// gitPRDetailMsg is a pull request read for the pull request view; req
	// is the number asked for (0: the current branch's).
	gitPRDetailMsg struct {
		req    int
		pr     *gh.PR
		checks []gh.Check
		err    error
	}
	// gitPRDetailTickMsg refreshes the pull request view.
	gitPRDetailTickMsg struct{}
	// gitPrefillMsg is what the create pull request form starts with.
	gitPrefillMsg struct {
		branch, title, base, body, notes string
		existing                         int // the branch already has this pull request
		err                              error
	}
	// gitLogMsg is a failed check's log.
	gitLogMsg struct {
		number int
		check  gh.Check
		log    string
		err    error
	}
	// gitHandoffMsg is the composer text that hands a failed log to an
	// agent.
	gitHandoffMsg struct {
		text string
		err  error
	}
	// gitShowMsg is a commit read for the commit view.
	gitShowMsg struct {
		hash   string
		detail *gh.CommitDetail
		err    error
	}
	// gitPaneTickMsg turns the git pane's spinner.
	gitPaneTickMsg struct{}
)

func (gitStatusMsg) gitMsg()       {}
func (gitPRMsg) gitMsg()           {}
func (gitOpMsg) gitMsg()           {}
func (gitPRDetailMsg) gitMsg()     {}
func (gitPRDetailTickMsg) gitMsg() {}
func (gitPrefillMsg) gitMsg()      {}
func (gitLogMsg) gitMsg()          {}
func (gitHandoffMsg) gitMsg()      {}
func (gitShowMsg) gitMsg()         {}
func (gitPaneTickMsg) gitMsg()     {}

// gitClient is the git/gh client for the workspace's directory.
func (m *UI) gitClient() *gh.Client {
	dir := m.com.Workspace.WorkingDir()
	if m.git.client == nil || m.git.client.Dir() != dir {
		m.git = gitState{client: gh.New(dir)}
	}
	return m.git.client
}

// gitPoll runs with the branch poll: a status read when it is due, and a
// pull request read when that is due.
func (m *UI) gitPoll() tea.Cmd {
	g := &m.git
	cli := m.gitClient()
	now := time.Now()
	var cmds []tea.Cmd
	if !g.statusBusy && now.Sub(g.statusAt) >= gitStatusInterval {
		g.statusBusy = true
		g.polls++
		cmds = append(cmds, gitFetchStatus(cli, g.status.Head, g.statusOK && g.polls%gitListEvery != 0))
	}
	if cmd := m.gitMaybePollPR(now); cmd != nil {
		cmds = append(cmds, cmd)
	}
	return tea.Batch(cmds...)
}

// gitMaybePollPR asks GitHub about the current branch's pull request when
// it is due: at once after a checkout, every 20s while checks run, every two
// minutes otherwise. The open pull requests are listed every two minutes
// for the pane.
func (m *UI) gitMaybePollPR(now time.Time) tea.Cmd {
	g := &m.git
	if g.prBusy || !g.statusOK || g.status.Branch == "" || (g.ghChecked && !g.ghAvail) {
		return nil
	}
	interval := gitPRIdleInterval
	if g.checks.State() == gh.ChecksPending {
		interval = gitPRPendingInterval
	}
	list := now.Sub(g.headsAt) >= gitPRListInterval
	if !list && g.prBranch == g.status.Branch && now.Sub(g.prAt) < interval {
		return nil
	}
	g.prBusy = true
	return gitFetchPR(g.client, g.status.Branch, list)
}

// nudgeable reports whether b could get the "open a PR" nudge, its pull
// request aside: pushed, committed to within the hour, not the default
// branch and not dismissed.
func (g *gitState) nudgeable(b gh.Branch, now time.Time) bool {
	if b.Upstream == "" || b.Committed.IsZero() || now.Sub(b.Committed) >= gitNudgeWindow || g.dismissed[b.Name] {
		return false
	}
	switch b.Name {
	case g.defaultBranch, "main", "master", "trunk", "develop":
		return false
	}
	return true
}

// gitNudge is the branch to nudge about and its last commit, if any: the
// current branch first, whose pull request is always known, then another
// recent one the open pull request list says has none.
func (m *UI) gitNudge() (string, time.Time) {
	g := &m.git
	if !g.ghAvail || !g.statusOK {
		return "", time.Time{}
	}
	now := time.Now()
	for _, b := range g.recent {
		if b.Name == g.status.Branch && g.prBranch == b.Name && g.pr == nil && g.nudgeable(b, now) {
			return b.Name, b.Committed
		}
	}
	if !g.headsAt.IsZero() {
		for _, b := range g.recent {
			if b.Name != g.status.Branch && g.heads[b.Name] == 0 && g.nudgeable(b, now) {
				return b.Name, b.Committed
			}
		}
	}
	return "", time.Time{}
}

// gitNudgeLine is the one-line hint drawn above the composer when a branch
// was pushed recently and has no pull request; "" otherwise.
func (m *UI) gitNudgeLine(width int) string {
	branch, at := m.gitNudge()
	if branch == "" || width <= 2 {
		return ""
	}
	muted := gitMuted(m.com.Styles)
	text := fmt.Sprintf("%s pushed %s · %s then P to open a PR", branch, gitAgo(at, time.Now()), gitKey)
	return " " + muted.Render(ansi.Truncate(text, width-2, "…"))
}

// gitHeaderBadge is the current branch's pull request and checks for the
// header: "#12 ✓", "#12 ● 3/5", "#12 ✗"; "" when there is none.
func (m *UI) gitHeaderBadge() string {
	g := &m.git
	if g.pr == nil || g.prBranch != m.gitBranch || m.gitBranch == "" {
		return ""
	}
	num := m.com.Styles.Header.WorkingDir.Render(fmt.Sprintf("#%d", g.pr.Number))
	if b := gitChecksBadge(g.checks); b != "" {
		return num + " " + b
	}
	return num
}

// handleGitMsg applies a git command's result.
func (m *UI) handleGitMsg(msg gitMsg) tea.Cmd {
	cmd := m.applyGitMsg(msg)
	return tea.Batch(cmd, m.gitPaneTick())
}

func (m *UI) applyGitMsg(msg gitMsg) tea.Cmd {
	g := &m.git
	p := &g.pane
	now := time.Now()
	switch msg := msg.(type) {
	case gitStatusMsg:
		g.statusBusy = false
		g.statusAt = now
		if msg.err != nil {
			g.statusOK = false
			g.statusErr = gitErrText(msg.err)
			g.statusAt = now.Add(gitStatusErrBackoff)
			p.refreshing = false
			return nil
		}
		g.status, g.statusOK, g.statusErr = msg.status, true, ""
		g.files = msg.files
		if !msg.same {
			g.recent, g.commits = msg.recent, msg.commits
		}
		cmd := m.gitMaybePollPR(now)
		if !g.prBusy {
			p.refreshing = false
		}
		return cmd

	case gitPRMsg:
		g.prBusy = false
		g.prAt = now
		p.refreshing = false
		g.ghChecked, g.ghAvail = true, msg.avail
		if !msg.avail {
			g.pr, g.prBranch, g.checks, g.prs = nil, "", gh.CheckSummary{}, nil
			return nil
		}
		if msg.listed {
			g.setHeads(msg.prs, now)
		}
		if msg.err != nil {
			// Keep what was known; the next poll tries again.
			return nil
		}
		return m.gitSetPR(msg.branch, msg.pr, nil)

	case gitOpMsg:
		m.jobDone(msg.job, msg.err)
		p.busy = ""
		var cmds []tea.Cmd
		if msg.err != nil {
			p.err, p.info = gitErrText(msg.err), ""
			cmds = append(cmds, gitReportErr(msg.err))
		} else {
			p.err, p.info = "", msg.info
			if msg.info != "" {
				cmds = append(cmds, util.ReportInfo(msg.info))
			}
			if msg.openPR > 0 {
				if p.top().kind == gitViewCreate {
					p.pop()
				}
				cmds = append(cmds, m.gitOpenPR(msg.openPR))
			}
			if msg.thenCreatePR {
				cmds = append(cmds, m.gitStartCreatePR())
			}
		}
		if msg.refresh {
			cmds = append(cmds, m.gitRefreshAll())
		}
		return tea.Batch(cmds...)

	case gitPRDetailMsg:
		v := p.findPR(msg.req)
		if v == nil {
			return nil
		}
		v.loading = false
		if msg.err != nil {
			v.err = gitErrText(msg.err)
			return nil
		}
		v.setPR(msg.pr, msg.checks)
		var cmds []tea.Cmd
		if msg.pr.HeadRefName == g.status.Branch && msg.pr.State == "OPEN" {
			cmds = append(cmds, m.gitSetPR(msg.pr.HeadRefName, msg.pr, msg.checks))
		}
		if v.pending() && !g.detailTick {
			g.detailTick = true
			cmds = append(cmds, tea.Tick(gitPRDetailInterval, func(time.Time) tea.Msg { return gitPRDetailTickMsg{} }))
		}
		return tea.Batch(cmds...)

	case gitPRDetailTickMsg:
		g.detailTick = false
		if v := p.top(); v.kind == gitViewPR && v.pending() && !v.loading {
			v.loading = true
			return m.gitLoadDetail(v.number)
		}
		return nil

	case gitPrefillMsg:
		v := p.top()
		if v.kind != gitViewCreate || v.form == nil || !v.form.preparing {
			// The user went elsewhere meanwhile.
			return nil
		}
		switch {
		case msg.err != nil:
			p.pop()
			p.err, p.info = gitErrText(msg.err), ""
			return gitReportErr(msg.err)
		case msg.existing > 0:
			p.pop()
			p.info = fmt.Sprintf("%s already has #%d", msg.branch, msg.existing)
			return m.gitOpenPR(msg.existing)
		}
		if msg.base != "" {
			g.defaultBranch = msg.base
		}
		return v.form.fill(msg.branch, msg.title, msg.base, msg.body, msg.notes)

	case gitLogMsg:
		p.busy = ""
		switch {
		case msg.err != nil:
			p.err = gitErrText(msg.err)
			return gitReportErr(msg.err)
		case strings.TrimSpace(msg.log) == "":
			p.info = "gh has no failed log for " + msg.check.Name + " (yet)"
			return nil
		}
		p.err, p.info = "", ""
		if p.top().kind == gitViewLog {
			p.pop()
		}
		p.push(&gitView{kind: gitViewLog, number: msg.number, check: msg.check, log: msg.log, off: gitBottom})
		return nil

	case gitHandoffMsg:
		p.busy = ""
		if msg.err != nil {
			p.err = gitErrText(msg.err)
			return gitReportErr(msg.err)
		}
		return m.gitPutInComposer(msg.text)

	case gitShowMsg:
		for _, v := range p.stack {
			if v.kind == gitViewCommit && v.hash == msg.hash {
				v.loading = false
				if msg.err != nil {
					v.err = gitErrText(msg.err)
				} else {
					v.commit = msg.detail
				}
			}
		}
		return nil

	case gitPaneTickMsg:
		p.ticking = false
		p.frame++
		return nil
	}
	return nil
}

// setHeads records the open pull requests and their head branches.
func (g *gitState) setHeads(prs []gh.PR, now time.Time) {
	g.prs = prs
	g.heads = make(map[string]int, len(prs))
	for _, p := range prs {
		g.heads[p.HeadRefName] = p.Number
	}
	g.headsAt = now
}

// gitSetPR records branch's pull request and its checks (checks overrides
// the pull request's rollup when set), and says so when its checks just
// finished.
func (m *UI) gitSetPR(branch string, pr *gh.PR, checks []gh.Check) tea.Cmd {
	g := &m.git
	g.pr, g.prBranch = pr, branch
	if pr == nil {
		g.checks = gh.CheckSummary{}
		return nil
	}
	if checks == nil {
		checks = pr.Checks
	}
	g.checks = gh.Summarize(checks)
	if g.heads == nil {
		g.heads = map[string]int{}
	}
	g.heads[branch] = pr.Number
	if g.verdicts == nil {
		g.verdicts = map[int]string{}
	}
	prev, cur := g.verdicts[pr.Number], g.checks.State()
	g.verdicts[pr.Number] = cur
	if prev != gh.ChecksPending || (cur != gh.ChecksPassed && cur != gh.ChecksFailed) {
		return nil
	}
	text := fmt.Sprintf("CI passed on #%d", pr.Number)
	report := util.ReportInfo(text)
	if cur == gh.ChecksFailed {
		text = fmt.Sprintf("CI failed on #%d — %s to see why", pr.Number, gitKey)
		report = util.ReportWarn(text)
	}
	return tea.Batch(
		report,
		m.sendNotification(notification.Notification{Title: "Deck", Message: text}),
		gitBell(),
	)
}

// gitBell rings the terminal bell when the user's Deck settings allow it.
func gitBell() tea.Cmd {
	return func() tea.Msg {
		cfg, err := deckconfig.Load()
		if err != nil || !cfg.Bell {
			return nil
		}
		return tea.Raw("\a")()
	}
}

// gitReportErr reports a failed git or gh command.
func gitReportErr(err error) tea.Cmd {
	if errors.Is(err, gh.ErrNoGH) {
		return util.ReportWarn("GitHub needs the gh CLI: " + gitNoGHHint)
	}
	return util.ReportError(errors.New(gitErrText(err)))
}

// gitErrText is a failed command's error as a line for the user.
func gitErrText(err error) string {
	s := err.Error()
	if strings.Contains(strings.ToLower(s), "not a git repository") {
		return "not a git repository"
	}
	return s
}

// gitFetchStatus reads the status with the changed files, the recent
// branches and the recent commits. When reuse is set and HEAD is still head,
// the branches and commits are not read again (msg.same), so a quiet poll is
// one git process instead of three.
func gitFetchStatus(cli *gh.Client, head string, reuse bool) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), gitPollTimeout)
		defer cancel()
		st, files, err := cli.StatusFiles(ctx)
		if err != nil {
			return gitStatusMsg{err: err}
		}
		if reuse && head != "" && st.Head == head {
			return gitStatusMsg{status: st, files: files, same: true}
		}
		recent, _ := cli.RecentBranches(ctx, gitRecentBranches)
		commits, _ := cli.Graph(ctx, gitRecentCommits)
		return gitStatusMsg{status: st, files: files, recent: recent, commits: commits}
	}
}

// gitFetchPR reads branch's pull request and, when list is set, the open
// pull requests.
func gitFetchPR(cli *gh.Client, branch string, list bool) tea.Cmd {
	return func() tea.Msg {
		ctx := context.Background()
		msg := gitPRMsg{branch: branch}
		if !cli.Available(ctx) {
			return msg
		}
		msg.avail = true
		msg.pr, msg.err = cli.PRForBranch(ctx, branch)
		if list {
			if prs, err := cli.PRList(ctx); err == nil {
				msg.prs, msg.listed = prs, true
			}
		}
		return msg
	}
}

// openGitPane focuses the git pane (ctrl+x) and refreshes it. A branch the
// composer nudged about is selected, and the nudge goes away.
func (m *UI) openGitPane() tea.Cmd {
	m.gitClient().Recheck()
	cmds := []tea.Cmd{m.focusPane(paneGit), m.gitRefreshAll()}
	if nudge, _ := m.gitNudge(); nudge != "" {
		if m.git.dismissed == nil {
			m.git.dismissed = map[string]bool{}
		}
		m.git.dismissed[nudge] = true
		if nudge != m.git.status.Branch {
			p := &m.git.pane
			p.toRoot()
			p.root.selKey = gitRowKey(gitRowRecent, nudge)
		}
	}
	return tea.Batch(cmds...)
}

// handleGitPaneAction runs a git command from the palette: it focuses the
// git pane and goes to the view or starts the action asked for.
func (m *UI) handleGitPaneAction(a dialog.ActionGitPane) tea.Cmd {
	m.dialog.CloseDialog(dialog.CommandsID)
	open := m.openGitPane()
	p := &m.git.pane
	var cmd tea.Cmd
	switch a.Do {
	case dialog.GitPaneCreatePR:
		p.toRoot()
		cmd = m.gitStartCreatePR()
	case dialog.GitPanePRs:
		p.toRoot()
		p.root.selKey = gitSectionKey(gitSectionPRs)
	case dialog.GitPaneChecks:
		p.toRoot()
		cmd = m.gitOpenPR(0)
	case dialog.GitPanePush:
		p.toRoot()
		cmd = m.gitPush()
	case dialog.GitPaneCommit:
		p.toRoot()
		cmd = m.gitStartCommit()
	}
	return tea.Batch(open, cmd, m.gitPaneTick())
}

// gitRefreshAll re-reads after something changed: the status at once, the
// pull request and the open pull requests right after, and the pull
// request or commit the pane shows.
func (m *UI) gitRefreshAll() tea.Cmd {
	g := &m.git
	cli := m.gitClient()
	g.prAt = time.Time{}
	g.headsAt = time.Time{}
	var cmds []tea.Cmd
	if !g.statusBusy {
		g.statusBusy = true
		cmds = append(cmds, gitFetchStatus(cli, "", false))
	}
	if v := g.pane.top(); v.kind == gitViewPR && !v.loading {
		v.loading = true
		cmds = append(cmds, m.gitLoadDetail(v.number))
	}
	return tea.Batch(cmds...)
}

// gitOp runs a git or gh operation off the UI loop, showing label with a
// spinner on the git pane's status line and as a job in the Commands pane.
func (m *UI) gitOp(label string, run func(context.Context) gitOpMsg) tea.Cmd {
	p := &m.git.pane
	p.busy, p.err, p.info = label, "", ""
	job := m.startJob("git", label)
	return tea.Batch(m.gitPaneTick(), func() tea.Msg {
		msg := run(context.Background())
		msg.job = job
		return msg
	})
}

// gitCommitAll stages everything and commits it with msg.
func (m *UI) gitCommitAll(msg string) tea.Cmd {
	cli := m.gitClient()
	return m.gitOp("Committing…", func(ctx context.Context) gitOpMsg {
		err := cli.CommitAll(ctx, msg)
		return gitOpMsg{info: "Committed: " + firstLineOf(msg), err: err, refresh: true}
	})
}

// gitPush pushes the current branch.
func (m *UI) gitPush() tea.Cmd {
	cli := m.gitClient()
	branch := m.git.status.Branch
	return m.gitOp("Pushing…", func(ctx context.Context) gitOpMsg {
		err := cli.Push(ctx)
		info := "Pushed"
		if branch != "" {
			info = "Pushed " + branch
		}
		return gitOpMsg{info: info, err: err, refresh: true}
	})
}

// gitFetch fetches from the remotes.
func (m *UI) gitFetch() tea.Cmd {
	cli := m.gitClient()
	return m.gitOp("Fetching…", func(ctx context.Context) gitOpMsg {
		return gitOpMsg{info: "Fetched", err: cli.Fetch(ctx), refresh: true}
	})
}

// gitCheckout checks out branch, then starts a pull request for it when
// thenPR is set.
func (m *UI) gitCheckout(branch string, thenPR bool) tea.Cmd {
	cli := m.gitClient()
	return m.gitOp("Checking out "+branch+"…", func(ctx context.Context) gitOpMsg {
		err := cli.Checkout(ctx, branch)
		return gitOpMsg{info: "On " + branch + " now", err: err, refresh: true, thenCreatePR: thenPR}
	})
}

// gitSubmitPR pushes the branch if it needs it and opens the pull request.
func (m *UI) gitSubmitPR(title, base, body string, draft bool) tea.Cmd {
	cli := m.gitClient()
	return m.gitOp("Opening the pull request…", func(ctx context.Context) gitOpMsg {
		st, err := cli.Status(ctx)
		if err != nil {
			return gitOpMsg{err: err}
		}
		if st.Branch == "" {
			return gitOpMsg{err: errors.New("HEAD is detached; check out a branch first")}
		}
		if st.Upstream == "" || st.Ahead > 0 {
			if err := cli.Push(ctx); err != nil {
				return gitOpMsg{err: err}
			}
		}
		n, url, err := cli.CreatePR(ctx, title, body, base, draft)
		if err != nil {
			return gitOpMsg{err: err, refresh: true}
		}
		return gitOpMsg{info: fmt.Sprintf("PR #%d opened · %s", n, url), openPR: n, refresh: true}
	})
}

// gitRerun re-runs the failed jobs of pull request number's workflow runs.
func (m *UI) gitRerun(number int, runIDs []string) tea.Cmd {
	cli := m.gitClient()
	return m.gitOp("Re-running the failed jobs…", func(ctx context.Context) gitOpMsg {
		for _, id := range runIDs {
			if err := cli.RerunFailed(ctx, id); err != nil {
				return gitOpMsg{err: err, refresh: true}
			}
		}
		return gitOpMsg{info: fmt.Sprintf("Re-running the failed jobs on #%d", number), refresh: true}
	})
}

// gitMerge merges pull request number by method.
func (m *UI) gitMerge(number int, method string) tea.Cmd {
	cli := m.gitClient()
	return m.gitOp(fmt.Sprintf("Merging #%d…", number), func(ctx context.Context) gitOpMsg {
		err := cli.Merge(ctx, number, method)
		return gitOpMsg{info: fmt.Sprintf("Merged #%d (%s)", number, method), err: err, refresh: true}
	})
}

// gitCheckoutPR checks out pull request number's branch.
func (m *UI) gitCheckoutPR(number int) tea.Cmd {
	cli := m.gitClient()
	return m.gitOp(fmt.Sprintf("Checking out #%d…", number), func(ctx context.Context) gitOpMsg {
		err := cli.CheckoutPR(ctx, number)
		return gitOpMsg{info: fmt.Sprintf("Checked out #%d", number), err: err, refresh: true}
	})
}

// gitOpenWeb opens pull request number in the browser.
func (m *UI) gitOpenWeb(number int) tea.Cmd {
	cli := m.gitClient()
	return m.gitOp("Opening the browser…", func(ctx context.Context) gitOpMsg {
		err := cli.OpenWeb(ctx, number)
		return gitOpMsg{info: fmt.Sprintf("Opened #%d in the browser", number), err: err}
	})
}

// gitFailedLog fetches check's failed log for the log view.
func (m *UI) gitFailedLog(number int, check gh.Check) tea.Cmd {
	cli := m.gitClient()
	p := &m.git.pane
	p.busy, p.err, p.info = "Fetching the failed log…", "", ""
	return tea.Batch(m.gitPaneTick(), func() tea.Msg {
		log, err := cli.FailedLog(context.Background(), check.RunID(), check.JobID())
		return gitLogMsg{number: number, check: check, log: log, err: err}
	})
}

// gitHandoff hands check's failed log to an agent through the composer,
// fetching the log first when log is empty.
func (m *UI) gitHandoff(number int, check gh.Check, log string) tea.Cmd {
	p := &m.git.pane
	p.err, p.info = "", ""
	if log == "" {
		p.busy = "Fetching the failed log…"
	}
	return tea.Batch(m.gitPaneTick(), gitHandoffCmd(m.gitClient(), number, check, log))
}

// firstLineOf is s up to its first newline.
func firstLineOf(s string) string {
	l, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	return l
}

// gitStartCreatePR opens the create pull request view in the pane and
// reads what its form starts with (or shows the branch's pull request, if
// it has one).
func (m *UI) gitStartCreatePR() tea.Cmd {
	cli := m.gitClient()
	cli.Recheck()
	p := &m.git.pane
	p.err, p.info = "", ""
	if v := p.top(); v.kind == gitViewCreate {
		p.pop()
	}
	p.push(&gitView{kind: gitViewCreate, form: newGitPRForm(m.com.Styles)})
	return tea.Batch(m.gitPaneTick(), func() tea.Msg {
		ctx := context.Background()
		if !cli.Available(ctx) {
			return gitPrefillMsg{err: gh.ErrNoGH}
		}
		st, err := cli.Status(ctx)
		if err != nil {
			return gitPrefillMsg{err: err}
		}
		if st.Branch == "" {
			return gitPrefillMsg{err: errors.New("HEAD is detached; check out a branch first")}
		}
		if pr, err := cli.PRForBranch(ctx, st.Branch); err == nil && pr != nil && pr.State == "OPEN" {
			return gitPrefillMsg{branch: st.Branch, existing: pr.Number}
		}
		base := cli.DefaultBranch(ctx)
		if st.Branch == base {
			return gitPrefillMsg{err: fmt.Errorf("%s is the default branch; make a branch for the change first", base)}
		}
		title, _ := cli.LastSubject(ctx)
		subjects, _ := cli.LogSince(ctx, base)
		return gitPrefillMsg{
			branch: st.Branch,
			title:  title,
			base:   base,
			body:   gitPRBody(subjects, title),
			notes:  gitPRNotes(st),
		}
	})
}

// gitPRBody is a pull request body listing the commits, oldest first; empty
// when the only commit is the title.
func gitPRBody(subjects []string, title string) string {
	if len(subjects) == 0 || (len(subjects) == 1 && subjects[0] == title) {
		return ""
	}
	subjects = slices.Clone(subjects)
	slices.Reverse(subjects)
	var b strings.Builder
	for _, s := range subjects {
		b.WriteString("- " + s + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// gitPRNotes says what submitting will do besides opening the pull request.
func gitPRNotes(st gh.Status) string {
	var notes []string
	switch {
	case st.Upstream == "":
		notes = append(notes, "pushes the branch first")
	case st.Ahead > 0:
		notes = append(notes, fmt.Sprintf("pushes %d commit(s) first", st.Ahead))
	}
	if st.Dirty > 0 {
		notes = append(notes, fmt.Sprintf("%d uncommitted change(s) stay out", st.Dirty))
	}
	return strings.Join(notes, " · ")
}

// gitOpenPR shows pull request number (0: the current branch's) in the
// pane and loads it.
func (m *UI) gitOpenPR(number int) tea.Cmd {
	g := &m.git
	m.gitClient().Recheck()
	p := &g.pane
	v := &gitView{kind: gitViewPR, number: number, loading: true}
	if g.pr != nil && ((number == 0 && g.prBranch == g.status.Branch) || g.pr.Number == number) {
		// Show what is known at once; the read below refreshes it.
		v.setPR(g.pr, nil)
	}
	if t := p.top(); t.kind == gitViewPR && t.number == v.number && v.number != 0 {
		p.pop()
	}
	p.push(v)
	return tea.Batch(m.gitPaneTick(), m.gitLoadDetail(number))
}

// gitLoadDetail reads pull request number (0: the current branch's) and its
// checks for the pull request view.
func (m *UI) gitLoadDetail(number int) tea.Cmd {
	cli := m.gitClient()
	return func() tea.Msg {
		ctx := context.Background()
		var pr *gh.PR
		var err error
		if number == 0 {
			pr, err = cli.PRForBranch(ctx, "")
			if err == nil && pr == nil {
				err = errors.New("this branch has no pull request yet; P opens one")
			}
		} else {
			pr, err = cli.PRView(ctx, number)
		}
		if err != nil {
			return gitPRDetailMsg{req: number, err: err}
		}
		// gh pr checks knows more than the rollup; without it, the
		// rollup is shown.
		checks, _ := cli.Checks(ctx, pr.Number)
		return gitPRDetailMsg{req: number, pr: pr, checks: checks}
	}
}

// gitOpenCommit shows commit hash in the pane and loads it.
func (m *UI) gitOpenCommit(hash string) tea.Cmd {
	cli := m.gitClient()
	m.git.pane.push(&gitView{kind: gitViewCommit, hash: hash, loading: true})
	return tea.Batch(m.gitPaneTick(), func() tea.Msg {
		d, err := cli.Show(context.Background(), hash)
		return gitShowMsg{hash: hash, detail: d, err: err}
	})
}

// gitHandoffCmd builds the composer text that hands a failed check to the
// user's default agent, fetching the log when there is none.
func gitHandoffCmd(cli *gh.Client, number int, check gh.Check, log string) tea.Cmd {
	return func() tea.Msg {
		if log == "" {
			l, err := cli.FailedLog(context.Background(), check.RunID(), check.JobID())
			if err != nil {
				return gitHandoffMsg{err: err}
			}
			log = l
		}
		agent := "claude"
		if cfg, err := deckconfig.Load(); err == nil && cfg.DefaultAgent != "" {
			agent = cfg.DefaultAgent
		}
		return gitHandoffMsg{text: gitHandoffText(agent, number, check.Name, log)}
	}
}

// gitHandoffText is "$agent CI failed on #N (check):" and the log in a code
// fence long enough not to be closed by the log itself.
func gitHandoffText(agent string, number int, check, log string) string {
	fence := "```"
	for strings.Contains(log, fence) {
		fence += "`"
	}
	return fmt.Sprintf("$%s CI failed on #%d (%s):\n%s\n%s\n%s", agent, number, check, fence, strings.TrimRight(log, "\n"), fence)
}

// gitPutInComposer puts text in the composer, after whatever is there,
// without sending it, and moves the focus there.
func (m *UI) gitPutInComposer(text string) tea.Cmd {
	prevHeight := m.textarea.Height()
	if cur := m.textarea.Value(); strings.TrimSpace(cur) != "" {
		text = strings.TrimRight(cur, "\n") + "\n\n" + text
	}
	m.textarea.SetValue(text)
	m.textarea.MoveToEnd()
	m.focus = uiFocusEditor
	m.chat.Blur()
	m.git.pane.info = "The failed log is in the composer"
	return tea.Batch(
		m.textarea.Focus(),
		m.handleTextareaHeightChange(prevHeight),
		util.ReportInfo("The failed log is in the composer: change the $agent if you like, then send"),
	)
}
