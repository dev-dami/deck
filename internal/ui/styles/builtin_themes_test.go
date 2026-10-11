package styles

import (
	"image/color"
	"slices"
	"testing"

	"charm.land/lipgloss/v2"
)

// lightThemes are the built-in themes for light terminals.
var lightThemes = map[string]bool{
	"deck-light":       true,
	"catppuccin-latte": true,
}

func TestBuiltinThemes_Registered(t *testing.T) {
	names := BuiltinThemeNames()
	for _, want := range []string{
		"deck", "deck-light", "gruvbox-dark",
		"tokyo-night", "catppuccin-mocha", "catppuccin-latte",
		"nord", "rose-pine", "dracula",
	} {
		if !slices.Contains(names, want) {
			t.Errorf("built-in theme %q is missing from %v", want, names)
		}
	}
}

func TestBuiltinThemes_KeyColors(t *testing.T) {
	for _, name := range BuiltinThemeNames() {
		t.Run(name, func(t *testing.T) {
			s, err := LoadTheme(name)
			if err != nil {
				t.Fatalf("LoadTheme(%q): %v", name, err)
			}
			if s.IsDark == lightThemes[name] {
				t.Errorf("IsDark = %v, want %v", s.IsDark, !lightThemes[name])
			}

			keys := map[string]color.Color{
				"Background":               s.Background,
				"WorkingGradFromColor":     s.WorkingGradFromColor,
				"WorkingGradToColor":       s.WorkingGradToColor,
				"WorkingLabelColor":        s.WorkingLabelColor,
				"Header.LogoGradFromColor": s.Header.LogoGradFromColor,
				"Header.LogoGradToColor":   s.Header.LogoGradToColor,
				"Logo.TaglineColor":        s.Logo.TaglineColor,
				"Logo.VersionColor":        s.Logo.VersionColor,
				"Logo.MarkLowColor":        s.Logo.MarkLowColor,
				"Logo.MarkColor":           s.Logo.MarkColor,
				"Logo.MarkHighColor":       s.Logo.MarkHighColor,
				"Logo.NameColor":           s.Logo.NameColor,
				"Diff.InsertLine.Code":     s.Diff.InsertLine.Code.GetBackground(),
				"Diff.DeleteLine.Code":     s.Diff.DeleteLine.Code.GetBackground(),
				"Diff.InsertLine.Symbol":   s.Diff.InsertLine.Symbol.GetForeground(),
				"Diff.DeleteLine.Symbol":   s.Diff.DeleteLine.Symbol.GetForeground(),
			}
			for field, c := range keys {
				if !isSetColor(c) {
					t.Errorf("%s is unset", field)
				}
			}
			for i, c := range s.ANSI {
				if !isSetColor(c) {
					t.Errorf("ANSI[%d] is unset", i)
				}
			}
			if s.Markdown.Code.Color == nil || s.Markdown.Document.Color == nil {
				t.Error("markdown colors are unset")
			}

			// Diff bands must stand out from the page and from each other.
			bg := s.Background
			add := s.Diff.InsertLine.Code.GetBackground()
			del := s.Diff.DeleteLine.Code.GetBackground()
			if sameRGB(add, bg) || sameRGB(del, bg) || sameRGB(add, del) {
				t.Errorf("diff backgrounds do not stand apart: add %s, delete %s, page %s",
					hexOf(add), hexOf(del), hexOf(bg))
			}
		})
	}
}

func TestBuiltinThemes_InlineCodeContrast(t *testing.T) {
	const minAA = 4.5
	for _, name := range []string{
		"gruvbox-dark", "tokyo-night", "catppuccin-mocha", "catppuccin-latte",
		"nord", "rose-pine", "dracula",
	} {
		t.Run(name, func(t *testing.T) {
			s, err := LoadTheme(name)
			if err != nil {
				t.Fatalf("LoadTheme(%q): %v", name, err)
			}
			code := s.Markdown.Code
			if code.Color == nil || code.BackgroundColor == nil {
				t.Fatal("inline code style is missing fg/bg colors")
			}
			if ratio := contrastRatio(*code.Color, *code.BackgroundColor); ratio < minAA {
				t.Errorf("inline code contrast %.2f is below WCAG AA (%.1f)", ratio, minAA)
			}
		})
	}
}

func TestBuiltinThemes_WordmarkFollowsAccent(t *testing.T) {
	s, err := LoadTheme("dracula")
	if err != nil {
		t.Fatalf("LoadTheme(dracula): %v", err)
	}
	if got := hexOf(s.Logo.MarkColor); got != "#bd93f9" {
		t.Errorf("dracula wordmark = %s, want the purple accent #bd93f9", got)
	}
	if got := hexOf(s.Logo.MarkHighColor); got != "#ff79c6" {
		t.Errorf("dracula wordmark highlight = %s, want the pink #ff79c6", got)
	}
}

func isSetColor(c color.Color) bool {
	if c == nil {
		return false
	}
	_, isNone := c.(lipgloss.NoColor)
	return !isNone
}

func sameRGB(a, b color.Color) bool {
	return hexOf(a) == hexOf(b)
}

func hexOf(c color.Color) string {
	if c == nil {
		return ""
	}
	return *hex(c)
}
