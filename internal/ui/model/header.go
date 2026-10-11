package model

import (
	"fmt"
	"image"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"charm.land/lipgloss/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/dev-dami/deck/internal/fsext"
	"github.com/dev-dami/deck/internal/session"
	"github.com/dev-dami/deck/internal/ui/common"
	"github.com/dev-dami/deck/internal/ui/decklogo"
	"github.com/dev-dami/deck/internal/ui/logo"
	"github.com/dev-dami/deck/internal/ui/styles"
	"github.com/dev-dami/deck/internal/version"
	"github.com/dev-dami/deck/internal/workspace"
)

const (
	headerDiag           = "─"
	minHeaderDiags       = 3
	leftPadding          = 1
	rightPadding         = 1
	diagToDetailsSpacing = 1 // space between diagonal pattern and details section
)

type header struct {
	// cached logo and compact logo
	logo        string
	compactLogo string
	// markEdges are the agents' card edges compactLogo was drawn with;
	// markOK is false when it must be drawn again.
	markEdges []decklogo.Edge
	markOK    bool

	// anim is the landing screen's living wordmark; ticking is true while
	// a frame tick is on its way.
	anim    *decklogo.Model
	ticking bool
	// pending is an intro asked for before the wordmark knew its size; it
	// starts at the first draw.
	pending bool
	area    uv.Rectangle // where the wordmark was last drawn

	com     *common.Common
	width   int
	compact bool
}

// newHeader creates a new header model.
func newHeader(com *common.Common) *header {
	h := &header{
		com:  com,
		anim: decklogo.NewWithPalette(logo.PaletteFor(com.Styles)),
	}
	h.refresh()
	return h
}

// refresh rebuilds cached logo strings using the current styles. Call
// after the theme changes.
func (h *header) refresh() {
	t := h.com.Styles
	h.invalidateMark()
	h.anim.SetPalette(logo.PaletteFor(t))
	// Force drawHeader to re-render the wide logo on the next frame.
	h.width = 0
	h.logo = ""
}

// invalidateMark makes the next frame draw the compact logo's mark again:
// call it when the agents' limits or context change.
func (h *header) invalidateMark() { h.markOK = false }

// mark returns the compact logo, " ▂▅█ deck ", the agents' card edges
// filled by their usage, drawn again only when the edges change.
func (h *header) mark(edges []decklogo.Edge) string {
	if h.markOK && slices.Equal(edges, h.markEdges) {
		return h.compactLogo
	}
	t := h.com.Styles
	h.compactLogo = " " + decklogo.SmallMark(edges, t.Header.Mark.GetForeground(), t.Logo.MarkLowColor) + " " +
		styles.ApplyBoldForegroundGrad(t.Header.LogoGradCanvas, "deck", t.Header.LogoGradFromColor, t.Header.LogoGradToColor) + " "
	h.markEdges = append(h.markEdges[:0], edges...)
	h.markOK = true
	return h.compactLogo
}

// drawHeader draws the header for the given session. edges are the agents'
// card edges in Deck's mark (see UI.markLevels). lspErrorCount comes
// from the UI's memoized LSP state: drawing runs on every frame and must not
// probe the workspace (a synchronous HTTP round-trip in client/server mode).
func (h *header) drawHeader(
	scr uv.Screen,
	area uv.Rectangle,
	session *session.Session,
	edges []decklogo.Edge,
	compact bool,
	detailsOpen bool,
	width int,
	lspErrorCount int,
	gitBranch string,
	gitBadge string,
	roster workspace.Roster,
) {
	t := h.com.Styles
	if width != h.width || compact != h.compact {
		h.logo = renderLogo(h.com.Styles, compact, width)
	}

	h.width = width
	h.compact = compact

	if !compact || session == nil {
		uv.NewStyledString(h.landingLogo(area, edges)).Draw(scr, area)
		return
	}

	if session.ID == "" {
		return
	}

	var b strings.Builder
	b.WriteString(h.mark(edges))

	availDetailWidth := width - leftPadding - rightPadding - lipgloss.Width(b.String()) - minHeaderDiags - diagToDetailsSpacing
	details := renderHeaderDetails(
		h.com,
		session,
		lspErrorCount,
		detailsOpen,
		availDetailWidth,
		gitBranch,
		gitBadge,
		roster,
	)

	remainingWidth := width -
		lipgloss.Width(b.String()) -
		lipgloss.Width(details) -
		leftPadding -
		rightPadding -
		diagToDetailsSpacing

	if remainingWidth > 0 {
		b.WriteString(t.Header.Diagonals.Render(
			strings.Repeat(headerDiag, max(minHeaderDiags, remainingWidth)),
		))
		b.WriteString(" ")
	}

	b.WriteString(details)

	view := uv.NewStyledString(
		t.Header.Wrapper.Padding(0, rightPadding, 0, leftPadding).Render(b.String()),
	)
	view.Draw(scr, area)
}

