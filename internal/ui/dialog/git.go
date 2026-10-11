package dialog

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	uv "github.com/charmbracelet/ultraviolet"

	"github.com/dev-dami/deck/internal/deck/gh"
	"github.com/dev-dami/deck/internal/ui/common"
	"github.com/dev-dami/deck/internal/ui/util"
)

// GitID is the identifier for the git panel.
const GitID = "git"

// Git panel sizing.
const (
	gitDialogMaxWidth = 78
	gitListMaxRows    = 14
)

// GitPanelData is what the git panel shows, read off the UI loop.
type GitPanelData struct {
	// Loaded is false until the first read came back.
	Loaded bool
	Status gh.Status
	// StatusErr says why there is no status (not a repository, say).
	StatusErr   string
	GHAvailable bool
	// PR is the current branch's pull request, nil when it has none.
	PR *gh.PR
	// PRs is the repository's open pull requests.
	PRs   []gh.PR
	PRErr string
	// Recent is the local branches, the most recently committed first.
	Recent []gh.Branch
	Now    time.Time
}

// gitItem is one action of the Branch tab.
type gitItem struct {
	label, hint string
	enabled     bool
	action      Action
}

// Git is the git panel: the branch and what can be done with it, the open
// pull requests, and the recent branches.
type Git struct {
	com     *common.Common
	help    help.Model
	spinner spinner.Model
	loading bool
	busy    string

	tab  GitTab
	sel  [gitTabCount]int
	off  [gitTabCount]int
	data GitPanelData

	// nudge is the branch the "recently pushed" nudge is about, if any.
	nudge string

	committing bool
	input      textinput.Model
	// confirm is a checkout waiting for a yes, the work tree being dirty.
	confirm *ActionGitCheckout

	keyMap struct {
		Next, Prev, One, Two, Three,
		Up, Down, Select, CreatePR, Refresh,
		Commit, CancelInput, Yes, No, Close key.Binding
	}
}

var (
	_ Dialog        = (*Git)(nil)
	_ LoadingDialog = (*Git)(nil)
	_ GitBusyDialog = (*Git)(nil)
)

// NewGit opens the git panel on tab. data may be what is already known;
// the UI loads the rest and calls SetData.
func NewGit(com *common.Common, tab GitTab, data GitPanelData, nudge string) *Git {
	g := &Git{com: com, tab: tab, data: data, nudge: nudge}
	g.help = help.New()
	g.help.Styles = com.Styles.DialogHelpStyles()

	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = com.Styles.Dialog.Spinner
	g.spinner = s

	in := textinput.New()
	in.SetVirtualCursor(false)
	in.SetStyles(com.Styles.TextInput)
	in.Prompt = "> "
	in.Placeholder = "What changed?"
	in.CharLimit = 500
	g.input = in

	g.keyMap.Next = key.NewBinding(key.WithKeys("tab", "right"), key.WithHelp("tab", "next tab"))
	g.keyMap.Prev = key.NewBinding(key.WithKeys("shift+tab", "left"), key.WithHelp("shift+tab", "previous tab"))
	g.keyMap.One = key.NewBinding(key.WithKeys("1"), key.WithHelp("1", "branch"))
	g.keyMap.Two = key.NewBinding(key.WithKeys("2"), key.WithHelp("2", "pull requests"))
	g.keyMap.Three = key.NewBinding(key.WithKeys("3"), key.WithHelp("3", "recent"))
	g.keyMap.Up = key.NewBinding(key.WithKeys("up", "k", "ctrl+p"), key.WithHelp("↑↓", "choose"))
	g.keyMap.Down = key.NewBinding(key.WithKeys("down", "j", "ctrl+n"), key.WithHelp("↑↓", "choose"))
	g.keyMap.Select = key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "select"))
	g.keyMap.CreatePR = key.NewBinding(key.WithKeys("p"), key.WithHelp("p", "open PR"))
	g.keyMap.Refresh = key.NewBinding(key.WithKeys("R", "shift+r", "ctrl+r"), key.WithHelp("R", "refresh"))
	g.keyMap.Commit = key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "commit"))
	g.keyMap.CancelInput = key.NewBinding(key.WithKeys("esc", "alt+esc"), key.WithHelp("esc", "cancel"))
	g.keyMap.Yes = key.NewBinding(key.WithKeys("y", "Y", "enter"), key.WithHelp("y", "check out anyway"))
	g.keyMap.No = key.NewBinding(key.WithKeys("n", "N", "esc", "alt+esc"), key.WithHelp("n", "keep this branch"))
	g.keyMap.Close = CloseKey
	return g
}

