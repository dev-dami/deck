package model

import (
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/dev-dami/deck/internal/deck/gh"
	"github.com/dev-dami/deck/internal/ui/motion"
	"github.com/dev-dami/deck/internal/ui/styles"
)

// The git pane is a small git and GitHub browser in the sidebar strip. Its
// root lists the branch's status and changed files, the branch's pull
// request, the open pull requests, the recent branches and the recent
// commits; enter goes into a pull request (its checks, failed logs, merge)
// or a commit, and esc comes back. Everything it shows is read off the UI's
// gitState; everything it does goes through the git commands in git.go.

func init() { registerPane(paneGit, func() pane { return gitPane{} }) }

// gitPaneWidth is the column width the git pane asks for.
const gitPaneWidth = 48

// gitPaneTickInterval is the spinner's frame time.
const gitPaneTickInterval = 120 * time.Millisecond

// gitDrawnRecently is how recently the pane must have been drawn for
// pending checks to keep its spinner turning.
const gitDrawnRecently = 2 * time.Second

// gitBottom as a view's offset scrolls it to the end.
const gitBottom = 1 << 30

// gitPane is the pane itself. Its state lives in m.git.pane, next to the
// data it shows, so the git messages can reach it without the strip.
type gitPane struct{}

var _ pane = gitPane{}

func (gitPane) id() paneID         { return paneGit }
func (gitPane) title(*UI) string   { return "Git" }
func (gitPane) width() int         { return gitPaneWidth }
func (gitPane) badge(m *UI) string { return m.gitPaneBadge() }
func (gitPane) height(m *UI, w int) int {
	return m.gitPaneHeight(w)
}

func (gitPane) render(m *UI, w, h int, focused bool) string {
	return m.gitPaneRender(w, h, focused)
}

func (gitPane) key(m *UI, msg tea.KeyPressMsg) (bool, tea.Cmd) {
	handled, cmd := m.gitPaneKey(msg)
	return handled, tea.Batch(cmd, m.gitPaneTick())
}

func (gitPane) click(m *UI, x, y int) tea.Cmd {
	return tea.Batch(m.gitPaneClick(x, y), m.gitPaneTick())
}

// gitViewKind is a kind of view in the pane.
type gitViewKind int

const (
	gitViewRoot   gitViewKind = iota // status, pull requests, branches, commits
	gitViewPR                        // one pull request and its checks
	gitViewLog                       // a failed check's log
	gitViewCommit                    // one commit's files
	gitViewCreate                    // the create pull request form
)

// gitView is one view on the pane's back stack.
type gitView struct {
	kind gitViewKind
	// sel is the cursor (the root's selected row, the PR view's check)
	// and off the first line shown.
	sel, off int
	// selKey is the root's selected row, by key, so the cursor stays on it
	// when the lists above it change.
	selKey string

	// PR view: number is 0 until known when it is the current branch's.
	number  int
	pr      *gh.PR
	checks  []gh.Check
	loading bool
	err     string
	// merging is the merge confirmation row, method the choice in
	// gh.MergeMethods.
	merging bool
	method  int

	// log view
	check gh.Check
	log   string

	// commit view
	hash   string
	commit *gh.CommitDetail

	// create view
	form *gitPRForm
}

// setPR shows a freshly read pull request. checks may be nil, in which
// case the pull request's own rollup is shown.
func (v *gitView) setPR(pr *gh.PR, checks []gh.Check) {
	v.err = ""
	v.pr = pr
	if pr == nil {
		return
	}
	v.number = pr.Number
	if checks == nil {
		checks = pr.Checks
	}
	v.checks = checks
	if v.sel >= len(v.checks) || v.sel < 0 {
		v.sel = 0
	}
	// Start on the first failure: it is what there is to look at.
	if v.sel == 0 {
		for i, c := range v.checks {
			if c.Failed() {
				v.sel = i
				break
			}
		}
	}
}

