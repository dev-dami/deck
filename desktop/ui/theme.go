package ui

import (
	"image/color"
	"strconv"
	"strings"

	"gioui.org/font/gofont"
	"gioui.org/text"
	"gioui.org/widget/material"

	"github.com/dev-dami/deck/pkg/rpc/deckv1"
)

// Colors is the app's palette; Deck's theme overrides the accent.
type Colors struct {
	Bg, Rail, Card, Line, Fg, Muted, Accent, Danger, Busy color.NRGBA
}

var dark = Colors{
	Bg:     rgb(0x111114),
	Rail:   rgb(0x18181c),
	Card:   rgb(0x1f1f25),
	Line:   rgb(0x2a2a31),
	Fg:     rgb(0xe8e8ea),
	Muted:  rgb(0x8b8b96),
	Accent: rgb(0x5fd7af),
	Danger: rgb(0xe5646e),
	Busy:   rgb(0xf0b35b),
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

func newTheme(c Colors) *material.Theme {
	th := material.NewTheme()
	th.Shaper = text.NewShaper(text.WithCollection(gofont.Collection()))
	th.Palette = material.Palette{Bg: c.Bg, Fg: c.Fg, ContrastBg: c.Accent, ContrastFg: c.Bg}
	th.TextSize = 14
	return th
}
