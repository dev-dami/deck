package model

import (
	"cmp"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/dev-dami/deck/internal/deck/gh"
)

// Root list limits.
const (
	gitPaneMaxFiles = 8
	gitHintLines    = 2
)

// The root's sections, in order.
const (
	gitSectionStatus = iota
	gitSectionThisPR
	gitSectionPRs
	gitSectionBranches
	gitSectionCommits
)

// gitRowKind is a kind of root row.
type gitRowKind int

const (
	gitRowHeader gitRowKind = iota // a section's title
	gitRowNote                     // a line to read, not to select
	gitRowBranch                   // the current branch and its upstream
	gitRowFile                     // a changed file
	gitRowThisPR                   // the branch's pull request
	gitRowNoPR                     // "no pull request · P opens one"
	gitRowPR                       // an open pull request
	gitRowRecent                   // a recent branch
	gitRowCommit                   // a recent commit
)

// gitRow is one line of the root view.
type gitRow struct {
	kind    gitRowKind
	section int
	key     string
	text    string // styled, not yet cut to the width
	right   string // styled, drawn at the right edge
	number  int    // a pull request
	name    string // a branch or a commit hash
}

func (r gitRow) selectable() bool { return r.kind >= gitRowBranch }

// gitRowKey is the key of a selectable root row.
func gitRowKey(kind gitRowKind, name string) string {
	return strconv.Itoa(int(kind)) + ":" + name
}

// gitSectionKey selects a section's first row.
func gitSectionKey(section int) string { return "§" + strconv.Itoa(section) }

// gitSelected is the index of the root's selected row: by key, else the
// remembered index moved onto a selectable row; -1 when nothing can be
// selected.
func gitSelected(rows []gitRow, v *gitView) int {
	if v.selKey != "" {
		for i, r := range rows {
			if !r.selectable() {
				continue
			}
			if r.key == v.selKey || gitSectionKey(r.section) == v.selKey {
				return i
			}
		}
	}
	i := max(0, min(v.sel, len(rows)-1))
	if i < len(rows) && rows[i].selectable() {
		return i
	}
	if j := gitStep(rows, i, 1); j != i {
		return j
	}
	if j := gitStep(rows, i, -1); j >= 0 && j < len(rows) && rows[j].selectable() {
		return j
	}
	return -1
}

// selectIn puts the root's cursor on row i.
func (v *gitView) selectIn(rows []gitRow, i int) {
	if i < 0 || i >= len(rows) {
		return
	}
	v.sel, v.selKey = i, rows[i].key
}

// gitStep is the next selectable row from cur in dir, or cur when there is
// none; -1 when cur is -1 and nothing is selectable.
func gitStep(rows []gitRow, cur, dir int) int {
	for i := cur + dir; i >= 0 && i < len(rows); i += dir {
		if rows[i].selectable() {
			return i
		}
	}
	if cur >= len(rows) {
		return -1
	}
	return cur
}

// gitNextSection is the first selectable row of the next (dir 1) or
// previous (dir -1) section that has one, wrapping around.
func gitNextSection(rows []gitRow, cur, dir int) int {
	firsts := map[int]int{}
	var order []int
	for i, r := range rows {
		if !r.selectable() {
			continue
		}
		if _, ok := firsts[r.section]; !ok {
			firsts[r.section] = i
			order = append(order, r.section)
		}
	}
	if len(order) == 0 {
		return cur
	}
	sec := -1
	if cur >= 0 && cur < len(rows) {
		sec = rows[cur].section
	}
	at := slices.Index(order, sec)
	if at < 0 {
		return firsts[order[0]]
	}
	return firsts[order[(at+dir+len(order))%len(order)]]
}

