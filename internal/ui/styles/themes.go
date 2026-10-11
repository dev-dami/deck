package styles

import (
	"fmt"
	"image/color"
	"log/slog"
	"sort"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/dev-dami/deck/internal/ui/tone"
)

// ThemeKeyForProvider returns a stable identifier for the theme
// associated with the given provider ID. Every agent shares Deck's theme,
// so this is always "default"; callers use it to skip needless rebuilds.
func ThemeKeyForProvider(string) string { return "default" }

// ThemeForProvider returns the Styles for the given provider ID: Deck's
// own theme, whichever agent is speaking.
func ThemeForProvider(string) Styles { return DeckDark() }

// DeckDark is Deck's dark theme and the default: a graphite ground, a mint
// accent and soft status colours.
func DeckDark() Styles { return deckOverrides(quickStyle(deckDarkOpts())) }

// DeckLight is Deck's light theme.
func DeckLight() Styles { return deckOverrides(quickStyle(deckLightOpts())) }

func deckDarkOpts() quickStyleOpts {
	return quickStyleOpts{
		primary:   tone.Mint,
		secondary: tone.Seafoam,
		accent:    tone.Teal,
		keyword:   tone.Plum,

		fgBase:       tone.Fg,
		fgMoreSubtle: tone.Muted,
		fgSubtle:     tone.Mist,
		fgMostSubtle: tone.Fog,

		onPrimary: tone.Ink,

		bgBase:         tone.Ink,
		bgLeastVisible: tone.Slate,
		bgLessVisible:  tone.Coal,
		bgMostVisible:  tone.Shale,

		separator: tone.Rule,

		destructive:       tone.Coral,
		error:             tone.Ember,
		warningSubtle:     tone.Honey,
		warning:           tone.Amber,
		attention:         tone.Clay,
		busy:              tone.Mint,
		info:              tone.Sky,
		infoMoreSubtle:    tone.Seafoam,
		infoMostSubtle:    tone.Dusk,
		success:           tone.Moss,
		successMoreSubtle: tone.Teal,
		successMostSubtle: tone.Fern,
		yolo:              tone.Coral,
		plan:              tone.Sky,
		planMoreSubtle:    tone.Dusk,

		diffInsertFg:     tone.Moss,
		diffInsertCodeBg: tone.AddBg,
		diffDeleteFg:     tone.Coral,
		diffDeleteCodeBg: tone.DelBg,

		button:         tone.Mint,
		buttonSubtle:   tone.Coal,
		buttonInactive: tone.Shale,
		buttonHovered:  tone.Teal,

		ansiBlack:   tone.Slate,
		ansiRed:     tone.Coral,
		ansiGreen:   tone.Fern,
		ansiYellow:  tone.Amber,
		ansiBlue:    tone.Dusk,
		ansiMagenta: tone.Plum,
		ansiCyan:    tone.Teal,
		ansiWhite:   tone.Mist,

		ansiBrightBlack:   tone.Stone,
		ansiBrightRed:     tone.Coral,
		ansiBrightGreen:   tone.Moss,
		ansiBrightYellow:  tone.Honey,
		ansiBrightBlue:    tone.Sky,
		ansiBrightMagenta: tone.Plum,
		ansiBrightCyan:    tone.Mint,
		ansiBrightWhite:   tone.Paper,
	}
}

func deckLightOpts() quickStyleOpts {
	return quickStyleOpts{
		primary:   tone.LightMint,
		secondary: tone.Teal,
		accent:    tone.DeepTeal,
		keyword:   tone.LightPlum,

		fgBase:       tone.LightFg,
		fgMoreSubtle: tone.LightMuted,
		fgSubtle:     tone.LightMuted,
		fgMostSubtle: tone.LightFaint,

		onPrimary: tone.LightPage,

		bgBase:         tone.LightPage,
		bgLeastVisible: tone.LightRaise,
		bgLessVisible:  tone.LightPanel,
		bgMostVisible:  tone.LightEdge,

		separator: tone.LightRule,

		destructive:       tone.LightCoral,
		error:             tone.LightCoral,
		warningSubtle:     tone.LightAmber,
		warning:           tone.LightAmber,
		attention:         tone.LightClay,
		busy:              tone.LightMint,
		info:              tone.LightSky,
		infoMoreSubtle:    tone.LightSky,
		infoMostSubtle:    tone.LightMuted,
		success:           tone.LightMoss,
		successMoreSubtle: tone.LightMint,
		successMostSubtle: tone.LightMoss,
		yolo:              tone.LightCoral,
		plan:              tone.LightSky,
		planMoreSubtle:    tone.LightMuted,

		diffInsertFg: tone.LightMoss,
		diffDeleteFg: tone.LightCoral,

		button:         tone.LightMint,
		buttonSubtle:   tone.LightPanel,
		buttonInactive: tone.LightEdge,
		buttonHovered:  tone.DeepTeal,

		ansiBlack:   tone.LightFg,
		ansiRed:     tone.LightCoral,
		ansiGreen:   tone.LightMoss,
		ansiYellow:  tone.LightAmber,
		ansiBlue:    tone.LightSky,
		ansiMagenta: tone.LightPlum,
		ansiCyan:    tone.LightMint,
		ansiWhite:   tone.LightMuted,

		ansiBrightBlack:   tone.LightFaint,
		ansiBrightRed:     tone.LightCoral,
		ansiBrightGreen:   tone.LightMoss,
		ansiBrightYellow:  tone.LightAmber,
		ansiBrightBlue:    tone.LightIris,
		ansiBrightMagenta: tone.LightPlum,
		ansiBrightCyan:    tone.DeepTeal,
		ansiBrightWhite:   tone.LightFg,

		light: true,
	}
}