// pending reports whether some of the view's checks still run.
func (v *gitView) pending() bool {
	return v.kind == gitViewPR && gh.Summarize(v.checks).Pending > 0
}

// selectedFailed is the check the cursor is on when it failed, else the
// first failed one.
func (v *gitView) selectedFailed() (gh.Check, bool) {
	if v.sel >= 0 && v.sel < len(v.checks) && v.checks[v.sel].Failed() {
		return v.checks[v.sel], true
	}
	for _, c := range v.checks {
		if c.Failed() {
			return c, true
		}
	}
	return gh.Check{}, false
}

// gitPaneState is the pane's views, inline inputs and status line.
type gitPaneState struct {
	root  gitView
	stack []*gitView // views above the root; empty at the root

	// committing shows the commit message input on the root.
	committing bool
	input      textinput.Model
	inputReady bool

	// confirm is a branch waiting for y/n before checkout (the tree is
	// dirty); confirmPR starts a pull request for it after.
	confirm   string
	confirmPR bool

	// busy is a running operation's label; err the last failure (red),
	// info the last result.
	busy, err, info string
	// refreshing is set while an R refresh reads.
	refreshing bool

	frame   int
	ticking bool

	// lastW and lastH are the size the pane was last drawn at, for clicks
	// and for keys that scroll.
	lastW, lastH int
	// drawnAt is when the pane was last drawn: pending checks keep the
	// spinner turning only while the pane is on screen.
	drawnAt time.Time
}

// w is the width the pane was last drawn at, or the one it asks for.
func (p *gitPaneState) w() int {
	if p.lastW > 0 {
		return p.lastW
	}
	return gitPaneWidth
}

// top is the view on show.
func (p *gitPaneState) top() *gitView {
	if n := len(p.stack); n > 0 {
		return p.stack[n-1]
	}
	return &p.root
}

func (p *gitPaneState) push(v *gitView) {
	p.confirm = ""
	p.stack = append(p.stack, v)
}

// pop goes back one view; false at the root.
func (p *gitPaneState) pop() bool {
	n := len(p.stack)
	if n == 0 {
		return false
	}
	p.stack = p.stack[:n-1]
	return true
}

// toRoot leaves every view and inline input.
func (p *gitPaneState) toRoot() {
	p.stack = nil
	p.committing = false
	p.confirm = ""
}

// findPR is the pull request view asked for as number, if one is open.
func (p *gitPaneState) findPR(number int) *gitView {
	for i := len(p.stack) - 1; i >= 0; i-- {
		if v := p.stack[i]; v.kind == gitViewPR && v.number == number {
			return v
		}
	}
	return nil
}

// animating reports whether something on show needs the spinner.
func (p *gitPaneState) animating() bool {
	if p.busy != "" || p.refreshing {
		return true
	}
	v := p.top()
	switch v.kind {
	case gitViewPR:
		return v.loading || (v.pending() && time.Since(p.drawnAt) < gitDrawnRecently)
	case gitViewCommit:
		return v.loading
	case gitViewCreate:
		return v.form != nil && v.form.preparing
	}
	return false
}

// gitPaneTick keeps the spinner turning while something runs. Only one
// tick is ever in flight; motion off holds it still.
func (m *UI) gitPaneTick() tea.Cmd {
	p := &m.git.pane
	if p.ticking || motion.Off() || !p.animating() {
		return nil
	}
	p.ticking = true
	return tea.Tick(gitPaneTickInterval, func(time.Time) tea.Msg { return gitPaneTickMsg{} })
}

// gitPaneBadge is the pane header's note: "#12 ✓", "#12 ● 3/5", "#12 ✗"
// for the branch's pull request, "↑2" when ahead without one, else "".
func (m *UI) gitPaneBadge() string {
	g := &m.git
	if !g.statusOK {
		return ""
	}
	if g.pr != nil && g.prBranch == g.status.Branch && g.status.Branch != "" {
		num := gitPrimary(m.com.Styles).Render(fmt.Sprintf("#%d", g.pr.Number))
		if b := gitChecksBadge(g.checks); b != "" {
			return num + " " + b
		}
		return num
	}
	if g.status.Ahead > 0 {
		return gitPrimary(m.com.Styles).Render(fmt.Sprintf("↑%d", g.status.Ahead))
	}
	return ""
}

