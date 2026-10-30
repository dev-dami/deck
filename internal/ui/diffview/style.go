package diffview

import (
	"charm.land/lipgloss/v2"
	"github.com/dev-dami/deck/internal/ui/tone"
)

// LineStyle defines the styles for a given line type in the diff view.
type LineStyle struct {
	LineNumber lipgloss.Style
	Symbol     lipgloss.Style
	Code       lipgloss.Style
}

// Style defines the overall style for the diff view, including styles for
// different line types such as divider, missing, equal, insert, and delete
// lines.
type Style struct {
	DividerLine LineStyle
	MissingLine LineStyle
	EqualLine   LineStyle
	InsertLine  LineStyle
	DeleteLine  LineStyle
	Filename    LineStyle
}

// DefaultLightStyle provides a default light theme style for the diff view:
// the desktop app's light diff colours (tone.Light).
func DefaultLightStyle() Style { return defaultStyle(tone.Light) }

// DefaultDarkStyle provides a default dark theme style for the diff view:
// the desktop app's dark diff colours (tone.Dark).
func DefaultDarkStyle() Style { return defaultStyle(tone.Dark) }

// defaultStyle is the diff view in p's colours: added and removed lines on
// their tints of the canvas, as the desktop's diffs are.
func defaultStyle(p tone.Palette) Style {
	// Blend1D runs from the hue to the canvas: 20% success, 15% destructive.
	addBg := lipgloss.Blend1D(21, p.Success, p.Canvas)[16]
	delBg := lipgloss.Blend1D(21, p.Destructive, p.Canvas)[17]
	return Style{
		DividerLine: LineStyle{
			LineNumber: lipgloss.NewStyle().
				Foreground(p.Faint).
				Background(p.Surface1),
			Code: lipgloss.NewStyle().
				Foreground(p.Muted).
				Background(p.Surface1),
		},
		MissingLine: LineStyle{
			LineNumber: lipgloss.NewStyle().
				Background(p.Surface1),
			Code: lipgloss.NewStyle().
				Background(p.Surface1),
		},
		EqualLine: LineStyle{
			LineNumber: lipgloss.NewStyle().
				Foreground(p.Faint).
				Background(p.Canvas),
			Code: lipgloss.NewStyle().
				Foreground(p.Text).
				Background(p.Canvas),
		},
		InsertLine: LineStyle{
			LineNumber: lipgloss.NewStyle().
				Foreground(p.DiffAddText).
				Background(addBg),
			Symbol: lipgloss.NewStyle().
				Foreground(p.DiffAddText).
				Background(addBg),
			Code: lipgloss.NewStyle().
				Foreground(p.Text).
				Background(addBg),
		},
		DeleteLine: LineStyle{
			LineNumber: lipgloss.NewStyle().
				Foreground(p.DiffDelText).
				Background(delBg),
			Symbol: lipgloss.NewStyle().
				Foreground(p.DiffDelText).
				Background(delBg),
			Code: lipgloss.NewStyle().
				Foreground(p.Text).
				Background(delBg),
		},
		Filename: LineStyle{
			LineNumber: lipgloss.NewStyle().
				Foreground(p.Muted).
				Background(p.Surface2),
			Code: lipgloss.NewStyle().
				Foreground(p.Text).
				Background(p.Surface2),
		},
	}
}