// deckOverrides applies Deck's touches that don't fit the token model of
// [quickStyleOpts]: calm syntax colours and the bang prompt in the accent.
func deckOverrides(s Styles) Styles {
	// Deck's dark tones, and their light-theme counterparts so deck-light
	// does not inherit pale dark-ground colours.
	ink, mint, deep, shale, fog := tone.Ink, tone.Mint, tone.DeepTeal, tone.Shale, tone.Fog
	clay, plum, sky, seafoam, paper, honey := tone.Clay, tone.Plum, tone.Sky, tone.Seafoam, tone.Paper, tone.Honey
	faint := tone.Faint
	if !s.IsDark {
		ink, mint, deep, shale, fog = tone.LightPage, tone.LightMint, tone.Teal, tone.LightEdge, tone.LightFaint
		clay, plum, sky, seafoam, paper, honey = tone.LightClay, tone.LightPlum, tone.LightSky, tone.DeepTeal, tone.LightFg, tone.LightAmber
		faint = tone.LightFaint
	}

	s.Editor.PromptBangIconFocused = s.Editor.PromptBangIconFocused.
		Foreground(ink).
		Background(mint)
	s.Editor.PromptBangDotsFocused = s.Editor.PromptBangDotsFocused.
		Foreground(mint)
	s.Editor.PromptBangDotsBlurred = s.Editor.PromptBangDotsBlurred.
		Foreground(deep)

	s.Messages.ShellBarFocused = s.Messages.ShellBarFocused.
		BorderForeground(mint)
	s.Messages.ShellBarBlurred = s.Messages.ShellBarBlurred.
		BorderForeground(shale)
	s.Messages.ShellPrompt = s.Messages.ShellPrompt.
		Foreground(mint)
	s.Messages.ShellPromptBlurred = s.Messages.ShellPromptBlurred.
		Foreground(fog)

	chroma := s.Markdown.CodeBlock.Chroma
	if chroma != nil {
		chroma.CommentPreproc.Color = hex(clay)
		chroma.KeywordReserved.Color = hex(plum)
		chroma.KeywordNamespace.Color = hex(plum)
		chroma.KeywordType.Color = hex(sky)
		chroma.Operator.Color = hex(seafoam)
		chroma.NameTag.Color = hex(plum)
		chroma.NameAttribute.Color = hex(sky)
		chroma.NameClass.Color = hex(paper)
		chroma.LiteralString.Color = hex(honey)
	}
	s.Markdown.Link.Color = hex(mint)
	s.Markdown.Image.Color = hex(plum)
	s.Markdown.Code.Color = hex(mint)

	// The wordmark keeps its hand-picked ramp.
	s.Logo.MarkLowColor, s.Logo.MarkColor, s.Logo.MarkHighColor = faint, mint, seafoam
	return s
}

// gruvboxDarkOpts returns the quickStyleOpts for the Gruvbox Dark theme,
// using canonical colors from the morhetz/gruvbox palette.
func gruvboxDarkOpts() quickStyleOpts {
	return quickStyleOpts{
		primary:   lipgloss.Color("#fabd2f"), // yellow
		secondary: lipgloss.Color("#d3869b"), // purple
		accent:    lipgloss.Color("#b8bb26"), // green
		keyword:   lipgloss.Color("#fe8019"), // orange

		fgBase:       lipgloss.Color("#ebdbb2"), // fg
		fgMoreSubtle: lipgloss.Color("#a89984"), // fg4/gray
		fgSubtle:     lipgloss.Color("#bdae93"), // fg3
		fgMostSubtle: lipgloss.Color("#928374"), // gray

		onPrimary: lipgloss.Color("#282828"), // bg on primary

		bgBase:         lipgloss.Color("#282828"), // bg
		bgLeastVisible: lipgloss.Color("#3c3836"), // bg1
		bgLessVisible:  lipgloss.Color("#504945"), // bg2
		bgMostVisible:  lipgloss.Color("#665c54"), // bg3

		separator: lipgloss.Color("#504945"), // bg2

		destructive:       lipgloss.Color("#fb4934"), // red bright
		error:             lipgloss.Color("#cc241d"), // red dark
		warningSubtle:     lipgloss.Color("#fabd2f"), // yellow bright
		warning:           lipgloss.Color("#d79921"), // yellow dark
		attention:         lipgloss.Color("#fe8019"), // orange
		busy:              lipgloss.Color("#fabd2f"), // yellow bright
		info:              lipgloss.Color("#83a598"), // blue bright
		infoMoreSubtle:    lipgloss.Color("#83a598"), // blue bright
		infoMostSubtle:    lipgloss.Color("#458588"), // blue dark
		success:           lipgloss.Color("#b8bb26"), // green bright
		successMoreSubtle: lipgloss.Color("#b8bb26"), // green bright
		successMostSubtle: lipgloss.Color("#8ec07c"), // aqua bright

		yolo:           lipgloss.Color("#fabd2f"), // yellow bright
		plan:           lipgloss.Color("#d3869b"), // purple
		planMoreSubtle: lipgloss.Color("#665c54"), // bg3

		// Diff colors derive from success/destructive over bgBase.

		button:         lipgloss.Color("#d3869b"), // purple
		buttonSubtle:   lipgloss.Color("#504945"), // bg2
		buttonInactive: lipgloss.Color("#665c54"), // bg3
		buttonHovered:  lipgloss.Color("#928374"), // gray

		// ANSI 16-color palette for remapping raw terminal output
		// (e.g. bang-mode shell commands) onto legible Gruvbox colors.
		ansiBlack:   lipgloss.Color("#282828"),
		ansiRed:     lipgloss.Color("#cc241d"),
		ansiGreen:   lipgloss.Color("#98971a"),
		ansiYellow:  lipgloss.Color("#d79921"),
		ansiBlue:    lipgloss.Color("#458588"),
		ansiMagenta: lipgloss.Color("#b16286"),
		ansiCyan:    lipgloss.Color("#689d6a"),
		ansiWhite:   lipgloss.Color("#a89984"),

		ansiBrightBlack:   lipgloss.Color("#928374"),
		ansiBrightRed:     lipgloss.Color("#fb4934"),
		ansiBrightGreen:   lipgloss.Color("#b8bb26"),
		ansiBrightYellow:  lipgloss.Color("#fabd2f"),
		ansiBrightBlue:    lipgloss.Color("#83a598"),
		ansiBrightMagenta: lipgloss.Color("#d3869b"),
		ansiBrightCyan:    lipgloss.Color("#8ec07c"),
		ansiBrightWhite:   lipgloss.Color("#ebdbb2"),
	}
}