// ID implements [Dialog].
func (*Git) ID() string { return GitID }

// SetData shows freshly read data.
func (g *Git) SetData(d GitPanelData) {
	g.data = d
	g.loading = false
	for tab := range gitTabCount {
		g.sel[tab] = max(0, min(g.sel[tab], g.rowCount(tab)-1))
	}
}

// Data is what the panel shows now.
func (g *Git) Data() GitPanelData { return g.data }

// SetTab switches to tab.
func (g *Git) SetTab(tab GitTab) {
	if tab >= 0 && tab < gitTabCount {
		g.tab = tab
	}
}

// SetNudge sets the branch the "recently pushed" nudge is about and, when
// it is not the current branch, selects it in the Recent tab.
func (g *Git) SetNudge(branch string) {
	g.nudge = branch
	if branch == "" {
		return
	}
	for i, b := range g.data.Recent {
		if b.Name == branch {
			g.sel[GitTabRecent] = i
		}
	}
}

// Nudge is the branch the panel was opened about, if any.
func (g *Git) Nudge() string { return g.nudge }

// StartCommit puts the panel on the commit message input.
func (g *Git) StartCommit() tea.Cmd {
	g.tab = GitTabBranch
	g.committing = true
	g.input.SetValue("")
	return g.input.Focus()
}

// StartLoading implements [LoadingDialog].
func (g *Git) StartLoading() tea.Cmd {
	if g.loading {
		return nil
	}
	g.loading = true
	return g.spinner.Tick
}

// StopLoading implements [LoadingDialog].
func (g *Git) StopLoading() { g.loading = false }

// SetBusy implements [GitBusyDialog].
func (g *Git) SetBusy(label string) tea.Cmd {
	was := g.busy != "" || g.loading
	g.busy = label
	if label != "" && !was {
		return g.spinner.Tick
	}
	return nil
}

// current is the checked-out branch.
func (g *Git) current() string { return g.data.Status.Branch }

// branchItems is the Branch tab's actions as things stand.
func (g *Git) branchItems() []gitItem {
	st := g.data.Status
	var items []gitItem

	commit := gitItem{label: "Commit all…", enabled: st.Dirty > 0, action: ActionGitCommitAll{}}
	if st.Dirty > 0 {
		commit.hint = fmt.Sprintf("stage and commit %d changed", st.Dirty)
	} else {
		commit.hint = "nothing to commit"
	}
	items = append(items, commit)

	push := gitItem{label: "Push", enabled: st.Branch != "", action: ActionGitPush{}}
	switch {
	case st.Branch == "":
		push.hint = "no branch checked out"
	case st.Upstream == "":
		push.hint = "first push, sets origin/" + st.Branch + " as upstream"
	case st.Ahead > 0:
		push.hint = fmt.Sprintf("%d commit(s) to %s", st.Ahead, st.Upstream)
	default:
		push.hint = "up to date with " + st.Upstream
	}
	items = append(items, push)

	fetch := gitItem{label: "Fetch", enabled: true, action: ActionGitFetch{}}
	if st.Behind > 0 {
		fetch.hint = fmt.Sprintf("%d behind %s", st.Behind, st.Upstream)
	}
	items = append(items, fetch)

	if g.data.PR != nil {
		pr := g.data.PR
		items = append(items, gitItem{
			label:   fmt.Sprintf("View this branch's PR #%d", pr.Number),
			hint:    pr.Title,
			enabled: true,
			action:  ActionGitViewPR{Number: pr.Number},
		})
	} else {
		create := gitItem{label: "Create pull request…", enabled: g.data.GHAvailable && st.Branch != "", action: ActionGitCreatePR{}}
		switch {
		case !g.data.GHAvailable:
			create.hint = GitNoGHHint
		case st.Upstream == "" || st.Ahead > 0:
			create.hint = "pushes first"
		}
		items = append(items, create)
	}
	return items
}

// sortedPRs is the open pull requests, the current branch's first, then
// those whose branch is checked out here, then the rest.
func (g *Git) sortedPRs() []gh.PR {
	rank := func(p gh.PR) int {
		switch {
		case p.HeadRefName == g.current():
			return 0
		case slices.ContainsFunc(g.data.Recent, func(b gh.Branch) bool { return b.Name == p.HeadRefName }):
			return 1
		}
		return 2
	}
	prs := slices.Clone(g.data.PRs)
	slices.SortStableFunc(prs, func(a, b gh.PR) int { return rank(a) - rank(b) })
	return prs
}

