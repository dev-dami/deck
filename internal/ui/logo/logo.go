// Package logo renders Deck's wordmark for the places it is drawn still: the
// landing screen's resting frame, the sidebar and the exit banner. The
// animated version lives in package decklogo.
package logo

import (
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/dev-dami/deck/internal/ui/decklogo"
	"github.com/dev-dami/deck/internal/ui/styles"
)

// Tagline is shown under the wordmark.
const Tagline = "one deck for all your agents"

// Opts are the options for rendering the Deck logo.
type Opts struct {
	TaglineColor color.Color // the tagline
	VersionColor color.Color // the version
	Width        int         // the room there is; lines are cut to it
	Dark         bool        // the dark palette (the default theme)
}

// Render renders the Deck logo with the tagline and version under it. The
// compact argument picks the smaller wordmark, for narrow places.
func Render(_ lipgloss.Style, version string, compact bool, o Opts) string {
	w := o.Width
	if w <= 0 {
		w = 80
	}
	h := 6
	if compact {
		h = 4
		w = min(w, 40)
	}
	mark := decklogo.Static(w, h, o.Dark || o.Width == 0)
	meta := lipgloss.NewStyle().Foreground(o.TaglineColor).Render(Tagline)
	if version != "" {
		meta += "  " + lipgloss.NewStyle().Foreground(o.VersionColor).Render(version)
	}
	out := mark + "\n" + meta
	if o.Width > 0 {
		lines := strings.Split(out, "\n")
		for i, l := range lines {
			lines[i] = ansi.Truncate(l, o.Width, "")
		}
		out = strings.Join(lines, "\n")
	}
	return out
}

// SmallRender renders the one-line logo, "◆ deck" and a rule to the edge,
// for small windows and the sidebar.
func SmallRender(t *styles.Styles, width int, _ Opts) string {
	title := t.Logo.SmallMark.Render("◆") + " " + t.Logo.SmallName.Render("deck")
	if rest := width - lipgloss.Width(title) - 1; rest > 0 {
		title += " " + t.Logo.SmallDiagonals.Render(strings.Repeat("─", rest))
	}
	return title
}