// gitRootRows is the root view's lines.
func (m *UI) gitRootRows(width int) []gitRow {
	g := &m.git
	t := m.com.Styles
	muted := gitMuted(t)
	primary := gitPrimary(t)
	now := time.Now()
	var rows []gitRow
	header := func(sec int, text string) {
		rows = append(rows, gitRow{kind: gitRowHeader, section: sec, text: t.Resource.Heading.Render(text)})
	}
	note := func(sec int, text string) {
		rows = append(rows, gitRow{kind: gitRowNote, section: sec, text: text})
	}

	// Status.
	header(gitSectionStatus, "Status")
	if !g.statusOK {
		if g.statusErr != "" {
			note(gitSectionStatus, gitFailStyle.Render(g.statusErr))
		} else {
			note(gitSectionStatus, muted.Render("loading…"))
		}
		return rows
	}
	st := g.status
	branch := st.Branch
	if st.Detached {
		branch = "detached HEAD"
	}
	text := t.Header.GitBranch.Render(branch)
	if ab := gitAheadBehind(st); ab != "" {
		text += " " + primary.Render(ab)
	}
	switch {
	case st.Upstream != "":
		text += muted.Render(" → " + st.Upstream)
	case st.Branch != "":
		text += muted.Render(" · not pushed")
	}
	rows = append(rows, gitRow{kind: gitRowBranch, section: gitSectionStatus, key: gitRowKey(gitRowBranch, ""), text: text})
	if len(g.files) == 0 && st.Dirty == 0 {
		note(gitSectionStatus, muted.Render("clean"))
	}
	for i, f := range g.files {
		if i == gitPaneMaxFiles {
			note(gitSectionStatus, muted.Render(fmt.Sprintf("+%d more", len(g.files)-i)))
			break
		}
		path := f.Path
		if f.Orig != "" {
			path = f.Orig + " → " + f.Path
		}
		rows = append(rows, gitRow{kind: gitRowFile, section: gitSectionStatus, key: gitRowKey(gitRowFile, f.Path), text: gitFileCode(f) + " " + path})
	}

	// GitHub.
	switch {
	case g.ghChecked && !g.ghAvail:
		header(gitSectionThisPR, "GitHub")
		note(gitSectionThisPR, muted.Render(gitNoGHHint))
	case !g.ghChecked:
		header(gitSectionThisPR, "Pull request")
		note(gitSectionThisPR, muted.Render("asking GitHub…"))
	default:
		if st.Branch != "" {
			header(gitSectionThisPR, "This branch's PR")
			if g.pr != nil && g.prBranch == st.Branch {
				pr := g.pr
				rows = append(rows, gitRow{kind: gitRowThisPR, section: gitSectionThisPR, key: gitRowKey(gitRowThisPR, ""),
					number: pr.Number, text: primary.Render(fmt.Sprintf("#%d", pr.Number)) + " " + pr.Title})
				facts := []string{gitPRState(t, pr)}
				if r := gitReview(pr); r != "" {
					facts = append(facts, r)
				}
				if c := gitChecksText(g.checks); c != "" {
					facts = append(facts, c)
				}
				note(gitSectionThisPR, strings.Join(facts, muted.Render(" · ")))
			} else {
				rows = append(rows, gitRow{kind: gitRowNoPR, section: gitSectionThisPR, key: gitRowKey(gitRowNoPR, ""),
					text: muted.Render("no pull request · P opens one")})
			}
		}

		header(gitSectionPRs, fmt.Sprintf("Pull requests (%d)", len(g.prs)))
		switch {
		case g.headsAt.IsZero():
			note(gitSectionPRs, muted.Render("loading…"))
		case len(g.prs) == 0:
			note(gitSectionPRs, muted.Render("none open"))
		}
		prs := slices.Clone(g.prs)
		slices.SortStableFunc(prs, func(a, b gh.PR) int {
			return cmp.Compare(gitBoolRank(a.HeadRefName != st.Branch), gitBoolRank(b.HeadRefName != st.Branch))
		})
		for _, pr := range prs {
			right := gitChecksBadge(gh.Summarize(pr.Checks))
			if pr.IsDraft {
				right = strings.TrimSpace(muted.Render("draft") + " " + right)
			}
			rows = append(rows, gitRow{kind: gitRowPR, section: gitSectionPRs, key: gitRowKey(gitRowPR, strconv.Itoa(pr.Number)),
				number: pr.Number, text: primary.Render(fmt.Sprintf("#%d", pr.Number)) + " " + pr.Title, right: right})
		}
	}

	// Recent branches.
	if len(g.recent) > 0 {
		header(gitSectionBranches, "Branches")
		for _, b := range g.recent {
			name := b.Name
			var text string
			if name == st.Branch {
				text = t.Header.GitBranch.Render("• " + name)
			} else {
				text = "  " + name
			}
			right := strings.TrimSuffix(gitAgo(b.Committed, now), " ago")
			if g.ghAvail {
				switch n := g.heads[name]; {
				case n > 0:
					right += " " + primary.Render(fmt.Sprintf("#%d", n))
				case !g.headsAt.IsZero():
					right += " no PR"
				}
			}
			rows = append(rows, gitRow{kind: gitRowRecent, section: gitSectionBranches, key: gitRowKey(gitRowRecent, name),
				name: name, text: text, right: muted.Render(right)})
		}
	}

	// Recent commits.
	if len(g.commits) > 0 {
		header(gitSectionCommits, "Commits")
		for _, c := range g.commits {
			rows = append(rows, gitRow{kind: gitRowCommit, section: gitSectionCommits, key: gitRowKey(gitRowCommit, c.Hash),
				name: c.Hash, text: muted.Render(c.Hash) + " " + c.Subject,
				right: muted.Render(strings.TrimSuffix(gitAgo(c.When, now), " ago"))})
		}
	}
	return rows
}