// gitStartCommit opens the commit message input on the root.
func (m *UI) gitStartCommit() tea.Cmd {
	g := &m.git
	p := &g.pane
	if g.statusOK && g.status.Dirty == 0 {
		p.info, p.err = "Nothing to commit", ""
		return nil
	}
	p.ensureInput(m.com.Styles)
	p.committing, p.confirm = true, ""
	p.input.SetValue("")
	return p.input.Focus()
}

// ensureInput makes the commit message input the first time it is needed.
func (p *gitPaneState) ensureInput(t *styles.Styles) {
	if p.inputReady {
		return
	}
	in := textinput.New()
	st := t.TextInput
	st.Cursor.Blink = false
	in.SetStyles(st)
	in.SetVirtualCursor(true)
	in.Prompt = ""
	in.Placeholder = "What changed?"
	in.CharLimit = 500
	p.input, p.inputReady = in, true
}

// The create form's fields, in tab order.
const (
	prFieldTitle = iota
	prFieldBase
	prFieldBody
	prFieldDraft
	prFieldCount
)

// gitPRBodyHeight is the description editor's height.
const gitPRBodyHeight = 5

// gitPRForm is the create pull request form shown in the pane.
type gitPRForm struct {
	// preparing is set until the form's prefill came back.
	preparing bool

	branch, notes string
	title, base   textinput.Model
	body          textarea.Model
	draft         bool
	field         int
}

func newGitPRForm(t *styles.Styles) *gitPRForm {
	f := &gitPRForm{preparing: true}
	newInput := func(placeholder string) textinput.Model {
		in := textinput.New()
		st := t.TextInput
		st.Cursor.Blink = false
		in.SetStyles(st)
		in.SetVirtualCursor(true)
		in.Prompt = "> "
		in.Placeholder = placeholder
		in.CharLimit = 256
		return in
	}
	f.title = newInput("Title")
	f.base = newInput("main")

	ta := textarea.New()
	st := t.Editor.Textarea
	st.Cursor.Blink = false
	ta.SetStyles(st)
	ta.Placeholder = "What does this change, and why?"
	ta.ShowLineNumbers = false
	ta.CharLimit = 0
	ta.SetVirtualCursor(true)
	ta.DynamicHeight = false
	ta.SetPromptFunc(2, func(textarea.PromptInfo) string { return "  " })
	ta.SetHeight(gitPRBodyHeight)
	ta.Blur()
	f.body = ta
	return f
}

// fill puts the prefill in the form and focuses the title.
func (f *gitPRForm) fill(branch, title, base, body, notes string) tea.Cmd {
	f.preparing = false
	f.branch, f.notes = branch, notes
	f.title.SetValue(title)
	f.title.CursorEnd()
	f.base.SetValue(base)
	f.base.CursorEnd()
	f.body.SetValue(body)
	return f.setField(prFieldTitle)
}

func (f *gitPRForm) setField(n int) tea.Cmd {
	f.field = (n + prFieldCount) % prFieldCount
	f.title.Blur()
	f.base.Blur()
	f.body.Blur()
	switch f.field {
	case prFieldTitle:
		return f.title.Focus()
	case prFieldBase:
		return f.base.Focus()
	case prFieldBody:
		return f.body.Focus()
	}
	return nil
}