// gruvboxDarkOverrides applies Gruvbox-specific tweaks on top of the
// token-driven base styles.
func gruvboxDarkOverrides(s Styles) Styles {
	// The shared quickStyle renders inline code as the destructive
	// (bright red) color on the code background. In Gruvbox that pairing
	// (#fb4934 on #504945) is only ~2.6:1 contrast, which is hard to
	// read. Use Gruvbox orange on the darkest background instead, which
	// keeps a warm "code" feel while clearing WCAG AA (~5.8:1).
	s.Markdown.Code.Color = hex(lipgloss.Color("#fe8019"))
	s.Markdown.Code.BackgroundColor = hex(lipgloss.Color("#282828"))
	return s
}

// inlineCodeOverride returns an override that draws inline code in fg on
// bg (nil keeps the background). The shared quickStyle draws it in the
// destructive hue on bgLessVisible, which some palettes leave below WCAG AA.
func inlineCodeOverride(fg, bg color.Color) func(Styles) Styles {
	return func(s Styles) Styles {
		s.Markdown.Code.Color = hex(fg)
		if bg != nil {
			s.Markdown.Code.BackgroundColor = hex(bg)
		}
		return s
	}
}

// tokyoNightOpts returns the quickStyleOpts for Tokyo Night, using the
// "night" palette from folke/tokyonight.nvim.
func tokyoNightOpts() quickStyleOpts {
	return quickStyleOpts{
		primary:   lipgloss.Color("#7aa2f7"), // blue
		secondary: lipgloss.Color("#bb9af7"), // magenta
		accent:    lipgloss.Color("#7dcfff"), // cyan
		keyword:   lipgloss.Color("#9d7cd8"), // purple

		fgBase:       lipgloss.Color("#c0caf5"), // fg
		fgSubtle:     lipgloss.Color("#a9b1d6"), // fg_dark
		fgMoreSubtle: lipgloss.Color("#737aa2"), // dark5
		fgMostSubtle: lipgloss.Color("#565f89"), // comment

		onPrimary: lipgloss.Color("#1a1b26"), // bg

		bgBase:         lipgloss.Color("#1a1b26"), // bg
		bgLeastVisible: lipgloss.Color("#1f2335"), // bg_dark (storm)
		bgLessVisible:  lipgloss.Color("#292e42"), // bg_highlight
		bgMostVisible:  lipgloss.Color("#3b4261"), // fg_gutter

		separator: lipgloss.Color("#3b4261"), // fg_gutter

		destructive:       lipgloss.Color("#f7768e"), // red
		error:             lipgloss.Color("#db4b4b"), // red1
		warningSubtle:     lipgloss.Color("#e0af68"), // yellow
		warning:           lipgloss.Color("#e0af68"), // yellow
		attention:         lipgloss.Color("#ff9e64"), // orange
		busy:              lipgloss.Color("#7aa2f7"), // blue
		info:              lipgloss.Color("#7dcfff"), // cyan
		infoMoreSubtle:    lipgloss.Color("#2ac3de"), // blue1
		infoMostSubtle:    lipgloss.Color("#3d59a1"), // blue0
		success:           lipgloss.Color("#9ece6a"), // green
		successMoreSubtle: lipgloss.Color("#73daca"), // green1
		successMostSubtle: lipgloss.Color("#41a6b5"), // green2

		yolo:           lipgloss.Color("#ff9e64"), // orange
		plan:           lipgloss.Color("#bb9af7"), // magenta
		planMoreSubtle: lipgloss.Color("#565f89"), // comment

		// Diff colors derive from success/destructive over bgBase.

		button:         lipgloss.Color("#7aa2f7"), // blue
		buttonSubtle:   lipgloss.Color("#292e42"), // bg_highlight
		buttonInactive: lipgloss.Color("#3b4261"), // fg_gutter
		buttonHovered:  lipgloss.Color("#3d59a1"), // blue0

		ansiBlack:   lipgloss.Color("#15161e"),
		ansiRed:     lipgloss.Color("#f7768e"),
		ansiGreen:   lipgloss.Color("#9ece6a"),
		ansiYellow:  lipgloss.Color("#e0af68"),
		ansiBlue:    lipgloss.Color("#7aa2f7"),
		ansiMagenta: lipgloss.Color("#bb9af7"),
		ansiCyan:    lipgloss.Color("#7dcfff"),
		ansiWhite:   lipgloss.Color("#a9b1d6"),

		ansiBrightBlack:   lipgloss.Color("#414868"),
		ansiBrightRed:     lipgloss.Color("#f7768e"),
		ansiBrightGreen:   lipgloss.Color("#9ece6a"),
		ansiBrightYellow:  lipgloss.Color("#e0af68"),
		ansiBrightBlue:    lipgloss.Color("#7aa2f7"),
		ansiBrightMagenta: lipgloss.Color("#bb9af7"),
		ansiBrightCyan:    lipgloss.Color("#7dcfff"),
		ansiBrightWhite:   lipgloss.Color("#c0caf5"),
	}
}

