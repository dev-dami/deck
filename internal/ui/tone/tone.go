// Package tone is Deck's colour palette: the desktop app's own colours
// (its default look, Liquid Glass on Apple's system colours, in
// desktop-shell/src/renderer/styles.css), for dark and light terminals,
// plus each agent's own tint and mark. Every colour in the interface comes
// from here or from a theme built on it, so the terminal app and the
// desktop app read as one product.
//
// The values are truecolor; on a terminal with fewer colours the renderer
// (lipgloss and colorprofile) maps each one to the nearest it has, and
// NO_COLOR drops them.
package tone

import (
	"image/color"
	"sort"
	"strings"
	"sync/atomic"

	"charm.land/lipgloss/v2"
)

// Palette is one look: the desktop's tokens under their own names.
// Translucent desktop tokens (hairlines, the bubble) are given as the
// solid colour they make over the canvas.
type Palette struct {
	// Grounds: the canvas (--canvas), raised surfaces (--surface-1,
	// --surface-2), code blocks (--code), the person's bubble (--bubble),
	// a chosen row (--selected) and a control's fill (the system grey a
	// step up).
	Canvas, Surface1, Surface2, Code, Bubble, Selected, Control color.Color
	// Hairline is --hairline (borders, rules); HairlineStrong is
	// --hairline-strong (cards, inputs).
	Hairline, HairlineStrong color.Color

	// Inks, strongest first: --text, --secondary-label, --text-muted,
	// --text-faint, and the quaternary label for what should barely show.
	Text, Secondary, Muted, Faint, Quaternary color.Color

	// The accent (--signal, the system blue) and what sits on it, and
	// --link, its readable step for text.
	Signal, OnSignal, Link color.Color

	// States: the fill (--destructive, --warning, --success) and the text
	// step of each (--*-foreground).
	Destructive, DestructiveText color.Color
	Warning, WarningText         color.Color
	Success, SuccessText         color.Color

	// More system hues, for charts, graph lanes and code.
	Teal, Purple, Indigo, Yellow, Pink color.Color

	// Diff text (--diff-add-fg, --diff-del-fg). The tinted grounds derive
	// from Success and Destructive over the canvas, as the desktop's do.
	DiffAddText, DiffDelText color.Color

	// Syntax is the desktop's code colours.
	Syntax Syntax

	// Agent tints (--agent-*).
	Claude, Codex, Opencode, Antigravity, Pi color.Color
}

// Syntax is the desktop's code colours (--syn-*), after Xcode's Default
// (Dark) and Default (Light).
type Syntax struct {
	Keyword, String, Comment, Number, Function, Type, Punct, Variable, Tag, Attr, Operator color.Color
}

// Dark is the desktop's dark look.
var Dark = Palette{
	Canvas:         lipgloss.Color("#19191b"),
	Surface1:       lipgloss.Color("#222225"),
	Surface2:       lipgloss.Color("#2a2a2d"),
	Code:           lipgloss.Color("#141416"),
	Bubble:         lipgloss.Color("#1e3856"),
	Selected:       lipgloss.Color("#353537"),
	Control:        lipgloss.Color("#3a3a3c"),
	Hairline:       lipgloss.Color("#303032"),
	HairlineStrong: lipgloss.Color("#3e3e40"),

	Text:       lipgloss.Color("#e3e3e5"),
	Secondary:  lipgloss.Color("#c7c7cc"),
	Muted:      lipgloss.Color("#a1a1a6"),
	Faint:      lipgloss.Color("#8e8e93"),
	Quaternary: lipgloss.Color("#636366"),

	Signal:   lipgloss.Color("#0a84ff"),
	OnSignal: lipgloss.Color("#ffffff"),
	Link:     lipgloss.Color("#409cff"),

	Destructive:     lipgloss.Color("#ff453a"),
	DestructiveText: lipgloss.Color("#ff6961"),
	Warning:         lipgloss.Color("#ff9f0a"),
	WarningText:     lipgloss.Color("#ffb340"),
	Success:         lipgloss.Color("#30d158"),
	SuccessText:     lipgloss.Color("#30d158"),

	Teal:   lipgloss.Color("#64d2ff"),
	Purple: lipgloss.Color("#bf5af2"),
	Indigo: lipgloss.Color("#5e5ce6"),
	Yellow: lipgloss.Color("#ffd60a"),
	Pink:   lipgloss.Color("#ff375f"),

	DiffAddText: lipgloss.Color("#5fe07f"),
	DiffDelText: lipgloss.Color("#ff7b72"),

	Syntax: Syntax{
		Keyword:  lipgloss.Color("#ff7ab2"),
		String:   lipgloss.Color("#ff8170"),
		Comment:  lipgloss.Color("#7f8c98"),
		Number:   lipgloss.Color("#d9c97c"),
		Function: lipgloss.Color("#78c2b3"),
		Type:     lipgloss.Color("#dabaff"),
		Punct:    lipgloss.Color("#a1a1a6"),
		Variable: lipgloss.Color("#e3e3e5"),
		Tag:      lipgloss.Color("#ff7ab2"),
		Attr:     lipgloss.Color("#6bdfff"),
		Operator: lipgloss.Color("#c9c9cc"),
	},

	Claude:      lipgloss.Color("#ff9f5a"),
	Codex:       lipgloss.Color("#a1a1a6"),
	Opencode:    lipgloss.Color("#64d2ff"),
	Antigravity: lipgloss.Color("#bf5af2"),
	Pi:          lipgloss.Color("#30d158"),
}

