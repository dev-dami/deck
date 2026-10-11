package graph

import (
	"image/color"

	"github.com/dev-dami/deck/internal/ui/styles"
	"github.com/dev-dami/deck/internal/ui/tone"
)

// agentKinds are the agents a Theme carries a series colour for.
var agentKinds = []string{"claude", "codex", "opencode", "antigravity"}

// FromStyles is the chart theme for Deck's active styles. Series colours
// come from tone.Tint, so the UI must have called tone.SetLight for the
// theme first (it does whenever the theme changes).
func FromStyles(s *styles.Styles) Theme {
	t := Theme{
		Fg:     s.Dialog.NormalItem.GetForeground(),
		Subtle: s.Help.ShortKey.GetForeground(),
		Dim:    s.Dialog.SecondaryText.GetForeground(),
		Grid:   s.Section.Line.GetForeground(),
		Accent: s.Dialog.TitleGradFromColor,
		Bg:     s.Background,
		Series: make(map[string]color.Color, len(agentKinds)),
	}
	for _, k := range agentKinds {
		t.Series[k] = tone.Tint(k)
	}
	think, tool := tone.Plum, tone.Sky
	if !s.IsDark {
		think, tool = tone.LightPlum, tone.LightSky
	}
	t.Spans = map[string]color.Color{
		"think": think,
		"text":  t.Accent,
		"tool":  tool,
		"wait":  t.Dim,
	}
	t.Heat = HeatScale(t.Bg, t.Accent, HeatLevels)
	return t
}