// catppuccinMochaOpts returns the quickStyleOpts for Catppuccin Mocha,
// using the canonical catppuccin/palette colors.
func catppuccinMochaOpts() quickStyleOpts {
	return quickStyleOpts{
		primary:   lipgloss.Color("#cba6f7"), // mauve
		secondary: lipgloss.Color("#b4befe"), // lavender
		accent:    lipgloss.Color("#94e2d5"), // teal
		keyword:   lipgloss.Color("#cba6f7"), // mauve

		fgBase:       lipgloss.Color("#cdd6f4"), // text
		fgSubtle:     lipgloss.Color("#bac2de"), // subtext1
		fgMoreSubtle: lipgloss.Color("#a6adc8"), // subtext0
		fgMostSubtle: lipgloss.Color("#7f849c"), // overlay1

		onPrimary: lipgloss.Color("#1e1e2e"), // base

		bgBase:         lipgloss.Color("#1e1e2e"), // base
		bgLeastVisible: lipgloss.Color("#181825"), // mantle
		bgLessVisible:  lipgloss.Color("#313244"), // surface0
		bgMostVisible:  lipgloss.Color("#45475a"), // surface1

		separator: lipgloss.Color("#45475a"), // surface1

		destructive:       lipgloss.Color("#f38ba8"), // red
		error:             lipgloss.Color("#f38ba8"), // red
		warningSubtle:     lipgloss.Color("#f9e2af"), // yellow
		warning:           lipgloss.Color("#f9e2af"), // yellow
		attention:         lipgloss.Color("#fab387"), // peach
		busy:              lipgloss.Color("#cba6f7"), // mauve
		info:              lipgloss.Color("#89b4fa"), // blue
		infoMoreSubtle:    lipgloss.Color("#89dceb"), // sky
		infoMostSubtle:    lipgloss.Color("#74c7ec"), // sapphire
		success:           lipgloss.Color("#a6e3a1"), // green
		successMoreSubtle: lipgloss.Color("#a6e3a1"), // green
		successMostSubtle: lipgloss.Color("#94e2d5"), // teal

		yolo:           lipgloss.Color("#fab387"), // peach
		plan:           lipgloss.Color("#89b4fa"), // blue
		planMoreSubtle: lipgloss.Color("#6c7086"), // overlay0

		// Diff colors derive from success/destructive over bgBase.

		button:         lipgloss.Color("#cba6f7"), // mauve
		buttonSubtle:   lipgloss.Color("#313244"), // surface0
		buttonInactive: lipgloss.Color("#45475a"), // surface1
		buttonHovered:  lipgloss.Color("#b4befe"), // lavender

		ansiBlack:   lipgloss.Color("#45475a"),
		ansiRed:     lipgloss.Color("#f38ba8"),
		ansiGreen:   lipgloss.Color("#a6e3a1"),
		ansiYellow:  lipgloss.Color("#f9e2af"),
		ansiBlue:    lipgloss.Color("#89b4fa"),
		ansiMagenta: lipgloss.Color("#f5c2e7"),
		ansiCyan:    lipgloss.Color("#94e2d5"),
		ansiWhite:   lipgloss.Color("#bac2de"),

		ansiBrightBlack:   lipgloss.Color("#585b70"),
		ansiBrightRed:     lipgloss.Color("#f38ba8"),
		ansiBrightGreen:   lipgloss.Color("#a6e3a1"),
		ansiBrightYellow:  lipgloss.Color("#f9e2af"),
		ansiBrightBlue:    lipgloss.Color("#89b4fa"),
		ansiBrightMagenta: lipgloss.Color("#f5c2e7"),
		ansiBrightCyan:    lipgloss.Color("#94e2d5"),
		ansiBrightWhite:   lipgloss.Color("#a6adc8"),
	}
}