// renderHeaderDetails renders the details section of the header.
func renderHeaderDetails(
	com *common.Common,
	session *session.Session,
	lspErrorCount int,
	detailsOpen bool,
	availWidth int,
	gitBranch string,
	gitBadge string,
	roster workspace.Roster,
) string {
	t := com.Styles

	var parts []string

	if lspErrorCount > 0 {
		parts = append(parts, t.LSP.ErrorDiagnostic.Render(fmt.Sprintf("%s%d", styles.LSPErrorIcon, lspErrorCount)))
	}

	if percentage, ok := contextPercent(com, roster, session); ok {
		percentageText := fmt.Sprintf("%d%%", percentage)
		if session.EstimatedUsage {
			percentageText = "~" + percentageText
		}
		formattedPercentage := t.Header.Percentage.Render(percentageText)
		parts = append(parts, formattedPercentage)
	}

	const keystroke = "ctrl+d"
	if detailsOpen {
		parts = append(parts, t.Header.Keystroke.Render(keystroke)+t.Header.KeystrokeTip.Render(" close"))
	} else {
		parts = append(parts, t.Header.Keystroke.Render(keystroke)+t.Header.KeystrokeTip.Render(" open "))
	}

	dot := t.Header.Separator.Render(" • ")
	metadata := strings.Join(parts, dot)
	metadata = dot + metadata

	const dirTrimLimit = 4
	workingDir := com.Workspace.WorkingDir()
	cwd := t.Header.WorkingDir.Render(fsext.DirTrim(fsext.PrettyPath(workingDir), dirTrimLimit))

	if gitBranch != "" {
		// Reserve space for the dot separator between branch and path and
		// at least some of the path.
		branchSep := t.Header.Separator.Render(" • ")
		// The branch's pull request and checks ("#12 ✓") follow it.
		badge := ""
		if gitBadge != "" {
			badge = " " + gitBadge
		}
		metadataWidth := lipgloss.Width(metadata)
		maxBranchWidth := max(0, availWidth-metadataWidth-lipgloss.Width(cwd)-lipgloss.Width(branchSep)-lipgloss.Width(badge))
		if maxBranchWidth > 0 {
			truncBranch := ansi.Truncate(gitBranch, maxBranchWidth, "…")
			cwd = t.Header.GitBranch.Render(truncBranch) + badge + branchSep + cwd
		}
	}

	result := cwd + metadata
	return ansi.Truncate(result, max(0, availWidth), "…")
}

// logoFrame is the wordmark's frame rate while it moves.
const logoFrame = time.Second / 30

// logoTickMsg asks for the wordmark's next frame.
type logoTickMsg time.Time

func logoTick() tea.Cmd {
	return tea.Tick(logoFrame, func(t time.Time) tea.Msg { return logoTickMsg(t) })
}

// landingLogo is the living wordmark beside the agents' card edges, with the
// tagline and version under it, sized to area.
func (h *header) landingLogo(area uv.Rectangle, edges []decklogo.Edge) string {
	t := h.com.Styles
	h.area = area
	h.anim.SetPalette(logo.PaletteFor(t))
	h.anim.SetEdges(edges)
	h.anim.SetSize(area.Dx(), max(1, area.Dy()-1))
	if h.pending {
		if w, _ := h.anim.Bounds(); w > 0 {
			h.pending = false
			h.anim.Start(time.Now())
		}
	}
	meta := lipgloss.NewStyle().Foreground(t.Logo.TaglineColor).Render(logo.Tagline) + "  " +
		lipgloss.NewStyle().Foreground(t.Logo.VersionColor).Render(version.Version)
	return lipgloss.JoinVertical(lipgloss.Left, h.anim.View(), meta)
}

// startLogo plays the wordmark's intro: at once when it has been drawn
// before, otherwise at its first draw, once it knows how much room it has.
func (h *header) startLogo(now time.Time) tea.Cmd {
	if w, _ := h.anim.Bounds(); w > 0 {
		h.anim.Start(now)
	} else {
		h.pending = true
	}
	return h.wakeLogo()
}

// wakeLogo makes sure frames are coming while the wordmark moves.
func (h *header) wakeLogo() tea.Cmd {
	if h.ticking {
		return nil
	}
	h.ticking = true
	return logoTick()
}

// logoFrameAt advances the wordmark; it asks for another frame while it
// still moves.
func (h *header) logoFrameAt(now time.Time) tea.Cmd {
	if h.anim.Advance(now) || h.pending {
		return logoTick()
	}
	h.ticking = false
	return nil
}

// logoMouse sends the pointer to the wordmark when it is over it: a move
// ripples the surface, a click splashes it.
func (h *header) logoMouse(x, y int, click bool, now time.Time) tea.Cmd {
	if !image.Pt(x, y).In(h.area) {
		return nil
	}
	h.anim.Mouse(x-h.area.Min.X, y-h.area.Min.Y, click, now)
	return h.wakeLogo()
}