func gitBoolRank(b bool) int {
	if b {
		return 1
	}
	return 0
}

// gitBody is a view's scrollable lines.
type gitBody struct {
	lines []string
	// items is, per line, the index of the row or check on it, or -1.
	items []int
	// sel is the selected line, or -1 when the view only scrolls.
	sel int
}

func (b *gitBody) add(line string, item int) {
	b.lines = append(b.lines, line)
	b.items = append(b.items, item)
}

// gitFit is text cut to width after a one-cell inset, with right (if any)
// at the right edge.
func gitFit(text, right string, width int) string {
	inner := max(0, width-1)
	if right == "" {
		return " " + ansi.Truncate(text, inner, "…")
	}
	rw := lipgloss.Width(right)
	if rw+2 > inner {
		return " " + ansi.Truncate(text, inner, "…")
	}
	left := ansi.Truncate(text, inner-rw-1, "…")
	gap := max(1, inner-lipgloss.Width(left)-rw)
	return " " + left + strings.Repeat(" ", gap) + right
}

// gitHighlight is a selected line: plain text on the highlight, so inner
// colours cannot break its background.
func (m *UI) gitHighlight(text, right string, width int) string {
	st := m.com.Styles.Dialog.SelectedItem
	inner := max(0, width-st.GetHorizontalFrameSize())
	line := strings.TrimPrefix(gitFit(ansi.Strip(text), ansi.Strip(right), inner+1), " ")
	return st.Width(width).Render(line)
}

// gitBodyLines is the view on show as lines of width.
func (m *UI) gitBodyLines(width int, focused bool) gitBody {
	p := &m.git.pane
	v := p.top()
	b := gitBody{sel: -1}
	switch v.kind {
	case gitViewRoot:
		m.gitRootBody(&b, v, width, focused)
	case gitViewPR:
		m.gitPRBody(&b, v, width, focused)
	case gitViewLog:
		m.gitLogBody(&b, v, width)
	case gitViewCommit:
		m.gitCommitBody(&b, v, width)
	case gitViewCreate:
		m.gitFormBody(&b, v, width)
	}
	return b
}

func (m *UI) gitRootBody(b *gitBody, v *gitView, width int, focused bool) {
	rows := m.gitRootRows(width)
	cur := gitSelected(rows, v)
	if cur >= 0 {
		v.sel = cur
	}
	for i, r := range rows {
		item := -1
		if r.selectable() {
			item = i
		}
		if i == cur && focused {
			b.sel = len(b.lines)
			b.add(m.gitHighlight(r.text, r.right, width), item)
			continue
		}
		if i == cur {
			b.sel = len(b.lines)
		}
		b.add(gitFit(r.text, r.right, width), item)
	}
}