// catppuccinLatteOpts returns the quickStyleOpts for Catppuccin Latte, the
// light flavor, using the canonical catppuccin/palette colors.
func catppuccinLatteOpts() quickStyleOpts {
	return quickStyleOpts{
		primary:   lipgloss.Color("#8839ef"), // mauve
		secondary: lipgloss.Color("#1e66f5"), // blue
		accent:    lipgloss.Color("#179299"), // teal
		keyword:   lipgloss.Color("#8839ef"), // mauve

		fgBase:       lipgloss.Color("#4c4f69"), // text
		fgSubtle:     lipgloss.Color("#5c5f77"), // subtext1
		fgMoreSubtle: lipgloss.Color("#6c6f85"), // subtext0
		fgMostSubtle: lipgloss.Color("#8c8fa1"), // overlay1

		onPrimary: lipgloss.Color("#eff1f5"), // base

		bgBase:         lipgloss.Color("#eff1f5"), // base
		bgLeastVisible: lipgloss.Color("#e6e9ef"), // mantle
		bgLessVisible:  lipgloss.Color("#dce0e8"), // crust
		bgMostVisible:  lipgloss.Color("#ccd0da"), // surface0

		separator: lipgloss.Color("#ccd0da"), // surface0

		destructive:       lipgloss.Color("#d20f39"), // red
		error:             lipgloss.Color("#d20f39"), // red
		warningSubtle:     lipgloss.Color("#df8e1d"), // yellow
		warning:           lipgloss.Color("#df8e1d"), // yellow
		attention:         lipgloss.Color("#fe640b"), // peach
		busy:              lipgloss.Color("#8839ef"), // mauve
		info:              lipgloss.Color("#1e66f5"), // blue
		infoMoreSubtle:    lipgloss.Color("#209fb5"), // sapphire
		infoMostSubtle:    lipgloss.Color("#04a5e5"), // sky
		success:           lipgloss.Color("#40a02b"), // green
		successMoreSubtle: lipgloss.Color("#179299"), // teal
		successMostSubtle: lipgloss.Color("#40a02b"), // green

		yolo:           lipgloss.Color("#fe640b"), // peach
		plan:           lipgloss.Color("#1e66f5"), // blue
		planMoreSubtle: lipgloss.Color("#8c8fa1"), // overlay1

		// Diff colors derive from success/destructive over bgBase.

		button:         lipgloss.Color("#8839ef"), // mauve
		buttonSubtle:   lipgloss.Color("#dce0e8"), // crust
		buttonInactive: lipgloss.Color("#ccd0da"), // surface0
		buttonHovered:  lipgloss.Color("#7287fd"), // lavender

		// Catppuccin's terminal white and bright white are pale greys that
		// vanish on the light ground, so they map to the text greys, as in
		// deck-light.
		ansiBlack:   lipgloss.Color("#5c5f77"),
		ansiRed:     lipgloss.Color("#d20f39"),
		ansiGreen:   lipgloss.Color("#40a02b"),
		ansiYellow:  lipgloss.Color("#df8e1d"),
		ansiBlue:    lipgloss.Color("#1e66f5"),
		ansiMagenta: lipgloss.Color("#ea76cb"),
		ansiCyan:    lipgloss.Color("#179299"),
		ansiWhite:   lipgloss.Color("#6c6f85"),

		ansiBrightBlack:   lipgloss.Color("#8c8fa1"),
		ansiBrightRed:     lipgloss.Color("#d20f39"),
		ansiBrightGreen:   lipgloss.Color("#40a02b"),
		ansiBrightYellow:  lipgloss.Color("#df8e1d"),
		ansiBrightBlue:    lipgloss.Color("#1e66f5"),
		ansiBrightMagenta: lipgloss.Color("#ea76cb"),
		ansiBrightCyan:    lipgloss.Color("#179299"),
		ansiBrightWhite:   lipgloss.Color("#4c4f69"),

		light: true,
	}
}

// catppuccinLatteOverrides draws inline code in text on surface0: Latte's
// red on crust is only ~4.1:1.
var catppuccinLatteOverrides = inlineCodeOverride(lipgloss.Color("#4c4f69"), lipgloss.Color("#ccd0da"))

