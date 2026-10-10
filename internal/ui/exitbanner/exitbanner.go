// Package exitbanner renders what Deck prints after the TUI exits.
package exitbanner

import (
	"math/rand/v2"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/dev-dami/deck/internal/config"
	"github.com/dev-dami/deck/internal/session"
	"github.com/dev-dami/deck/internal/ui/logo"
	"github.com/dev-dami/deck/internal/ui/styles"
	"github.com/dev-dami/deck/internal/ui/tone"
	"github.com/dev-dami/deck/internal/version"
)

// FallbackWidth is used when stdout is not a terminal, so the banner still
// wraps to something sane when output is piped or captured.
const FallbackWidth = 80

// Render returns the exit banner for the given style, or an empty string when
// there is nothing to print. A nil or untitled session means the resume hint is
// omitted, which for the compact banner leaves nothing at all.
func Render(banner config.ExitBanner, sess *session.Session, width int) string {
	if width <= 0 {
		width = FallbackWidth
	}
	hasSession := sess != nil && sess.ID != ""

	switch banner {
	case config.ExitBannerNone:
		return ""

	case config.ExitBannerCompact:
		if !hasSession {
			return ""
		}
		return sessionResumeLines(sess, width)

	default:
		// Unrecognized values render the full banner rather than nothing.
		style := lipgloss.NewStyle().Padding(1, 3)
		contentWidth := width - style.GetHorizontalFrameSize()

		sections := []string{logoSection(contentWidth)}
		if hasSession {
			sections = append(sections, sessionResumeLines(sess, contentWidth))
		}
		return style.Render(strings.Join(sections, "\n\n"))
	}
}

// logoSection returns the ASCII art logo followed by the parting message.
func logoSection(contentWidth int) string {
	t := styles.ThemeForProvider("")
	deckLogo := logo.Render(t.Logo.GradCanvas, version.Version, true, logo.Opts{
		TaglineColor: t.Logo.TaglineColor,
		VersionColor: t.Logo.VersionColor,
		Width:        contentWidth,
		Dark:         true,
	})
	// Wrap the greeting and the message together: wrapping only the message
	// leaves the greeting's own width unaccounted for and overflows the frame.
	return deckLogo + "\n" +
		lipgloss.NewStyle().Width(contentWidth).Render("Thanks for using deck. "+randomExitMessage())
}

// sessionResumeLines returns the "Session  <title>\nContinue deck -s <hash>"
// pair used by the exit banner.
func sessionResumeLines(sess *session.Session, contentWidth int) string {
	title := strings.ReplaceAll(sess.Title, "\n", " ")

	labelWidth := lipgloss.Width("Session  ")
	titleWidth := contentWidth - labelWidth
	if titleWidth > 0 {
		title = ansi.Truncate(title, titleWidth, "…")
	}

	hash := session.HashID(sess.ID)[:7]
	label := lipgloss.NewStyle().Foreground(tone.Mint)
	sessionLine := label.Render("Session  ") + title
	continueLine := label.Render("Continue ") + "deck -s " + hash
	return sessionLine + "\n" + continueLine
}

// randomExitMessage returns a random exit message.
func randomExitMessage() string {
	messages := []string{
		"",
		"See you soon.",
		"Your threads are saved.",
		"The agents will be here.",
		"Good work today.",
	}
	return messages[rand.IntN(len(messages))]
}
