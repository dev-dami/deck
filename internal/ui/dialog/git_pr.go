package dialog

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"

	"github.com/dev-dami/deck/internal/deck/gh"
	"github.com/dev-dami/deck/internal/ui/common"
	"github.com/dev-dami/deck/internal/ui/util"
)

// GitPRID is the identifier for the pull request detail dialog.
const GitPRID = "git_pr"

// gitPRMaxRows bounds the checks list.
const gitPRMaxRows = 12

// GitPR shows one pull request and its checks, and acts on them: re-run
// what failed, read or hand a failed log to an agent, merge, check out.
type GitPR struct {
	com     *common.Common
	help    help.Model
	spinner spinner.Model
	loading bool
	busy    string

	number int // 0 until known when opened for the current branch
	pr     *gh.PR
	checks []gh.Check
	err    string

	sel, off int

	keyMap struct {
		UpDown, Up, Down, Rerun, Log, Handoff,
		Merge, Checkout, Open, Refresh, Close key.Binding
	}
}

var (
	_ Dialog        = (*GitPR)(nil)
	_ LoadingDialog = (*GitPR)(nil)
	_ GitBusyDialog = (*GitPR)(nil)
)

// NewGitPR opens the detail of pull request number (0: the current
// branch's, once it is known). The UI loads it and calls SetData.
func NewGitPR(com *common.Common, number int) *GitPR {
	d := &GitPR{com: com, number: number}
	d.help = help.New()
	d.help.Styles = com.Styles.DialogHelpStyles()
	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = com.Styles.Dialog.Spinner
	d.spinner = s

	d.keyMap.UpDown = key.NewBinding(key.WithKeys("up", "down"), key.WithHelp("↑↓", "check"))
	d.keyMap.Up = key.NewBinding(key.WithKeys("up", "k"))
	d.keyMap.Down = key.NewBinding(key.WithKeys("down", "j"))
	d.keyMap.Rerun = key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "rerun failed"))
	d.keyMap.Log = key.NewBinding(key.WithKeys("l", "enter"), key.WithHelp("l", "failed log"))
	d.keyMap.Handoff = key.NewBinding(key.WithKeys("a"), key.WithHelp("a", "hand to agent"))
	d.keyMap.Merge = key.NewBinding(key.WithKeys("m"), key.WithHelp("m", "merge"))
	d.keyMap.Checkout = key.NewBinding(key.WithKeys("c"), key.WithHelp("c", "check out"))
	d.keyMap.Open = key.NewBinding(key.WithKeys("o"), key.WithHelp("o", "browser"))
	d.keyMap.Refresh = key.NewBinding(key.WithKeys("R", "shift+r", "ctrl+r"), key.WithHelp("R", "refresh"))
	d.keyMap.Close = CloseKey
	return d
}

// ID implements [Dialog].
func (*GitPR) ID() string { return GitPRID }

// Number is the pull request shown (0 until known).
func (d *GitPR) Number() int { return d.number }

// SetData shows a freshly read pull request. checks may be nil, in which
// case the pull request's own rollup is shown.
func (d *GitPR) SetData(pr *gh.PR, checks []gh.Check) {
	d.loading = false
	d.err = ""
	d.pr = pr
	if pr != nil {
		d.number = pr.Number
		if checks == nil {
			checks = pr.Checks
		}
	}
	d.checks = checks
	if d.sel >= len(d.checks) || d.sel < 0 {
		d.sel = 0
	}
	// Start on the first failure: it is what there is to look at.
	if d.sel == 0 {
		for i, c := range d.checks {
			if c.Failed() {
				d.sel = i
				break
			}
		}
	}
}

// SetError shows why the pull request could not be read.
func (d *GitPR) SetError(msg string) {
	d.loading = false
	d.err = msg
}

// Pending reports whether some checks are still running.
func (d *GitPR) Pending() bool {
	return gh.Summarize(d.checks).Pending > 0
}

// StartLoading implements [LoadingDialog].
func (d *GitPR) StartLoading() tea.Cmd {
	if d.loading {
		return nil
	}
	d.loading = true
	return d.spinner.Tick
}