// nordOpts returns the quickStyleOpts for Nord, using the canonical
// arcticicestudio/nord palette (nord0–nord15).
func nordOpts() quickStyleOpts {
	return quickStyleOpts{
		primary:   lipgloss.Color("#88c0d0"), // nord8
		secondary: lipgloss.Color("#8fbcbb"), // nord7
		accent:    lipgloss.Color("#81a1c1"), // nord9
		keyword:   lipgloss.Color("#81a1c1"), // nord9

		fgBase:       lipgloss.Color("#eceff4"), // nord6
		fgSubtle:     lipgloss.Color("#d8dee9"), // nord4
		fgMoreSubtle: lipgloss.Color("#a9b3c5"), // midway nord4 → comment grey
		fgMostSubtle: lipgloss.Color("#7b88a1"), // brightened nord3, for comments

		onPrimary: lipgloss.Color("#2e3440"), // nord0

		bgBase:         lipgloss.Color("#2e3440"), // nord0
		bgLeastVisible: lipgloss.Color("#3b4252"), // nord1
		bgLessVisible:  lipgloss.Color("#434c5e"), // nord2
		bgMostVisible:  lipgloss.Color("#4c566a"), // nord3

		separator: lipgloss.Color("#4c566a"), // nord3

		destructive:       lipgloss.Color("#bf616a"), // nord11
		error:             lipgloss.Color("#bf616a"), // nord11
		warningSubtle:     lipgloss.Color("#ebcb8b"), // nord13
		warning:           lipgloss.Color("#ebcb8b"), // nord13
		attention:         lipgloss.Color("#d08770"), // nord12
		busy:              lipgloss.Color("#88c0d0"), // nord8
		info:              lipgloss.Color("#81a1c1"), // nord9
		infoMoreSubtle:    lipgloss.Color("#88c0d0"), // nord8
		infoMostSubtle:    lipgloss.Color("#5e81ac"), // nord10
		success:           lipgloss.Color("#a3be8c"), // nord14
		successMoreSubtle: lipgloss.Color("#8fbcbb"), // nord7
		successMostSubtle: lipgloss.Color("#88c0d0"), // nord8

		yolo:           lipgloss.Color("#d08770"), // nord12
		plan:           lipgloss.Color("#b48ead"), // nord15
		planMoreSubtle: lipgloss.Color("#4c566a"), // nord3

		// Diff colors derive from success/destructive over bgBase.

		button:         lipgloss.Color("#88c0d0"), // nord8
		buttonSubtle:   lipgloss.Color("#434c5e"), // nord2
		buttonInactive: lipgloss.Color("#4c566a"), // nord3
		buttonHovered:  lipgloss.Color("#81a1c1"), // nord9

		ansiBlack:   lipgloss.Color("#3b4252"),
		ansiRed:     lipgloss.Color("#bf616a"),
		ansiGreen:   lipgloss.Color("#a3be8c"),
		ansiYellow:  lipgloss.Color("#ebcb8b"),
		ansiBlue:    lipgloss.Color("#81a1c1"),
		ansiMagenta: lipgloss.Color("#b48ead"),
		ansiCyan:    lipgloss.Color("#88c0d0"),
		ansiWhite:   lipgloss.Color("#e5e9f0"),

		ansiBrightBlack:   lipgloss.Color("#4c566a"),
		ansiBrightRed:     lipgloss.Color("#bf616a"),
		ansiBrightGreen:   lipgloss.Color("#a3be8c"),
		ansiBrightYellow:  lipgloss.Color("#ebcb8b"),
		ansiBrightBlue:    lipgloss.Color("#81a1c1"),
		ansiBrightMagenta: lipgloss.Color("#b48ead"),
		ansiBrightCyan:    lipgloss.Color("#8fbcbb"),
		ansiBrightWhite:   lipgloss.Color("#eceff4"),
	}
}

// nordOverrides draws inline code in frost on nord1: Aurora red on nord2
// is only ~2.1:1.
var nordOverrides = inlineCodeOverride(lipgloss.Color("#88c0d0"), lipgloss.Color("#3b4252"))

// rosePineOpts returns the quickStyleOpts for Rosé Pine (main), using the
// canonical rose-pine/palette colors.
func rosePineOpts() quickStyleOpts {
	return quickStyleOpts{
		primary:   lipgloss.Color("#ebbcba"), // rose
		secondary: lipgloss.Color("#c4a7e7"), // iris
		accent:    lipgloss.Color("#9ccfd8"), // foam
		keyword:   lipgloss.Color("#31748f"), // pine

		fgBase:       lipgloss.Color("#e0def4"), // text
		fgSubtle:     lipgloss.Color("#e0def4"), // text
		fgMoreSubtle: lipgloss.Color("#908caa"), // subtle
		fgMostSubtle: lipgloss.Color("#6e6a86"), // muted

		onPrimary: lipgloss.Color("#191724"), // base

		bgBase:         lipgloss.Color("#191724"), // base
		bgLeastVisible: lipgloss.Color("#1f1d2e"), // surface
		bgLessVisible:  lipgloss.Color("#26233a"), // overlay
		bgMostVisible:  lipgloss.Color("#403d52"), // highlight med

		separator: lipgloss.Color("#403d52"), // highlight med

		// Rosé Pine has no green: foam marks additions and success, love
		// deletions and errors, as in its own git colors.
		destructive:       lipgloss.Color("#eb6f92"), // love
		error:             lipgloss.Color("#eb6f92"), // love
		warningSubtle:     lipgloss.Color("#f6c177"), // gold
		warning:           lipgloss.Color("#f6c177"), // gold
		attention:         lipgloss.Color("#f6c177"), // gold
		busy:              lipgloss.Color("#ebbcba"), // rose
		info:              lipgloss.Color("#9ccfd8"), // foam
		infoMoreSubtle:    lipgloss.Color("#9ccfd8"), // foam
		infoMostSubtle:    lipgloss.Color("#31748f"), // pine
		success:           lipgloss.Color("#9ccfd8"), // foam
		successMoreSubtle: lipgloss.Color("#9ccfd8"), // foam
		successMostSubtle: lipgloss.Color("#ebbcba"), // rose

		yolo:           lipgloss.Color("#eb6f92"), // love
		plan:           lipgloss.Color("#c4a7e7"), // iris
		planMoreSubtle: lipgloss.Color("#6e6a86"), // muted

		// Diff colors derive from success/destructive over bgBase.

		button:         lipgloss.Color("#ebbcba"), // rose
		buttonSubtle:   lipgloss.Color("#26233a"), // overlay
		buttonInactive: lipgloss.Color("#403d52"), // highlight med
		buttonHovered:  lipgloss.Color("#c4a7e7"), // iris

		ansiBlack:   lipgloss.Color("#26233a"),
		ansiRed:     lipgloss.Color("#eb6f92"),
		ansiGreen:   lipgloss.Color("#31748f"),
		ansiYellow:  lipgloss.Color("#f6c177"),
		ansiBlue:    lipgloss.Color("#9ccfd8"),
		ansiMagenta: lipgloss.Color("#c4a7e7"),
		ansiCyan:    lipgloss.Color("#ebbcba"),
		ansiWhite:   lipgloss.Color("#e0def4"),

		ansiBrightBlack:   lipgloss.Color("#6e6a86"),
		ansiBrightRed:     lipgloss.Color("#eb6f92"),
		ansiBrightGreen:   lipgloss.Color("#31748f"),
		ansiBrightYellow:  lipgloss.Color("#f6c177"),
		ansiBrightBlue:    lipgloss.Color("#9ccfd8"),
		ansiBrightMagenta: lipgloss.Color("#c4a7e7"),
		ansiBrightCyan:    lipgloss.Color("#ebbcba"),
		ansiBrightWhite:   lipgloss.Color("#e0def4"),
	}
}

