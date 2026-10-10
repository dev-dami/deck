// Package theme holds Deck's design tokens. Every colour and glyph in the UI
// comes from here; components never hard-code colours.
package theme

import "github.com/charmbracelet/lipgloss"

// Colour tokens. Light value first, dark value second.
var (
	Fg     = lipgloss.AdaptiveColor{Light: "#1B1F1E", Dark: "#E6EDEB"}
	Muted  = lipgloss.AdaptiveColor{Light: "#5C6664", Dark: "#9AA5A2"}
	Faint  = lipgloss.AdaptiveColor{Light: "#A3ACAA", Dark: "#4E5755"}
	Rule   = lipgloss.AdaptiveColor{Light: "#DDE3E1", Dark: "#2A3130"}
	Accent = lipgloss.AdaptiveColor{Light: "#0F8A6E", Dark: "#5FD7AF"} // mint / teal

	Warn   = lipgloss.AdaptiveColor{Light: "#A86A00", Dark: "#E8B04B"} // approvals
	Danger = lipgloss.AdaptiveColor{Light: "#B3261E", Dark: "#F0716A"} // errors, diff del
	Add    = lipgloss.AdaptiveColor{Light: "#3D7A3A", Dark: "#9CCB8A"} // diff add (softer than Accent)

	AddBg = lipgloss.AdaptiveColor{Light: "#EAF4E6", Dark: "#1C2A1C"}
	DelBg = lipgloss.AdaptiveColor{Light: "#FBEAE8", Dark: "#2E1C1B"}
)

// Agent tints: used only for an agent's ◆ glyph and name in a shared thread,
// so you can tell speakers apart at a glance. Accent stays the UI colour.
var (
	TintClaude = lipgloss.AdaptiveColor{Light: "#C15F3C", Dark: "#D97757"} // Claude's spark
	TintCodex  = lipgloss.AdaptiveColor{Light: "#3D4BE0", Dark: "#8C9EFF"} // the Codex logo's blue
	TintOther  = Muted
)

// Tint returns the agent tint for an agent kind ("claude", "codex", ...).
func Tint(kind string) lipgloss.AdaptiveColor {
	switch kind {
	case "claude":
		return TintClaude
	case "codex":
		return TintCodex
	}
	return TintOther
}

// Mark is how an agent kind is drawn: the mark its own CLI uses in the
// terminal (Claude Code's ✻, Codex CLI's >_), ◆ for anything else.
func Mark(kind string) string {
	switch kind {
	case "claude":
		return "✻"
	case "codex":
		return ">_"
	}
	return GlyphAgent
}

// Glyphs.
const (
	GlyphWorking  = "●"
	GlyphTool     = "◐"
	GlyphIdle     = "○"
	GlyphApproval = "⚠"
	GlyphAgent    = "◆"
	GlyphFolded   = "▸"
	GlyphOpen     = "▾"
	GlyphCursor   = "▍"
	GlyphBar      = "│"
	GlyphRule     = "─"
)

// SpinnerFrames are the frames of the running-tool spinner.
var SpinnerFrames = []string{"◐", "◓", "◑", "◒"}

// Wordmark is the product wordmark text (rendered with the glyph in Accent).
const Wordmark = "deck"

// Tagline is shown under the wordmark on the empty deck.
const Tagline = "one deck for all your agents"

// Common styles.
var (
	Text     = lipgloss.NewStyle().Foreground(Fg)
	Dim      = lipgloss.NewStyle().Foreground(Muted)
	Ghost    = lipgloss.NewStyle().Foreground(Faint)
	Accented = lipgloss.NewStyle().Foreground(Accent)
	Bold     = lipgloss.NewStyle().Foreground(Fg).Bold(true)
	Warning  = lipgloss.NewStyle().Foreground(Warn)
	Error    = lipgloss.NewStyle().Foreground(Danger)
)