func (m *UI) gitPRBody(b *gitBody, v *gitView, width int, focused bool) {
	t := m.com.Styles
	muted := gitMuted(t)
	primary := gitPrimary(t)
	spin := gitSpinner(m.git.pane.frame)
	if v.pr == nil {
		title := "Pull request"
		if v.number > 0 {
			title = fmt.Sprintf("Pull request #%d", v.number)
		}
		b.add(gitFit(t.Resource.Heading.Render(title), "", width), -1)
		switch {
		case v.err != "":
			for _, l := range gitWrap(v.err, width-1) {
				b.add(gitFit(gitFailStyle.Render(l), "", width), -1)
			}
		default:
			b.add(gitFit(spin+" "+muted.Render("loading…"), "", width), -1)
		}
		return
	}
	pr := v.pr
	b.add(gitFit(primary.Render(fmt.Sprintf("#%d", pr.Number))+" "+pr.Title, "", width), -1)
	facts := []string{gitPRState(t, pr)}
	if pr.IsDraft && pr.State == "OPEN" {
		facts[0] = muted.Render("draft")
	}
	if r := gitReview(pr); r != "" {
		facts = append(facts, r)
	}
	switch pr.Mergeable {
	case "MERGEABLE":
		facts = append(facts, muted.Render("mergeable"))
	case "CONFLICTING":
		facts = append(facts, gitFailStyle.Render("conflicts"))
	}
	right := ""
	if v.loading {
		right = spin
	}
	b.add(gitFit(strings.Join(facts, muted.Render(" · ")), right, width), -1)
	b.add(gitFit(t.Header.GitBranch.Render(pr.HeadRefName)+muted.Render(" → ")+t.Header.GitBranch.Render(pr.BaseRefName), "", width), -1)
	if v.err != "" {
		b.add(gitFit(gitFailStyle.Render(v.err), "", width), -1)
	}
	b.add("", -1)
	sum := gh.Summarize(v.checks)
	if sum.Total == 0 {
		b.add(gitFit(muted.Render("No checks."), "", width), -1)
		return
	}
	b.add(gitFit(gitChecksText(sum), "", width), -1)
	frame := m.git.pane.frame
	for i, c := range v.checks {
		text := gitCheckGlyph(t, c, frame) + " " + c.Name
		right := ""
		if c.Workflow != "" {
			right = muted.Render(c.Workflow)
		}
		if i == v.sel {
			b.sel = len(b.lines)
			if focused {
				// Keep the glyph's colour: it is the point of the row.
				line := m.gitHighlight(c.Name, right, max(0, width-2))
				b.add(" "+gitCheckGlyph(t, c, frame)+line, i)
				continue
			}
		}
		b.add(gitFit(text, right, width), i)
	}
}

func (m *UI) gitLogBody(b *gitBody, v *gitView, width int) {
	t := m.com.Styles
	title := fmt.Sprintf("#%d · %s · failed log", v.number, v.check.Name)
	b.add(gitFit(t.Resource.Heading.Render(title), "", width), -1)
	for _, l := range strings.Split(strings.TrimRight(v.log, "\n"), "\n") {
		b.add(gitFit(strings.ReplaceAll(l, "\t", "    "), "", width), -1)
	}
}

func (m *UI) gitCommitBody(b *gitBody, v *gitView, width int) {
	t := m.com.Styles
	muted := gitMuted(t)
	if v.commit == nil {
		b.add(gitFit(t.Resource.Heading.Render(v.hash), "", width), -1)
		if v.err != "" {
			b.add(gitFit(gitFailStyle.Render(v.err), "", width), -1)
		} else {
			b.add(gitFit(gitSpinner(m.git.pane.frame)+" "+muted.Render("loading…"), "", width), -1)
		}
		return
	}
	c := v.commit
	b.add(gitFit(muted.Render(c.Hash)+" "+gitPrimary(t).Render(c.Subject), "", width), -1)
	b.add(gitFit(muted.Render(c.Author+" · "+gitAgo(c.When, time.Now())), "", width), -1)
	if c.Body != "" {
		b.add("", -1)
		for _, l := range gitWrap(c.Body, width-1) {
			b.add(gitFit(muted.Render(l), "", width), -1)
		}
	}
	b.add("", -1)
	if len(c.Files) == 0 {
		b.add(gitFit(muted.Render("No file changes (a merge, or empty)."), "", width), -1)
		return
	}
	files := "files"
	if len(c.Files) == 1 {
		files = "file"
	}
	b.add(gitFit(fmt.Sprintf("%d %s · ", len(c.Files), files)+
		gitPassStyle.Render(fmt.Sprintf("+%d", c.Added))+" "+gitFailStyle.Render(fmt.Sprintf("−%d", c.Deleted)), "", width), -1)
	for _, f := range c.Files {
		right := muted.Render("binary")
		if !f.Binary {
			right = gitPassStyle.Render(fmt.Sprintf("+%d", f.Added)) + " " + gitFailStyle.Render(fmt.Sprintf("−%d", f.Deleted))
		}
		b.add(gitFit(f.Path, right, width), -1)
	}
}

