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
	// Mark is the wordmark's colours, usually PaletteFor the active
	// theme. Unset, the wordmark uses Deck's own Dark or light palette.
	Mark decklogo.Palette
	// Edges are the agents' card edges, their usage meters. None draws
	// the plain stack of cards.
	Edges []decklogo.Edge
}

// PaletteFor returns the wordmark colours of a theme.
func PaletteFor(t *styles.Styles) decklogo.Palette {
	return decklogo.Palette{
		Low:    t.Logo.MarkLowColor,
		Accent: t.Logo.MarkColor,
		High:   t.Logo.MarkHighColor,
		Name:   t.Logo.NameColor,
	}
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
	pal := o.Mark
	if pal.Accent == nil {
		pal = decklogo.DefaultPalette(o.Dark || o.Width == 0)
	}
	m := decklogo.NewWithPalette(pal)
	m.SetEdges(o.Edges)
	m.SetSize(w, h)
	mark := m.View()
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

// SmallRender renders the one-line logo, the small mark (a cell per agent's
// card edge, filled by its usage, as in "▂▅█ deck"), "deck" and a rule to
// the edge, for small windows and the sidebar.
func SmallRender(t *styles.Styles, width int, o Opts) string {
	title := decklogo.SmallMark(o.Edges, t.Logo.SmallMark.GetForeground(), t.Logo.MarkLowColor) +
		" " + t.Logo.SmallName.Render("deck")
	if rest := width - lipgloss.Width(title) - 1; rest > 0 {
		title += " " + t.Logo.SmallDiagonals.Render(strings.Repeat("─", rest))
	}
	return title
}
