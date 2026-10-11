package model

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	deckconfig "github.com/dev-dami/deck/internal/deck/config"
	"github.com/dev-dami/deck/internal/deck/gh"
	"github.com/dev-dami/deck/internal/ui/dialog"
	"github.com/dev-dami/deck/internal/ui/notification"
	"github.com/dev-dami/deck/internal/ui/util"
)

// Git polling. Status is a local read and cheap; GitHub is asked more often
// while checks run, and never when gh is missing or signed out (the git panel
// asks again when it opens).
const (
	gitStatusInterval    = 10 * time.Second
	gitStatusErrBackoff  = time.Minute
	gitPRPendingInterval = 20 * time.Second
	gitPRIdleInterval    = 2 * time.Minute
	gitPRListInterval    = 2 * time.Minute
	gitPRDetailInterval  = 20 * time.Second
	// gitNudgeWindow is how recent a pushed branch's last commit must be
	// for the "open a PR" nudge.
	gitNudgeWindow = time.Hour
	// gitRecentBranches is how many branches the Recent tab lists.
	gitRecentBranches = 15
)

// gitState is what the UI knows about the work tree's git and GitHub state,
// for the header badge, the sidebar block and the nudge above the composer.
// It is only touched in Update; reads happen in commands that report back
// with gitMsg messages.
type gitState struct {
	client *gh.Client

	status     gh.Status
	statusOK   bool
	statusAt   time.Time
	statusBusy bool
	recent     []gh.Branch

	ghChecked bool
	ghAvail   bool

	// pr is prBranch's pull request (nil: it has none), checks its checks.
	pr       *gh.PR
	prBranch string
	checks   gh.CheckSummary
	prAt     time.Time
	prBusy   bool

	// heads maps the head branch of each open pull request to its number,
	// as of headsAt.
	heads   map[string]int
	headsAt time.Time

	// defaultBranch is the repository's default branch once read; it never
	// gets the nudge.
	defaultBranch string

	// dismissed is the branches whose nudge the user closed.
	dismissed map[string]bool
	// verdicts is the last check state seen per pull request, to notice a
	// pending → passed/failed change.
	verdicts map[int]string

	// detailTick is set while a pull request detail refresh is scheduled.
	detailTick bool
}

// gitMsg is a message from one of the git commands; Update hands them all
// to handleGitMsg.
type gitMsg interface{ gitMsg() }

type (
	// gitStatusMsg is a status poll's result.
	gitStatusMsg struct {
		status gh.Status
		recent []gh.Branch
		err    error
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
	// gitPanelMsg is what the git panel shows.
	gitPanelMsg struct {
		data dialog.GitPanelData
	}
	// gitOpMsg is a finished git or gh operation.
	gitOpMsg struct {
		busyID       string // the dialog that showed it running
		info         string
		err          error
		closeID      string // a dialog to close when it worked
		refresh      bool
		thenCreatePR bool
		job          string // the Commands pane's job for it
	}
	// gitPRDetailMsg is a pull request read for the detail dialog.
	gitPRDetailMsg struct {
		pr     *gh.PR
		checks []gh.Check
		err    error
	}
	// gitPRDetailTickMsg refreshes the open pull request detail.
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
)

func (gitStatusMsg) gitMsg()       {}
func (gitPRMsg) gitMsg()           {}
func (gitPanelMsg) gitMsg()        {}
func (gitOpMsg) gitMsg()           {}
func (gitPRDetailMsg) gitMsg()     {}
func (gitPRDetailTickMsg) gitMsg() {}
func (gitPrefillMsg) gitMsg()      {}
func (gitLogMsg) gitMsg()          {}
func (gitHandoffMsg) gitMsg()      {}

// gitDialogIDs is every git dialog, front-most kind first.
var gitDialogIDs = []string{dialog.GitLogID, dialog.GitMergeID, dialog.GitPRCreateID, dialog.GitPRID, dialog.GitID}

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
		cmds = append(cmds, gitFetchStatus(cli))
	}
	if cmd := m.gitMaybePollPR(now); cmd != nil {
		cmds = append(cmds, cmd)
	}
	return tea.Batch(cmds...)
}

