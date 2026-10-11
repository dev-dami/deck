package dialog

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"

	"github.com/dev-dami/deck/internal/deck/gh"
	"github.com/dev-dami/deck/internal/ui/common"
	"github.com/dev-dami/deck/internal/ui/styles"
	"github.com/dev-dami/deck/internal/ui/tone"
)

// GitKey is the key that opens the git panel, as hints spell it.
const GitKey = "ctrl+x"

// GitNoGHHint is shown wherever GitHub is needed but gh is missing or
// signed out.
const GitNoGHHint = "install gh and run `gh auth login`"

// GitBusyDialog is a git dialog that can show a running operation.
type GitBusyDialog interface {
	// SetBusy shows label with a spinner; "" clears it.
	SetBusy(label string) tea.Cmd
}

// Check colours: mint passes, coral fails, amber waits.
var (
	gitPassStyle = lipgloss.NewStyle().Foreground(tone.Mint)
	gitFailStyle = lipgloss.NewStyle().Foreground(tone.Coral)
	gitWaitStyle = lipgloss.NewStyle().Foreground(tone.Amber)
)

// Check glyphs.
const (
	gitGlyphPass = "✓"
	gitGlyphFail = "✗"
	gitGlyphWait = "●"
	gitGlyphSkip = "–"
)

// GitCheckGlyph is a check's glyph in its colour.
func GitCheckGlyph(t *styles.Styles, c gh.Check) string {
	switch {
	case c.Pending():
		return gitWaitStyle.Render(gitGlyphWait)
	case c.Failed():
		return gitFailStyle.Render(gitGlyphFail)
	case c.Bucket == gh.BucketSkipping:
		return gitMuted(t).Render(gitGlyphSkip)
	default:
		return gitPassStyle.Render(gitGlyphPass)
	}
}

// GitChecksBadge is a compact verdict on a set of checks: "✓", "✗", or
// "● 3/5" while some still run. It is "" when there are no checks.
func GitChecksBadge(s gh.CheckSummary) string {
	switch s.State() {
	case gh.ChecksPassed:
		return gitPassStyle.Render(gitGlyphPass)
	case gh.ChecksFailed:
		return gitFailStyle.Render(gitGlyphFail)
	case gh.ChecksPending:
		st := gitWaitStyle
		if s.Failed > 0 {
			st = gitFailStyle
		}
		return st.Render(fmt.Sprintf("%s %d/%d", gitGlyphWait, s.Done(), s.Total))
	}
	return ""
}

// GitChecksText says where checks stand in words, coloured: "checks
// passed", "2 failed", "3/5 checks done".
func GitChecksText(s gh.CheckSummary) string {
	switch s.State() {
	case gh.ChecksPassed:
		return gitPassStyle.Render(gitGlyphPass + " checks passed")
	case gh.ChecksFailed:
		return gitFailStyle.Render(fmt.Sprintf("%s %d of %d checks failed", gitGlyphFail, s.Failed, s.Total))
	case gh.ChecksPending:
		st := gitWaitStyle
		if s.Failed > 0 {
			st = gitFailStyle
		}
		return st.Render(fmt.Sprintf("%s %d/%d checks done", gitGlyphWait, s.Done(), s.Total))
	}
	return ""
}