// StopLoading implements [LoadingDialog].
func (d *GitPR) StopLoading() { d.loading = false }

// SetBusy implements [GitBusyDialog].
func (d *GitPR) SetBusy(label string) tea.Cmd {
	was := d.busy != "" || d.loading || d.Pending()
	d.busy = label
	if label != "" && !was {
		return d.spinner.Tick
	}
	return nil
}

// Tick keeps the spinner of pending checks turning.
func (d *GitPR) Tick() tea.Cmd { return d.spinner.Tick }

// selected is the check the cursor is on; for log and hand-off, the first
// failed one when the cursor is on one that did not fail.
func (d *GitPR) selectedFailed() (gh.Check, bool) {
	if d.sel < len(d.checks) && d.checks[d.sel].Failed() {
		return d.checks[d.sel], true
	}
	for _, c := range d.checks {
		if c.Failed() {
			return c, true
		}
	}
	return gh.Check{}, false
}

// HandleMsg implements [Dialog].
func (d *GitPR) HandleMsg(msg tea.Msg) Action {
	switch msg := msg.(type) {
	case spinner.TickMsg:
		if d.loading || d.busy != "" || d.Pending() {
			var cmd tea.Cmd
			d.spinner, cmd = d.spinner.Update(msg)
			return ActionCmd{Cmd: cmd}
		}
	case tea.KeyPressMsg:
		return d.handleKey(msg)
	}
	return nil
}

func (d *GitPR) handleKey(msg tea.KeyPressMsg) Action {
	switch {
	case key.Matches(msg, d.keyMap.Close):
		return ActionClose{}
	case key.Matches(msg, d.keyMap.Up):
		if n := len(d.checks); n > 0 {
			d.sel = (d.sel + n - 1) % n
		}
		return nil
	case key.Matches(msg, d.keyMap.Down):
		if n := len(d.checks); n > 0 {
			d.sel = (d.sel + 1) % n
		}
		return nil
	case key.Matches(msg, d.keyMap.Refresh):
		return ActionGitPRRefresh{Number: d.number}
	}
	if d.pr == nil || d.busy != "" {
		return nil
	}
	n := d.pr.Number
	switch {
	case key.Matches(msg, d.keyMap.Rerun):
		ids := gh.FailedRunIDs(d.checks)
		if len(ids) == 0 {
			return ActionCmd{Cmd: util.ReportInfo("No failed Actions run to re-run")}
		}
		return ActionGitPRRerun{Number: n, RunIDs: ids}
	case key.Matches(msg, d.keyMap.Log), key.Matches(msg, d.keyMap.Handoff):
		c, ok := d.selectedFailed()
		if !ok {
			return ActionCmd{Cmd: util.ReportInfo("No failed check")}
		}
		if c.RunID() == "" {
			return ActionCmd{Cmd: util.ReportInfo(c.Name + " is not a GitHub Actions check; o opens it in the browser")}
		}
		if key.Matches(msg, d.keyMap.Handoff) {
			return ActionGitPRHandoff{Number: n, Check: c}
		}
		return ActionGitPRLog{Number: n, Check: c}
	case key.Matches(msg, d.keyMap.Merge):
		if d.pr.State != "" && d.pr.State != "OPEN" {
			return ActionCmd{Cmd: util.ReportInfo(fmt.Sprintf("#%d is %s", n, strings.ToLower(d.pr.State)))}
		}
		return ActionGitPRAskMerge{Number: n, Title: d.pr.Title}
	case key.Matches(msg, d.keyMap.Checkout):
		return ActionGitPRCheckout{Number: n}
	case key.Matches(msg, d.keyMap.Open):
		return ActionGitPROpenWeb{Number: n}
	}
	return nil
}