// prFor is the open pull request whose branch is name, or nil.
func (g *Git) prFor(name string) *gh.PR {
	if g.data.PR != nil && g.data.PR.HeadRefName == name {
		return g.data.PR
	}
	for i := range g.data.PRs {
		if g.data.PRs[i].HeadRefName == name {
			return &g.data.PRs[i]
		}
	}
	return nil
}

func (g *Git) rowCount(tab GitTab) int {
	switch tab {
	case GitTabBranch:
		return len(g.branchItems())
	case GitTabPRs:
		return len(g.data.PRs)
	case GitTabRecent:
		return len(g.data.Recent)
	}
	return 0
}

// HandleMsg implements [Dialog].
func (g *Git) HandleMsg(msg tea.Msg) Action {
	switch msg := msg.(type) {
	case spinner.TickMsg:
		if g.loading || g.busy != "" {
			var cmd tea.Cmd
			g.spinner, cmd = g.spinner.Update(msg)
			return ActionCmd{Cmd: cmd}
		}
	case tea.PasteMsg:
		if g.committing {
			var cmd tea.Cmd
			g.input, cmd = g.input.Update(msg)
			return ActionCmd{Cmd: cmd}
		}
	case tea.KeyPressMsg:
		return g.handleKey(msg)
	}
	return nil
}

func (g *Git) handleKey(msg tea.KeyPressMsg) Action {
	if g.committing {
		switch {
		case key.Matches(msg, g.keyMap.CancelInput):
			g.committing = false
			g.input.Blur()
		case key.Matches(msg, g.keyMap.Commit):
			text := strings.TrimSpace(g.input.Value())
			if text == "" {
				return ActionCmd{Cmd: util.ReportWarn("Write a commit message first")}
			}
			g.committing = false
			g.input.Blur()
			g.input.SetValue("")
			return ActionGitCommitAll{Message: text}
		default:
			var cmd tea.Cmd
			g.input, cmd = g.input.Update(msg)
			return ActionCmd{Cmd: cmd}
		}
		return nil
	}
	if g.confirm != nil {
		switch {
		case key.Matches(msg, g.keyMap.Yes):
			a := *g.confirm
			g.confirm = nil
			return a
		case key.Matches(msg, g.keyMap.No):
			g.confirm = nil
		}
		return nil
	}

	switch {
	case key.Matches(msg, g.keyMap.Close):
		return ActionGitClosePanel{}
	case key.Matches(msg, g.keyMap.Next):
		g.tab = (g.tab + 1) % gitTabCount
	case key.Matches(msg, g.keyMap.Prev):
		g.tab = (g.tab + gitTabCount - 1) % gitTabCount
	case key.Matches(msg, g.keyMap.One):
		g.tab = GitTabBranch
	case key.Matches(msg, g.keyMap.Two):
		g.tab = GitTabPRs
	case key.Matches(msg, g.keyMap.Three):
		g.tab = GitTabRecent
	case key.Matches(msg, g.keyMap.Up):
		if n := g.rowCount(g.tab); n > 0 {
			g.sel[g.tab] = (g.sel[g.tab] + n - 1) % n
		}
	case key.Matches(msg, g.keyMap.Down):
		if n := g.rowCount(g.tab); n > 0 {
			g.sel[g.tab] = (g.sel[g.tab] + 1) % n
		}
	case key.Matches(msg, g.keyMap.Refresh):
		return ActionGitRefresh{}
	case g.busy != "":
		// One operation at a time.
		return nil
	case key.Matches(msg, g.keyMap.CreatePR):
		return g.createPR()
	case key.Matches(msg, g.keyMap.Select):
		return g.activate()
	}
	return nil
}

// createPR opens a pull request for the selected recent branch, or the
// branch the nudge is about, or the current one; a branch that is not
// checked out is checked out first.
func (g *Git) createPR() Action {
	if !g.data.GHAvailable {
		return ActionCmd{Cmd: util.ReportWarn("To open pull requests, " + GitNoGHHint)}
	}
	branch := g.current()
	if g.tab == GitTabRecent && g.sel[GitTabRecent] < len(g.data.Recent) {
		branch = g.data.Recent[g.sel[GitTabRecent]].Name
	} else if g.nudge != "" {
		branch = g.nudge
	}
	if pr := g.prFor(branch); pr != nil {
		return ActionGitViewPR{Number: pr.Number}
	}
	if branch == "" {
		return ActionCmd{Cmd: util.ReportWarn("Check out a branch first")}
	}
	if branch == g.current() {
		return ActionGitCreatePR{}
	}
	return g.checkout(branch, true)
}