// gitPaneKey handles a key while the pane is focused. At the root, esc is
// left to the strip.
func (m *UI) gitPaneKey(msg tea.KeyPressMsg) (bool, tea.Cmd) {
	p := &m.git.pane
	k := msg.String()
	v := p.top()

	// Inline inputs and confirmations take every key.
	switch {
	case v.kind == gitViewCreate:
		return true, m.gitFormKey(v, msg)
	case p.committing && v.kind == gitViewRoot:
		return true, m.gitCommitKey(msg)
	case p.confirm != "" && v.kind == gitViewRoot:
		return true, m.gitConfirmKey(k)
	case v.merging:
		return true, m.gitMergeKey(v, k)
	}

	switch k {
	case "esc", "backspace":
		if p.pop() {
			p.err, p.info = "", ""
			return true, nil
		}
		if p.err != "" || p.info != "" {
			p.err, p.info = "", ""
		}
		return false, nil
	}

	switch v.kind {
	case gitViewRoot:
		return m.gitRootKey(msg, k)
	case gitViewPR:
		return m.gitPRKey(v, k)
	case gitViewLog:
		if k == "a" {
			if p.busy != "" {
				return true, nil
			}
			return true, m.gitHandoff(v.number, v.check, v.log)
		}
		return m.gitScrollKey(v, k)
	case gitViewCommit:
		return m.gitScrollKey(v, k)
	}
	return false, nil
}

// gitCommitKey edits the commit message: enter commits, esc cancels.
func (m *UI) gitCommitKey(msg tea.KeyPressMsg) tea.Cmd {
	p := &m.git.pane
	switch msg.String() {
	case "esc":
		p.committing = false
		p.input.Blur()
		return nil
	case "enter":
		text := strings.TrimSpace(p.input.Value())
		if text == "" {
			return nil
		}
		p.committing = false
		p.input.Blur()
		return m.gitCommitAll(text)
	}
	var cmd tea.Cmd
	p.input, cmd = p.input.Update(msg)
	return cmd
}

// gitConfirmKey answers the dirty-tree checkout question.
func (m *UI) gitConfirmKey(k string) tea.Cmd {
	p := &m.git.pane
	switch k {
	case "y", "Y", "enter":
		branch, thenPR := p.confirm, p.confirmPR
		p.confirm, p.confirmPR = "", false
		return m.gitCheckout(branch, thenPR)
	case "n", "N", "esc":
		p.confirm, p.confirmPR = "", false
	}
	return nil
}

// gitMergeKey picks how to merge: ←/→ choose, enter merges, esc cancels.
// Nothing else merges, so a stray key never does.
func (m *UI) gitMergeKey(v *gitView, k string) tea.Cmd {
	n := len(gh.MergeMethods)
	switch k {
	case "left", "h", "shift+tab":
		v.method = (v.method + n - 1) % n
	case "right", "l", "tab":
		v.method = (v.method + 1) % n
	case "esc":
		v.merging = false
	case "enter":
		v.merging = false
		if v.pr == nil {
			return nil
		}
		return m.gitMerge(v.pr.Number, gh.MergeMethods[v.method])
	}
	return nil
}

// gitFormKey edits the create pull request form: tab moves between fields,
// ctrl+s opens the pull request, esc leaves.
func (m *UI) gitFormKey(v *gitView, msg tea.KeyPressMsg) tea.Cmd {
	p := &m.git.pane
	f := v.form
	k := msg.String()
	if k == "esc" {
		p.pop()
		return nil
	}
	if f == nil || f.preparing || p.busy != "" {
		return nil
	}
	submit := func() tea.Cmd {
		title := strings.TrimSpace(f.title.Value())
		if title == "" {
			p.err = "A pull request needs a title"
			return f.setField(prFieldTitle)
		}
		p.err = ""
		return m.gitSubmitPR(title, strings.TrimSpace(f.base.Value()), strings.TrimSpace(f.body.Value()), f.draft)
	}
	switch k {
	case "ctrl+s":
		return submit()
	case "tab":
		return f.setField(f.field + 1)
	case "shift+tab":
		return f.setField(f.field - 1)
	}
	var cmd tea.Cmd
	switch f.field {
	case prFieldTitle, prFieldBase:
		if k == "enter" {
			return f.setField(f.field + 1)
		}
		if f.field == prFieldTitle {
			f.title, cmd = f.title.Update(msg)
		} else {
			f.base, cmd = f.base.Update(msg)
		}
	case prFieldBody:
		f.body, cmd = f.body.Update(msg)
	case prFieldDraft:
		switch k {
		case "space", " ", "x":
			f.draft = !f.draft
		case "enter":
			return submit()
		}
	}
	return cmd
}