// Draw implements [Dialog].
func (d *GitPR) Draw(scr uv.Screen, area uv.Rectangle) *tea.Cursor {
	t := d.com.Styles
	width := gitDialogWidth(t, area, gitDialogMaxWidth)
	inner := gitInner(t, width)
	muted := gitMuted(t)
	primary := gitPrimary(t)

	title := "Pull request"
	if d.number > 0 {
		title = fmt.Sprintf("Pull request #%d", d.number)
	}
	lines := []string{gitTitle(t, title, inner)}

	switch {
	case d.err != "":
		lines = append(lines, gitLine(gitFailStyle.Render(d.err), inner))
	case d.pr == nil:
		lines = append(lines, gitLine(d.spinner.View()+" "+muted.Render("loading…"), inner))
	default:
		pr := d.pr
		lines = append(lines, gitLine(primary.Render(pr.Title), inner))

		var facts []string
		state := strings.ToLower(pr.State)
		switch pr.State {
		case "MERGED":
			state = gitPassStyle.Render("merged")
		case "CLOSED":
			state = gitFailStyle.Render("closed")
		}
		if pr.IsDraft {
			state += muted.Render(" draft")
		}
		facts = append(facts, state)
		switch pr.ReviewDecision {
		case "APPROVED":
			facts = append(facts, gitPassStyle.Render("approved"))
		case "CHANGES_REQUESTED":
			facts = append(facts, gitFailStyle.Render("changes requested"))
		case "REVIEW_REQUIRED":
			facts = append(facts, gitWaitStyle.Render("review required"))
		}
		switch pr.Mergeable {
		case "MERGEABLE":
			facts = append(facts, muted.Render("mergeable"))
		case "CONFLICTING":
			facts = append(facts, gitFailStyle.Render("conflicts"))
		}
		if d.loading {
			facts = append(facts, d.spinner.View())
		}
		lines = append(lines, gitLine(strings.Join(facts, muted.Render(" · ")), inner))
		lines = append(lines, gitLine(t.Header.GitBranch.Render(pr.HeadRefName)+muted.Render(" → ")+t.Header.GitBranch.Render(pr.BaseRefName), inner))
		lines = append(lines, "")

		sum := gh.Summarize(d.checks)
		if sum.Total == 0 {
			lines = append(lines, gitLine(muted.Render("No checks."), inner))
		} else {
			lines = append(lines, gitLine(GitChecksText(sum), inner))
			fixed := len(lines) + 3
			rows := max(1, min(gitPRMaxRows, area.Dy()-t.Dialog.View.GetVerticalFrameSize()-fixed))
			d.off = gitWindow(d.sel, d.off, rows, len(d.checks))
			for i := d.off; i < min(len(d.checks), d.off+rows); i++ {
				c := d.checks[i]
				glyph := GitCheckGlyph(t, c)
				if c.Pending() {
					glyph = d.spinner.View()
				}
				text := glyph + " " + c.Name
				if c.Workflow != "" {
					text += muted.Render(" · " + c.Workflow)
				}
				lines = append(lines, gitRow(t, text, i == d.sel, inner))
			}
		}
	}

	lines = append(lines, "")
	if d.busy != "" {
		lines = append(lines, gitLine(d.spinner.View()+" "+muted.Render(d.busy), inner))
	}
	lines = append(lines, renderDialogHelp(t, &d.help, d, inner))
	DrawCenter(scr, area, gitView(t, width, lines))
	return nil
}

// ShortHelp implements [help.KeyMap].
func (d *GitPR) ShortHelp() []key.Binding {
	if _, ok := d.selectedFailed(); ok {
		return []key.Binding{d.keyMap.Log, d.keyMap.Handoff, d.keyMap.Rerun, d.keyMap.Merge, d.keyMap.Checkout, d.keyMap.Open, d.keyMap.Close}
	}
	return []key.Binding{d.keyMap.Merge, d.keyMap.Checkout, d.keyMap.Open, d.keyMap.Refresh, d.keyMap.UpDown, d.keyMap.Close}
}

// FullHelp implements [help.KeyMap].
func (d *GitPR) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{d.keyMap.UpDown, d.keyMap.Log, d.keyMap.Handoff, d.keyMap.Rerun},
		{d.keyMap.Merge, d.keyMap.Checkout, d.keyMap.Open, d.keyMap.Refresh, d.keyMap.Close},
	}
}