// draculaOpts returns the quickStyleOpts for Dracula, using the canonical
// draculatheme.com palette and its UI background steps.
func draculaOpts() quickStyleOpts {
	return quickStyleOpts{
		primary:   lipgloss.Color("#bd93f9"), // purple
		secondary: lipgloss.Color("#ff79c6"), // pink
		accent:    lipgloss.Color("#8be9fd"), // cyan
		keyword:   lipgloss.Color("#ff79c6"), // pink

		fgBase:       lipgloss.Color("#f8f8f2"), // foreground
		fgSubtle:     lipgloss.Color("#f8f8f2"), // foreground
		fgMoreSubtle: lipgloss.Color("#adb5cb"), // midway foreground → comment
		fgMostSubtle: lipgloss.Color("#6272a4"), // comment

		onPrimary: lipgloss.Color("#282a36"), // background

		bgBase:         lipgloss.Color("#282a36"), // background
		bgLeastVisible: lipgloss.Color("#21222c"), // BGDark
		bgLessVisible:  lipgloss.Color("#343746"), // BGLight
		bgMostVisible:  lipgloss.Color("#44475a"), // current line / selection

		separator: lipgloss.Color("#44475a"), // current line

		destructive:       lipgloss.Color("#ff5555"), // red
		error:             lipgloss.Color("#ff5555"), // red
		warningSubtle:     lipgloss.Color("#f1fa8c"), // yellow
		warning:           lipgloss.Color("#ffb86c"), // orange
		attention:         lipgloss.Color("#ffb86c"), // orange
		busy:              lipgloss.Color("#bd93f9"), // purple
		info:              lipgloss.Color("#8be9fd"), // cyan
		infoMoreSubtle:    lipgloss.Color("#8be9fd"), // cyan
		infoMostSubtle:    lipgloss.Color("#6272a4"), // comment
		success:           lipgloss.Color("#50fa7b"), // green
		successMoreSubtle: lipgloss.Color("#50fa7b"), // green
		successMostSubtle: lipgloss.Color("#50fa7b"), // green

		yolo:           lipgloss.Color("#ffb86c"), // orange
		plan:           lipgloss.Color("#8be9fd"), // cyan
		planMoreSubtle: lipgloss.Color("#6272a4"), // comment

		// Diff colors derive from success/destructive over bgBase.

		button:         lipgloss.Color("#bd93f9"), // purple
		buttonSubtle:   lipgloss.Color("#343746"), // BGLight
		buttonInactive: lipgloss.Color("#44475a"), // current line
		buttonHovered:  lipgloss.Color("#ff79c6"), // pink

		ansiBlack:   lipgloss.Color("#21222c"),
		ansiRed:     lipgloss.Color("#ff5555"),
		ansiGreen:   lipgloss.Color("#50fa7b"),
		ansiYellow:  lipgloss.Color("#f1fa8c"),
		ansiBlue:    lipgloss.Color("#bd93f9"),
		ansiMagenta: lipgloss.Color("#ff79c6"),
		ansiCyan:    lipgloss.Color("#8be9fd"),
		ansiWhite:   lipgloss.Color("#f8f8f2"),

		ansiBrightBlack:   lipgloss.Color("#6272a4"),
		ansiBrightRed:     lipgloss.Color("#ff6e6e"),
		ansiBrightGreen:   lipgloss.Color("#69ff94"),
		ansiBrightYellow:  lipgloss.Color("#ffffa5"),
		ansiBrightBlue:    lipgloss.Color("#d6acff"),
		ansiBrightMagenta: lipgloss.Color("#ff92df"),
		ansiBrightCyan:    lipgloss.Color("#a4ffff"),
		ansiBrightWhite:   lipgloss.Color("#ffffff"),
	}
}

// draculaOverrides draws inline code in green, Dracula's usual color for
// it: red on BGLight is only ~3.8:1.
var draculaOverrides = inlineCodeOverride(lipgloss.Color("#50fa7b"), nil)

// builtinThemes maps theme names to their quickStyleOpts palette definitions.
var builtinThemes = map[string]func() quickStyleOpts{
	"deck":             deckDarkOpts,
	"deck-light":       deckLightOpts,
	"gruvbox-dark":     gruvboxDarkOpts,
	"tokyo-night":      tokyoNightOpts,
	"catppuccin-mocha": catppuccinMochaOpts,
	"catppuccin-latte": catppuccinLatteOpts,
	"nord":             nordOpts,
	"rose-pine":        rosePineOpts,
	"dracula":          draculaOpts,
}

// builtinThemeOverrides maps theme names to functions that apply
// theme-specific style tweaks on top of the styles produced by
// [quickStyle]. Themes without overrides are absent from the map.
var builtinThemeOverrides = map[string]func(Styles) Styles{
	"deck":             deckOverrides,
	"deck-light":       deckOverrides,
	"gruvbox-dark":     gruvboxDarkOverrides,
	"catppuccin-latte": catppuccinLatteOverrides,
	"nord":             nordOverrides,
	"dracula":          draculaOverrides,
}

