package workspace

import (
	"regexp"
	"testing"

	"github.com/stretchr/testify/require"
)

// Every colour the phone is sent is "#rrggbb": the phone drops anything
// else, so a palette name ("canvas") would leave its token unset.
func TestThemeIsHex(t *testing.T) {
	hex := regexp.MustCompile(`^#[0-9a-f]{6}$`)
	for _, name := range []string{"deck", "deck-light"} {
		th := themeFor(name)
		require.NotEmpty(t, th.Palette, name)
		for token, v := range th.Palette {
			require.Regexp(t, hex, v, "%s: %s", name, token)
		}
		for kind, k := range th.Kinds {
			require.Regexp(t, hex, k.Tint, "%s: kind %s", name, kind)
		}
	}
}

// The phone knows Deck's own look by its canvas and accent and then draws
// the desktop's colours itself (pkg/remote/web/src/theme.ts, deckLook).
func TestThemeDeckLook(t *testing.T) {
	dark, light := themeFor("deck").Palette, themeFor("deck-light").Palette
	require.Equal(t, "#19191b", dark["bg_base"])
	require.Equal(t, "#0a84ff", dark["primary"])
	require.Equal(t, "#fbfbfd", light["bg_base"])
	require.Equal(t, "#007aff", light["primary"])
}
