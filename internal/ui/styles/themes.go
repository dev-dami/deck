package styles

import (
	"fmt"
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
	s.Editor.PromptBangIconFocused = s.Editor.PromptBangIconFocused.
		Foreground(tone.Ink).
		Background(tone.Mint)
	s.Editor.PromptBangDotsFocused = s.Editor.PromptBangDotsFocused.
		Foreground(tone.Mint)
	s.Editor.PromptBangDotsBlurred = s.Editor.PromptBangDotsBlurred.
		Foreground(tone.DeepTeal)

	s.Messages.ShellBarFocused = s.Messages.ShellBarFocused.
		BorderForeground(tone.Mint)
	s.Messages.ShellBarBlurred = s.Messages.ShellBarBlurred.
		BorderForeground(tone.Shale)
	s.Messages.ShellPrompt = s.Messages.ShellPrompt.
		Foreground(tone.Mint)
	s.Messages.ShellPromptBlurred = s.Messages.ShellPromptBlurred.
		Foreground(tone.Fog)

	chroma := s.Markdown.CodeBlock.Chroma
	if chroma != nil {
		chroma.CommentPreproc.Color = hex(tone.Clay)
		chroma.KeywordReserved.Color = hex(tone.Plum)
		chroma.KeywordNamespace.Color = hex(tone.Plum)
		chroma.KeywordType.Color = hex(tone.Sky)
		chroma.Operator.Color = hex(tone.Seafoam)
		chroma.NameTag.Color = hex(tone.Plum)
		chroma.NameAttribute.Color = hex(tone.Sky)
		chroma.NameClass.Color = hex(tone.Paper)
		chroma.LiteralString.Color = hex(tone.Honey)
	}
	s.Markdown.Link.Color = hex(tone.Mint)
	s.Markdown.Image.Color = hex(tone.Plum)
	s.Markdown.Code.Color = hex(tone.Mint)
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

// builtinThemes maps theme names to their quickStyleOpts palette definitions.
var builtinThemes = map[string]func() quickStyleOpts{
	"deck":       deckDarkOpts,
	"deck-light": deckLightOpts,
	"gruvbox-dark":      gruvboxDarkOpts,
}

// builtinThemeOverrides maps theme names to functions that apply
// theme-specific style tweaks on top of the styles produced by
// [quickStyle]. Themes without overrides are absent from the map.
var builtinThemeOverrides = map[string]func(Styles) Styles{
	"deck":       deckOverrides,
	"deck-light": deckOverrides,
	"gruvbox-dark":      gruvboxDarkOverrides,
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