// checkout checks out branch, asking first when the work tree is dirty.
func (g *Git) checkout(branch string, thenPR bool) Action {
	a := ActionGitCheckout{Branch: branch, ThenCreatePR: thenPR}
	if g.data.Status.Dirty > 0 {
		g.confirm = &a
		return nil
	}
	return a
}

// activate runs the selected row.
func (g *Git) activate() Action {
	i := g.sel[g.tab]
	switch g.tab {
	case GitTabBranch:
		items := g.branchItems()
		if i >= len(items) {
			return nil
		}
		it := items[i]
		if !it.enabled {
			if it.hint != "" {
				return ActionCmd{Cmd: util.ReportInfo(it.hint)}
			}
			return nil
		}
		if _, ok := it.action.(ActionGitCommitAll); ok {
			return ActionCmd{Cmd: g.StartCommit()}
		}
		return it.action
	case GitTabPRs:
		prs := g.sortedPRs()
		if i < len(prs) {
			return ActionGitViewPR{Number: prs[i].Number}
		}
	case GitTabRecent:
		if i >= len(g.data.Recent) {
			return nil
		}
		b := g.data.Recent[i].Name
		if b == g.current() {
			return ActionCmd{Cmd: util.ReportInfo("Already on " + b)}
		}
		return g.checkout(b, false)
	}
	return nil
}

// Draw implements [Dialog].
func (g *Git) Draw(scr uv.Screen, area uv.Rectangle) *tea.Cursor {
	t := g.com.Styles
	width := gitDialogWidth(t, area, gitDialogMaxWidth)
	inner := gitInner(t, width)
	muted := gitMuted(t)

	lines := []string{gitTitle(t, "Git", inner), gitLine(g.renderTabs(), inner)}

	switch {
	case !g.data.Loaded && g.data.Status.Branch == "" && !g.data.Status.Detached:
		lines = append(lines, gitLine(g.spinner.View()+" "+muted.Render("loading…"), inner))
	case g.data.StatusErr != "":
		lines = append(lines, gitLine(gitFailStyle.Render(g.data.StatusErr), inner))
	default:
		status := gitStatusLine(t, g.data.Status, g.data.PR)
		if g.loading {
			status += " " + g.spinner.View()
		}
		lines = append(lines, gitLine(status, inner))
	}
	lines = append(lines, "")

	// The fixed lines: title, tabs, status, a blank, then a blank, the
	// footer line and help below the body.
	fixed := len(lines) + 3
	rows := max(1, min(gitListMaxRows, area.Dy()-t.Dialog.View.GetVerticalFrameSize()-fixed))

	var cur *tea.Cursor
	switch {
	case g.committing:
		lines = append(lines, gitLine(gitPrimary(t).Render("Commit message")+muted.Render(fmt.Sprintf(" · stages all %d changed", g.data.Status.Dirty)), inner))
		g.input.SetWidth(max(1, inner-2*gitPad-lipgloss.Width(g.input.Prompt)-1))
		row := lipgloss.Height(strings.Join(lines, "\n"))
		lines = append(lines, gitLine(g.input.View(), inner))
		cur = gitCursor(t, g.input.Cursor(), row, gitPad)
	default:
		lines = append(lines, g.renderBody(rows, inner)...)
	}

	lines = append(lines, "")
	switch {
	case g.confirm != nil:
		lines = append(lines, gitLine(gitWaitStyle.Render(fmt.Sprintf(
			"%d uncommitted change(s) come along. Check out %s anyway? y/n", g.data.Status.Dirty, g.confirm.Branch)), inner))
	case g.busy != "":
		lines = append(lines, gitLine(g.spinner.View()+" "+muted.Render(g.busy), inner))
	case !g.data.GHAvailable && g.data.Loaded:
		lines = append(lines, gitLine(muted.Render("GitHub: "+GitNoGHHint), inner))
	}
	lines = append(lines, renderDialogHelp(t, &g.help, g, inner))

	DrawCenterCursor(scr, area, gitView(t, width, lines), cur)
	return cur
}

// renderTabs is the tab bar, the open tab highlighted.
func (g *Git) renderTabs() string {
	t := g.com.Styles
	names := []string{"1 Branch", "2 Pull requests", "3 Recent"}
	parts := make([]string, len(names))
	for i, n := range names {
		if GitTab(i) == g.tab {
			parts[i] = t.Dialog.SelectedItem.Render(n)
		} else {
			parts[i] = t.Dialog.NormalItem.Render(n)
		}
	}
	return strings.Join(parts, " ")
}