// gitMaybePollPR asks GitHub about the current branch's pull request when
// it is due: at once after a checkout, every 20s while checks run, every two
// minutes otherwise.
func (m *UI) gitMaybePollPR(now time.Time) tea.Cmd {
	g := &m.git
	if g.prBusy || !g.statusOK || g.status.Branch == "" || (g.ghChecked && !g.ghAvail) {
		return nil
	}
	interval := gitPRIdleInterval
	if g.checks.State() == gh.ChecksPending {
		interval = gitPRPendingInterval
	}
	if g.prBranch == g.status.Branch && now.Sub(g.prAt) < interval {
		return nil
	}
	g.prBusy = true
	return gitFetchPR(g.client, g.status.Branch, g.wantPRList(now))
}

// wantPRList reports whether the open pull requests should be listed too: a
// recently pushed branch other than the current one could get the nudge,
// and only the list says whether it has a pull request.
func (g *gitState) wantPRList(now time.Time) bool {
	if now.Sub(g.headsAt) < gitPRListInterval {
		return false
	}
	for _, b := range g.recent {
		if b.Name != g.status.Branch && g.nudgeable(b, now) {
			return true
		}
	}
	return false
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
	muted := lipgloss.NewStyle().Foreground(m.com.Styles.Dialog.SecondaryText.GetForeground())
	text := fmt.Sprintf("%s pushed %s · %s then p to open a PR", branch, dialog.GitAgo(at, time.Now()), dialog.GitKey)
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
	if b := dialog.GitChecksBadge(g.checks); b != "" {
		return num + " " + b
	}
	return num
}

// gitSidebarBlock is the sidebar's git lines under the branch: how it
// stands against its upstream, its pull request and the checks. It is ""
// when there is nothing to say.
func (m *UI) gitSidebarBlock(width int) string {
	g := &m.git
	if !g.statusOK || width <= 0 {
		return ""
	}
	t := m.com.Styles
	muted := lipgloss.NewStyle().Foreground(t.Dialog.SecondaryText.GetForeground())
	var lines []string
	var facts []string
	if ab := dialog.GitAheadBehind(g.status); ab != "" {
		facts = append(facts, ab)
	}
	if g.status.Branch != "" && g.status.Upstream == "" {
		facts = append(facts, "not pushed")
	}
	if g.status.Dirty > 0 {
		facts = append(facts, fmt.Sprintf("%d changed", g.status.Dirty))
	}
	if len(facts) > 0 {
		lines = append(lines, muted.Render(ansi.Truncate(strings.Join(facts, " · "), width, "…")))
	}
	if g.pr != nil && g.prBranch == g.status.Branch {
		num := t.Header.GitBranch.Render(fmt.Sprintf("#%d", g.pr.Number))
		lines = append(lines, num+" "+ansi.Truncate(g.pr.Title, max(0, width-lipgloss.Width(num)-1), "…"))
		if s := dialog.GitChecksText(g.checks); s != "" {
			lines = append(lines, ansi.Truncate(s, width, "…"))
		}
	}
	return strings.Join(lines, "\n")
}