// GitAgo says how long ago t was: "just now", "4m ago", "3h ago", "2d ago".
func GitAgo(t, now time.Time) string {
	if t.IsZero() {
		return ""
	}
	d := now.Sub(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}

// GitAheadBehind is "↑2 ↓1", leaving out zeros; "" when level.
func GitAheadBehind(st gh.Status) string {
	var parts []string
	if st.Ahead > 0 {
		parts = append(parts, fmt.Sprintf("↑%d", st.Ahead))
	}
	if st.Behind > 0 {
		parts = append(parts, fmt.Sprintf("↓%d", st.Behind))
	}
	return strings.Join(parts, " ")
}

// gitStatusLine is the branch, how it stands against its upstream, how many
// files changed and its pull request: "feature ↑2 ↓1 · 3 changed · #12 ✓".
func gitStatusLine(t *styles.Styles, st gh.Status, pr *gh.PR) string {
	muted := gitMuted(t)
	branch := st.Branch
	if st.Detached {
		branch = "detached HEAD"
	}
	parts := []string{t.Header.GitBranch.Render(branch)}
	if ab := GitAheadBehind(st); ab != "" {
		parts[0] += " " + gitPrimary(t).Render(ab)
	}
	switch {
	case st.Branch != "" && st.Upstream == "":
		parts = append(parts, muted.Render("not pushed"))
	}
	if st.Dirty > 0 {
		parts = append(parts, gitWaitStyle.Render(fmt.Sprintf("%d changed", st.Dirty)))
	} else {
		parts = append(parts, muted.Render("clean"))
	}
	if pr != nil {
		s := gitPrimary(t).Render(fmt.Sprintf("#%d", pr.Number))
		if b := GitChecksBadge(gh.Summarize(pr.Checks)); b != "" {
			s += " " + b
		}
		parts = append(parts, s)
	}
	return strings.Join(parts, muted.Render(" · "))
}

// gitMuted is the dialogs' secondary text colour without its padding.
func gitMuted(t *styles.Styles) lipgloss.Style {
	return lipgloss.NewStyle().Foreground(t.Dialog.SecondaryText.GetForeground())
}

// gitPrimary is the dialogs' primary text colour without its padding.
func gitPrimary(t *styles.Styles) lipgloss.Style {
	return lipgloss.NewStyle().Foreground(t.Dialog.PrimaryText.GetForeground())
}

// gitDialogWidth is a dialog's total width: want, clamped to the area.
func gitDialogWidth(t *styles.Styles, area uv.Rectangle, want int) int {
	return max(0, min(want, area.Dx()-t.Dialog.View.GetHorizontalMargins()))
}

// gitInner is the content width of a dialog of total width w.
func gitInner(t *styles.Styles, w int) int {
	return max(0, w-t.Dialog.View.GetHorizontalFrameSize())
}

// gitTitle is a dialog's title line, sized to the content width.
func gitTitle(t *styles.Styles, title string, inner int) string {
	w := max(0, inner-t.Dialog.Title.GetHorizontalFrameSize())
	return t.Dialog.Title.Render(common.DialogTitle(t, title, w, t.Dialog.TitleGradFromColor, t.Dialog.TitleGradToColor))
}

// gitPad is the one-cell inset of a dialog's body lines.
const gitPad = 1

// gitLine is one body line: text inset by gitPad, cut to fit.
func gitLine(text string, inner int) string {
	w := max(0, inner-2*gitPad)
	return lipgloss.NewStyle().Padding(0, gitPad).Width(inner).Render(ansi.Truncate(text, w, "…"))
}

// gitRow is one selectable row. A selected row is drawn plain on the
// highlight, so inner colours cannot break its background.
func gitRow(t *styles.Styles, text string, selected bool, inner int) string {
	if selected {
		w := max(0, inner-t.Dialog.SelectedItem.GetHorizontalFrameSize())
		return t.Dialog.SelectedItem.Width(inner).Render(ansi.Truncate(ansi.Strip(text), w, "…"))
	}
	return gitLine(text, inner)
}

// gitView frames a dialog's lines.
func gitView(t *styles.Styles, width int, lines []string) string {
	return t.Dialog.View.Width(width).Render(strings.Join(lines, "\n"))
}

// gitCursor moves a cursor reported by an input on body line row (counted
// from the top of the content) and indented by col into dialog space.
func gitCursor(t *styles.Styles, cur *tea.Cursor, row, col int) *tea.Cursor {
	if cur == nil {
		return nil
	}
	v := t.Dialog.View
	cur.X += v.GetMarginLeft() + v.GetBorderLeftSize() + v.GetPaddingLeft() + col
	cur.Y += v.GetMarginTop() + v.GetBorderTopSize() + v.GetPaddingTop() + row
	return cur
}

// gitWindow is the slice of n rows of a list of total rows that keeps sel
// in view, given the previous offset.
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
