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

// DefaultLightStyle provides a default light theme style for the diff view.
func DefaultLightStyle() Style {
	return Style{
		DividerLine: LineStyle{
			LineNumber: lipgloss.NewStyle().
				Foreground(tone.Shale).
				Background(tone.Stone),
			Code: lipgloss.NewStyle().
				Foreground(tone.Fog).
				Background(tone.Fog),
		},
		MissingLine: LineStyle{
			LineNumber: lipgloss.NewStyle().
				Background(tone.Fg),
			Code: lipgloss.NewStyle().
				Background(tone.Fg),
		},
		EqualLine: LineStyle{
			LineNumber: lipgloss.NewStyle().
				Foreground(tone.Coal).
				Background(tone.Fg),
			Code: lipgloss.NewStyle().
				Foreground(tone.Ink).
				Background(tone.Paper),
		},
		InsertLine: LineStyle{
			LineNumber: lipgloss.NewStyle().
				Foreground(tone.Moss).
				Background(lipgloss.Color("#c8e6c9")),
			Symbol: lipgloss.NewStyle().
				Foreground(tone.Moss).
				Background(lipgloss.Color("#e8f5e9")),
			Code: lipgloss.NewStyle().
				Foreground(tone.Ink).
				Background(lipgloss.Color("#e8f5e9")),
		},
		DeleteLine: LineStyle{
			LineNumber: lipgloss.NewStyle().
				Foreground(tone.Coral).
				Background(lipgloss.Color("#ffcdd2")),
			Symbol: lipgloss.NewStyle().
				Foreground(tone.Coral).
				Background(lipgloss.Color("#ffebee")),
			Code: lipgloss.NewStyle().
				Foreground(tone.Ink).
				Background(lipgloss.Color("#ffebee")),
		},
		Filename: LineStyle{
			LineNumber: lipgloss.NewStyle().
				Foreground(tone.Shale).
				Background(tone.Stone),
			Code: lipgloss.NewStyle().
				Foreground(tone.Shale).
				Background(tone.Stone),
		},
	}
}

// DefaultDarkStyle provides a default dark theme style for the diff view.
func DefaultDarkStyle() Style {
	return Style{
		DividerLine: LineStyle{
			LineNumber: lipgloss.NewStyle().
				Foreground(tone.Mist).
				Background(tone.Sky),
			Code: lipgloss.NewStyle().
				Foreground(tone.Mist).
				Background(tone.Faint),
		},
		MissingLine: LineStyle{
			LineNumber: lipgloss.NewStyle().
				Background(tone.Coal),
			Code: lipgloss.NewStyle().
				Background(tone.Coal),
		},
		EqualLine: LineStyle{
			LineNumber: lipgloss.NewStyle().
				Foreground(tone.Fg).
				Background(tone.Coal),
			Code: lipgloss.NewStyle().
				Foreground(tone.Paper).
				Background(tone.Ink),
		},
		InsertLine: LineStyle{
			LineNumber: lipgloss.NewStyle().
				Foreground(tone.Moss).
				Background(lipgloss.Color("#293229")),
			Symbol: lipgloss.NewStyle().
				Foreground(tone.Moss).
				Background(lipgloss.Color("#303a30")),
			Code: lipgloss.NewStyle().
				Foreground(tone.Paper).
				Background(lipgloss.Color("#303a30")),
		},
		DeleteLine: LineStyle{
			LineNumber: lipgloss.NewStyle().
				Foreground(tone.Coral).
				Background(lipgloss.Color("#332929")),
			Symbol: lipgloss.NewStyle().
				Foreground(tone.Coral).
				Background(lipgloss.Color("#3a3030")),
			Code: lipgloss.NewStyle().
				Foreground(tone.Paper).
				Background(lipgloss.Color("#3a3030")),
		},
		Filename: LineStyle{
			LineNumber: lipgloss.NewStyle().
				Foreground(tone.Mist).
				Background(tone.Sky),
			Code: lipgloss.NewStyle().
				Foreground(tone.Mist).
				Background(tone.Sky),
		},
	}
}
