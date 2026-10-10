package dialog

import (
	"testing"

	"github.com/dev-dami/deck/internal/ui/list"
	"github.com/stretchr/testify/require"
)

func TestHasThemeItem(t *testing.T) {
	t.Parallel()
	require.False(t, hasThemeItem(nil))
	require.False(t, hasThemeItem([]list.Item{
		&ThemeSectionHeader{Versioned: &list.Versioned{}},
		&themeSpacer{Versioned: &list.Versioned{}},
	}))
	require.True(t, hasThemeItem([]list.Item{
		&ThemeItem{Versioned: &list.Versioned{}, name: "charmtone"},
	}))
}