// deprecatedThemeNames maps legacy built-in theme names to their current
// names so existing configs and user theme files keep resolving.
var deprecatedThemeNames = map[string]string{
	"charmtone":         "deck",
	"charmtone-panther": "deck",
}

// normalizeThemeName lowercases a theme name and maps deprecated built-in
// names to their current equivalents.
func normalizeThemeName(name string) string {
	key := strings.ToLower(name)
	if renamed, ok := deprecatedThemeNames[key]; ok {
		return renamed
	}
	return key
}

// BuiltinThemeNames returns the names of all built-in themes, sorted.
func BuiltinThemeNames() []string {
	names := make([]string, 0, len(builtinThemes))
	for name := range builtinThemes {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// LoadTheme loads a theme by name. User theme files (in ThemeDirs) take
// precedence over built-in themes of the same name. Returns
// DeckDark styles for an empty name. Returns an error if the
// name is not recognized as either a user file or a built-in.
// LoadTheme loads a theme by name. Global user theme files take precedence
// over built-in themes of the same name. Empty names use Deck.
func LoadTheme(name string) (Styles, error) {
	if name == "" {
		return DeckDark(), nil
	}
	key := normalizeThemeName(name)

	if path, err := FindThemeFile(key); err == nil {
		tf, err := LoadThemeFile(path)
		if err != nil {
			return Styles{}, err
		}
		return LoadPaletteTheme(tf.Base, tf.Palette)
	}

	optsFn, ok := builtinThemes[key]
	if !ok {
		return Styles{}, fmt.Errorf("unknown theme %q; available themes: %s", name, strings.Join(BuiltinThemeNames(), ", "))
	}
	s := quickStyle(optsFn())
	if override, ok := builtinThemeOverrides[key]; ok {
		s = override(s)
	}
	return s, nil
}

// ThemeFromConfig resolves the configured theme name, falling back to the
// default Deck theme when the config value is empty or invalid. The
// fallback is logged so a missing or broken theme file is not silently
// replaced by different colors.
func ThemeFromConfig(name string) Styles {
	s, err := LoadTheme(name)
	if err != nil {
		slog.Warn("Falling back to the default theme", "configured_theme", name, "reason", err)
		return DeckDark()
	}
	return s
}

// ThemeSource indicates where a theme definition comes from.
type ThemeSource int

const (
	// ThemeSourceBuiltin is a theme compiled into the binary.
	ThemeSourceBuiltin ThemeSource = iota
	// ThemeSourceUser is a theme file in ~/.config/deck/themes/.
	ThemeSourceUser
)

// String returns a human-readable label for the theme source.
func (s ThemeSource) String() string {
	if s == ThemeSourceUser {
		return "user"
	}
	return "builtin"
}

// ThemeInfo describes an available theme for listing purposes.
type ThemeInfo struct {
	Name       string
	Source     ThemeSource
	Overridden bool // true if a user file shadows a builtin
}

// ListAllThemes returns all available themes (built-in + global user files),
// sorted by name. Built-in themes shadowed by a user file are marked as
// Overridden.
func ListAllThemes() []ThemeInfo {
	userThemes, _ := ListUserThemes()
	userSet := make(map[string]bool, len(userThemes))
	for _, n := range userThemes {
		userSet[n] = true
	}

	// Determine which user themes shadow builtins.
	overridden := make(map[string]bool)
	for _, n := range userThemes {
		if _, ok := builtinThemes[n]; ok {
			overridden[n] = true
		}
	}

	var infos []ThemeInfo

	// Add built-in themes (mark overridden ones).
	for _, name := range BuiltinThemeNames() {
		infos = append(infos, ThemeInfo{
			Name:       name,
			Source:     ThemeSourceBuiltin,
			Overridden: overridden[name],
		})
	}

	// Add user themes that don't shadow a builtin.
	for _, name := range userThemes {
		if _, isBuiltin := builtinThemes[name]; isBuiltin {
			continue
		}
		infos = append(infos, ThemeInfo{
			Name:   name,
			Source: ThemeSourceUser,
		})
	}

	sort.Slice(infos, func(i, j int) bool {
		return infos[i].Name < infos[j].Name
	})
	return infos
}

// ExportResolvedPalette resolves a theme fully (all palette tokens
// filled) and returns it as a ThemeFile suitable for writing to disk.
// This is used when forking a built-in theme or exporting the current
// palette. The returned ThemeFile has Base set to the source theme name
// and all Palette fields populated with resolved color values.
// ExportResolvedPalette resolves a theme fully and returns it as a ThemeFile
// suitable for writing to disk.
func ExportResolvedPalette(name string) (*ThemeFile, error) {
	key := strings.ToLower(name)
	palette, root, err := resolveThemePalette(key, map[string]bool{})
	if err != nil {
		return nil, err
	}
	rootOpts, err := builtinThemeOpts(root)
	if err != nil {
		return nil, err
	}
	opts := palette.ToQuickStyleOpts(rootOpts)
	opts.deriveDiffColors()
	return &ThemeFile{Base: root, Palette: PaletteFromOpts(opts)}, nil
}

// IsBuiltinTheme reports whether the given name matches a built-in theme.
// Deprecated names still count as built-in.
func IsBuiltinTheme(name string) bool {
	_, ok := builtinThemes[normalizeThemeName(name)]
	return ok
}