// gitRootKey moves through the root's rows and runs its actions.
func (m *UI) gitRootKey(msg tea.KeyPressMsg, k string) (bool, tea.Cmd) {
	g := &m.git
	p := &g.pane
	rows := m.gitRootRows(p.w())
	v := &p.root
	cur := gitSelected(rows, v)

	switch k {
	case "up", "k", "ctrl+p":
		v.selectIn(rows, gitStep(rows, cur, -1))
		return true, nil
	case "down", "j", "ctrl+n":
		v.selectIn(rows, gitStep(rows, cur, 1))
		return true, nil
	case "tab":
		v.selectIn(rows, gitNextSection(rows, cur, 1))
		return true, nil
	case "shift+tab":
		v.selectIn(rows, gitNextSection(rows, cur, -1))
		return true, nil
	case "home", "g":
		v.selectIn(rows, gitStep(rows, -1, 1))
		return true, nil
	case "end", "G":
		v.selectIn(rows, gitStep(rows, len(rows), -1))
		return true, nil
	case "R", "shift+r", "ctrl+r":
		m.gitClient().Recheck()
		p.refreshing, p.err, p.info = true, "", ""
		return true, m.gitRefreshAll()
	}

	if g.statusErr != "" && !g.statusOK {
		return false, nil
	}
	busy := p.busy != ""
	switch k {
	case "enter":
		if cur < 0 || busy {
			return true, nil
		}
		return true, m.gitRootEnter(rows[cur])
	case "c":
		if busy {
			return true, nil
		}
		return true, m.gitStartCommit()
	case "p":
		if busy {
			return true, nil
		}
		return true, m.gitPush()
	case "f":
		if busy {
			return true, nil
		}
		return true, m.gitFetch()
	case "P", "shift+p":
		if busy {
			return true, nil
		}
		// On another recent branch, P checks it out first.
		if cur >= 0 && rows[cur].kind == gitRowRecent && rows[cur].name != g.status.Branch {
			return true, m.gitAskCheckout(rows[cur].name, true)
		}
		return true, m.gitStartCreatePR()
	}
	return false, nil
}

// gitRootEnter acts on a root row.
func (m *UI) gitRootEnter(r gitRow) tea.Cmd {
	g := &m.git
	p := &g.pane
	switch r.kind {
	case gitRowThisPR, gitRowPR:
		return m.gitOpenPR(r.number)
	case gitRowRecent:
		if r.name == g.status.Branch {
			p.info, p.err = "Already on "+r.name, ""
			return nil
		}
		return m.gitAskCheckout(r.name, false)
	case gitRowCommit:
		return m.gitOpenCommit(r.name)
	case gitRowNoPR:
		return m.gitStartCreatePR()
	}
	return nil
}

// gitAskCheckout checks branch out, asking first when the tree is dirty.
func (m *UI) gitAskCheckout(branch string, thenPR bool) tea.Cmd {
	g := &m.git
	if g.status.Dirty > 0 {
		g.pane.confirm, g.pane.confirmPR = branch, thenPR
		g.pane.committing = false
		return nil
	}
	return m.gitCheckout(branch, thenPR)
}