func (m *UI) gitFormBody(b *gitBody, v *gitView, width int) {
	t := m.com.Styles
	muted := gitMuted(t)
	f := v.form
	if f == nil || f.preparing {
		b.add(gitFit(t.Resource.Heading.Render("Open a pull request"), "", width), -1)
		b.add(gitFit(gitSpinner(m.git.pane.frame)+" "+muted.Render("preparing…"), "", width), -1)
		return
	}
	label := func(text string, field int) string {
		if f.field == field {
			return t.Dialog.Arguments.InputLabelFocused.Render(text)
		}
		return t.Dialog.Arguments.InputLabelBlurred.Render(text)
	}
	inner := max(1, width-1)
	b.add(gitFit(t.Resource.Heading.Render("Open a PR from ")+t.Header.GitBranch.Render(f.branch), "", width), -1)
	if f.notes != "" {
		b.add(gitFit(muted.Render(f.notes), "", width), -1)
	}
	b.add("", -1)
	f.title.SetWidth(max(1, inner-lipgloss.Width(f.title.Prompt)-1))
	f.base.SetWidth(max(1, inner-lipgloss.Width(f.base.Prompt)-1))
	b.add(gitFit(label("Title", prFieldTitle), "", width), -1)
	b.add(" "+ansi.Truncate(f.title.View(), inner, ""), -1)
	b.add(gitFit(label("Base branch", prFieldBase), "", width), -1)
	b.add(" "+ansi.Truncate(f.base.View(), inner, ""), -1)
	b.add(gitFit(label("Description", prFieldBody), "", width), -1)
	f.body.SetWidth(inner)
	f.body.SetHeight(gitPRBodyHeight)
	for _, l := range strings.Split(f.body.View(), "\n") {
		b.add(" "+ansi.Truncate(l, inner, ""), -1)
	}
	box := "[ ] Draft"
	if f.draft {
		box = "[x] Draft"
	}
	b.add(gitFit(label(box, prFieldDraft), "", width), -1)
}

// gitWrap breaks text into lines of at most width cells.
func gitWrap(text string, width int) []string {
	if width <= 0 {
		return nil
	}
	return strings.Split(ansi.Wrap(text, width, ""), "\n")
}

// gitFooter is the pane's fixed bottom lines: an inline input or question,
// the status line, then the key hints.
func (m *UI) gitFooter(width int) (lines, hints []string) {
	g := &m.git
	p := &g.pane
	t := m.com.Styles
	muted := gitMuted(t)
	v := p.top()
	inner := max(1, width-1)

	var keys []string
	switch {
	case v.kind == gitViewRoot && p.committing:
		prompt := gitPrimary(t).Render("commit › ")
		p.input.SetWidth(max(1, inner-lipgloss.Width(prompt)-1))
		lines = append(lines, " "+ansi.Truncate(prompt+p.input.View(), inner, ""))
		keys = []string{"enter commit", "esc cancel"}
	case v.kind == gitViewRoot && p.confirm != "":
		q := fmt.Sprintf("%d changed · check out %s anyway?", g.status.Dirty, p.confirm)
		lines = append(lines, gitFit(gitWaitStyle.Render(q), "", width))
		keys = []string{"y check out", "n keep this branch"}
	case v.merging && v.pr != nil:
		parts := make([]string, len(gh.MergeMethods))
		for i, meth := range gh.MergeMethods {
			if i == v.method {
				parts[i] = t.Dialog.Arguments.InputLabelFocused.Render("‹" + meth + "›")
			} else {
				parts[i] = muted.Render(" " + meth + " ")
			}
		}
		lines = append(lines, gitFit(gitWaitStyle.Render(fmt.Sprintf("merge #%d:", v.pr.Number))+" "+strings.Join(parts, " "), "", width))
		keys = []string{"←→ method", "enter merge", "esc cancel"}
	case v.kind == gitViewRoot:
		keys = []string{"↑↓ move", "tab section", "enter open", "c commit", "p push", "f fetch", "P PR", "R refresh"}
	case v.kind == gitViewPR:
		keys = []string{"↑↓ check", "l log", "a agent", "r rerun", "m merge", "c checkout", "o web", "R refresh", "esc back"}
	case v.kind == gitViewLog:
		keys = []string{"↑↓ scroll", "a agent", "esc back"}
	case v.kind == gitViewCommit:
		keys = []string{"↑↓ scroll", "esc back"}
	case v.kind == gitViewCreate:
		if v.form != nil && v.form.field == prFieldDraft {
			keys = []string{"space draft", "enter open PR", "esc cancel"}
		} else {
			keys = []string{"tab next", "ctrl+s open PR", "esc cancel"}
		}
	}

	switch {
	case p.busy != "":
		lines = append(lines, gitFit(gitSpinner(p.frame)+" "+muted.Render(p.busy), "", width))
	case p.refreshing:
		lines = append(lines, gitFit(gitSpinner(p.frame)+" "+muted.Render("refreshing…"), "", width))
	case p.err != "":
		lines = append(lines, gitFit(gitFailStyle.Render(p.err), "", width))
	case p.info != "":
		lines = append(lines, gitFit(muted.Render(p.info), "", width))
	}
	return lines, gitPackHints(muted.Render, keys, inner)
}