// Light is the desktop's light look.
var Light = Palette{
	Canvas:         lipgloss.Color("#fbfbfd"),
	Surface1:       lipgloss.Color("#f3f3f6"),
	Surface2:       lipgloss.Color("#ededf0"),
	Code:           lipgloss.Color("#f3f3f6"),
	Bubble:         lipgloss.Color("#d2e2f6"),
	Selected:       lipgloss.Color("#e8e8eb"),
	Control:        lipgloss.Color("#d1d1d6"),
	Hairline:       lipgloss.Color("#e2e2e4"),
	HairlineStrong: lipgloss.Color("#d3d3d5"),

	Text:       lipgloss.Color("#1d1d1f"),
	Secondary:  lipgloss.Color("#3a3a3c"),
	Muted:      lipgloss.Color("#636366"),
	Faint:      lipgloss.Color("#78787d"),
	Quaternary: lipgloss.Color("#aeaeb2"),

	Signal:   lipgloss.Color("#007aff"),
	OnSignal: lipgloss.Color("#ffffff"),
	Link:     lipgloss.Color("#0066cc"),

	Destructive:     lipgloss.Color("#ff3b30"),
	DestructiveText: lipgloss.Color("#d70015"),
	Warning:         lipgloss.Color("#ff9500"),
	WarningText:     lipgloss.Color("#c93400"),
	Success:         lipgloss.Color("#28cd41"),
	SuccessText:     lipgloss.Color("#248a3d"),

	Teal:   lipgloss.Color("#0077a8"),
	Purple: lipgloss.Color("#8944ab"),
	Indigo: lipgloss.Color("#5856d6"),
	Yellow: lipgloss.Color("#a05a00"),
	Pink:   lipgloss.Color("#d30f45"),

	DiffAddText: lipgloss.Color("#1a7f37"),
	DiffDelText: lipgloss.Color("#c4161c"),

	Syntax: Syntax{
		Keyword:  lipgloss.Color("#ad3da4"),
		String:   lipgloss.Color("#c41a16"),
		Comment:  lipgloss.Color("#5d6c79"),
		Number:   lipgloss.Color("#272ad8"),
		Function: lipgloss.Color("#3e8087"),
		Type:     lipgloss.Color("#6c36a9"),
		Punct:    lipgloss.Color("#636366"),
		Variable: lipgloss.Color("#1d1d1f"),
		Tag:      lipgloss.Color("#ad3da4"),
		Attr:     lipgloss.Color("#0f68a0"),
		Operator: lipgloss.Color("#3a3a3c"),
	},

	Claude:      lipgloss.Color("#c4581b"),
	Codex:       lipgloss.Color("#6e6e73"),
	Opencode:    lipgloss.Color("#0077a8"),
	Antigravity: lipgloss.Color("#8944ab"),
	Pi:          lipgloss.Color("#248a3d"),
}

// light is true while a light theme is active; set by SetLight.
var light atomic.Bool

// SetLight tells Current and Tint whether the active theme is light, so
// agent tints and the colours drawn outside a theme stay readable on it.
// The UI calls it whenever the theme changes.
func SetLight(on bool) { light.Store(on) }

// Current is the palette for the active theme's ground: Light while a light
// theme is on, else Dark.
func Current() Palette {
	if light.Load() {
		return Light
	}
	return Dark
}

// Kinds are the agent kinds with a tint and a mark of their own.
var Kinds = []string{"claude", "codex", "opencode", "antigravity", "pi"}