// gitPRKey runs the pull request view's keys.
func (m *UI) gitPRKey(v *gitView, k string) (bool, tea.Cmd) {
	p := &m.git.pane
	switch k {
	case "up", "k", "ctrl+p":
		if n := len(v.checks); n > 0 {
			v.sel = max(0, v.sel-1)
		}
		return true, nil
	case "down", "j", "ctrl+n":
		if n := len(v.checks); n > 0 {
			v.sel = min(n-1, v.sel+1)
		}
		return true, nil
	case "R", "shift+r", "ctrl+r":
		if !v.loading {
			v.loading = true
			p.err, p.info = "", ""
			return true, m.gitLoadDetail(v.number)
		}
		return true, nil
	}
	if v.pr == nil || p.busy != "" {
		switch k {
		case "r", "l", "a", "m", "c", "o", "enter":
			return true, nil
		}
		return false, nil
	}
	n := v.pr.Number
	switch k {
	case "r":
		ids := gh.FailedRunIDs(v.checks)
		if len(ids) == 0 {
			p.info, p.err = "No failed Actions run to re-run", ""
			return true, nil
		}
		return true, m.gitRerun(n, ids)
	case "l", "enter", "a":
		c, ok := v.selectedFailed()
		if !ok {
			p.info, p.err = "No failed check", ""
			return true, nil
		}
		if c.RunID() == "" {
			p.info, p.err = c.Name+" is not a GitHub Actions check; o opens it in the browser", ""
			return true, nil
		}
		if k == "a" {
			return true, m.gitHandoff(n, c, "")
		}
		return true, m.gitFailedLog(n, c)
	case "m":
		if v.pr.State != "" && v.pr.State != "OPEN" {
			p.info, p.err = fmt.Sprintf("#%d is %s", n, strings.ToLower(v.pr.State)), ""
			return true, nil
		}
		v.merging, v.method = true, 0
		p.err, p.info = "", ""
		return true, nil
	case "c":
		return true, m.gitCheckoutPR(n)
	case "o":
		return true, m.gitOpenWeb(n)
	}
	return false, nil
}

// gitScrollKey scrolls a view that has no cursor.
func (m *UI) gitScrollKey(v *gitView, k string) (bool, tea.Cmd) {
	p := &m.git.pane
	w := p.w()
	n := max(1, m.gitContentHeight(w, p.lastH))
	page := max(1, n-1)
	total := len(m.gitBodyLines(w, false).lines)
	v.off = max(0, min(v.off, total-n))
	switch k {
	case "up", "k":
		v.off--
	case "down", "j":
		v.off++
	case "pgup", "ctrl+u":
		v.off -= page
	case "pgdown", "ctrl+d", "space":
		v.off += page
	case "home", "g":
		v.off = 0
	case "end", "G":
		v.off = gitBottom
	default:
		return false, nil
	}
	v.off = max(0, min(v.off, total-n))
	return true, nil
}

// gitPaneClick selects the row under the pointer; clicking the selected row
// acts on it like enter.
func (m *UI) gitPaneClick(_, y int) tea.Cmd {
	p := &m.git.pane
	v := p.top()
	if v.kind != gitViewRoot && v.kind != gitViewPR {
		return nil
	}
	if p.committing || p.confirm != "" || v.merging {
		return nil
	}
	w := max(p.lastW, 1)
	body := m.gitBodyLines(w, true)
	line := v.off + y
	if y < 0 || line < 0 || line >= len(body.lines) || line >= v.off+m.gitContentHeight(w, p.lastH) {
		return nil
	}
	idx := body.items[line]
	if idx < 0 {
		return nil
	}
	switch v.kind {
	case gitViewRoot:
		rows := m.gitRootRows(w)
		if idx >= len(rows) {
			return nil
		}
		if gitSelected(rows, v) == idx {
			if p.busy != "" {
				return nil
			}
			return m.gitRootEnter(rows[idx])
		}
		v.selectIn(rows, idx)
	case gitViewPR:
		if v.sel == idx {
			_, cmd := m.gitPRKey(v, "l")
			return cmd
		}
		v.sel = idx
	}
	return nil
}
