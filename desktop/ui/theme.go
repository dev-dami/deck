package ui

import (
	"image/color"
	"strconv"
	"strings"

	"gioui.org/font/gofont"
	"gioui.org/text"
	"gioui.org/unit"
	"gioui.org/widget/material"

	"github.com/dev-dami/deck/pkg/rpc/deckv1"
)

// Colors is the app's palette; Deck's theme tints the agents.
type Colors struct {
	Bg, Rail, Card, Code, Line, Fg, Muted, Accent, OnAccent, Danger, Busy, Add, Del color.NRGBA
}

var dark = Colors{
	Bg:       rgb(0x111114),
	Rail:     rgb(0x18181c),
	Card:     rgb(0x1f1f25),
	Code:     rgb(0x0b0b0d),
	Line:     rgb(0x2a2a31),
	Fg:       rgb(0xe8e8ea),
	Muted:    rgb(0x8b8b96),
	Accent:   rgb(0x5fd7af),
	OnAccent: rgb(0x0d1512),
	Danger:   rgb(0xe5646e),
	Busy:     rgb(0xf0b35b),
	Add:      rgb(0x6cc98a),
	Del:      rgb(0xe5646e),
}

var light = Colors{
	Bg:       rgb(0xfbfbfc),
	Rail:     rgb(0xf1f1f4),
	Card:     rgb(0xe9e9ee),
	Code:     rgb(0xf4f4f7),
	Line:     rgb(0xdcdce3),
	Fg:       rgb(0x1b1b20),
	Muted:    rgb(0x6b6b76),
	Accent:   rgb(0x1f9e78),
	OnAccent: rgb(0xffffff),
	Danger:   rgb(0xc93c48),
	Busy:     rgb(0xb7791f),
	Add:      rgb(0x1f8a4c),
	Del:      rgb(0xc93c48),
}

func rgb(v uint32) color.NRGBA {
	return color.NRGBA{R: uint8(v >> 16), G: uint8(v >> 8), B: uint8(v), A: 0xff}
}

// hex parses "#rrggbb".
func hex(s string) (color.NRGBA, bool) {
	s = strings.TrimPrefix(s, "#")
	if len(s) != 6 {
		return color.NRGBA{}, false
	}
	v, err := strconv.ParseUint(s, 16, 32)
	if err != nil {
		return color.NRGBA{}, false
	}
	return rgb(uint32(v)), true
}

// tint is an agent kind's colour from Deck's theme, or the accent.
func (c Colors) tint(th *deckv1.Theme, kind string) color.NRGBA {
	if k := th.GetKinds()[kind]; k != nil {
		if col, ok := hex(k.GetTint()); ok {
			return col
		}
	}
	return c.Accent
}

func newTheme(c Colors, size int) *material.Theme {
	th := material.NewTheme()
	th.Shaper = text.NewShaper(text.WithCollection(gofont.Collection()))
	th.Palette = material.Palette{Bg: c.Bg, Fg: c.Fg, ContrastBg: c.Accent, ContrastFg: c.OnAccent}
	th.TextSize = unit.Sp(size)
	return th
}

// sp scales a size given for 14pt text to the chosen text size.
func (a *App) sp(v unit.Sp) unit.Sp { return v * a.th.TextSize / 14 }
