package model

import (
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/spinner"
	"charm.land/lipgloss/v2"

	"github.com/dev-dami/deck/internal/deck/gh"
	"github.com/dev-dami/deck/internal/ui/motion"
	"github.com/dev-dami/deck/internal/ui/styles"
	"github.com/dev-dami/deck/internal/ui/tone"
)

// gitKey is the key that opens the git pane, as hints spell it.
const gitKey = "ctrl+x"

// gitNoGHHint is shown wherever GitHub is needed but gh is missing or
// signed out.
const gitNoGHHint = "install gh and run `gh auth login`"

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

// gitSpinnerFrames turn while something runs; one cell wide.
var gitSpinnerFrames = spinner.MiniDot.Frames

// gitSpinner is the spinner glyph for frame, in the waiting colour; a still
// dot when motion is off.
func gitSpinner(frame int) string {
	if motion.Off() {
		return gitWaitStyle.Render(gitGlyphWait)
	}
	return gitWaitStyle.Render(gitSpinnerFrames[frame%len(gitSpinnerFrames)])
}

// gitMuted is the secondary text colour.
func gitMuted(t *styles.Styles) lipgloss.Style {
	return lipgloss.NewStyle().Foreground(t.Dialog.SecondaryText.GetForeground())
}

// gitPrimary is the primary text colour.
func gitPrimary(t *styles.Styles) lipgloss.Style {
	return lipgloss.NewStyle().Foreground(t.Dialog.PrimaryText.GetForeground())
}

// gitCheckGlyph is a check's glyph in its colour; a pending check gets the
// spinner at frame.
func gitCheckGlyph(t *styles.Styles, c gh.Check, frame int) string {
	switch {
	case c.Pending():
		return gitSpinner(frame)
	case c.Failed():
		return gitFailStyle.Render(gitGlyphFail)
	case c.Bucket == gh.BucketSkipping:
		return gitMuted(t).Render(gitGlyphSkip)
	default:
		return gitPassStyle.Render(gitGlyphPass)
	}
}

// gitChecksBadge is a compact verdict on a set of checks: "✓", "✗", or
// "● 3/5" while some still run. It is "" when there are no checks.
func gitChecksBadge(s gh.CheckSummary) string {
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

// gitChecksText says where checks stand in words, coloured: "checks
// passed", "2 of 5 checks failed", "3/5 checks done".
func gitChecksText(s gh.CheckSummary) string {
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

// gitAgo says how long ago t was: "just now", "4m ago", "3h ago", "2d ago".
func gitAgo(t, now time.Time) string {
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

// gitAgoUntil is when gitAgo(t, ·) next reads differently after now, so
// text built with it at now holds until then; zero when t is zero, whose
// "" never changes.
func gitAgoUntil(t, now time.Time) time.Time {
	if t.IsZero() {
		return time.Time{}
	}
	d := now.Sub(t)
	var step time.Duration
	switch {
	case d < time.Minute:
		return t.Add(time.Minute)
	case d < time.Hour:
		step = time.Minute
	case d < 48*time.Hour:
		step = time.Hour
	default:
		step = 24 * time.Hour
	}
	return t.Add((d/step + 1) * step)
}

// gitAheadBehind is "↑2 ↓1", leaving out zeros; "" when level.
func gitAheadBehind(st gh.Status) string {
	var parts []string
	if st.Ahead > 0 {
		parts = append(parts, fmt.Sprintf("↑%d", st.Ahead))
	}
	if st.Behind > 0 {
		parts = append(parts, fmt.Sprintf("↓%d", st.Behind))
	}
	return strings.Join(parts, " ")
}

// gitPRState is a pull request's state in a word, coloured: "open",
// "draft", "merged", "closed".
func gitPRState(t *styles.Styles, pr *gh.PR) string {
	muted := gitMuted(t)
	switch pr.State {
	case "MERGED":
		return gitPassStyle.Render("merged")
	case "CLOSED":
		return gitFailStyle.Render("closed")
	}
	if pr.IsDraft {
		return muted.Render("draft")
	}
	return gitPrimary(t).Render("open")
}

// gitReview is a pull request's review decision in words, coloured; "" when
// none is needed.
func gitReview(pr *gh.PR) string {
	switch pr.ReviewDecision {
	case "APPROVED":
		return gitPassStyle.Render("approved")
	case "CHANGES_REQUESTED":
		return gitFailStyle.Render("changes requested")
	case "REVIEW_REQUIRED":
		return gitWaitStyle.Render("review required")
	}
	return ""
}

// gitFileCode is a changed file's status letter, coloured: green when
// staged, amber in the work tree, coral for a conflict.
func gitFileCode(f gh.ChangedFile) string {
	switch {
	case f.Code == "U":
		return gitFailStyle.Render(f.Code)
	case f.Staged:
		return gitPassStyle.Render(f.Code)
	default:
		return gitWaitStyle.Render(f.Code)
	}
}
