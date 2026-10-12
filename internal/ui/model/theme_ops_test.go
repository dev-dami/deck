package model

import (
	"errors"
	"testing"

	"github.com/dev-dami/deck/internal/config"
	"github.com/dev-dami/deck/internal/ui/common"
	"github.com/dev-dami/deck/internal/ui/dialog"
	"github.com/dev-dami/deck/internal/ui/styles"
	"github.com/dev-dami/deck/internal/workspace"
	"github.com/stretchr/testify/require"
)

// configFieldWorkspace records config field writes.
type configFieldWorkspace struct {
	workspace.Workspace
	fields map[string]any
	err    error
}

func (w *configFieldWorkspace) Config() *config.Config { return &config.Config{} }
func (w *configFieldWorkspace) SetConfigField(_ config.Scope, key string, value any) error {
	if w.err != nil {
		return w.err
	}
	w.fields[key] = value
	return nil
}

// TestSwitchThemeWritesOffTheLoop asserts switching themes leaves the
// config write to the command, and that a failed write reports the error
// without taking the theme.
func TestSwitchThemeWritesOffTheLoop(t *testing.T) {
	t.Parallel()

	ws := &configFieldWorkspace{fields: map[string]any{}}
	u := &UI{com: common.DefaultCommon(ws), dialog: dialog.NewOverlay()}
	newStyles := styles.DeckDark()

	cmd := u.switchThemeCmd("deck", newStyles)
	require.Empty(t, ws.fields, "nothing is written before the command runs")
	msg, ok := cmd().(themeSwitchedMsg)
	require.True(t, ok)
	require.NoError(t, msg.err)
	require.Equal(t, "deck", ws.fields[activeThemeField])
	require.Equal(t, "deck", msg.name)

	ws.err = errors.New("disk full")
	msg = u.switchThemeCmd("deck", newStyles)().(themeSwitchedMsg)
	require.Error(t, msg.err)
	require.NotNil(t, u.handleThemeSwitched(msg), "a failed save is reported")
	require.False(t, u.userThemeSelected, "a failed save does not take the theme")
}
