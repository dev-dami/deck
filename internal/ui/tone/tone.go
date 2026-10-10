// Package tone is Deck's colour palette: a calm graphite ground, a mint
// accent and soft status colours, plus each agent's own tint. Every colour
// in the interface comes from here or from a theme built on it.
package tone

import (
	"image/color"
	"sort"
	"strings"

	"charm.land/lipgloss/v2"
)

// Grounds, darkest first.
var (
	Ink   = lipgloss.Color("#0E1211") // the page
	Slate = lipgloss.Color("#141918")
	Coal  = lipgloss.Color("#1A201F")
	Shale = lipgloss.Color("#222928")
	Rule  = lipgloss.Color("#2A3130") // dividers
	Stone = lipgloss.Color("#3A4341")
)

// Inks, faintest first.
var (
	Faint = lipgloss.Color("#4E5755")
	Fog   = lipgloss.Color("#6F7A77")
	Muted = lipgloss.Color("#9AA5A2")
	Mist  = lipgloss.Color("#BFC8C5")
	Fg    = lipgloss.Color("#E6EDEB")
	Paper = lipgloss.Color("#F5F8F7")
)

// The accent: Deck's mint, with a deeper and a lighter step.
var (
	Mint     = lipgloss.Color("#5FD7AF")
	Teal     = lipgloss.Color("#2FA387")
	DeepTeal = lipgloss.Color("#1D6B59")
	Seafoam  = lipgloss.Color("#A8EBD4")
)

// Status colours.
var (
	Amber = lipgloss.Color("#E8B04B") // approvals, warnings
	Honey = lipgloss.Color("#F2CC7A")
	Coral = lipgloss.Color("#F0716A") // destructive, diff deletions
	Ember = lipgloss.Color("#D9534F") // errors
	Moss  = lipgloss.Color("#9CCB8A") // success, diff additions
	Fern  = lipgloss.Color("#6FA35E")
	Sky   = lipgloss.Color("#7FB8FF") // information
	Dusk  = lipgloss.Color("#5B7FB0")
	Plum  = lipgloss.Color("#B48EAD")
)

// Diff backgrounds.
var (
	AddBg = lipgloss.Color("#1C2A1C")
	DelBg = lipgloss.Color("#2E1C1B")
)

// Agent tints: each agent's mark and name are drawn in its own CLI's colour,
// so speakers in a shared thread are told apart at a glance.
var (
	Claude   = lipgloss.Color("#D97757") // Claude's spark
	Codex    = lipgloss.Color("#8C9EFF") // the Codex logo's blue
	Opencode = lipgloss.Color("#E4E0DF") // OpenCode's monochrome mark
	Iris     = Codex
	Clay     = Claude
)

// Light grounds and inks, for the light theme.
var (
	LightPage  = lipgloss.Color("#FAFCFB")
	LightRaise = lipgloss.Color("#F0F4F2")
	LightPanel = lipgloss.Color("#E6ECEA")
	LightRule  = lipgloss.Color("#DDE3E1")
	LightEdge  = lipgloss.Color("#C9D1CE")
	LightFaint = lipgloss.Color("#A3ACAA")
	LightMuted = lipgloss.Color("#5C6664")
	LightFg    = lipgloss.Color("#1B1F1E")
	LightMint  = lipgloss.Color("#0F8A6E")
	LightAmber = lipgloss.Color("#A86A00")
	LightCoral = lipgloss.Color("#B3261E")
	LightMoss  = lipgloss.Color("#3D7A3A")
	LightSky   = lipgloss.Color("#2F6FB8")
	LightPlum  = lipgloss.Color("#8A5A84")
	LightClay  = lipgloss.Color("#C15F3C")
	LightIris  = lipgloss.Color("#3D4BE0")
)

// Tint is an agent kind's colour ("claude", "codex", "opencode").
func Tint(kind string) color.Color {
	switch kind {
	case "claude":
		return Claude
	case "codex":
		return Codex
	case "opencode":
		return Opencode
	}
	return Muted
}

// Mark is how an agent kind is drawn: the mark its own CLI uses in the
// terminal (Claude Code's ✻, Codex CLI's >_, OpenCode's hollow block ▯), ◆
// for anything else.
func Mark(kind string) string {
	switch kind {
	case "claude":
		return "✻"
	case "codex":
		return ">_"
	case "opencode":
		return "▯"
	}
	return "◆"
}

// named lists the palette by name, for theme files.
var named = map[string]color.Color{
	"ink": Ink, "slate": Slate, "coal": Coal, "shale": Shale, "rule": Rule, "stone": Stone,
	"faint": Faint, "fog": Fog, "muted": Muted, "mist": Mist, "fg": Fg, "paper": Paper,
	"mint": Mint, "teal": Teal, "deepteal": DeepTeal, "seafoam": Seafoam,
	"amber": Amber, "honey": Honey, "coral": Coral, "ember": Ember, "moss": Moss, "fern": Fern,
	"sky": Sky, "dusk": Dusk, "plum": Plum, "addbg": AddBg, "delbg": DelBg,
	"claude": Claude, "codex": Codex, "opencode": Opencode,
}

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
func NameFromHex(hex string) string {
	hex = strings.ToLower(hex)
	for _, n := range Names() {
		if Hex(named[n]) == hex {
			return n
		}
	}
	return ""
}