// Tint is an agent kind's colour ("claude", "codex", "opencode",
// "antigravity", "pi"), the desktop's for the same kind; Muted for any
// other kind.
func Tint(kind string) color.Color {
	p := Current()
	switch kind {
	case "claude":
		return p.Claude
	case "codex":
		return p.Codex
	case "opencode":
		return p.Opencode
	case "antigravity":
		return p.Antigravity
	case "pi":
		return p.Pi
	}
	return p.Muted
}

// KindOf is the agent kind an agent's name starts with ("codex-2",
// "claude/review" are "codex" and "claude"); "" when it names none.
func KindOf(name string) string {
	name = strings.ToLower(name)
	for _, k := range Kinds {
		if strings.HasPrefix(name, k) {
			return k
		}
	}
	return ""
}

// Mark is how an agent kind is drawn: the mark its own CLI uses in the
// terminal (Claude Code's ✻, Codex CLI's >_, OpenCode's hollow block ▯,
// Antigravity's ◎, Pi's π), ▌ (a card edge, as in Deck's mark) for anything
// else.
func Mark(kind string) string {
	switch kind {
	case "claude":
		return "✻"
	case "codex":
		return ">_"
	case "opencode":
		return "▯"
	case "antigravity":
		return "◎"
	case "pi":
		return "π"
	}
	return "▌"
}

// named lists colours by name, for theme files: the dark palette's tokens,
// and the names of Deck's earlier palette, which theme files written for it
// may still use.
var named = func() map[string]color.Color {
	m := map[string]color.Color{}
	for n, h := range map[string]string{
		"ink": "#0e1211", "slate": "#141918", "coal": "#1a201f", "shale": "#222928", "rule": "#2a3130", "stone": "#3a4341",
		"fog": "#6f7a77", "mist": "#bfc8c5", "paper": "#f5f8f7",
		"mint": "#5fd7af", "teal": "#2fa387", "deepteal": "#1d6b59", "seafoam": "#a8ebd4",
		"amber": "#e8b04b", "honey": "#f2cc7a", "coral": "#f0716a", "ember": "#d9534f", "moss": "#9ccb8a", "fern": "#6fa35e",
		"sky": "#7fb8ff", "dusk": "#5b7fb0", "plum": "#b48ead", "addbg": "#1c2a1c", "delbg": "#2e1c1b",
	} {
		m[n] = lipgloss.Color(h)
	}
	d := Dark
	for n, c := range map[string]color.Color{
		"canvas": d.Canvas, "surface1": d.Surface1, "surface2": d.Surface2, "code": d.Code, "bubble": d.Bubble,
		"hairline": d.Hairline, "hairlinestrong": d.HairlineStrong,
		"fg": d.Text, "secondary": d.Secondary, "muted": d.Muted, "faint": d.Faint, "quaternary": d.Quaternary,
		"signal": d.Signal, "link": d.Link,
		"destructive": d.Destructive, "warning": d.Warning, "success": d.Success,
		"cyan": d.Teal, "purple": d.Purple, "indigo": d.Indigo, "yellow": d.Yellow, "pink": d.Pink,
		"claude": d.Claude, "codex": d.Codex, "opencode": d.Opencode, "antigravity": d.Antigravity, "pi": d.Pi,
	} {
		m[n] = c
	}
	return m
}()

// Hex is a colour as "#rrggbb".
func Hex(c color.Color) string {
	if c == nil {
		return ""
	}
	r, g, b, _ := c.RGBA()
	const digits = "0123456789abcdef"
	out := []byte{'#', 0, 0, 0, 0, 0, 0}
	for i, v := range []uint32{r >> 8, g >> 8, b >> 8} {
		out[1+2*i], out[2+2*i] = digits[v>>4], digits[v&15]
	}
	return string(out)
}

// Names is every palette name, sorted.
func Names() []string {
	out := make([]string, 0, len(named))
	for n := range named {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// ByName is the hex of a palette colour, by name (any case).
func ByName(name string) (string, bool) {
	c, ok := named[strings.ToLower(name)]
	if !ok {
		return "", false
	}
	return Hex(c), true
}

// NameFromHex is the palette name of a hex colour, "" when it has none.
// When several names share a colour, the first in order is given.
func NameFromHex(hex string) string {
	hex = strings.ToLower(hex)
	for _, n := range Names() {
		if Hex(named[n]) == hex {
			return n
		}
	}
	return ""
}