// gitPackHints lays key hints out in at most gitHintLines lines of width.
func gitPackHints(style func(...string) string, keys []string, width int) []string {
	var out []string
	cur := ""
	for _, k := range keys {
		switch {
		case cur == "":
			cur = k
		case lipgloss.Width(cur)+3+lipgloss.Width(k) <= width:
			cur += " · " + k
		default:
			out = append(out, cur)
			cur = k
		}
	}
	if cur != "" {
		out = append(out, cur)
	}
	if len(out) > gitHintLines {
		out = out[:gitHintLines]
	}
	for i, l := range out {
		out[i] = " " + style(ansi.Truncate(l, width, "…"))
	}
	return out
}

// gitLayout is the footer for a body of height h and how many body lines
// fit above it. Hints go first when space is short.
func (m *UI) gitLayout(width, h int) (footer []string, n int) {
	lines, hints := m.gitFooter(width)
	footer = append(lines, hints...)
	if h-len(footer) < 2 {
		footer = lines
	}
	if len(footer) > h {
		footer = footer[len(footer)-h:]
	}
	return footer, max(0, h-len(footer))
}

// gitContentHeight is how many body lines show at width x h.
func (m *UI) gitContentHeight(width, h int) int {
	_, n := m.gitLayout(width, h)
	return n
}

// gitPaneHeight is the lines the view on show needs at width.
func (m *UI) gitPaneHeight(width int) int {
	if width <= 0 {
		return 0
	}
	body := m.gitBodyLines(width, false)
	lines, hints := m.gitFooter(width)
	return len(body.lines) + len(lines) + len(hints)
}

// gitPaneRender draws the view on show in width x h, scrolled so the
// cursor stays in view, with the footer at the bottom.
func (m *UI) gitPaneRender(width, h int, focused bool) string {
	p := &m.git.pane
	p.lastW, p.lastH, p.drawnAt = width, h, time.Now()
	if width <= 0 || h <= 0 {
		return ""
	}
	body := m.gitBodyLines(width, focused)
	footer, n := m.gitLayout(width, h)
	v := p.top()
	total := len(body.lines)
	if body.sel >= 0 {
		v.off = gitWindow(body.sel, v.off, n, total)
	} else {
		v.off = max(0, min(v.off, total-n))
	}
	out := make([]string, 0, h)
	for i := v.off; i < min(total, v.off+n); i++ {
		out = append(out, body.lines[i])
	}
	for len(out) < n {
		out = append(out, "")
	}
	out = append(out, footer...)
	return strings.Join(out, "\n")
}

// gitWindow is the first of n rows to show of total so that sel stays in
// view, given the previous offset.
func gitWindow(sel, offset, n, total int) int {
	if n <= 0 || total <= n {
		return 0
	}
	if sel < offset {
		offset = sel
	}
	if sel >= offset+n {
		offset = sel - n + 1
	}
	return max(0, min(offset, total-n))
}
