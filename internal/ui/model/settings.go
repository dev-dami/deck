package model

import (
	tea "charm.land/bubbletea/v2"

	"github.com/dev-dami/deck/internal/config"
	"github.com/dev-dami/deck/internal/ui/dialog"
	"github.com/dev-dami/deck/internal/ui/util"
	"github.com/dev-dami/deck/internal/workspace"
)

// openSettingsDialog opens the settings dialog, on its Usage page when id
// is [dialog.SettingsUsageID]. An open one is brought to the front on that
// page.
func (m *UI) openSettingsDialog(id string) tea.Cmd {
	tab := dialog.SettingsTabGeneral
	if id == dialog.SettingsUsageID {
		tab = dialog.SettingsTabUsage
	}
	if d, ok := m.dialog.Dialog(dialog.SettingsID).(*dialog.Settings); ok {
		m.dialog.BringToFront(dialog.SettingsID)
		if id == dialog.SettingsUsageID && d.Tab() != tab {
			return d.SetTab(tab)
		}
		return nil
	}
	d := dialog.NewSettings(m.com, tab)
	m.dialog.OpenDialog(d)
	return d.InitialCmd()
}

// applyDeckSettings saves a change to Deck's own settings, off the UI loop.
// The dialog shows the change already.
func (m *UI) applyDeckSettings(msg dialog.ActionSetDeckSettings) tea.Cmd {
	editor, ok := m.com.Workspace.(workspace.SettingsEditor)
	if !ok || msg.Edit == nil {
		return util.ReportWarn("Deck settings are not available here")
	}
	return func() tea.Msg {
		if err := editor.SetDeckSettings(msg.Edit); err != nil {
			return util.ReportError(err)()
		}
		return util.NewInfoMsg(msg.Label)
	}
}

// setTUIOption saves one TUI option, off the UI loop.
func (m *UI) setTUIOption(msg dialog.ActionSetTUIOption) tea.Cmd {
	return func() tea.Msg {
		if err := m.com.Workspace.SetConfigField(config.ScopeGlobal, msg.Key, msg.Value); err != nil {
			return util.ReportError(err)()
		}
		return util.NewInfoMsg(msg.Label)
	}
}
