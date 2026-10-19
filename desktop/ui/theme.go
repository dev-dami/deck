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
	Bg, Rail, Raised, Field, Hover, Selected, Code, Line color.NRGBA
	Fg, Muted, Accent, OnAccent, Danger, Busy, Add, Del  color.NRGBA
	// Chroma is the syntax-highlighting style for code.
	Chroma string
}

var dark = Colors{
	Bg: rgb(0x141416), Rail: rgb(0x18181b), Raised: rgb(0x232327), Field: rgb(0x1b1b1f),
	Hover: rgb(0x25252a), Selected: rgb(0x2c2c33), Code: rgb(0x0f0f12), Line: rgb(0x2b2b31),
	Fg: rgb(0xececef), Muted: rgb(0x8e8e99), Accent: rgb(0x5fd7af), OnAccent: rgb(0x0b1310),
	Danger: rgb(0xef6b73), Busy: rgb(0xf2b45c), Add: rgb(0x6cc98a), Del: rgb(0xef6b73),
	Chroma: "github-dark",
}

var light = Colors{
	Bg: rgb(0xffffff), Rail: rgb(0xf6f6f8), Raised: rgb(0xededf1), Field: rgb(0xffffff),
	Hover: rgb(0xececf0), Selected: rgb(0xe3e3e9), Code: rgb(0xf6f6f8), Line: rgb(0xe2e2e8),
	Fg: rgb(0x18181b), Muted: rgb(0x6b6b76), Accent: rgb(0x13906b), OnAccent: rgb(0xffffff),
	Danger: rgb(0xd0414c), Busy: rgb(0xb7791f), Add: rgb(0x1f8a4c), Del: rgb(0xc93c48),
	Chroma: "github",
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

// mark is an agent kind's glyph from Deck's theme, or its initial.
func mark(th *deckv1.Theme, kind, name string) string {
	if k := th.GetKinds()[kind]; k != nil && k.GetMark() != "" {
		return k.GetMark()
	}
	for _, r := range strings.ToUpper(name) {
		return string(r)
	}
	return "•"
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