// renderBody is the open tab's rows, at most rows of them.
func (g *Git) renderBody(rows, inner int) []string {
	t := g.com.Styles
	muted := gitMuted(t)
	var out []string
	window := func(n int) (int, int) {
		g.off[g.tab] = gitWindow(g.sel[g.tab], g.off[g.tab], rows, n)
		return g.off[g.tab], min(n, g.off[g.tab]+rows)
	}

	switch g.tab {
	case GitTabBranch:
		items := g.branchItems()
		from, to := window(len(items))
		for i := from; i < to; i++ {
			it := items[i]
			label := it.label
			if !it.enabled {
				label = muted.Render(label)
			}
			text := label
			if it.hint != "" {
				text += muted.Render("  " + it.hint)
			}
			out = append(out, gitRow(t, text, i == g.sel[g.tab], inner))
		}
	case GitTabPRs:
		switch {
		case !g.data.Loaded:
			out = append(out, gitLine(muted.Render("loading…"), inner))
		case !g.data.GHAvailable:
			out = append(out, gitLine(muted.Render("To see pull requests, "+GitNoGHHint+"."), inner))
		case g.data.PRErr != "":
			out = append(out, gitLine(gitFailStyle.Render(g.data.PRErr), inner))
		case len(g.data.PRs) == 0:
			out = append(out, gitLine(muted.Render("No open pull requests."), inner))
		default:
			prs := g.sortedPRs()
			from, to := window(len(prs))
			for i := from; i < to; i++ {
				p := prs[i]
				glyph := gitRowGlyph(gh.Summarize(p.Checks))
				text := glyph + " " + gitPrimary(t).Render(fmt.Sprintf("#%d", p.Number)) + " " + p.Title
				meta := " · " + p.HeadRefName
				if p.IsDraft {
					meta += " · draft"
				}
				if p.HeadRefName == g.current() {
					meta += " · this branch"
				}
				text += muted.Render(meta)
				out = append(out, gitRow(t, text, i == g.sel[g.tab], inner))
			}
		}
	case GitTabRecent:
		if len(g.data.Recent) == 0 {
			msg := "No branches yet."
			if !g.data.Loaded {
				msg = "loading…"
			}
			out = append(out, gitLine(muted.Render(msg), inner))
			break
		}
		now := g.data.Now
		if now.IsZero() {
			now = time.Now()
		}
		from, to := window(len(g.data.Recent))
		for i := from; i < to; i++ {
			b := g.data.Recent[i]
			mark := " "
			if b.Name == g.current() {
				mark = gitPassStyle.Render("●")
			}
			when := GitAgo(b.Committed, now)
			if b.Upstream != "" {
				when = "pushed " + when
			} else {
				when = "local · " + when
			}
			text := mark + " " + b.Name + muted.Render(" · "+when)
			if g.data.GHAvailable {
				if pr := g.prFor(b.Name); pr != nil {
					text += muted.Render(" · ") + gitPrimary(t).Render(fmt.Sprintf("#%d", pr.Number))
				} else {
					text += muted.Render(" · no PR")
				}
			}
			out = append(out, gitRow(t, text, i == g.sel[g.tab], inner))
		}
	}
	return out
}

// gitRowGlyph is the one-cell check verdict of a list row.
func gitRowGlyph(s gh.CheckSummary) string {
	switch s.State() {
	case gh.ChecksPassed:
		return gitPassStyle.Render(gitGlyphPass)
	case gh.ChecksFailed:
		return gitFailStyle.Render(gitGlyphFail)
	case gh.ChecksPending:
		return gitWaitStyle.Render(gitGlyphWait)
	}
	return " "
}

// ShortHelp implements [help.KeyMap].
func (g *Git) ShortHelp() []key.Binding {
	switch {
	case g.committing:
		return []key.Binding{g.keyMap.Commit, g.keyMap.CancelInput}
	case g.confirm != nil:
		return []key.Binding{g.keyMap.Yes, g.keyMap.No}
	}
	sel := g.keyMap.Select
	switch g.tab {
	case GitTabPRs:
		sel.SetHelp("enter", "open")
	case GitTabRecent:
		sel.SetHelp("enter", "check out")
	}
	return []key.Binding{g.keyMap.Next, g.keyMap.Up, sel, g.keyMap.CreatePR, g.keyMap.Refresh, g.keyMap.Close}
}

// FullHelp implements [help.KeyMap].
func (g *Git) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{g.keyMap.Next, g.keyMap.Prev, g.keyMap.One, g.keyMap.Two, g.keyMap.Three},
		{g.keyMap.Up, g.keyMap.Select, g.keyMap.CreatePR, g.keyMap.Refresh, g.keyMap.Close},
	}
}