// handleGitMsg applies a git command's result.
func (m *UI) handleGitMsg(msg gitMsg) tea.Cmd {
	g := &m.git
	now := time.Now()
	switch msg := msg.(type) {
	case gitStatusMsg:
		g.statusBusy = false
		g.statusAt = now
		if msg.err != nil {
			g.statusOK = false
			g.statusAt = now.Add(gitStatusErrBackoff)
			return nil
		}
		g.status, g.statusOK, g.recent = msg.status, true, msg.recent
		return m.gitMaybePollPR(now)

	case gitPRMsg:
		g.prBusy = false
		g.prAt = now
		g.ghChecked, g.ghAvail = true, msg.avail
		if !msg.avail {
			g.pr, g.prBranch, g.checks = nil, "", gh.CheckSummary{}
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

	case gitPanelMsg:
		d := msg.data
		if d.StatusErr == "" {
			g.status, g.statusOK, g.statusAt = d.Status, true, now
		}
		g.recent = d.Recent
		g.ghChecked, g.ghAvail = true, d.GHAvailable
		var cmd tea.Cmd
		if d.GHAvailable && d.PRErr == "" {
			g.setHeads(d.PRs, now)
			g.prAt = now
			cmd = m.gitSetPR(d.Status.Branch, d.PR, nil)
		}
		if p, ok := m.dialog.Dialog(dialog.GitID).(*dialog.Git); ok {
			p.SetData(d)
			p.SetNudge(p.Nudge())
		}
		return cmd

	case gitOpMsg:
		m.jobDone(msg.job, msg.err)
		if d, ok := m.dialog.Dialog(msg.busyID).(dialog.GitBusyDialog); ok {
			d.SetBusy("")
		}
		var cmds []tea.Cmd
		if msg.err != nil {
			cmds = append(cmds, gitReportErr(msg.err))
		} else {
			if msg.info != "" {
				cmds = append(cmds, util.ReportInfo(msg.info))
			}
			if msg.closeID != "" {
				m.dialog.CloseDialog(msg.closeID)
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
		d, ok := m.dialog.Dialog(dialog.GitPRID).(*dialog.GitPR)
		if !ok {
			return nil
		}
		if msg.err != nil {
			d.SetError(gitErrText(msg.err))
			return nil
		}
		if d.Number() != 0 && d.Number() != msg.pr.Number {
			return nil
		}
		d.SetData(msg.pr, msg.checks)
		var cmds []tea.Cmd
		if msg.pr.HeadRefName == g.status.Branch && msg.pr.State == "OPEN" {
			cmds = append(cmds, m.gitSetPR(msg.pr.HeadRefName, msg.pr, msg.checks))
		}
		if d.Pending() {
			cmds = append(cmds, d.Tick())
			if !g.detailTick {
				g.detailTick = true
				cmds = append(cmds, tea.Tick(gitPRDetailInterval, func(time.Time) tea.Msg { return gitPRDetailTickMsg{} }))
			}
		}
		return tea.Batch(cmds...)

	case gitPRDetailTickMsg:
		g.detailTick = false
		if d, ok := m.dialog.Dialog(dialog.GitPRID).(*dialog.GitPR); ok && d.Pending() {
			return tea.Batch(d.StartLoading(), m.gitLoadDetail(d.Number()))
		}
		return nil

	case gitPrefillMsg:
		switch {
		case msg.err != nil:
			return gitReportErr(msg.err)
		case msg.existing > 0:
			return tea.Batch(
				util.ReportInfo(fmt.Sprintf("%s already has #%d", msg.branch, msg.existing)),
				m.openGitPR(msg.existing),
			)
		}
		if msg.base != "" {
			g.defaultBranch = msg.base
		}
		m.dialog.CloseDialog(dialog.GitPRCreateID)
		d := dialog.NewGitPRCreate(m.com, msg.branch, msg.title, msg.base, msg.body, msg.notes)
		m.dialog.OpenDialog(d)
		return d.InitialCmd()

	case gitLogMsg:
		if d, ok := m.dialog.Dialog(dialog.GitPRID).(dialog.GitBusyDialog); ok {
			d.SetBusy("")
		}
		switch {
		case msg.err != nil:
			return gitReportErr(msg.err)
		case strings.TrimSpace(msg.log) == "":
			return util.ReportInfo("gh has no failed log for " + msg.check.Name + " (yet)")
		}
		m.dialog.CloseDialog(dialog.GitLogID)
		m.dialog.OpenDialog(dialog.NewGitLog(m.com, msg.number, msg.check, msg.log))
		return nil

	case gitHandoffMsg:
		if d, ok := m.dialog.Dialog(dialog.GitPRID).(dialog.GitBusyDialog); ok {
			d.SetBusy("")
		}
		if msg.err != nil {
			return gitReportErr(msg.err)
		}
		return m.gitPutInComposer(msg.text)
	}
	return nil
}

// setHeads records the open pull requests' head branches.
func (g *gitState) setHeads(prs []gh.PR, now time.Time) {
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
		text = fmt.Sprintf("CI failed on #%d — %s to see why", pr.Number, dialog.GitKey)
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
		return util.ReportWarn("GitHub needs the gh CLI: " + dialog.GitNoGHHint)
	}
	return util.ReportError(errors.New(gitErrText(err)))
}

// gitErrText is a failed command's error as a line for the user.
func gitErrText(err error) string {
	s := err.Error()
	if strings.Contains(strings.ToLower(s), "not a git repository") {
		return "Not a git repository"
	}
	return s
}

// gitFetchStatus reads the status and the recent branches.
func gitFetchStatus(cli *gh.Client) tea.Cmd {
	return func() tea.Msg {
		ctx := context.Background()
		st, err := cli.Status(ctx)
		if err != nil {
			return gitStatusMsg{err: err}
		}
		recent, _ := cli.RecentBranches(ctx, gitRecentBranches)
		return gitStatusMsg{status: st, recent: recent}
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

// gitLoadPanel reads everything the git panel shows.
func gitLoadPanel(cli *gh.Client) tea.Cmd {
	return func() tea.Msg {
		ctx := context.Background()
		d := dialog.GitPanelData{Loaded: true, Now: time.Now()}
		st, err := cli.Status(ctx)
		if err != nil {
			d.StatusErr = gitErrText(err)
			return gitPanelMsg{data: d}
		}
		d.Status = st
		d.Recent, _ = cli.RecentBranches(ctx, gitRecentBranches)
		d.GHAvailable = cli.Available(ctx)
		if !d.GHAvailable {
			return gitPanelMsg{data: d}
		}
		if st.Branch != "" {
			d.PR, err = cli.PRForBranch(ctx, st.Branch)
			if err != nil {
				d.PRErr = gitErrText(err)
			}
		}
		prs, err := cli.PRList(ctx)
		if err != nil {
			d.PRErr = gitErrText(err)
		}
		d.PRs = prs
		return gitPanelMsg{data: d}
	}
}

// openGitPanel opens the git panel on tab (or brings it to the front) and
// loads it; commit puts it on the commit message input.
func (m *UI) openGitPanel(tab dialog.GitTab, commit bool) tea.Cmd {
	cli := m.gitClient()
	// gh may have been signed in to since it was last asked.
	cli.Recheck()
	nudge, _ := m.gitNudge()
	var cmds []tea.Cmd
	p, ok := m.dialog.Dialog(dialog.GitID).(*dialog.Git)
	if ok {
		m.dialog.BringToFront(dialog.GitID)
		p.SetTab(tab)
	} else {
		g := &m.git
		seed := dialog.GitPanelData{Status: g.status, GHAvailable: g.ghAvail, Recent: g.recent, Now: time.Now()}
		if g.prBranch == g.status.Branch {
			seed.PR = g.pr
		}
		p = dialog.NewGit(m.com, tab, seed, "")
		p.SetNudge(nudge)
		m.dialog.OpenDialog(p)
	}
	if commit {
		cmds = append(cmds, p.StartCommit())
	}
	cmds = append(cmds, p.StartLoading(), gitLoadPanel(cli))
	return tea.Batch(cmds...)
}

// gitRefreshAll re-reads after something changed: the status at once, the
// pull request on the next poll, and whatever git dialog is open.
func (m *UI) gitRefreshAll() tea.Cmd {
	g := &m.git
	cli := m.gitClient()
	g.prAt = time.Time{}
	g.headsAt = time.Time{}
	var cmds []tea.Cmd
	if !g.statusBusy {
		g.statusBusy = true
		cmds = append(cmds, gitFetchStatus(cli))
	}
	if p, ok := m.dialog.Dialog(dialog.GitID).(*dialog.Git); ok {
		cmds = append(cmds, p.StartLoading(), gitLoadPanel(cli))
	}
	if d, ok := m.dialog.Dialog(dialog.GitPRID).(*dialog.GitPR); ok {
		cmds = append(cmds, d.StartLoading(), m.gitLoadDetail(d.Number()))
	}
	return tea.Batch(cmds...)
}

// gitOp runs a git or gh operation off the UI loop, showing label as busy
// on dialog busyID (or in the status bar when it is not open).
func (m *UI) gitOp(busyID, label string, run func(context.Context) gitOpMsg) tea.Cmd {
	var cmds []tea.Cmd
	if label != "" {
		if d, ok := m.dialog.Dialog(busyID).(dialog.GitBusyDialog); ok {
			cmds = append(cmds, d.SetBusy(label))
		} else {
			cmds = append(cmds, util.ReportInfo(label))
		}
	}
	job := m.startJob("git", label)
	cmds = append(cmds, func() tea.Msg {
		msg := run(context.Background())
		msg.busyID = busyID
		msg.job = job
		return msg
	})
	return tea.Batch(cmds...)
}

// handleGitAction runs what a git dialog or a git palette command asked
// for.
func (m *UI) handleGitAction(action dialog.GitAction) tea.Cmd {
	m.dialog.CloseDialog(dialog.CommandsID)
	cli := m.gitClient()
	branch := m.git.status.Branch

	switch a := action.(type) {
	case dialog.ActionGitOpen:
		return m.openGitPanel(a.Tab, a.Commit)

	case dialog.ActionGitRefresh:
		return m.gitRefreshAll()

	case dialog.ActionGitClosePanel:
		if p, ok := m.dialog.Dialog(dialog.GitID).(*dialog.Git); ok && p.Nudge() != "" {
			if m.git.dismissed == nil {
				m.git.dismissed = map[string]bool{}
			}
			m.git.dismissed[p.Nudge()] = true
		}
		m.dialog.CloseDialog(dialog.GitID)
		if m.focus == uiFocusEditor {
			return m.textarea.Focus()
		}
		return nil

	case dialog.ActionGitCommitAll:
		return m.gitOp(dialog.GitID, "Committing…", func(ctx context.Context) gitOpMsg {
			err := cli.CommitAll(ctx, a.Message)
			return gitOpMsg{info: "Committed: " + firstLineOf(a.Message), err: err, refresh: true}
		})

	case dialog.ActionGitPush:
		return m.gitOp(dialog.GitID, "Pushing…", func(ctx context.Context) gitOpMsg {
			err := cli.Push(ctx)
			info := "Pushed"
			if branch != "" {
				info = "Pushed " + branch
			}
			return gitOpMsg{info: info, err: err, refresh: true}
		})

	case dialog.ActionGitFetch:
		return m.gitOp(dialog.GitID, "Fetching…", func(ctx context.Context) gitOpMsg {
			return gitOpMsg{info: "Fetched", err: cli.Fetch(ctx), refresh: true}
		})

	case dialog.ActionGitCheckout:
		return m.gitOp(dialog.GitID, "Checking out "+a.Branch+"…", func(ctx context.Context) gitOpMsg {
			err := cli.Checkout(ctx, a.Branch)
			return gitOpMsg{info: "On " + a.Branch + " now", err: err, refresh: true, thenCreatePR: a.ThenCreatePR}
		})

	case dialog.ActionGitCreatePR:
		return m.gitStartCreatePR()

	case dialog.ActionGitSubmitPR:
		return m.gitOp(dialog.GitPRCreateID, "Opening the pull request…", func(ctx context.Context) gitOpMsg {
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
			n, url, err := cli.CreatePR(ctx, a.Title, a.Body, a.Base, a.Draft)
			if err != nil {
				return gitOpMsg{err: err, refresh: true}
			}
			return gitOpMsg{info: fmt.Sprintf("PR #%d opened · %s", n, url), closeID: dialog.GitPRCreateID, refresh: true}
		})

	case dialog.ActionGitViewPR:
		return m.openGitPR(a.Number)

	case dialog.ActionGitPRRefresh:
		if d, ok := m.dialog.Dialog(dialog.GitPRID).(*dialog.GitPR); ok {
			return tea.Batch(d.StartLoading(), m.gitLoadDetail(a.Number))
		}
		return nil

	case dialog.ActionGitPRRerun:
		return m.gitOp(dialog.GitPRID, "Re-running the failed jobs…", func(ctx context.Context) gitOpMsg {
			for _, id := range a.RunIDs {
				if err := cli.RerunFailed(ctx, id); err != nil {
					return gitOpMsg{err: err, refresh: true}
				}
			}
			return gitOpMsg{info: fmt.Sprintf("Re-running the failed jobs on #%d", a.Number), refresh: true}
		})

	case dialog.ActionGitPRLog:
		var cmds []tea.Cmd
		if d, ok := m.dialog.Dialog(dialog.GitPRID).(dialog.GitBusyDialog); ok {
			cmds = append(cmds, d.SetBusy("Fetching the failed log…"))
		}
		cmds = append(cmds, func() tea.Msg {
			log, err := cli.FailedLog(context.Background(), a.Check.RunID(), a.Check.JobID())
			return gitLogMsg{number: a.Number, check: a.Check, log: log, err: err}
		})
		return tea.Batch(cmds...)

	case dialog.ActionGitPRHandoff:
		var cmds []tea.Cmd
		if a.Log == "" {
			if d, ok := m.dialog.Dialog(dialog.GitPRID).(dialog.GitBusyDialog); ok {
				cmds = append(cmds, d.SetBusy("Fetching the failed log…"))
			}
		}
		cmds = append(cmds, gitHandoff(cli, a))
		return tea.Batch(cmds...)

	case dialog.ActionGitPRAskMerge:
		m.dialog.CloseDialog(dialog.GitMergeID)
		m.dialog.OpenDialog(dialog.NewGitMerge(m.com, a.Number, a.Title))
		return nil

	case dialog.ActionGitPRMerge:
		m.dialog.CloseDialog(dialog.GitMergeID)
		return m.gitOp(dialog.GitPRID, fmt.Sprintf("Merging #%d…", a.Number), func(ctx context.Context) gitOpMsg {
			err := cli.Merge(ctx, a.Number, a.Method)
			return gitOpMsg{info: fmt.Sprintf("Merged #%d (%s)", a.Number, a.Method), err: err, refresh: true}
		})

	case dialog.ActionGitPRCheckout:
		return m.gitOp(dialog.GitPRID, fmt.Sprintf("Checking out #%d…", a.Number), func(ctx context.Context) gitOpMsg {
			err := cli.CheckoutPR(ctx, a.Number)
			return gitOpMsg{info: fmt.Sprintf("Checked out #%d", a.Number), err: err, refresh: true}
		})

	case dialog.ActionGitPROpenWeb:
		return m.gitOp(dialog.GitPRID, "", func(ctx context.Context) gitOpMsg {
			err := cli.OpenWeb(ctx, a.Number)
			return gitOpMsg{info: fmt.Sprintf("Opened #%d in the browser", a.Number), err: err}
		})
	}
	return nil
}

// firstLineOf is s up to its first newline.
func firstLineOf(s string) string {
	l, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	return l
}

// gitStartCreatePR reads what the create pull request form starts with,
// then opens it (or the branch's pull request, if it has one).
func (m *UI) gitStartCreatePR() tea.Cmd {
	cli := m.gitClient()
	cli.Recheck()
	return tea.Batch(util.ReportInfo("Preparing the pull request…"), func() tea.Msg {
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

// openGitPR opens the detail of pull request number (0: the current
// branch's) and loads it.
func (m *UI) openGitPR(number int) tea.Cmd {
	g := &m.git
	m.gitClient().Recheck()
	m.dialog.CloseDialog(dialog.GitMergeID)
	m.dialog.CloseDialog(dialog.GitLogID)
	m.dialog.CloseDialog(dialog.GitPRID)
	d := dialog.NewGitPR(m.com, number)
	if g.pr != nil && ((number == 0 && g.prBranch == g.status.Branch) || g.pr.Number == number) {
		// Show what is known at once; the read below refreshes it.
		d.SetData(g.pr, nil)
		number = g.pr.Number
	}
	m.dialog.OpenDialog(d)
	return tea.Batch(d.StartLoading(), m.gitLoadDetail(number))
}

// gitLoadDetail reads pull request number (0: the current branch's) and its
// checks for the detail dialog.
func (m *UI) gitLoadDetail(number int) tea.Cmd {
	cli := m.gitClient()
	return func() tea.Msg {
		ctx := context.Background()
		var pr *gh.PR
		var err error
		if number == 0 {
			pr, err = cli.PRForBranch(ctx, "")
			if err == nil && pr == nil {
				err = errors.New("this branch has no pull request yet; p in the git panel opens one")
			}
		} else {
			pr, err = cli.PRView(ctx, number)
		}
		if err != nil {
			return gitPRDetailMsg{err: err}
		}
		// gh pr checks knows more than the rollup; without it, the
		// rollup is shown.
		checks, _ := cli.Checks(ctx, pr.Number)
		return gitPRDetailMsg{pr: pr, checks: checks}
	}
}

// gitHandoff builds the composer text that hands a failed check to the
// user's default agent, fetching the log when a has none.
func gitHandoff(cli *gh.Client, a dialog.ActionGitPRHandoff) tea.Cmd {
	return func() tea.Msg {
		log := a.Log
		if log == "" {
			l, err := cli.FailedLog(context.Background(), a.Check.RunID(), a.Check.JobID())
			if err != nil {
				return gitHandoffMsg{err: err}
			}
			log = l
		}
		agent := "claude"
		if cfg, err := deckconfig.Load(); err == nil && cfg.DefaultAgent != "" {
			agent = cfg.DefaultAgent
		}
		return gitHandoffMsg{text: gitHandoffText(agent, a.Number, a.Check.Name, log)}
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

// gitPutInComposer closes the git dialogs and puts text in the composer,
// after whatever is there, without sending it.
func (m *UI) gitPutInComposer(text string) tea.Cmd {
	for _, id := range gitDialogIDs {
		m.dialog.CloseDialog(id)
	}
	prevHeight := m.textarea.Height()
	if cur := m.textarea.Value(); strings.TrimSpace(cur) != "" {
		text = strings.TrimRight(cur, "\n") + "\n\n" + text
	}
	m.textarea.SetValue(text)
	m.textarea.MoveToEnd()
	m.focus = uiFocusEditor
	m.chat.Blur()
	return tea.Batch(
		m.textarea.Focus(),
		m.handleTextareaHeightChange(prevHeight),
		util.ReportInfo("The failed log is in the composer: change the $agent if you like, then send"),
	)
}
